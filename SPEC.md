# Technical Specification: system-controller

## Overview

`system-controller` is a terminal user interface (TUI) tool for monitoring and
controlling systemd services across multiple remote hosts over SSH. It reads an
Ansible-style inventory file to discover hosts and a YAML file to define which
services to track. Service name patterns support shell glob syntax, which is
resolved live against each host's unit list.

The tool is written in Go and targets Linux systems running systemd.

---

## Table of Contents

1. [Invocation](#invocation)
2. [Input Files](#input-files)
3. [Architecture](#architecture)
4. [Package Reference](#package-reference)
5. [SSH Layer](#ssh-layer)
6. [Service Resolution](#service-resolution)
7. [TUI Behaviour](#tui-behaviour)
8. [Key Bindings](#key-bindings)
9. [Concurrency Model](#concurrency-model)
10. [Dependencies](#dependencies)
11. [Build](#build)

---

## Invocation

```
system-controller [--log <logfile>] [--user <username>] <inventory.ini> <services.yaml>
```

| Flag | Description |
|------|-------------|
| `--log <path>` | Write structured log output to `path`. If omitted, logging is suppressed entirely. |
| `--user <name>` | SSH username. Overrides the local `$USER` for all connections. |
| `inventory.ini` | Path to an Ansible-style INI inventory file (required). |
| `services.yaml` | Path to the services YAML configuration file (required). |

The process exits with status 1 if either config file cannot be parsed, or if
the flag syntax is invalid.

---

## Input Files

### Inventory (INI format)

The inventory file follows the Ansible INI convention:

```ini
# comment
[group-name]
hostname-or-ip
alias ansible_host=10.0.0.5
192.168.1.1  extra=tokens  are=ignored
```

**Parsing rules:**

- Lines beginning with `#` or `;` are comments and are skipped.
- Blank lines are skipped.
- `[section]` lines set the current group name.
- Sections whose name contains `:` (e.g. `[all:children]`, `[web:vars]`) are
  skipped entirely — all lines until the next valid section are discarded.
- For each host line, the address is resolved in order of preference:
  1. The value of the `ansible_host=VALUE` key-value token, if present.
  2. The first token that looks like an IPv4 address (only digits and dots,
     contains at least one dot).
  3. The first token that is not a `key=value` pair (bare hostname fallback).
- A file that yields zero hosts is an error.

**`Host` struct:**

```go
type Host struct {
    Address string  // IP or hostname used for SSH
    Group   string  // inventory group name
}
```

### Services (YAML format)

```yaml
services:
  nginx*:
    files:
      - /etc/nginx/nginx.conf
      - /var/log/nginx/error.log
    commands:
      - nginx -T
  sshd:
    commands: []
```

Each key under `services` is a service **name pattern**. A pattern is treated as
a shell glob if it contains `*`, `?`, or `[`. Otherwise it is matched literally.

**`ServiceConfig` struct:**

```go
type ServiceConfig struct {
    NamePattern string
    Files       []string
    Commands    []string
    IsGlob      bool
}
```

Configs are sorted alphabetically by `NamePattern` after parsing. Both `files`
and `commands` default to empty slices if absent from the YAML.

---

## Architecture

```
main.go
  │  parses flags, loads config files, starts bubbletea program
  │
  ├── config/          pure parsing — no I/O beyond file reads
  │   ├── inventory.go
  │   └── services.go
  │
  ├── ssh/             persistent SSH client pool
  │   └── session.go
  │
  ├── monitor/         SSH-driven service status logic
  │   └── status.go
  │
  ├── tui/             bubbletea Elm-architecture UI
  │   ├── keys.go      message types, screen/entry enums
  │   ├── model.go     Model, Init, Update — all state transitions
  │   └── view.go      View — pure string rendering with lipgloss
  │
  └── logging/
      └── logging.go   redirects log package to file or /dev/null
```

The program is single-process. All SSH work happens in goroutines launched by
bubbletea `tea.Cmd` functions, which communicate results back to the main loop
as typed `tea.Msg` values.

---

## Package Reference

### `config`

**`ParseInventory(path string) ([]Host, error)`**

Reads and parses an INI inventory file. Returns a non-empty slice or an error.

**`ParseServices(path string) ([]ServiceConfig, error)`**

Reads and parses a YAML services file. Returns configs sorted by `NamePattern`
or an error.

---

### `ssh`

**`NewSessionManager(sshUser string) *SessionManager`**

Creates a session manager. `sshUser` is used as the SSH login name; if empty,
`$USER` / `$LOGNAME` is used.

**`(*SessionManager) GetClient(ctx, host) (*ssh.Client, error)`**

Returns a cached `*ssh.Client` for `host`, or dials a new one. Dial timeout is
2 seconds (enforced via `context.WithTimeout`). The client is cached for the
lifetime of the `SessionManager`.

**`(*SessionManager) RunCommand(ctx, host, cmd) (string, error)`**

Opens a new SSH channel on the cached client for `host`, executes `cmd` via
`session.Run`. Stdout and stderr are captured separately.

- If the command exits 0: returns stdout.
- If it exits non-zero with stderr: returns an error wrapping stderr content.
- If it exits non-zero with only stdout (e.g. `systemctl is-active`): returns
  stdout (not an error), so the caller can parse the output.
- If it exits non-zero with no output: returns an error with the exit status.

**`(*SessionManager) CloseAll()`**

Closes all cached clients and resets the pool.

**Authentication**

Auth methods are tried in order:
1. SSH agent (via `$SSH_AUTH_SOCK`).
2. Private key files: `~/.ssh/id_ed25519`, `~/.ssh/id_rsa`, `~/.ssh/id_ecdsa`.

Host key verification uses `~/.ssh/known_hosts`. If the file cannot be loaded,
the tool falls back to accepting all host keys (equivalent to
`StrictHostKeyChecking=no`) and logs a warning.

---

### `monitor`

#### `ServiceStatus`

```go
type ServiceStatus int

const (
    StatusUnknown  ServiceStatus = iota
    StatusActive
    StatusInactive
    StatusFailed
    StatusNotFound
    StatusError
)
```

`StatusError` carries a free-form message stored in `HostService.ErrorMsg`.

**`ParseStatus(s string) (ServiceStatus, string)`**

Maps a single line of `systemctl is-active` output to a status and optional
error message:

| Input | Status |
|-------|--------|
| `active` | `StatusActive` |
| `inactive` | `StatusInactive` |
| `failed` | `StatusFailed` |
| `not-found` / `not found` | `StatusNotFound` |
| contains `"could not be found"` | `StatusNotFound` |
| empty / whitespace | `StatusUnknown` |
| anything else | `StatusError` (msg = trimmed input) |

**`(ServiceStatus) Display(errMsg string) string`**

Returns a short display string: `"active"`, `"inactive"`, `"FAILED"`,
`"not found"`, `"???"`, or the error message for `StatusError`.

#### `HostService`

```go
type HostService struct {
    HostAddress string
    ServiceName string
    Config      config.ServiceConfig
    Status      ServiceStatus
    ErrorMsg    string
}
```

The `Config` stored here is augmented: two commands are appended automatically —
`systemctl status <name>` and `journalctl -u <name>` — so they are always
available on the detail screen.

#### `ExpandGlobs`

```go
func ExpandGlobs(ctx, mgr, host, configs) []struct{ Name string; Config ServiceConfig }
```

If any config is a glob, runs `systemctl list-units --type=service --all
--no-legend --no-pager` on the host in one SSH call, strips the `.service`
suffix, and matches each glob pattern against the result using
`github.com/gobwas/glob`. Non-glob configs are passed through unchanged.
Results within each glob are sorted alphabetically.

#### `FetchStatuses`

```go
func FetchStatuses(ctx, mgr, host string, names []string) []HostService
```

Runs `systemctl is-active svc1.service svc2.service ...` in a single SSH call.
Parses one output line per service name. If the SSH command itself fails, all
cells are returned as `StatusError`. If fewer lines are returned than expected,
remaining cells are `StatusUnknown`.

#### `BuildGrid`

```go
func BuildGrid(ctx, mgr, hosts, serviceConfigs) GridResult
```

Two-pass parallel algorithm:

1. **Probe pass** — all hosts are probed concurrently. Each host gets its own
   goroutine and its own `SessionManager`. The goroutine runs `true` via SSH;
   on failure the host is marked unreachable with a classified reason. On
   success, `ExpandGlobs` is called to resolve service name patterns. All
   goroutines are awaited with `sync.WaitGroup` before results are merged.
   The ordered union of service names is built in host-index order to keep
   columns stable across refreshes.

2. **Status pass** — all reachable hosts are queried concurrently in the same
   pattern: one goroutine and one `SessionManager` per host, calling
   `FetchStatuses`. Rows are built aligned to the global service name list.
   Services with `StatusNotFound` are silently omitted (the service does not
   exist on that host).

```go
type GridResult struct {
    ServiceNames     []string         // ordered union of all service names
    Grid             [][]HostService  // one row per host, aligned to ServiceNames
    UnreachableHosts map[int]string   // host index → reason string
}
```

#### `ClassifySSHError(err string) string`

Maps an SSH error message to one of three reason strings:
- `"connection request timed out"` — message contains `timed out` or `timeout`
- `"authentication error"` — message contains `permission denied`, `authentication`, or `auth`
- `"connection error"` — all other cases

#### `RefreshCell`

```go
func RefreshCell(ctx, mgr, host, service string) HostService
```

Calls `FetchStatuses` for a single service and returns the result.

---

### `tui`

The TUI is implemented as a bubbletea Elm-architecture model.

#### `NewModel(hosts, serviceConfigs, sshUser) Model`

Creates the initial model. Grid is empty; the first `BuildGrid` call is
triggered by `Init()`.

#### `Model.Init() tea.Cmd`

Returns a `tea.Cmd` that runs `BuildGrid` in a goroutine and sends the result
back as a `monitor.GridResult` message.

#### `Model.Update(msg tea.Msg) (tea.Model, tea.Cmd)`

Handles:

| Message type | Action |
|---|---|
| `tea.WindowSizeMsg` | Store terminal dimensions for rendering. |
| `monitor.GridResult` | Apply grid, clamp cursor, clear `refreshing` flag. |
| `cellRefreshMsg` | Update a single `(hostIdx, svcIdx)` cell. |
| `vimMsg` | Launch `vim -R <path>` via `tea.ExecProcess`; delete temp file on exit. |
| `tea.KeyMsg` | Dispatch to `updateMain` or `updateDetail` based on active screen. |

#### `Model.View() string`

Pure function. Renders either the main service table or the detail pane,
followed by a one-line status bar. Uses lipgloss for colours and borders.

---

## TUI Behaviour

### Main Screen

Displays a scrollable table with three columns: **Service**, **Host**,
**Status**.

Row ordering:
1. Unreachable hosts (red).
2. Services with `StatusFailed` (red).
3. All other services (normal order).

If the grid is empty and a refresh is in progress, shows `"Refreshing..."`.
If the grid is empty and no refresh is running, shows a prompt to press `r`.

Scrolling: the visible window tracks the cursor. Rows outside the window are
not rendered. A summary line `(start-end of total)` appears when the list
overflows the terminal height.

### Detail Screen

Shows the augmented file and command list for the selected `(host, service)`
pair. Items are grouped under `--- Files ---` and `--- Commands ---` headers
(yellow, bold). The selected item is highlighted with reverse video. Header
rows are not selectable.

Pressing Enter on a file runs `cat <path>` on the remote host; pressing Enter
on a command runs that command. Output is written to a temp file
(`/tmp/sctl-<host>-<timestamp>.txt`) and opened in `vim -R`. The temp file is
deleted when vim exits.

### Status Bar

One line at the bottom showing available key bindings, or `"Refreshing..."`
when an async refresh is in progress.

---

## Key Bindings

### Main Screen

| Key | Action |
|-----|--------|
| `↑` / `↓` | Move cursor |
| `Enter` | Open detail view for selected service |
| `r` | Trigger full grid refresh |
| `c` | Open SSH session to selected host (`ssh [user@]host`) |
| `s` | `sudo systemctl stop <service>` then refresh cell |
| `t` | `sudo systemctl restart <service>` then refresh cell |
| `q` / `Esc` | Quit |
| `Ctrl+C` | Quit |

`c` works on both service rows and unreachable host rows.

### Detail Screen

| Key | Action |
|-----|--------|
| `↑` / `↓` | Move cursor (skips header rows) |
| `Enter` | View file (`cat`) or run command on host; open output in vim |
| `r` | Trigger full grid refresh |
| `c` | Open SSH session |
| `s` | Stop service |
| `t` | Restart service |
| `q` / `Esc` | Return to main screen |
| `Ctrl+C` | Quit |

---

## Concurrency Model

The program is single-threaded from bubbletea's perspective. All state lives in
the `Model` value, which is replaced atomically on each `Update` call.

SSH work is offloaded to goroutines via `tea.Cmd`:

```
Init()
  └─ goroutine: BuildGrid → sends monitor.GridResult

Key 'r'
  └─ goroutine: BuildGrid → sends monitor.GridResult (if not already refreshing)

Key 's' / 't'
  └─ goroutine: RunCommand (stop/restart) → RefreshCell → sends cellRefreshMsg

Key 'c'
  └─ tea.ExecProcess: ssh [user@]host (suspends TUI, resumes on exit)

Enter (detail)
  └─ goroutine: RunCommand (cat/cmd) → writes temp file → sends vimMsg
       └─ tea.ExecProcess: vim -R <tmp> (suspends TUI, resumes on exit)
```

The `refreshing` flag prevents a second `BuildGrid` goroutine from being
spawned while one is already running.

`BuildGrid` fans out one goroutine per host for both the probe and status
phases, each with its own `SessionManager`. With N hosts and a 2-second dial
timeout, the worst-case refresh time is 2 seconds (all hosts unreachable in
parallel) rather than 2N seconds. There is no shared SSH client state between
concurrent goroutines.

### SSH Connection Lifetime

- Connections for `BuildGrid` and service actions are opened at the start of
  each goroutine and closed via `CloseAll()` before it returns.
- `tea.ExecProcess` for SSH and vim is a subprocess, not a Go goroutine; the
  TUI is fully suspended while it runs.

---

## Dependencies

### Direct

| Module | Version | Purpose |
|--------|---------|---------|
| `github.com/charmbracelet/bubbletea` | v1.3.10 | TUI event loop (Elm architecture) |
| `github.com/charmbracelet/lipgloss` | v1.1.0 | Terminal styling / colours |
| `github.com/gobwas/glob` | v0.2.3 | Shell glob pattern matching |
| `golang.org/x/crypto` | v0.48.0 | SSH client (`crypto/ssh`, `ssh/agent`, `ssh/knownhosts`) |
| `gopkg.in/yaml.v3` | v3.0.1 | YAML parsing for services file |

### Runtime Requirements

- `ssh` binary must be on `$PATH` for the `c` key binding (interactive shell session).
- `vim` must be on `$PATH` for the Enter key binding in the detail view.
- The remote hosts must run `systemd` (uses `systemctl` and `journalctl`).
- `sudo` must be configured to allow passwordless `systemctl stop/restart` for
  the connecting user, or the user must have direct permission.

---

## Build

Requires Go 1.24 or later.

```bash
go build -o system-controller .
```

Run tests:

```bash
go test ./...
```

The test suite covers:
- `config`: inventory parsing (INI edge cases, group handling, ansible_host key,
  fallback logic, error cases) and services parsing (glob detection, sorting,
  defaults, YAML error cases).
- `monitor`: `ParseStatus` for all status variants and edge cases, `Display`
  output, multi-line systemctl output parsing, `ClassifySSHError` for all
  three categories.

SSH and TUI packages have no unit tests; they are exercised by integration
testing against a real inventory.

### Integration Test

```bash
./test/run-integration-test.sh
```

Requires a valid `test/inventory-small.ini` and `test/services.yaml`. The
script runs the binary with `--log test/test.log` so SSH and grid-build
activity is captured.

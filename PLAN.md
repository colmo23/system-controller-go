# Go Rewrite Plan: system-controller

## Overview

Rewrite of `system-controller` (Rust/ratatui) in Go. The tool SSHes into hosts
from an Ansible-style INI inventory, checks systemd service statuses (with glob
expansion), and provides a TUI to inspect and control services.

Source: ~/git/system-controller-rust (~700 lines, 8 source files)

---

## Package Structure

```
system-controller/
├── main.go
├── go.mod
├── go.sum
├── config/
│   ├── inventory.go       # parse Ansible INI → []Host
│   ├── inventory_test.go
│   ├── services.go        # parse YAML → []ServiceConfig
│   └── services_test.go
├── ssh/
│   └── session.go         # SessionManager (connect, run_command, close_all)
├── monitor/
│   ├── status.go          # expand_globs, fetch_statuses, build_grid, refresh_cell
│   └── status_test.go
├── tui/
│   ├── model.go           # bubbletea Model + Update()
│   ├── view.go            # bubbletea View() rendering
│   └── keys.go            # key binding constants / msg types
└── logging/
    └── logging.go         # file logger setup
```

---

## Dependency Choices

| Concern        | Rust crate          | Go module                                  |
|----------------|---------------------|--------------------------------------------|
| TUI framework  | ratatui + crossterm | github.com/charmbracelet/bubbletea         |
| TUI styling    | ratatui styles      | github.com/charmbracelet/lipgloss          |
| SSH            | openssh (mux)       | golang.org/x/crypto/ssh                    |
| YAML           | serde_yaml          | gopkg.in/yaml.v3                           |
| Glob matching  | glob-match          | github.com/gobwas/glob                     |
| INI parsing    | manual              | manual (~60 lines, same logic)             |
| Error handling | anyhow              | stdlib errors + fmt.Errorf                 |
| Async          | tokio               | goroutines + channels                      |

---

## Module-by-Module Translation

### config/inventory.go
`ParseInventory(path string) ([]Host, error)`

Same logic as Rust:
- Skip `#`/`;` comments and blank lines
- Parse `[group]` section headers
- Skip `:children` / `:vars` meta-groups
- Per line: prefer `ansible_host=VALUE`, then first IP-looking token, then first non-kv token

### config/services.go
`ParseServices(path string) ([]ServiceConfig, error)`

- Unmarshal YAML with `gopkg.in/yaml.v3`
- Detect glob patterns (`*`, `?`, `[`) to set `IsGlob`
- Sort configs by name pattern

### ssh/session.go
`SessionManager` holds `map[string]*ssh.Client`.

- `GetSession(host)` dials with a 2-second context deadline
- `RunCommand(host, cmd)` opens a new channel per call (equivalent to ControlMaster mux — same underlying TCP connection)
- `CloseAll()` closes all clients

Note: `golang.org/x/crypto/ssh` is lower-level than openssh crate (which wraps
the system ssh binary). Here we dial natively and reuse the client for multiple
commands on the same host, which gives the same performance benefit as mux.

Auth: reads `~/.ssh/id_rsa` / `id_ed25519`, falls back to ssh-agent.

### monitor/status.go
Direct translation of Rust module:
- `ServiceStatus` as typed int + `String()` / `Display()` methods
- `ExpandGlobs(ctx, mgr, host, configs) []ExpandedService`
- `FetchStatuses(ctx, mgr, host, names) []ServiceStatus`
- `BuildGrid(ctx, mgr, hosts, configs) GridResult`
- `RefreshCell(ctx, mgr, host, service) ServiceStatus`

### tui/model.go  (main architecture change)

Rust uses a hand-rolled event loop. Go uses bubbletea's Elm architecture.

```go
type Model struct {
    hosts          []config.Host
    serviceConfigs []config.ServiceConfig
    grid           [][]monitor.HostService
    unreachable    map[int]string
    serviceNames   []string
    screen         Screen        // screenMain | screenDetail
    cursor         int
    detailCursor   int
    refreshing     bool
    sshUser        string
    tableOffset    int
}

// Async result messages
type gridResultMsg  monitor.GridResult
type cellRefreshMsg struct{ hostIdx, svcIdx int; status monitor.ServiceStatus }
```

`Init()` fires `buildGridCmd()` — a `tea.Cmd` goroutine that does SSH work and
returns `gridResultMsg`.

`Update(msg)` handles:
- `tea.KeyMsg` → navigation, r/s/t/c/Enter/q/Esc
- `gridResultMsg` → apply grid result
- `cellRefreshMsg` → update single cell

`tea.ExecProcess()` replaces `suspend_and_run` (ssh sessions, vim) — bubbletea
handles alternate-screen suspend/resume automatically.

### tui/view.go
`View() string` — renders using lipgloss for colors/styles.
Builds: service table (main screen) or file/command list (detail screen) + status bar.

### main.go
- Manual arg parsing (`--log`, `--user`, positional inventory + services)
- Parse config files
- `tea.NewProgram(tui.NewModel(...), tea.WithAltScreen()).Run()`

---

## Key Differences from Rust Version

1. **No tokio → goroutines**: `tea.Cmd` wraps goroutines. `BuildGrid` runs async
   and sends back a `tea.Msg`.

2. **Native SSH**: `golang.org/x/crypto/ssh` dials directly vs openssh crate
   wrapping the system `ssh` binary. Auth reads key files + ssh-agent.

3. **Elm rendering**: ratatui uses immediate-mode draw callbacks; bubbletea
   returns a string from `View()`. Lipgloss replaces ratatui style structs.

4. **`tea.ExecProcess`**: Replaces the manual `suspend → run → resume` dance
   in the Rust version.

5. **Tests**: Go table-driven tests replace `#[test]` units.

---

## Implementation Order

- [x] PLAN.md
- [x] go.mod + dependencies
- [x] config/inventory.go + tests
- [x] config/services.go + tests
- [x] ssh/session.go
- [x] monitor/status.go + tests
- [x] logging/logging.go
- [x] tui/keys.go
- [x] tui/model.go
- [x] tui/view.go
- [x] main.go
- [ ] Integration test against real inventory

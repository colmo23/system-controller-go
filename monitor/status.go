package monitor

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"

	"github.com/gobwas/glob"

	"system-controller/config"
	"system-controller/ssh"
)

// ServiceStatus represents the status of a systemd service.
type ServiceStatus int

const (
	StatusUnknown  ServiceStatus = iota
	StatusActive
	StatusInactive
	StatusFailed
	StatusNotFound
	StatusError // carries a message — use ErrorStatus()
)

// errorStatus is used for the Error variant which carries a string.
type errorStatus struct {
	msg string
}

func (e errorStatus) Error() string { return e.msg }

// HostService represents one (host, service) cell in the grid.
type HostService struct {
	HostAddress string
	ServiceName string
	Config      config.ServiceConfig
	Status      ServiceStatus
	ErrorMsg    string // set when Status == StatusError
}

// Display returns a short human-readable status string.
func (s ServiceStatus) Display(errMsg string) string {
	switch s {
	case StatusUnknown:
		return "???"
	case StatusActive:
		return "active"
	case StatusInactive:
		return "inactive"
	case StatusFailed:
		return "FAILED"
	case StatusNotFound:
		return "not found"
	case StatusError:
		return errMsg
	default:
		return "???"
	}
}

// ParseStatus converts a systemctl is-active output line to a ServiceStatus.
func ParseStatus(s string) (ServiceStatus, string) {
	s = strings.TrimSpace(s)
	switch s {
	case "active":
		return StatusActive, ""
	case "inactive":
		return StatusInactive, ""
	case "failed":
		return StatusFailed, ""
	case "not-found", "not found":
		return StatusNotFound, ""
	case "":
		return StatusUnknown, ""
	default:
		lower := strings.ToLower(s)
		if strings.Contains(lower, "could not be found") || strings.Contains(lower, "not-found") {
			return StatusNotFound, ""
		}
		return StatusError, s
	}
}

// GridResult is returned by BuildGrid.
type GridResult struct {
	ServiceNames    []string
	Grid            [][]HostService
	UnreachableHosts map[int]string
}

// ClassifySSHError returns a short user-friendly reason for an SSH failure.
func ClassifySSHError(err string) string {
	lower := strings.ToLower(err)
	switch {
	case strings.Contains(lower, "timed out") || strings.Contains(lower, "timeout"):
		return "connection request timed out"
	case strings.Contains(lower, "permission denied") ||
		strings.Contains(lower, "authentication") ||
		strings.Contains(lower, "auth"):
		return "authentication error"
	default:
		return "connection error"
	}
}

// ExpandGlobs resolves glob patterns by querying systemctl list-units on the host.
// Returns a list of (serviceName, serviceConfig) pairs.
func ExpandGlobs(ctx context.Context, mgr *ssh.SessionManager, host *config.Host, configs []config.ServiceConfig) []struct {
	Name   string
	Config config.ServiceConfig
} {
	hasGlobs := false
	for _, c := range configs {
		if c.IsGlob {
			hasGlobs = true
			break
		}
	}

	var unitList []string
	if hasGlobs {
		log.Printf("Fetching unit list from %s for glob expansion", host.Address)
		out, err := mgr.RunCommand(ctx, host.Address,
			"systemctl list-units --type=service --all --no-legend --no-pager")
		if err != nil {
			log.Printf("Failed to list units on %s: %v", host.Address, err)
		} else {
			for _, line := range strings.Split(out, "\n") {
				unit := strings.Fields(line)
				if len(unit) == 0 {
					continue
				}
				name := strings.TrimSuffix(unit[0], ".service")
				unitList = append(unitList, name)
			}
			log.Printf("Found %d units on %s", len(unitList), host.Address)
		}
	}

	type result struct {
		Name   string
		Config config.ServiceConfig
	}
	var results []result

	for _, cfg := range configs {
		if !cfg.IsGlob {
			results = append(results, result{cfg.NamePattern, cfg})
			continue
		}
		g, err := glob.Compile(cfg.NamePattern)
		if err != nil {
			log.Printf("Invalid glob pattern %q: %v", cfg.NamePattern, err)
			continue
		}
		var matched []string
		for _, unit := range unitList {
			if g.Match(unit) {
				matched = append(matched, unit)
			}
		}
		sort.Strings(matched)
		log.Printf("Glob %q matched %d services on %s: %v", cfg.NamePattern, len(matched), host.Address, matched)
		for _, name := range matched {
			results = append(results, result{name, cfg})
		}
	}

	// Convert to anonymous struct slice (the public API type)
	out := make([]struct {
		Name   string
		Config config.ServiceConfig
	}, len(results))
	for i, r := range results {
		out[i].Name = r.Name
		out[i].Config = r.Config
	}
	return out
}

// FetchStatuses fetches systemctl is-active for a list of services in one SSH call.
func FetchStatuses(ctx context.Context, mgr *ssh.SessionManager, host string, names []string) []HostService {
	if len(names) == 0 {
		return nil
	}

	args := make([]string, len(names))
	for i, n := range names {
		args[i] = n + ".service"
	}
	cmd := "systemctl is-active " + strings.Join(args, " ")

	log.Printf("Fetching status for %d services on %s", len(names), host)

	out, err := mgr.RunCommand(ctx, host, cmd)
	if err != nil {
		log.Printf("Failed to fetch statuses on %s: %v", host, err)
		cells := make([]HostService, len(names))
		for i, n := range names {
			cells[i] = HostService{
				HostAddress: host,
				ServiceName: n,
				Status:      StatusError,
				ErrorMsg:    err.Error(),
			}
		}
		return cells
	}

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	cells := make([]HostService, len(names))
	for i, n := range names {
		var line string
		if i < len(lines) {
			line = lines[i]
		}
		st, msg := ParseStatus(line)
		cells[i] = HostService{
			HostAddress: host,
			ServiceName: n,
			Status:      st,
			ErrorMsg:    msg,
		}
	}
	return cells
}

// HostResult holds the fully resolved data for a single host.
type HostResult struct {
	HostIdx     int
	Unreachable string                // non-empty if the host could not be reached
	Cells       map[string]HostService // service name → augmented HostService
}

// ProbeHost connects to one host, expands globs, fetches statuses, and returns
// the result. It creates and closes its own SessionManager.
func ProbeHost(ctx context.Context, sshUser string, hostIdx int, host config.Host, serviceConfigs []config.ServiceConfig) HostResult {
	mgr := ssh.NewSessionManager(sshUser)
	defer mgr.CloseAll()

	if _, err := mgr.RunCommand(ctx, host.Address, "true"); err != nil {
		reason := ClassifySSHError(fmt.Sprintf("%v", err))
		log.Printf("Host %s is unreachable (%s): %v", host.Address, reason, err)
		return HostResult{HostIdx: hostIdx, Unreachable: reason}
	}
	log.Printf("Host %s is reachable", host.Address)

	expanded := ExpandGlobs(ctx, mgr, &host, serviceConfigs)

	names := make([]string, len(expanded))
	for i, e := range expanded {
		names[i] = e.Name
	}

	fetched := FetchStatuses(ctx, mgr, host.Address, names)

	cells := make(map[string]HostService, len(fetched))
	for i, cell := range fetched {
		if cell.Status == StatusNotFound {
			log.Printf("Skipping %s on %s (not found)", cell.ServiceName, host.Address)
			continue
		}
		cfg := expanded[i].Config
		cfg.Commands = append(append([]string{}, cfg.Commands...),
			"systemctl status "+cell.ServiceName,
			"journalctl -u "+cell.ServiceName,
		)
		cell.Config = cfg
		cells[cell.ServiceName] = cell
	}

	return HostResult{HostIdx: hostIdx, Cells: cells}
}

// BuildGrid probes all hosts in parallel and returns the combined GridResult.
// Prefer calling ProbeHost per host via tea.Batch for incremental UI updates.
func BuildGrid(ctx context.Context, sshUser string, hosts []config.Host, serviceConfigs []config.ServiceConfig) GridResult {
	log.Printf("Building grid for %d hosts, %d service configs", len(hosts), len(serviceConfigs))

	results := make([]HostResult, len(hosts))
	var wg sync.WaitGroup
	for i, host := range hosts {
		wg.Add(1)
		go func(i int, host config.Host) {
			defer wg.Done()
			results[i] = ProbeHost(ctx, sshUser, i, host, serviceConfigs)
		}(i, host)
	}
	wg.Wait()

	return MergeHostResults(results, len(hosts))
}

// MergeHostResults combines a slice of HostResults into a GridResult.
// Service names are sorted alphabetically for a stable column order.
func MergeHostResults(results []HostResult, numHosts int) GridResult {
	unreachable := make(map[int]string)
	seen := make(map[string]bool)
	var serviceNames []string

	for _, r := range results {
		if r.Unreachable != "" {
			unreachable[r.HostIdx] = r.Unreachable
			continue
		}
		for name := range r.Cells {
			if !seen[name] {
				seen[name] = true
				serviceNames = append(serviceNames, name)
			}
		}
	}
	sort.Strings(serviceNames)

	grid := make([][]HostService, numHosts)
	for _, r := range results {
		if r.Unreachable != "" {
			grid[r.HostIdx] = nil
			continue
		}
		var row []HostService
		for _, name := range serviceNames {
			if cell, ok := r.Cells[name]; ok {
				row = append(row, cell)
			}
		}
		grid[r.HostIdx] = row
	}

	log.Printf("Merged %d/%d hosts: %d services", len(results), numHosts, len(serviceNames))
	return GridResult{
		ServiceNames:     serviceNames,
		Grid:             grid,
		UnreachableHosts: unreachable,
	}
}

// RefreshCell fetches the current status of a single (host, service) cell.
func RefreshCell(ctx context.Context, mgr *ssh.SessionManager, host, service string) HostService {
	cells := FetchStatuses(ctx, mgr, host, []string{service})
	if len(cells) > 0 {
		return cells[0]
	}
	return HostService{HostAddress: host, ServiceName: service, Status: StatusUnknown}
}

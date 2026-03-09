package monitor

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"

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

// BuildGrid probes all hosts, expands globs, and fetches service statuses.
func BuildGrid(ctx context.Context, mgr *ssh.SessionManager, hosts []config.Host, serviceConfigs []config.ServiceConfig) GridResult {
	log.Printf("Building grid for %d hosts, %d service configs", len(hosts), len(serviceConfigs))

	unreachable := make(map[int]string)
	allExpanded := make([][]struct {
		Name   string
		Config config.ServiceConfig
	}, len(hosts))
	var allServiceNames []string
	seen := make(map[string]bool)

	for i, host := range hosts {
		host := host
		if _, err := mgr.RunCommand(ctx, host.Address, "true"); err != nil {
			reason := ClassifySSHError(fmt.Sprintf("%v", err))
			log.Printf("Host %s is unreachable (%s): %v", host.Address, reason, err)
			unreachable[i] = reason
			allExpanded[i] = nil
			continue
		}
		log.Printf("Host %s is reachable", host.Address)

		expanded := ExpandGlobs(ctx, mgr, &host, serviceConfigs)
		allExpanded[i] = expanded
		for _, e := range expanded {
			if !seen[e.Name] {
				seen[e.Name] = true
				allServiceNames = append(allServiceNames, e.Name)
			}
		}
	}

	log.Printf("Service columns after glob expansion: %v", allServiceNames)

	grid := make([][]HostService, len(hosts))

	for i, host := range hosts {
		if _, ok := unreachable[i]; ok {
			grid[i] = nil
			continue
		}

		expanded := allExpanded[i]
		expandedMap := make(map[string]config.ServiceConfig, len(expanded))
		for _, e := range expanded {
			expandedMap[e.Name] = e.Config
		}

		// Collect service names this host has
		var hostSvcNames []string
		for _, name := range allServiceNames {
			if _, ok := expandedMap[name]; ok {
				hostSvcNames = append(hostSvcNames, name)
			}
		}

		cells := FetchStatuses(ctx, mgr, host.Address, hostSvcNames)
		statusMap := make(map[string]HostService, len(cells))
		for _, c := range cells {
			statusMap[c.ServiceName] = c
		}

		var row []HostService
		for _, svcName := range allServiceNames {
			cfg, exists := expandedMap[svcName]
			if !exists {
				continue
			}
			cell, ok := statusMap[svcName]
			if !ok {
				cell = HostService{HostAddress: host.Address, ServiceName: svcName, Status: StatusUnknown}
			}

			if cell.Status == StatusNotFound {
				log.Printf("Skipping %s on %s (not found)", svcName, host.Address)
				continue
			}

			// Augment config with standard commands
			augCfg := cfg
			augCfg.Commands = append(append([]string{}, cfg.Commands...),
				"systemctl status "+svcName,
				"journalctl -u "+svcName,
			)
			cell.Config = augCfg
			row = append(row, cell)
		}
		grid[i] = row
	}

	log.Printf("Grid built: %d rows x %d columns", len(grid), len(allServiceNames))
	return GridResult{
		ServiceNames:    allServiceNames,
		Grid:            grid,
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

package config

import (
	"fmt"
	"os"
	"strings"
)

// Host represents a single entry from the Ansible inventory.
type Host struct {
	Address string
	Group   string
}

// ParseInventory parses an Ansible-style INI inventory file and returns the list of hosts.
func ParseInventory(path string) ([]Host, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read inventory file %q: %w", path, err)
	}

	var hosts []Host
	currentGroup := "ungrouped"

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)

		// Skip blank lines and comments
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		// Section header
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentGroup = line[1 : len(line)-1]
			// Skip meta-groups like [group:children] or [group:vars]
			if strings.Contains(currentGroup, ":") {
				currentGroup = "_skip"
			}
			continue
		}

		if currentGroup == "_skip" {
			continue
		}

		if addr := extractAddress(line); addr != "" {
			hosts = append(hosts, Host{Address: addr, Group: currentGroup})
		}
	}

	if len(hosts) == 0 {
		return nil, fmt.Errorf("no hosts found in inventory file: %s", path)
	}
	return hosts, nil
}

func extractAddress(line string) string {
	tokens := strings.Fields(line)
	if len(tokens) == 0 {
		return ""
	}

	// Prefer ansible_host=VALUE
	for _, tok := range tokens {
		if val, ok := strings.CutPrefix(tok, "ansible_host="); ok {
			return val
		}
	}

	// Then any token that looks like an IPv4 address
	for _, tok := range tokens {
		if strings.Contains(tok, "=") {
			continue
		}
		if isIPAddress(tok) {
			return tok
		}
	}

	// Fall back to the first non-kv token (could be a hostname)
	if !strings.Contains(tokens[0], "=") {
		return tokens[0]
	}
	return ""
}

// isIPAddress returns true for simple IPv4 addresses (digits and dots, at least one dot).
func isIPAddress(s string) bool {
	if !strings.Contains(s, ".") {
		return false
	}
	for _, c := range s {
		if c != '.' && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

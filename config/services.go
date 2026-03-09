package config

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ServiceConfig holds the configuration for a service name pattern.
type ServiceConfig struct {
	NamePattern string
	Files       []string
	Commands    []string
	IsGlob      bool
}

type servicesFile struct {
	Services map[string]serviceEntry `yaml:"services"`
}

type serviceEntry struct {
	Files    []string `yaml:"files"`
	Commands []string `yaml:"commands"`
}

// ParseServices parses the YAML services config and returns a sorted list of ServiceConfig.
func ParseServices(path string) ([]ServiceConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read services file %q: %w", path, err)
	}

	var sf servicesFile
	if err := yaml.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("failed to parse services YAML %q: %w", path, err)
	}

	configs := make([]ServiceConfig, 0, len(sf.Services))
	for name, entry := range sf.Services {
		isGlob := strings.ContainsAny(name, "*?[")
		files := entry.Files
		if files == nil {
			files = []string{}
		}
		commands := entry.Commands
		if commands == nil {
			commands = []string{}
		}
		configs = append(configs, ServiceConfig{
			NamePattern: name,
			Files:       files,
			Commands:    commands,
			IsGlob:      isGlob,
		})
	}

	sort.Slice(configs, func(i, j int) bool {
		return configs[i].NamePattern < configs[j].NamePattern
	})

	return configs, nil
}

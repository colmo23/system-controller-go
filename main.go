package main

import (
	"fmt"
	"log"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"system-controller/config"
	"system-controller/logging"
	"system-controller/tui"
)

func main() {
	args := os.Args[1:]

	var logFile, sshUser, pager string
	var positional []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--log":
			if i+1 >= len(args) {
				printUsage()
				os.Exit(1)
			}
			i++
			logFile = args[i]
		case "--user":
			if i+1 >= len(args) {
				printUsage()
				os.Exit(1)
			}
			i++
			sshUser = args[i]
		case "--pager":
			if i+1 >= len(args) {
				printUsage()
				os.Exit(1)
			}
			i++
			pager = args[i]
		default:
			positional = append(positional, args[i])
		}
	}

	if len(positional) != 2 {
		printUsage()
		os.Exit(1)
	}

	if logFile != "" {
		if err := logging.Init(logFile); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to initialize logging: %v\n", err)
			os.Exit(1)
		}
		log.Printf("system-controller starting")
	} else {
		logging.Discard()
	}

	inventoryPath := positional[0]
	servicesPath := positional[1]

	log.Printf("Parsing inventory: %s", inventoryPath)
	hosts, err := config.ParseInventory(inventoryPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse inventory: %v\n", err)
		os.Exit(1)
	}
	log.Printf("Loaded %d hosts", len(hosts))

	log.Printf("Parsing services config: %s", servicesPath)
	serviceConfigs, err := config.ParseServices(servicesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse services config: %v\n", err)
		os.Exit(1)
	}
	log.Printf("Loaded %d service configs", len(serviceConfigs))

	model := tui.NewModel(hosts, serviceConfigs, sshUser, pager)
	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}

	log.Printf("system-controller exiting")
}

func printUsage() {
	fmt.Fprintf(os.Stderr,
		"Usage: %s [--log <logfile>] [--user <username>] <inventory.ini> <services.yaml>\n",
		os.Args[0])
}

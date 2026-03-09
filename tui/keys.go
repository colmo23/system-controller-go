package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"system-controller/monitor"
)

// cellRefreshMsg carries an updated HostService cell after a service action.
type cellRefreshMsg struct {
	hostIdx int
	svcIdx  int
	cell    monitor.HostService
}

// screen describes which screen is active.
type screen int

const (
	screenMain   screen = iota
	screenDetail
)

// flatEntry is one row in the main list.
type flatEntry struct {
	kind    entryKind
	hostIdx int
	svcIdx  int    // only for kindService
	reason  string // only for kindUnreachable
}

type entryKind int

const (
	kindService     entryKind = iota
	kindUnreachable
)

// detailItem is one actionable row on the detail screen.
type detailItemKind int

const (
	detailHeader  detailItemKind = iota
	detailFile
	detailCommand
)

type detailItem struct {
	kind detailItemKind
	text string
}

func isQuit(msg tea.KeyMsg) bool {
	return msg.String() == "q" || msg.String() == "esc"
}

func isCtrlC(msg tea.KeyMsg) bool {
	return msg.String() == "ctrl+c"
}

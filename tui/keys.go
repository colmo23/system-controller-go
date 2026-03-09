package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"system-controller/monitor"
)

// hostResultMsg carries the result of probing a single host.
type hostResultMsg monitor.HostResult

// cellRefreshMsg carries an updated HostService cell after a service action.
type cellRefreshMsg struct {
	hostIdx  int
	svcIdx   int
	cell     monitor.HostService
	action   string // "stop" or "restart"
	actionOK bool
}

// clearTransientMsg is sent after a delay to remove a transient status display.
type clearTransientMsg struct{ hostIdx, svcIdx int }

// transientKey identifies a (host, service) cell.
type transientKey struct{ hostIdx, svcIdx int }

// transientInfo holds the text to show in place of the real status.
type transientInfo struct {
	text     string
	isResult bool // false = action in progress; true = completed result
	success  bool // only meaningful when isResult = true
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

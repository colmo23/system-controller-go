package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"system-controller/monitor"
)

var (
	styleActive     = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))            // green
	styleFailed     = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true) // red bold
	styleInactive   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))            // yellow
	styleNotFound   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))            // dark gray
	styleUnknown    = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))            // gray
	styleError      = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))            // red
	styleInProgress = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))            // yellow — action in flight
	styleActionOK   = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true) // green bold — action succeeded
	styleActionFail = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true) // red bold — action failed

	styleHeader   = lipgloss.NewStyle().Bold(true)
	styleSelected = lipgloss.NewStyle().Reverse(true)
	styleMuted    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleYellow   = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)
	styleRedRow   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))

	styleBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("8"))
)

// View renders the current state as a string (bubbletea's Elm-style rendering).
func (m Model) View() string {
	switch m.activeScreen {
	case screenMain:
		return m.viewMain()
	case screenDetail:
		return m.viewDetail()
	}
	return ""
}

// --- Main screen ---

func (m Model) viewMain() string {
	entries := m.flatEntries()

	var body string
	if len(entries) == 0 {
		msg := "No data. Press 'r' to refresh or check your config files."
		if m.refreshing() {
			msg = "Refreshing..."
		}
		body = styleBorder.Width(m.contentWidth()).Render(msg)
	} else {
		body = m.renderTable(entries)
	}

	bar := m.renderStatusBar()
	return body + "\n" + bar
}

func (m Model) renderTable(entries []flatEntry) string {
	const colSvc = 25
	const colHost = 20
	const colStatus = 15

	// Header
	header := styleHeader.Render(pad("Service", colSvc)) + "  " +
		styleHeader.Render(pad("Host", colHost)) + "  " +
		styleHeader.Render("Status")

	// Visible window.
	// Total lines on screen: top border(1) + header(1) + separator(1) +
	// visible rows + bottom border(1) + status bar(1) = visible + 5.
	// Must fit in m.height, so visible = m.height - 5.
	visible := m.height - 6
	if visible < 1 {
		visible = 1
	}
	start := m.tableOffset
	end := start + visible
	if end > len(entries) {
		end = len(entries)
	}
	if start > len(entries) {
		start = len(entries)
	}

	var lines []string
	lines = append(lines, header)
	lines = append(lines, strings.Repeat("─", m.contentWidth()-2))

	for i := start; i < end; i++ {
		e := entries[i]
		selected := i == m.cursor

		var line string
		switch e.kind {
		case kindService:
			hs := m.grid[e.hostIdx][e.svcIdx]
			statusStr := m.renderCellStatus(e.hostIdx, e.svcIdx, hs)
			row := pad(hs.ServiceName, colSvc) + "  " +
				pad(hs.HostAddress, colHost) + "  " +
				statusStr
			if selected {
				line = styleSelected.Render(row)
			} else {
				line = row
			}

		case kindUnreachable:
			host := m.hosts[e.hostIdx].Address
			row := pad("", colSvc) + "  " +
				pad(host, colHost) + "  " +
				e.reason
			if selected {
				line = styleSelected.Render(styleRedRow.Render(row))
			} else {
				line = styleRedRow.Render(row)
			}
		}
		lines = append(lines, line)
	}

	// Scroll indicator
	if len(entries) > visible {
		lines = append(lines, styleMuted.Render(fmt.Sprintf(
			"  (%d-%d of %d)", start+1, end, len(entries))))
	}

	content := strings.Join(lines, "\n")
	return styleBorder.Width(m.contentWidth()).Render(content)
}

func (m Model) renderStatusBar(args ...bool) string {
	var text string
	if m.refreshing() {
		text = "Refreshing..."
	} else {
		text = "r:refresh  Enter:detail  c:ssh  s:stop  t:restart  q:quit"
	}
	return styleMuted.Render(text)
}

// --- Detail screen ---

func (m Model) viewDetail() string {
	if m.detailHost >= len(m.grid) || m.detailSvc >= len(m.grid[m.detailHost]) {
		return "No service selected."
	}
	hs := m.grid[m.detailHost][m.detailSvc]
	items := m.detailItems(m.detailHost, m.detailSvc)

	title := fmt.Sprintf(" %s:%s [%s] ",
		hs.HostAddress, hs.ServiceName,
		m.renderCellStatus(m.detailHost, m.detailSvc, hs))

	var lines []string
	for i, item := range items {
		var line string
		switch item.kind {
		case detailHeader:
			line = styleYellow.Render(item.text)
		default:
			line = "  " + item.text
		}
		if i == m.detailCursor && item.kind != detailHeader {
			line = styleSelected.Render(line)
		}
		lines = append(lines, line)
	}

	if len(lines) == 0 {
		lines = append(lines, styleMuted.Render("(no files or commands configured)"))
	}

	body := styleBorder.
		Width(m.contentWidth()).
		BorderTop(true).BorderBottom(true).BorderLeft(true).BorderRight(true).
		Render(title + "\n\n" + strings.Join(lines, "\n"))

	bar := styleMuted.Render("Enter:view in vim  r:refresh  c:ssh  s:stop  t:restart  q:back")
	return body + "\n" + bar
}

// --- Helpers ---

// renderCellStatus returns the styled status string for a cell, showing a
// transient message (restarting.../stopped/restart failed/etc.) if one is set.
func (m Model) renderCellStatus(hostIdx, svcIdx int, hs monitor.HostService) string {
	if t, ok := m.transients[transientKey{hostIdx, svcIdx}]; ok {
		if !t.isResult {
			return styleInProgress.Render(t.text)
		}
		if t.success {
			return styleActionOK.Render(t.text)
		}
		return styleActionFail.Render(t.text)
	}
	return styleStatus(hs.Status, hs.ErrorMsg)
}

func styleStatus(st monitor.ServiceStatus, errMsg string) string {
	display := st.Display(errMsg)
	switch st {
	case monitor.StatusActive:
		return styleActive.Render(display)
	case monitor.StatusFailed:
		return styleFailed.Render(display)
	case monitor.StatusInactive:
		return styleInactive.Render(display)
	case monitor.StatusNotFound:
		return styleNotFound.Render(display)
	case monitor.StatusUnknown:
		return styleUnknown.Render(display)
	case monitor.StatusError:
		return styleError.Render(display)
	default:
		return display
	}
}

func pad(s string, n int) string {
	if len(s) >= n {
		return s[:n]
	}
	return s + strings.Repeat(" ", n-len(s))
}

func (m Model) contentWidth() int {
	if m.width > 4 {
		return m.width - 4
	}
	return 76
}

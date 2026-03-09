package tui

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"system-controller/config"
	"system-controller/monitor"
	"system-controller/ssh"
)

// vimMsg triggers opening vim via tea.ExecProcess from the Update loop.
type vimMsg struct{ path string }

// Model is the bubbletea application model.
type Model struct {
	hosts          []config.Host
	serviceConfigs []config.ServiceConfig
	sshUser        string

	// Raw per-host data, populated incrementally as hosts report in.
	// nil entry = host not yet loaded.
	perHostResults  []monitor.HostResult
	perHostLoaded   []bool
	hostsRemaining  int // counts down to 0; refreshing = hostsRemaining > 0

	// Derived grid state, rebuilt after each hostResultMsg.
	grid         [][]monitor.HostService
	serviceNames []string
	unreachable  map[int]string

	// Navigation
	activeScreen screen
	detailHost   int
	detailSvc    int
	cursor       int
	detailCursor int
	tableOffset  int

	width  int
	height int
}

// NewModel creates an initial Model ready to run.
func NewModel(hosts []config.Host, serviceConfigs []config.ServiceConfig, sshUser string) Model {
	return Model{
		hosts:          hosts,
		serviceConfigs: serviceConfigs,
		sshUser:        sshUser,
		perHostResults: make([]monitor.HostResult, len(hosts)),
		perHostLoaded:  make([]bool, len(hosts)),
		unreachable:    make(map[int]string),
		grid:           make([][]monitor.HostService, len(hosts)),
		activeScreen:   screenMain,
	}
}

// Init fires the first async grid refresh.
func (m Model) Init() tea.Cmd {
	m.hostsRemaining = len(m.hosts)
	return m.buildGridCmd()
}

// refreshing returns true while any host probe is still in flight.
func (m Model) refreshing() bool { return m.hostsRemaining > 0 }

// Update handles all messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case hostResultMsg:
		r := monitor.HostResult(msg)
		m.perHostResults[r.HostIdx] = r
		m.perHostLoaded[r.HostIdx] = true
		m.hostsRemaining--
		if r.Unreachable != "" {
			log.Printf("Host %s is unreachable: %s", m.hosts[r.HostIdx].Address, r.Unreachable)
		} else {
			log.Printf("Host %s data received (%d services)", m.hosts[r.HostIdx].Address, len(r.Cells))
		}
		m.rebuildGrid()
		if n := len(m.flatEntries()); n > 0 && m.cursor >= n {
			m.cursor = n - 1
		}
		return m, nil

	case cellRefreshMsg:
		if msg.hostIdx < len(m.grid) && msg.svcIdx < len(m.grid[msg.hostIdx]) {
			m.grid[msg.hostIdx][msg.svcIdx] = msg.cell //nolint:all
		}
		return m, nil

	case vimMsg:
		c := exec.Command("vim", "-R", msg.path)
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			os.Remove(msg.path)
			return nil
		})

	case tea.KeyMsg:
		switch m.activeScreen {
		case screenMain:
			return m.updateMain(msg)
		case screenDetail:
			return m.updateDetail(msg)
		}
	}

	return m, nil
}

func (m Model) updateMain(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if isCtrlC(key) || isQuit(key) {
		log.Printf("Quit requested")
		return m, tea.Quit
	}

	switch key.String() {
	case "up":
		if m.cursor > 0 {
			m.cursor--
			m.clampOffset()
		}
	case "down":
		if n := len(m.flatEntries()); m.cursor+1 < n {
			m.cursor++
			m.clampOffset()
		}
	case "enter":
		if e := m.selectedEntry(); e != nil && e.kind == kindService {
			log.Printf("Opening detail view for %s:%s",
				m.hosts[e.hostIdx].Address,
				m.grid[e.hostIdx][e.svcIdx].ServiceName)
			m.activeScreen = screenDetail
			m.detailHost = e.hostIdx
			m.detailSvc = e.svcIdx
			m.detailCursor = 0
		}
	case "r":
		log.Printf("Full refresh requested")
		return m, m.spawnRefresh()
	case "c":
		if e := m.selectedEntry(); e != nil {
			return m, m.sshCmd(m.hosts[e.hostIdx].Address)
		}
	case "s":
		if e := m.selectedEntry(); e != nil && e.kind == kindService {
			host := m.hosts[e.hostIdx].Address
			svc := m.grid[e.hostIdx][e.svcIdx].ServiceName
			return m, m.serviceActionCmd(host, svc, "stop", e.hostIdx, e.svcIdx)
		}
	case "t":
		if e := m.selectedEntry(); e != nil && e.kind == kindService {
			host := m.hosts[e.hostIdx].Address
			svc := m.grid[e.hostIdx][e.svcIdx].ServiceName
			return m, m.serviceActionCmd(host, svc, "restart", e.hostIdx, e.svcIdx)
		}
	}
	return m, nil
}

func (m Model) updateDetail(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if isCtrlC(key) {
		return m, tea.Quit
	}
	if isQuit(key) {
		log.Printf("Returning to main screen")
		m.activeScreen = screenMain
		m.detailCursor = 0
		return m, nil
	}

	items := m.detailItems(m.detailHost, m.detailSvc)

	switch key.String() {
	case "up":
		if m.detailCursor > 0 {
			m.detailCursor--
		}
	case "down":
		if m.detailCursor+1 < len(items) {
			m.detailCursor++
		}
	case "enter":
		if m.detailCursor < len(items) {
			item := items[m.detailCursor]
			host := m.hosts[m.detailHost].Address
			switch item.kind {
			case detailFile:
				log.Printf("Viewing file %s on %s", item.text, host)
				return m, m.openInVimCmd(host, "cat "+item.text)
			case detailCommand:
				log.Printf("Running command %q on %s and viewing in vim", item.text, host)
				return m, m.openInVimCmd(host, item.text)
			}
		}
	case "r":
		return m, m.spawnRefresh()
	case "c":
		return m, m.sshCmd(m.hosts[m.detailHost].Address)
	case "s":
		host := m.hosts[m.detailHost].Address
		svc := m.grid[m.detailHost][m.detailSvc].ServiceName
		return m, m.serviceActionCmd(host, svc, "stop", m.detailHost, m.detailSvc)
	case "t":
		host := m.hosts[m.detailHost].Address
		svc := m.grid[m.detailHost][m.detailSvc].ServiceName
		return m, m.serviceActionCmd(host, svc, "restart", m.detailHost, m.detailSvc)
	}
	return m, nil
}

// --- Commands (async side-effects) ---

func (m Model) buildGridCmd() tea.Cmd {
	cmds := make([]tea.Cmd, len(m.hosts))
	for i, host := range m.hosts {
		i, host := i, host
		user := m.sshUser
		configs := m.serviceConfigs
		cmds[i] = func() tea.Msg {
			r := monitor.ProbeHost(context.Background(), user, i, host, configs)
			return hostResultMsg(r)
		}
	}
	return tea.Batch(cmds...)
}

func (m Model) spawnRefresh() tea.Cmd {
	if m.refreshing() {
		return nil
	}
	m.hostsRemaining = len(m.hosts)
	m.perHostLoaded = make([]bool, len(m.hosts))
	m.perHostResults = make([]monitor.HostResult, len(m.hosts))
	return m.buildGridCmd()
}

func (m Model) serviceActionCmd(host, svc, action string, hostIdx, svcIdx int) tea.Cmd {
	user := m.sshUser
	return func() tea.Msg {
		mgr := ssh.NewSessionManager(user)
		defer mgr.CloseAll()

		cmd := fmt.Sprintf("sudo systemctl %s %s", action, svc)
		if _, err := mgr.RunCommand(context.Background(), host, cmd); err != nil {
			log.Printf("Service action %q failed for %s on %s: %v", action, svc, host, err)
		} else {
			log.Printf("Service action %q succeeded for %s on %s", action, svc, host)
		}

		cell := monitor.RefreshCell(context.Background(), mgr, host, svc)
		return cellRefreshMsg{hostIdx: hostIdx, svcIdx: svcIdx, cell: cell}
	}
}

func (m Model) sshCmd(host string) tea.Cmd {
	dest := host
	if m.sshUser != "" {
		dest = m.sshUser + "@" + host
	}
	log.Printf("Opening SSH session to %s", dest)
	c := exec.Command("ssh", dest)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		log.Printf("Returned from SSH session to %s", dest)
		return nil
	})
}

func (m Model) openInVimCmd(host, cmd string) tea.Cmd {
	user := m.sshUser
	return func() tea.Msg {
		mgr := ssh.NewSessionManager(user)
		out, err := mgr.RunCommand(context.Background(), host, cmd)
		mgr.CloseAll()
		if err != nil {
			out = fmt.Sprintf("Error: %v\n", err)
		}

		tmp := filepath.Join(os.TempDir(), fmt.Sprintf("sctl-%s-%d.txt",
			sanitize(host), time.Now().UnixMilli()))
		if writeErr := os.WriteFile(tmp, []byte(out), 0o600); writeErr != nil {
			log.Printf("Failed to write temp file: %v", writeErr)
			return nil
		}

		// Return vimMsg; Update will call tea.ExecProcess with it.
		return vimMsg{path: tmp}
	}
}

// --- Flat list helpers ---

func (m *Model) flatEntries() []flatEntry {
	var failed, rest []flatEntry

	for hostIdx, row := range m.grid {
		if reason, ok := m.unreachable[hostIdx]; ok {
			failed = append(failed, flatEntry{kind: kindUnreachable, hostIdx: hostIdx, reason: reason})
			continue
		}
		for svcIdx, hs := range row {
			e := flatEntry{kind: kindService, hostIdx: hostIdx, svcIdx: svcIdx}
			if hs.Status == monitor.StatusFailed {
				failed = append(failed, e)
			} else {
				rest = append(rest, e)
			}
		}
	}
	return append(failed, rest...)
}

func (m *Model) selectedEntry() *flatEntry {
	entries := m.flatEntries()
	if m.cursor < len(entries) {
		e := entries[m.cursor]
		return &e
	}
	return nil
}

func (m *Model) detailItems(hostIdx, svcIdx int) []detailItem {
	if hostIdx >= len(m.grid) || svcIdx >= len(m.grid[hostIdx]) {
		return nil
	}
	hs := m.grid[hostIdx][svcIdx]
	var items []detailItem

	if len(hs.Config.Files) > 0 {
		items = append(items, detailItem{kind: detailHeader, text: "--- Files ---"})
		for _, f := range hs.Config.Files {
			items = append(items, detailItem{kind: detailFile, text: f})
		}
	}
	if len(hs.Config.Commands) > 0 {
		items = append(items, detailItem{kind: detailHeader, text: "--- Commands ---"})
		for _, c := range hs.Config.Commands {
			items = append(items, detailItem{kind: detailCommand, text: c})
		}
	}
	return items
}

// rebuildGrid recomputes serviceNames, grid, and unreachable from perHostResults.
// Called after each hostResultMsg so the display updates incrementally.
func (m *Model) rebuildGrid() {
	loaded := make([]monitor.HostResult, 0, len(m.hosts))
	for i, ok := range m.perHostLoaded {
		if ok {
			loaded = append(loaded, m.perHostResults[i])
		}
	}
	result := monitor.MergeHostResults(loaded, len(m.hosts))
	m.serviceNames = result.ServiceNames
	m.grid = result.Grid
	m.unreachable = result.UnreachableHosts
}

// clampOffset keeps the selected row visible in the scrollable table.
func (m *Model) clampOffset() {
	visible := m.height - 4
	if visible < 1 {
		visible = 1
	}
	if m.cursor < m.tableOffset {
		m.tableOffset = m.cursor
	}
	if m.cursor >= m.tableOffset+visible {
		m.tableOffset = m.cursor - visible + 1
	}
}

func sanitize(s string) string {
	out := make([]byte, len(s))
	for i := range s {
		if s[i] == '.' || s[i] == '-' {
			out[i] = '_'
		} else {
			out[i] = s[i]
		}
	}
	return string(out)
}

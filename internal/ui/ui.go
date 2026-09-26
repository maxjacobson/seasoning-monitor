// Package ui renders the seasoning-monitor terminal interface.
package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/maxjacobson/seasoning-monitor/internal/collect"
)

type mode int

const (
	modeSummary mode = iota
	modeLogs
)

const scrollStep = 8

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	labelStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	goodStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	badStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	warnStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Background(lipgloss.Color("238"))
	panelStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
)

// Model is the bubbletea model for the monitor.
type Model struct {
	collector    collect.Collector
	ctx          context.Context
	refreshEvery time.Duration

	snap    collect.Snapshot
	err     error
	loading bool

	width, height int
	selected      int

	mode       mode
	logUnit    string
	logText    string
	logErr     error
	logLoading bool
	logY       int
	logX       int
}

// New builds a Model bound to the given collector.
func New(ctx context.Context, c collect.Collector) Model {
	return Model{
		collector:    c,
		ctx:          ctx,
		refreshEvery: 30 * time.Second,
		loading:      true,
	}
}

// snapshotMsg carries a fresh snapshot (or a collection error).
type snapshotMsg struct {
	snap collect.Snapshot
	err  error
}

// logsMsg carries journal output for a unit.
type logsMsg struct {
	unit string
	text string
	err  error
}

// tickMsg triggers a periodic refresh.
type tickMsg time.Time

func (m Model) load() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		defer cancel()
		snap, err := m.collector.Snapshot(ctx)
		return snapshotMsg{snap: snap, err: err}
	}
}

func (m Model) loadLogs(unit string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		defer cancel()
		text, err := m.collector.Logs(ctx, unit, 300)
		return logsMsg{unit: unit, text: text, err: err}
	}
}

func (m Model) tick() tea.Cmd {
	return tea.Tick(m.refreshEvery, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// Init kicks off the first load and the refresh timer.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.load(), m.tick())
}

// Update handles messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case snapshotMsg:
		m.loading = false
		m.err = msg.err
		if msg.err == nil {
			m.snap = msg.snap
		}
		if n := len(m.snap.Services); m.selected >= n {
			m.selected = n - 1
			if m.selected < 0 {
				m.selected = 0
			}
		}
		return m, nil

	case logsMsg:
		m.logLoading = false
		m.logText = msg.text
		m.logErr = msg.err
		m.logY = 0
		m.logX = 0
		return m, nil

	case tickMsg:
		return m, tea.Batch(m.load(), m.tick())

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}

	if m.mode == modeLogs {
		switch key {
		case "q", "esc", "backspace":
			m.mode = modeSummary
		case "up", "k":
			if m.logY > 0 {
				m.logY--
			}
		case "down", "j":
			m.logY++
		case "left", "h":
			if m.logX >= scrollStep {
				m.logX -= scrollStep
			} else {
				m.logX = 0
			}
		case "right", "l":
			m.logX += scrollStep
		case "r":
			m.logLoading = true
			return m, m.loadLogs(m.logUnit)
		}
		m.clampLog()
		return m, nil
	}

	switch key {
	case "q":
		return m, tea.Quit
	case "r":
		m.loading = true
		return m, m.load()
	case "up", "k":
		if m.selected > 0 {
			m.selected--
		}
	case "down", "j":
		if m.selected < len(m.snap.Services)-1 {
			m.selected++
		}
	case "enter":
		if len(m.snap.Services) == 0 {
			return m, nil
		}
		unit := m.snap.Services[m.selected].Unit
		m.mode = modeLogs
		m.logUnit = unit
		m.logText = ""
		m.logErr = nil
		m.logY = 0
		m.logX = 0
		m.logLoading = true
		return m, m.loadLogs(unit)
	}
	return m, nil
}

// View renders the current screen.
func (m Model) View() tea.View {
	var content string
	if m.mode == modeLogs {
		content = m.viewLogs()
	} else {
		content = m.viewSummary()
	}
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

func (m Model) viewSummary() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("seasoning-monitor"))
	if !m.snap.CollectedAt.IsZero() {
		b.WriteString(dimStyle.Render("  updated " + m.snap.CollectedAt.Format("15:04:05")))
	}
	if m.loading {
		b.WriteString(dimStyle.Render("  refreshing..."))
	}
	b.WriteString("\n\n")

	if m.err != nil {
		b.WriteString(badStyle.Render("error: " + m.err.Error()))
		b.WriteString("\n\n")
	}

	b.WriteString(m.viewMetrics())
	b.WriteString("\n\n")
	b.WriteString(labelStyle.Render("services"))
	b.WriteString("\n")

	if len(m.snap.Services) == 0 {
		b.WriteString(dimStyle.Render("  (none found)"))
		b.WriteString("\n")
	}
	for i, s := range m.snap.Services {
		line := m.renderTarget(s)
		if i == m.selected {
			line = selectedStyle.Render(line)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render("j/k move  enter logs  r refresh  q quit"))
	return b.String()
}

func (m Model) viewMetrics() string {
	var lines []string
	lines = append(lines, metricLine("disk", m.diskText(), m.diskStyle()))
	lines = append(lines, metricLine("mem", m.memText(), m.memStyle()))
	lines = append(lines, metricLine("load", m.loadText(), m.loadStyle()))
	lines = append(lines, metricLine("temp", m.tempText(), m.tempStyle()))
	if m.snap.Uptime != "" {
		lines = append(lines, metricLine("uptime", m.snap.Uptime, dimStyle))
	}
	if m.snap.Timer != "" {
		lines = append(lines, metricLine("timer", strings.Join(strings.Fields(m.snap.Timer), " "), dimStyle))
	}
	return panelStyle.Render(strings.Join(lines, "\n"))
}

func metricLine(name, value string, style lipgloss.Style) string {
	return fmt.Sprintf("%s %s", labelStyle.Width(7).Render(name), style.Render(value))
}

func (m Model) diskText() string {
	if m.snap.Disk.Mount == "" {
		return "unknown"
	}
	return fmt.Sprintf("%d%% used (%s free on %s)", m.snap.Disk.Percent, m.snap.Disk.Avail, m.snap.Disk.Mount)
}

func (m Model) diskStyle() lipgloss.Style {
	return threshold(m.snap.Disk.Percent, 80, 90)
}

func (m Model) memText() string {
	mem := m.snap.Mem
	if mem.TotalMB == 0 {
		return "unknown"
	}
	pct := mem.UsedMB * 100 / mem.TotalMB
	return fmt.Sprintf("%d%% used (%d/%d MB, %d MB available)", pct, mem.UsedMB, mem.TotalMB, mem.AvailableMB)
}

func (m Model) memStyle() lipgloss.Style {
	mem := m.snap.Mem
	if mem.TotalMB == 0 {
		return dimStyle
	}
	return threshold(mem.UsedMB*100/mem.TotalMB, 80, 90)
}

func (m Model) loadText() string {
	l := m.snap.Load
	if l.NProc == 0 {
		return "unknown"
	}
	return fmt.Sprintf("%.2f %.2f %.2f (%d cpus)", l.One, l.Five, l.Fifteen, l.NProc)
}

func (m Model) loadStyle() lipgloss.Style {
	l := m.snap.Load
	if l.NProc == 0 {
		return dimStyle
	}
	ratio := l.One / float64(l.NProc)
	switch {
	case ratio >= 1.0:
		return badStyle
	case ratio >= 0.7:
		return warnStyle
	default:
		return goodStyle
	}
}

func (m Model) tempText() string {
	if !m.snap.TempOK {
		return "unknown"
	}
	return fmt.Sprintf("%.1fC", m.snap.TempC)
}

func (m Model) tempStyle() lipgloss.Style {
	if !m.snap.TempOK {
		return dimStyle
	}
	switch {
	case m.snap.TempC >= 80:
		return badStyle
	case m.snap.TempC >= 70:
		return warnStyle
	default:
		return goodStyle
	}
}

func (m Model) renderTarget(s collect.Service) string {
	label := s.Label()

	if s.Missing() {
		return fmt.Sprintf(" %s %-22s %s", dimStyle.Render("●"), label, dimStyle.Render("not installed"))
	}
	if s.Running() {
		return fmt.Sprintf(" %s %-22s %s", goodStyle.Render("●"), label, goodStyle.Render("running"))
	}
	if s.OK() {
		detail := "success"
		if s.ExitTime != "" {
			detail += "  " + s.ExitTime
		}
		return fmt.Sprintf(" %s %-22s %s", goodStyle.Render("●"), label, goodStyle.Render(detail))
	}

	status := s.Result
	if status == "" {
		status = "unknown"
	}
	detail := status
	if s.ExitStatus > 0 {
		detail = fmt.Sprintf("%s (exit %d)", status, s.ExitStatus)
	}
	if s.ExitTime != "" {
		detail += "  " + dimStyle.Render(s.ExitTime)
	}
	return fmt.Sprintf(" %s %-22s %s", badStyle.Render("●"), label, badStyle.Render(detail))
}

func (m Model) viewLogs() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("logs: " + m.logUnit))
	if m.logLoading {
		b.WriteString(dimStyle.Render("  loading..."))
	}
	if m.logX > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  col %d+", m.logX)))
	}
	b.WriteString("\n\n")

	lines := m.logLines()
	switch {
	case m.logErr != nil:
		b.WriteString(badStyle.Render("error: " + m.logErr.Error()))
	case len(lines) == 0:
		b.WriteString(dimStyle.Render("(no entries)"))
	default:
		visible := m.height - 6
		if visible < 1 {
			visible = 20
		}
		start := m.logY
		if start > len(lines)-1 {
			start = len(lines) - 1
		}
		end := start + visible
		if end > len(lines) {
			end = len(lines)
		}
		for _, line := range lines[start:end] {
			b.WriteString(m.sliceLine(line))
			b.WriteString("\n")
		}
		if end < len(lines) {
			b.WriteString(dimStyle.Render(fmt.Sprintf("... %d more lines", len(lines)-end)))
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render("j/k scroll  h/l pan  r reload  esc back  q quit"))
	return b.String()
}

func (m Model) logLines() []string {
	if strings.TrimSpace(m.logText) == "" {
		return nil
	}
	if strings.Contains(m.logText, "-- No entries --") {
		return nil
	}
	return strings.Split(strings.TrimRight(m.logText, "\n"), "\n")
}

// sliceLine applies the current horizontal offset and clips to the viewport.
func (m Model) sliceLine(line string) string {
	runes := []rune(line)
	if m.logX >= len(runes) {
		return ""
	}
	runes = runes[m.logX:]

	width := m.width
	if width <= 0 {
		width = 200
	}
	if len(runes) > width {
		runes = runes[:width]
	}
	return string(runes)
}

// clampLog keeps the vertical and horizontal offsets inside the content.
func (m *Model) clampLog() {
	lines := m.logLines()
	if len(lines) == 0 {
		m.logY, m.logX = 0, 0
		return
	}
	if m.logY > len(lines)-1 {
		m.logY = len(lines) - 1
	}
	if m.logY < 0 {
		m.logY = 0
	}

	maxWidth := 0
	for _, line := range lines {
		if w := len([]rune(line)); w > maxWidth {
			maxWidth = w
		}
	}
	if m.logX > maxWidth {
		m.logX = maxWidth
	}
	if m.logX < 0 {
		m.logX = 0
	}
}

func threshold(value, warn, bad int) lipgloss.Style {
	switch {
	case value >= bad:
		return badStyle
	case value >= warn:
		return warnStyle
	default:
		return goodStyle
	}
}

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

type target struct {
	label string
	unit  string
	svc   collect.Service
	isApp bool
}

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
	logScroll  int
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

func (m Model) targets() []target {
	ts := make([]target, 0, len(m.snap.Services)+1)
	ts = append(ts, target{label: "rails app", unit: "seasoning", svc: m.snap.App, isApp: true})
	for _, s := range m.snap.Services {
		ts = append(ts, target{label: s.Name, unit: "seasoning-" + s.Name, svc: s})
	}
	return ts
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
		if n := len(m.targets()); m.selected >= n {
			m.selected = n - 1
		}
		return m, nil

	case logsMsg:
		m.logLoading = false
		m.logText = msg.text
		m.logErr = msg.err
		m.logScroll = 0
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
			if m.logScroll > 0 {
				m.logScroll--
			}
		case "down", "j":
			m.logScroll++
		case "r":
			m.logLoading = true
			return m, m.loadLogs(m.logUnit)
		}
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
		if m.selected < len(m.targets())-1 {
			m.selected++
		}
	case "enter":
		ts := m.targets()
		if len(ts) == 0 {
			return m, nil
		}
		t := ts[m.selected]
		m.mode = modeLogs
		m.logUnit = t.unit
		m.logText = ""
		m.logErr = nil
		m.logLoading = true
		return m, m.loadLogs(t.unit)
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

	for i, t := range m.targets() {
		line := m.renderTarget(t)
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

func (m Model) renderTarget(t target) string {
	dot := dimStyle.Render("●")
	if t.isApp {
		if t.svc.ActiveState == "active" {
			dot = goodStyle.Render("●")
		} else if t.svc.ActiveState != "" && t.svc.ActiveState != "unknown" {
			dot = badStyle.Render("●")
		}
		return fmt.Sprintf(" %s %-22s %s", dot, t.label, dimStyle.Render(t.svc.ActiveState))
	}

	status := t.svc.Result
	if status == "" {
		status = "unknown"
	}
	if t.svc.OK() {
		dot = goodStyle.Render("●")
		return fmt.Sprintf(" %s %-22s %s", dot, t.label, goodStyle.Render(status))
	}
	dot = badStyle.Render("●")
	detail := status
	if t.svc.ExitStatus >= 0 {
		detail = fmt.Sprintf("%s (exit %d)", status, t.svc.ExitStatus)
	}
	if t.svc.ExitTime != "" {
		detail += "  " + dimStyle.Render(t.svc.ExitTime)
	}
	return fmt.Sprintf(" %s %-22s %s", dot, t.label, badStyle.Render(detail))
}

func (m Model) viewLogs() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("logs: " + m.logUnit))
	if m.logLoading {
		b.WriteString(dimStyle.Render("  loading..."))
	}
	b.WriteString("\n\n")

	switch {
	case m.logErr != nil:
		b.WriteString(badStyle.Render("error: " + m.logErr.Error()))
	case m.logText == "":
		b.WriteString(dimStyle.Render("(no output)"))
	default:
		lines := strings.Split(strings.TrimRight(m.logText, "\n"), "\n")
		visible := m.height - 6
		if visible < 1 {
			visible = 20
		}
		start := m.logScroll
		if start > len(lines)-1 {
			start = len(lines) - 1
		}
		if start < 0 {
			start = 0
		}
		end := start + visible
		if end > len(lines) {
			end = len(lines)
		}
		for _, line := range lines[start:end] {
			b.WriteString(line)
			b.WriteString("\n")
		}
		if end < len(lines) {
			b.WriteString(dimStyle.Render(fmt.Sprintf("... %d more lines", len(lines)-end)))
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render("j/k scroll  r reload  esc back  q quit"))
	return b.String()
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

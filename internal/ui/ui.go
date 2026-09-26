// Package ui renders the seasoning-monitor terminal interface.
package ui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
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

const (
	scrollStep   = 8
	logBacklog   = 300
	maxLogLines  = 5000
	channelDepth = 256

	// defaultRefresh is how often the summary reloads itself.
	defaultRefresh = 5 * time.Second
	// defaultStatsRefresh is how often the expensive stats reload. They scan
	// the journal, so they run far less often than the summary.
	defaultStatsRefresh = 30 * time.Second
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

// Source provides snapshots, slower stats, and streaming logs.
// collect.Collector implements it; tests substitute fakes.
type Source interface {
	Snapshot(ctx context.Context) (collect.Snapshot, error)
	Stats(ctx context.Context) (collect.Stats, error)
	StreamLogs(ctx context.Context, unit string, lines int) (io.ReadCloser, error)
}

// Model is the bubbletea model for the monitor.
type Model struct {
	source       Source
	ctx          context.Context
	refreshEvery time.Duration
	statsEvery   time.Duration

	snap    collect.Snapshot
	err     error
	loading bool

	stats        collect.Stats
	statsErr     error
	statsLoading bool

	width, height int
	selected      int

	mode        mode
	logUnit     string
	logLines    []string
	logErr      error
	logStreamOn bool
	logFollow   bool
	logY        int
	logX        int

	logGen    int
	logCh     chan string
	logCancel context.CancelFunc
}

// New builds a Model bound to the given source. refresh is how often the
// summary reloads itself; statsRefresh is the slower cadence for traffic and
// other expensive-to-gather numbers. Values <= 0 use defaults.
func New(ctx context.Context, source Source, refresh, statsRefresh time.Duration) Model {
	if refresh <= 0 {
		refresh = defaultRefresh
	}
	if statsRefresh <= 0 {
		statsRefresh = defaultStatsRefresh
	}
	return Model{
		source:       source,
		ctx:          ctx,
		refreshEvery: refresh,
		statsEvery:   statsRefresh,
		loading:      true,
		statsLoading: true,
		logFollow:    true,
	}
}

// snapshotMsg carries a fresh snapshot (or a collection error).
type snapshotMsg struct {
	snap collect.Snapshot
	err  error
}

// logStreamStartMsg signals that a follow stream has been established.
type logStreamStartMsg struct {
	unit   string
	gen    int
	ch     chan string
	cancel context.CancelFunc
	err    error
}

// logLineMsg is a single new journal line.
type logLineMsg struct {
	unit string
	gen  int
	line string
}

// logStreamEndMsg signals that the follow stream has finished.
type logStreamEndMsg struct {
	unit string
	gen  int
}

// tickMsg triggers a periodic refresh.
type tickMsg time.Time

// statsMsg carries fresh traffic/health numbers (or a collection error).
type statsMsg struct {
	stats collect.Stats
	err   error
}

// statsTickMsg triggers a periodic stats refresh.
type statsTickMsg time.Time

func (m Model) load() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		defer cancel()
		snap, err := m.source.Snapshot(ctx)
		return snapshotMsg{snap: snap, err: err}
	}
}

func (m Model) loadStats() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 60*time.Second)
		defer cancel()
		stats, err := m.source.Stats(ctx)
		return statsMsg{stats: stats, err: err}
	}
}

func (m Model) statsTick() tea.Cmd {
	return tea.Tick(m.statsEvery, func(t time.Time) tea.Msg { return statsTickMsg(t) })
}

// openLogStream starts `journalctl -f` for the unit and returns a message
// carrying the channel and cancel function once the process is running.
func (m Model) openLogStream(unit string, gen int) tea.Cmd {
	source := m.source
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		rc, err := source.StreamLogs(ctx, unit, logBacklog)
		if err != nil {
			cancel()
			return logStreamStartMsg{unit: unit, gen: gen, err: err}
		}
		ch := make(chan string, channelDepth)
		go func() {
			defer close(ch)
			defer rc.Close()
			defer cancel()
			scanner := bufio.NewScanner(rc)
			scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			for scanner.Scan() {
				select {
				case ch <- scanner.Text():
				case <-ctx.Done():
					return
				}
			}
		}()
		return logStreamStartMsg{unit: unit, gen: gen, ch: ch, cancel: cancel}
	}
}

func waitForLogLine(ch chan string, unit string, gen int) tea.Cmd {
	return func() tea.Msg {
		line, ok := <-ch
		if !ok {
			return logStreamEndMsg{unit: unit, gen: gen}
		}
		return logLineMsg{unit: unit, gen: gen, line: line}
	}
}

func (m Model) tick() tea.Cmd {
	return tea.Tick(m.refreshEvery, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// Init kicks off the first loads and the refresh timers.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.load(), m.loadStats(), m.tick(), m.statsTick())
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

	case logStreamStartMsg:
		if msg.gen != m.logGen {
			if msg.cancel != nil {
				msg.cancel()
			}
			return m, nil
		}
		if msg.err != nil {
			m.logStreamOn = false
			m.logErr = msg.err
			return m, nil
		}
		m.logErr = nil
		m.logStreamOn = true
		m.logCh = msg.ch
		m.logCancel = msg.cancel
		return m, waitForLogLine(msg.ch, msg.unit, msg.gen)

	case logLineMsg:
		if msg.gen != m.logGen {
			return m, nil
		}
		m.logLines = append(m.logLines, msg.line)
		if len(m.logLines) > maxLogLines {
			m.logLines = m.logLines[len(m.logLines)-maxLogLines:]
		}
		return m, waitForLogLine(m.logCh, msg.unit, msg.gen)

	case logStreamEndMsg:
		if msg.gen != m.logGen {
			return m, nil
		}
		m.logStreamOn = false
		m.logCh = nil
		return m, nil

	case tickMsg:
		// Skip a tick if a load is still in flight, so a slow ssh call can't
		// pile up overlapping requests.
		if m.loading {
			return m, m.tick()
		}
		m.loading = true
		return m, tea.Batch(m.load(), m.tick())

	case statsMsg:
		m.statsLoading = false
		m.statsErr = msg.err
		if msg.err == nil {
			m.stats = msg.stats
		}
		return m, nil

	case statsTickMsg:
		if m.statsLoading {
			return m, m.statsTick()
		}
		m.statsLoading = true
		return m, tea.Batch(m.loadStats(), m.statsTick())

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) stopLogStream() {
	if m.logCancel != nil {
		m.logCancel()
		m.logCancel = nil
	}
	m.logCh = nil
	m.logStreamOn = false
}

func (m *Model) openUnit(unit string) tea.Cmd {
	m.stopLogStream()
	m.logGen++
	m.mode = modeLogs
	m.logUnit = unit
	m.logLines = nil
	m.logErr = nil
	m.logFollow = true
	m.logY = 0
	m.logX = 0
	return m.openLogStream(unit, m.logGen)
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}

	if m.mode == modeLogs {
		switch key {
		case "q", "esc", "backspace":
			m.stopLogStream()
			m.mode = modeSummary
		case "up", "k":
			if m.logFollow {
				m.logFollow = false
				m.logY = m.tailStart() - 1
				if m.logY < 0 {
					m.logY = 0
				}
			} else if m.logY > 0 {
				m.logY--
			}
		case "down", "j":
			if !m.logFollow {
				m.logY++
				if m.logY >= m.tailStart() {
					m.logFollow = true
				}
			}
		case "left", "h":
			if m.logX >= scrollStep {
				m.logX -= scrollStep
			} else {
				m.logX = 0
			}
		case "right", "l":
			m.logX += scrollStep
		case "g":
			m.logFollow = false
			m.logY = 0
		case "G", "f":
			m.logFollow = true
		case "r":
			return m, m.openUnit(m.logUnit)
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
		return m, m.openUnit(m.snap.Services[m.selected].Unit)
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
	b.WriteString("\n")
	b.WriteString(m.viewStats())
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
		lines = append(lines, metricLine("uptime", m.snap.Uptime, goodStyle))
	}
	return panelStyle.Render(strings.Join(lines, "\n"))
}

func (m Model) viewStats() string {
	if m.statsErr != nil {
		return panelStyle.Render(badStyle.Render("stats error: " + m.statsErr.Error()))
	}
	if m.statsLoading && m.stats.Traffic.Requests == 0 {
		return panelStyle.Render(dimStyle.Render("gathering traffic & health..."))
	}

	t := m.stats.Traffic
	now := m.snap.CollectedAt

	trafficStyle := goodStyle
	if t.S5xx > 0 {
		trafficStyle = badStyle
	}

	var lines []string
	lines = append(lines, statsLine("traffic", humanCount(t.Requests)+" requests (24h)", trafficStyle))
	breakdown := fmt.Sprintf("2xx %d · 3xx %d · 4xx %d · 5xx %d", t.S2xx, t.S3xx, t.S4xx, t.S5xx)
	lines = append(lines, statsLine("", breakdown, trafficStyle))

	failedValue := "0 units"
	failedStyle := goodStyle
	if n := len(m.stats.FailedUnits); n > 0 {
		failedValue = fmt.Sprintf("%d: %s", n, strings.Join(m.stats.FailedUnits, ", "))
		failedStyle = badStyle
	}
	lines = append(lines, statsLine("failed", failedValue, failedStyle))

	if m.stats.Database.OK {
		value := fmt.Sprintf("%s · %d conns", m.stats.Database.Size, m.stats.Database.Connections)
		lines = append(lines, statsLine("postgres", value, goodStyle))
	} else {
		lines = append(lines, statsLine("postgres", "unavailable", badStyle))
	}

	lines = append(lines, statsLine("backup", m.backupText(now), m.backupStyle(now)))

	nginxStyle := goodStyle
	if m.stats.NginxErrors > 0 {
		nginxStyle = badStyle
	}
	lines = append(lines, statsLine("nginx", fmt.Sprintf("%d errors", m.stats.NginxErrors), nginxStyle))

	return panelStyle.Render(strings.Join(lines, "\n"))
}

func (m Model) backupText(now time.Time) string {
	b := m.stats.Backup
	if !b.OK {
		return "none found in 7 days"
	}
	return humanDuration(now.Sub(b.At)) + " ago"
}

func (m Model) backupStyle(now time.Time) lipgloss.Style {
	b := m.stats.Backup
	if !b.OK || b.Stale(now) {
		return badStyle
	}
	return goodStyle
}

func statsLine(name, value string, style lipgloss.Style) string {
	return fmt.Sprintf("%s %s", labelStyle.Width(8).Render(name), style.Render(value))
}

func metricLine(name, value string, style lipgloss.Style) string {
	return fmt.Sprintf("%s %s", labelStyle.Width(7).Render(name), style.Render(value))
}

func humanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// humanCount formats n with thousands separators.
func humanCount(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	digits := strconv.Itoa(n)
	var out strings.Builder
	if neg {
		out.WriteByte('-')
	}
	for i, c := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteByte(byte(c))
	}
	return out.String()
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
		if s.Timer != nil {
			if rel := s.Timer.Relative(m.snap.CollectedAt); rel != "" {
				style := goodStyle
				if s.Timer.Overdue(m.snap.CollectedAt) {
					style = warnStyle
				}
				return fmt.Sprintf(" %s %-22s %s", style.Render("●"), label, style.Render("success  ·  "+rel))
			}
		}
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
	if s.Timer != nil {
		if rel := s.Timer.Relative(m.snap.CollectedAt); rel != "" {
			detail += "  ·  " + rel
		}
	} else if s.ExitTime != "" {
		detail += "  " + dimStyle.Render(s.ExitTime)
	}
	return fmt.Sprintf(" %s %-22s %s", badStyle.Render("●"), label, badStyle.Render(detail))
}

func (m Model) viewLogs() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("logs: " + m.logUnit))
	switch {
	case m.logStreamOn:
		b.WriteString("  " + goodStyle.Render("● live"))
	case m.logErr == nil && m.mode == modeLogs:
		b.WriteString("  " + dimStyle.Render("(ended)"))
	}
	if m.logX > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  col %d+", m.logX)))
	}
	b.WriteString("\n\n")

	switch {
	case m.logErr != nil:
		b.WriteString(badStyle.Render("error: " + m.logErr.Error()))
	case len(m.logLines) == 0:
		if m.logStreamOn {
			b.WriteString(dimStyle.Render("(waiting for output)"))
		} else {
			b.WriteString(dimStyle.Render("(no entries)"))
		}
	default:
		start := m.viewStart()
		end := start + m.visibleLines()
		if end > len(m.logLines) {
			end = len(m.logLines)
		}
		for _, line := range m.logLines[start:end] {
			b.WriteString(m.sliceLine(line))
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render("j/k scroll  h/l pan  g/G top/bottom  r restart  esc back  q quit"))
	return b.String()
}

func (m Model) visibleLines() int {
	visible := m.height - 6
	if visible < 1 {
		return 20
	}
	return visible
}

// tailStart is the first line shown when following the bottom of the log.
func (m Model) tailStart() int {
	start := len(m.logLines) - m.visibleLines()
	if start < 0 {
		return 0
	}
	return start
}

func (m Model) viewStart() int {
	if m.logFollow {
		return m.tailStart()
	}
	start := m.logY
	if start > len(m.logLines)-1 {
		start = len(m.logLines) - 1
	}
	if start < 0 {
		return 0
	}
	return start
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

// clampLog keeps the horizontal offset inside the content.
func (m *Model) clampLog() {
	maxWidth := 0
	for _, line := range m.logLines {
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

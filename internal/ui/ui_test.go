package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/maxjacobson/seasoning-monitor/internal/collect"
)

func sampleSnapshot() collect.Snapshot {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	return collect.Snapshot{
		CollectedAt: now,
		Disk:        collect.Disk{Percent: 12, Avail: "24G", Mount: "/"},
		Mem:         collect.Mem{TotalMB: 8192, UsedMB: 2048, AvailableMB: 5500},
		Load:        collect.Load{One: 0.15, Five: 0.42, Fifteen: 0.30, NProc: 4},
		TempC:       48.3,
		TempOK:      true,
		Uptime:      "up 3 days",
		Services: []collect.Service{
			{Unit: "seasoning.service", LoadState: "loaded", ActiveState: "active"},
			{
				Unit: "seasoning-nightly.service", LoadState: "loaded", ActiveState: "inactive", Result: "success",
				Timer: &collect.Timer{Unit: "seasoning-nightly.timer", Next: now.Add(20 * time.Minute), NextOK: true, Last: now.Add(-23 * time.Hour), LastOK: true},
			},
			{Unit: "seasoning-prune.service", LoadState: "not-found", Result: "success"},
		},
	}
}

func update(m Model, msg tea.Msg) Model {
	next, _ := m.Update(msg)
	return next.(Model)
}

func updateCmd(m Model, msg tea.Msg) (Model, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

func key(k tea.Key) tea.Msg { return tea.KeyPressMsg(k) }

// withLogs simulates an open follow stream that has already delivered lines.
func withLogs(m Model, unit string, lines ...string) Model {
	m.logGen++
	m.mode = modeLogs
	m.logUnit = unit
	m.logCancel = func() {}
	m = update(m, logStreamStartMsg{unit: unit, gen: m.logGen, ch: make(chan string), cancel: func() {}})
	for _, line := range lines {
		m = update(m, logLineMsg{unit: unit, gen: m.logGen, line: line})
	}
	return m
}

func TestRefreshInterval(t *testing.T) {
	if got := New(context.Background(), collect.Collector{}, 0, 0).refreshEvery; got != defaultRefresh {
		t.Errorf("default refresh = %v, want %v", got, defaultRefresh)
	}
	if got := New(context.Background(), collect.Collector{}, time.Minute, 0).refreshEvery; got != time.Minute {
		t.Errorf("custom refresh = %v, want 1m", got)
	}
	if got := New(context.Background(), collect.Collector{}, 0, time.Minute).statsEvery; got != time.Minute {
		t.Errorf("custom stats refresh = %v, want 1m", got)
	}
	if got := New(context.Background(), collect.Collector{}, 0, 0).statsEvery; got != defaultStatsRefresh {
		t.Errorf("default stats refresh = %v, want %v", got, defaultStatsRefresh)
	}
}

func TestTickReloadsWhenIdle(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0)
	m = update(m, snapshotMsg{snap: sampleSnapshot()})
	if m.loading {
		t.Fatalf("model should be idle after a snapshot")
	}

	m, cmd := updateCmd(m, tickMsg(time.Now()))
	if cmd == nil {
		t.Fatalf("tick should schedule a reload")
	}
	if !m.loading {
		t.Errorf("a tick should mark the model as loading")
	}
}

func TestTickSkipsWhileLoading(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0)
	if !m.loading {
		t.Fatalf("a fresh model starts loading")
	}
	m, cmd := updateCmd(m, tickMsg(time.Now()))
	if cmd == nil {
		t.Fatalf("the tick loop should keep running")
	}
	if !m.loading {
		t.Errorf("a tick while loading must not clear the in-flight state")
	}
}

func sampleStats() collect.Stats {
	return collect.Stats{
		Traffic:     collect.Traffic{Window: 24 * time.Hour, Requests: 8437, S2xx: 3704, S3xx: 4035, S4xx: 697, S5xx: 1},
		FailedUnits: []string{"foo.service"},
		NginxErrors: 2,
		Database:    collect.Database{Size: "104 MB", Connections: 7, OK: true},
		Backup:      collect.Backup{At: time.Date(2026, 9, 25, 11, 0, 0, 0, time.UTC), Key: "bucket/x.dump.gz", OK: true},
	}
}

func withStats(m Model) Model {
	return update(m, statsMsg{stats: sampleStats()})
}

func TestStatsPanelShowsTraffic(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0)
	m = update(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = update(m, snapshotMsg{snap: sampleSnapshot()})
	m = withStats(m)

	view := m.View().Content
	for _, want := range []string{"8,437 requests", "2xx 3704", "5xx 1", "104 MB · 7 conns", "1h ago", "1: foo.service"} {
		if !strings.Contains(view, want) {
			t.Errorf("stats panel missing %q\n%s", want, view)
		}
	}
}

func TestStatsPanelShowsError(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0)
	m = update(m, snapshotMsg{snap: sampleSnapshot()})
	m = update(m, statsMsg{err: context.DeadlineExceeded})
	if !strings.Contains(m.View().Content, "stats error:") {
		t.Errorf("expected a stats error:\n%s", m.View().Content)
	}
}

func TestProgressBarFollowsWork(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0)
	if m.View().ProgressBar == nil {
		t.Errorf("expected a progress bar during the initial load")
	}

	m = update(m, snapshotMsg{snap: sampleSnapshot()})
	m = update(m, statsMsg{stats: sampleStats()})
	if m.View().ProgressBar != nil {
		t.Errorf("expected no progress bar when idle")
	}

	// Reopening a log stream counts as work until the stream starts.
	m = update(m, snapshotMsg{snap: sampleSnapshot()})
	m, _ = updateCmd(m, key(tea.Key{Code: tea.KeyEnter}))
	if m.View().ProgressBar == nil {
		t.Errorf("expected a progress bar while connecting the log stream")
	}

	m = update(m, logStreamStartMsg{unit: "seasoning.service", gen: m.logGen, ch: make(chan string), cancel: func() {}})
	if m.View().ProgressBar != nil {
		t.Errorf("expected no progress bar once the stream is live")
	}
}

func TestStatsColourReflectsHealth(t *testing.T) {
	snap := sampleSnapshot()

	healthy := New(context.Background(), collect.Collector{}, 0, 0)
	healthy = update(healthy, snapshotMsg{snap: snap})
	healthy = withStats(healthy)
	if !strings.Contains(healthy.View().Content, "38;5;42") {
		t.Errorf("healthy stats should render green:\n%q", healthy.View().Content)
	}

	broken := New(context.Background(), collect.Collector{}, 0, 0)
	broken = update(broken, snapshotMsg{snap: snap})
	s := sampleStats()
	s.Traffic.S5xx = 3
	s.Backup.At = snap.CollectedAt.Add(-40 * time.Hour)
	broken = update(broken, statsMsg{stats: s})
	if !strings.Contains(broken.View().Content, "38;5;203") {
		t.Errorf("unhealthy stats should render red:\n%q", broken.View().Content)
	}
}

func TestStaleBackupFlagged(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0)
	snap := sampleSnapshot()
	m = update(m, snapshotMsg{snap: snap})
	stats := sampleStats()
	stats.Backup.At = snap.CollectedAt.Add(-40 * time.Hour)
	m = update(m, statsMsg{stats: stats})

	if !strings.Contains(m.View().Content, "1d ago") {
		t.Errorf("expected a stale backup age:\n%s", m.View().Content)
	}
}

func TestHumanCount(t *testing.T) {
	cases := map[int]string{0: "0", 7: "7", 999: "999", 1000: "1,000", 8437: "8,437", 1234567: "1,234,567"}
	for in, want := range cases {
		if got := humanCount(in); got != want {
			t.Errorf("humanCount(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestSummaryShowsHealth(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0)
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: 40})
	m = update(m, snapshotMsg{snap: sampleSnapshot()})

	view := m.View().Content
	for _, want := range []string{"12% used", "118.9F", "seasoning-nightly", "running", "not installed"} {
		if !strings.Contains(view, want) {
			t.Errorf("summary missing %q\n%s", want, view)
		}
	}
	if strings.Contains(view, " timer ") {
		t.Errorf("the timer should not be a global header line:\n%s", view)
	}
}

func TestServiceRowShowsItsTimer(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0)
	m = update(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = update(m, snapshotMsg{snap: sampleSnapshot()})

	view := m.View().Content
	if !strings.Contains(view, "next in 20m") {
		t.Errorf("nightly row should show its next run:\n%s", view)
	}
	if !strings.Contains(view, "ran 23h ago") {
		t.Errorf("nightly row should show when it last ran:\n%s", view)
	}
}

func TestOverdueTimerFlagged(t *testing.T) {
	snap := sampleSnapshot()
	now := snap.CollectedAt
	snap.Services[1].Timer = &collect.Timer{Next: now.Add(-5 * time.Minute), NextOK: true}

	m := New(context.Background(), collect.Collector{}, 0, 0)
	m = update(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = update(m, snapshotMsg{snap: snap})

	if !strings.Contains(m.View().Content, "overdue by 5m") {
		t.Errorf("overdue timer should be flagged:\n%s", m.View().Content)
	}
}

func TestSummaryShowsError(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0)
	m = update(m, snapshotMsg{err: context.DeadlineExceeded})
	if !strings.Contains(m.View().Content, "error:") {
		t.Errorf("expected error banner, got:\n%s", m.View().Content)
	}
}

func TestNavigationBounds(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0)
	m = update(m, snapshotMsg{snap: sampleSnapshot()})

	if m.selected != 0 {
		t.Fatalf("selected = %d, want 0", m.selected)
	}
	m = update(m, key(tea.Key{Code: tea.KeyUp}))
	if m.selected != 0 {
		t.Errorf("up at top moved selection to %d", m.selected)
	}

	m = update(m, key(tea.Key{Text: "j", Code: 'j'}))
	if m.selected != 1 {
		t.Errorf("down moved selection to %d, want 1", m.selected)
	}
	m = update(m, key(tea.Key{Text: "j", Code: 'j'}))
	m = update(m, key(tea.Key{Text: "j", Code: 'j'}))
	if m.selected != 2 {
		t.Errorf("down past end moved selection to %d, want 2", m.selected)
	}
}

func TestEnterOpensUnitLogs(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0)
	m = update(m, snapshotMsg{snap: sampleSnapshot()})
	m = update(m, key(tea.Key{Code: tea.KeyEnter}))

	if m.mode != modeLogs {
		t.Fatalf("enter did not open logs, mode = %v", m.mode)
	}
	if m.logUnit != "seasoning.service" {
		t.Errorf("log unit = %q, want seasoning.service", m.logUnit)
	}
}

func TestStreamAppendsAndShowsLive(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0)
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: 40})
	m = withLogs(m, "seasoning.service", "one", "two")

	if len(m.logLines) != 2 {
		t.Fatalf("logLines = %v, want 2 entries", m.logLines)
	}
	if !m.logStreamOn {
		t.Errorf("stream should be on")
	}
	if !m.logFollow {
		t.Errorf("should follow by default")
	}
	view := m.View().Content
	if !strings.Contains(view, "live") {
		t.Errorf("view should show a live indicator:\n%s", view)
	}
	if !strings.Contains(view, "one") || !strings.Contains(view, "two") {
		t.Errorf("view missing streamed lines:\n%s", view)
	}
}

func TestStreamEndMarksEnded(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0)
	m = withLogs(m, "seasoning.service", "one")
	m = update(m, logStreamEndMsg{unit: "seasoning.service", gen: m.logGen})

	if m.logStreamOn {
		t.Errorf("stream should have ended")
	}
	if !strings.Contains(m.View().Content, "ended") {
		t.Errorf("expected ended indicator:\n%s", m.View().Content)
	}
}

func TestStaleGenerationIgnored(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0)
	m = withLogs(m, "seasoning.service", "one")
	m = update(m, logLineMsg{unit: "seasoning.service", gen: m.logGen + 1, line: "stale"})

	if strings.Contains(strings.Join(m.logLines, "\n"), "stale") {
		t.Errorf("stale line was appended: %v", m.logLines)
	}
}

func TestLogsViewBack(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0)
	cancelled := false
	m = update(m, snapshotMsg{snap: sampleSnapshot()})
	m = withLogs(m, "seasoning.service", "hello")
	m.logCancel = func() { cancelled = true }

	if !strings.Contains(m.View().Content, "hello") {
		t.Errorf("logs view missing content:\n%s", m.View().Content)
	}

	m = update(m, key(tea.Key{Code: tea.KeyEscape}))
	if m.mode != modeSummary {
		t.Errorf("esc did not return to summary, mode = %v", m.mode)
	}
	if !cancelled {
		t.Errorf("leaving the log view should stop the stream")
	}
}

func TestFollowScrollsToBottom(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0)
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: 12})
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = "line"
	}
	m = withLogs(m, "seasoning.service", lines...)

	if !m.logFollow {
		t.Fatalf("expected to be following")
	}
	if got := m.viewStart(); got == 0 {
		t.Errorf("follow mode should start at the bottom, got start 0")
	}

	m = update(m, key(tea.Key{Code: tea.KeyUp}))
	if m.logFollow {
		t.Errorf("scrolling up should stop following")
	}
	if m.logY >= len(lines) {
		t.Errorf("logY = %d out of range", m.logY)
	}

	m = update(m, key(tea.Key{Code: tea.KeyDown}))
	if !m.logFollow {
		t.Errorf("scrolling back to the bottom should resume following")
	}
}

func TestLogsHorizontalScroll(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0)
	m = update(m, tea.WindowSizeMsg{Width: 80, Height: 40})
	m = withLogs(m, "seasoning.service", "ABCDEFGHIJKLMNOPQRSTUVWXYZ")

	if !strings.Contains(m.View().Content, "logs: seasoning.service") {
		t.Fatalf("unexpected header:\n%s", m.View().Content)
	}
	if !strings.Contains(m.View().Content, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
		t.Errorf("full line not visible at offset 0:\n%s", m.View().Content)
	}

	m = update(m, key(tea.Key{Code: tea.KeyRight}))
	if m.logX != scrollStep {
		t.Fatalf("logX = %d, want %d", m.logX, scrollStep)
	}
	if strings.Contains(m.View().Content, "ABCDEFGH") {
		t.Errorf("expected left side to be panned off:\n%s", m.View().Content)
	}
	if !strings.Contains(m.View().Content, "IJKLMNOPQRSTUVWXYZ") {
		t.Errorf("expected shifted content:\n%s", m.View().Content)
	}

	m = update(m, key(tea.Key{Code: tea.KeyLeft}))
	m = update(m, key(tea.Key{Code: tea.KeyLeft}))
	if m.logX != 0 {
		t.Errorf("logX = %d, want 0", m.logX)
	}
}

func TestLogsHorizontalScrollClamps(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0)
	m = update(m, tea.WindowSizeMsg{Width: 80, Height: 40})
	m = withLogs(m, "seasoning.service", "12345")

	for i := 0; i < 10; i++ {
		m = update(m, key(tea.Key{Code: tea.KeyRight}))
	}
	if m.logX != 5 {
		t.Errorf("logX = %d, want clamped to 5", m.logX)
	}
}

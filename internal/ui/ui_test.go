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
	return collect.Snapshot{
		CollectedAt: time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC),
		Disk:        collect.Disk{Percent: 12, Avail: "24G", Mount: "/"},
		Mem:         collect.Mem{TotalMB: 8192, UsedMB: 2048, AvailableMB: 5500},
		Load:        collect.Load{One: 0.15, Five: 0.42, Fifteen: 0.30, NProc: 4},
		TempC:       48.3,
		TempOK:      true,
		Uptime:      "up 3 days",
		Services: []collect.Service{
			{Unit: "seasoning.service", LoadState: "loaded", ActiveState: "active"},
			{Unit: "seasoning-nightly.service", LoadState: "loaded", ActiveState: "inactive", Result: "success"},
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
	if got := New(context.Background(), collect.Collector{}, 0).refreshEvery; got != defaultRefresh {
		t.Errorf("default refresh = %v, want %v", got, defaultRefresh)
	}
	if got := New(context.Background(), collect.Collector{}, time.Minute).refreshEvery; got != time.Minute {
		t.Errorf("custom refresh = %v, want 1m", got)
	}
}

func TestTickReloadsWhenIdle(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0)
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
	m := New(context.Background(), collect.Collector{}, 0)
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

func TestSummaryShowsHealth(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0)
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: 40})
	m = update(m, snapshotMsg{snap: sampleSnapshot()})

	view := m.View().Content
	for _, want := range []string{"12% used", "48.3C", "seasoning-nightly", "running", "not installed"} {
		if !strings.Contains(view, want) {
			t.Errorf("summary missing %q\n%s", want, view)
		}
	}
}

func TestSummaryShowsError(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0)
	m = update(m, snapshotMsg{err: context.DeadlineExceeded})
	if !strings.Contains(m.View().Content, "error:") {
		t.Errorf("expected error banner, got:\n%s", m.View().Content)
	}
}

func TestNavigationBounds(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0)
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
	m := New(context.Background(), collect.Collector{}, 0)
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
	m := New(context.Background(), collect.Collector{}, 0)
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
	m := New(context.Background(), collect.Collector{}, 0)
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
	m := New(context.Background(), collect.Collector{}, 0)
	m = withLogs(m, "seasoning.service", "one")
	m = update(m, logLineMsg{unit: "seasoning.service", gen: m.logGen + 1, line: "stale"})

	if strings.Contains(strings.Join(m.logLines, "\n"), "stale") {
		t.Errorf("stale line was appended: %v", m.logLines)
	}
}

func TestLogsViewBack(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0)
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
	m := New(context.Background(), collect.Collector{}, 0)
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
	m := New(context.Background(), collect.Collector{}, 0)
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
	m := New(context.Background(), collect.Collector{}, 0)
	m = update(m, tea.WindowSizeMsg{Width: 80, Height: 40})
	m = withLogs(m, "seasoning.service", "12345")

	for i := 0; i < 10; i++ {
		m = update(m, key(tea.Key{Code: tea.KeyRight}))
	}
	if m.logX != 5 {
		t.Errorf("logX = %d, want clamped to 5", m.logX)
	}
}

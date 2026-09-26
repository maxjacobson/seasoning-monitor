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

func key(k tea.Key) tea.Msg { return tea.KeyPressMsg(k) }

func TestSummaryShowsHealth(t *testing.T) {
	m := New(context.Background(), collect.Collector{})
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
	m := New(context.Background(), collect.Collector{})
	m = update(m, snapshotMsg{err: context.DeadlineExceeded})
	if !strings.Contains(m.View().Content, "error:") {
		t.Errorf("expected error banner, got:\n%s", m.View().Content)
	}
}

func TestNavigationBounds(t *testing.T) {
	m := New(context.Background(), collect.Collector{})
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
	if m.selected != 2 {
		t.Errorf("down moved selection to %d, want 2", m.selected)
	}
	m = update(m, key(tea.Key{Text: "j", Code: 'j'}))
	if m.selected != 2 {
		t.Errorf("down past end moved selection to %d, want 2", m.selected)
	}
}

func TestEnterOpensUnitLogs(t *testing.T) {
	m := New(context.Background(), collect.Collector{})
	m = update(m, snapshotMsg{snap: sampleSnapshot()})
	m = update(m, key(tea.Key{Code: tea.KeyEnter}))

	if m.mode != modeLogs {
		t.Fatalf("enter did not open logs, mode = %v", m.mode)
	}
	if m.logUnit != "seasoning.service" {
		t.Errorf("log unit = %q, want seasoning.service", m.logUnit)
	}
}

func TestLogsViewBack(t *testing.T) {
	m := New(context.Background(), collect.Collector{})
	m = update(m, snapshotMsg{snap: sampleSnapshot()})
	m = update(m, key(tea.Key{Code: tea.KeyEnter}))
	m = update(m, logsMsg{unit: "seasoning.service", text: "hello\nworld"})

	if !strings.Contains(m.View().Content, "hello") {
		t.Errorf("logs view missing content:\n%s", m.View().Content)
	}

	m = update(m, key(tea.Key{Code: tea.KeyEscape}))
	if m.mode != modeSummary {
		t.Errorf("esc did not return to summary, mode = %v", m.mode)
	}
}

func TestLogsHorizontalScroll(t *testing.T) {
	m := New(context.Background(), collect.Collector{})
	m = update(m, tea.WindowSizeMsg{Width: 80, Height: 40})
	m = update(m, snapshotMsg{snap: sampleSnapshot()})
	m = update(m, key(tea.Key{Code: tea.KeyEnter}))
	m = update(m, logsMsg{unit: "seasoning.service", text: "ABCDEFGHIJKLMNOPQRSTUVWXYZ"})

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

	// Panning back to zero and beyond stays clamped.
	m = update(m, key(tea.Key{Code: tea.KeyLeft}))
	m = update(m, key(tea.Key{Code: tea.KeyLeft}))
	if m.logX != 0 {
		t.Errorf("logX = %d, want 0", m.logX)
	}
}

func TestLogsHorizontalScrollClamps(t *testing.T) {
	m := New(context.Background(), collect.Collector{})
	m = update(m, tea.WindowSizeMsg{Width: 80, Height: 40})
	m = update(m, snapshotMsg{snap: sampleSnapshot()})
	m = update(m, key(tea.Key{Code: tea.KeyEnter}))
	m = update(m, logsMsg{unit: "seasoning.service", text: "12345"})

	for i := 0; i < 10; i++ {
		m = update(m, key(tea.Key{Code: tea.KeyRight}))
	}
	if m.logX != 5 {
		t.Errorf("logX = %d, want clamped to 5", m.logX)
	}
}

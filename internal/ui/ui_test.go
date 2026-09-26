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
		App:         collect.Service{Name: "app", ActiveState: "active"},
		Services: []collect.Service{
			{Name: "prune", Result: "success", ExitStatus: 0},
			{Name: "new-season-checker", Result: "failed", ExitStatus: 1},
		},
	}
}

func update(m Model, msg tea.Msg) Model {
	next, _ := m.Update(msg)
	return next.(Model)
}

func TestSummaryShowsHealth(t *testing.T) {
	m := New(context.Background(), collect.Collector{})
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: 40})
	m = update(m, snapshotMsg{snap: sampleSnapshot()})

	view := m.View().Content
	for _, want := range []string{"12% used", "48.3C", "prune", "new-season-checker", "failed"} {
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
	m = update(m, tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	if m.selected != 0 {
		t.Errorf("up at top moved selection to %d", m.selected)
	}

	m = update(m, tea.KeyPressMsg(tea.Key{Text: "j", Code: 'j'}))
	if m.selected != 1 {
		t.Errorf("down moved selection to %d, want 1", m.selected)
	}

	m = update(m, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if m.mode != modeLogs {
		t.Errorf("enter did not open logs, mode = %v", m.mode)
	}
	if m.logUnit != "seasoning-prune" {
		t.Errorf("log unit = %q, want seasoning-prune", m.logUnit)
	}
}

func TestLogsViewBack(t *testing.T) {
	m := New(context.Background(), collect.Collector{})
	m = update(m, snapshotMsg{snap: sampleSnapshot()})
	m = update(m, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = update(m, logsMsg{unit: "seasoning-prune", text: "hello\nworld"})

	if !strings.Contains(m.View().Content, "hello") {
		t.Errorf("logs view missing content:\n%s", m.View().Content)
	}

	m = update(m, tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if m.mode != modeSummary {
		t.Errorf("esc did not return to summary, mode = %v", m.mode)
	}
}

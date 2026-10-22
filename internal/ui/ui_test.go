package ui

import (
	"context"
	"errors"
	"io"
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
	m = update(m, logStreamStartMsg{unit: unit, gen: m.logGen, ch: make(chan logItem), cancel: func() {}})
	for _, line := range lines {
		m = update(m, logLineMsg{unit: unit, gen: m.logGen, line: line})
	}
	return m
}

func TestRefreshInterval(t *testing.T) {
	if got := New(context.Background(), collect.Collector{}, 0, 0, 0).refreshEvery; got != defaultRefresh {
		t.Errorf("default refresh = %v, want %v", got, defaultRefresh)
	}
	if got := New(context.Background(), collect.Collector{}, time.Minute, 0, 0).refreshEvery; got != time.Minute {
		t.Errorf("custom refresh = %v, want 1m", got)
	}
	if got := New(context.Background(), collect.Collector{}, 0, time.Minute, 0).statsEvery; got != time.Minute {
		t.Errorf("custom stats refresh = %v, want 1m", got)
	}
	if got := New(context.Background(), collect.Collector{}, 0, 0, 0).statsEvery; got != defaultStatsRefresh {
		t.Errorf("default stats refresh = %v, want %v", got, defaultStatsRefresh)
	}
	if got := New(context.Background(), collect.Collector{}, 0, 0, time.Minute).backupEvery; got != time.Minute {
		t.Errorf("custom backup refresh = %v, want 1m", got)
	}
	if got := New(context.Background(), collect.Collector{}, 0, 0, 0).backupEvery; got != defaultBackupRefresh {
		t.Errorf("default backup refresh = %v, want %v", got, defaultBackupRefresh)
	}
}

func TestTickReloadsWhenIdle(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0, 0)
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
	m := New(context.Background(), collect.Collector{}, 0, 0, 0)
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
		Nginx:       collect.Nginx{Count: 2, LastAt: time.Date(2026, 9, 25, 11, 0, 0, 0, time.UTC), LastOK: true},
		Database:    collect.Database{Size: "104 MB", Connections: 7, OK: true},
	}
}

func sampleBackup() collect.Backup {
	return collect.Backup{
		At:     time.Date(2026, 9, 25, 11, 0, 0, 0, time.UTC),
		Key:    "bucket/x.dump.gz",
		Size:   12860484,
		FromS3: true,
		OK:     true,
	}
}

func withStats(m Model) Model {
	return update(m, statsMsg{stats: sampleStats()})
}

func withBackup(m Model, b collect.Backup) Model {
	return update(m, backupMsg{backup: b})
}

func TestStatsPanelShowsTraffic(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0, 0)
	m = update(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = update(m, snapshotMsg{snap: sampleSnapshot()})
	m = withStats(m)
	m = withBackup(m, sampleBackup())

	view := m.View().Content
	for _, want := range []string{"8,437 requests", "2xx 3704", "5xx 1", "104 MB · 7 conns", "1h ago", "12.3 MB · s3", "1: foo.service"} {
		if !strings.Contains(view, want) {
			t.Errorf("stats panel missing %q\n%s", want, view)
		}
	}
	if n := strings.Count(view, "nginx"); n != 1 {
		t.Errorf("stats panel should have exactly one nginx line, got %d:\n%s", n, view)
	}
}

func TestStatsPanelShowsError(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0, 0)
	m = update(m, snapshotMsg{snap: sampleSnapshot()})
	m = update(m, statsMsg{err: context.DeadlineExceeded})
	if !strings.Contains(m.View().Content, "stats error:") {
		t.Errorf("expected a stats error:\n%s", m.View().Content)
	}
}

func TestProgressBarFollowsWork(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0, 0)
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

	m = update(m, logStreamStartMsg{unit: "seasoning.service", gen: m.logGen, ch: make(chan logItem), cancel: func() {}})
	if m.View().ProgressBar != nil {
		t.Errorf("expected no progress bar once the stream is live")
	}
}

func TestStatsColourReflectsHealth(t *testing.T) {
	snap := sampleSnapshot()

	healthy := New(context.Background(), collect.Collector{}, 0, 0, 0)
	healthy = update(healthy, snapshotMsg{snap: snap})
	healthy = withStats(healthy)
	if !strings.Contains(healthy.View().Content, "38;5;42") {
		t.Errorf("healthy stats should render green:\n%q", healthy.View().Content)
	}

	broken := New(context.Background(), collect.Collector{}, 0, 0, 0)
	broken = update(broken, snapshotMsg{snap: snap})
	s := sampleStats()
	s.Traffic.S5xx = 3
	broken = update(broken, statsMsg{stats: s})
	stale := sampleBackup()
	stale.At = snap.CollectedAt.Add(-40 * time.Hour)
	broken = withBackup(broken, stale)
	if !strings.Contains(broken.View().Content, "38;5;203") {
		t.Errorf("unhealthy stats should render red:\n%q", broken.View().Content)
	}
}

func TestStaleBackupFlagged(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0, 0)
	snap := sampleSnapshot()
	m = update(m, snapshotMsg{snap: snap})
	m = withStats(m)
	stale := sampleBackup()
	stale.At = snap.CollectedAt.Add(-40 * time.Hour)
	m = withBackup(m, stale)

	if !strings.Contains(m.View().Content, "1d ago") {
		t.Errorf("expected a stale backup age:\n%s", m.View().Content)
	}
}

func TestBackupJournalSourceUnmarked(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0, 0)
	m = update(m, snapshotMsg{snap: sampleSnapshot()})
	m = withStats(m)
	journal := sampleBackup()
	journal.FromS3 = false
	journal.Size = 0
	m = withBackup(m, journal)

	view := m.View().Content
	if !strings.Contains(view, "1h ago") {
		t.Errorf("backup line missing age:\n%s", view)
	}
	if strings.Contains(view, "s3") {
		t.Errorf("a journal-sourced backup should not claim S3:\n%s", view)
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
	m := New(context.Background(), collect.Collector{}, 0, 0, 0)
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
	m := New(context.Background(), collect.Collector{}, 0, 0, 0)
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

	m := New(context.Background(), collect.Collector{}, 0, 0, 0)
	m = update(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = update(m, snapshotMsg{snap: snap})

	if !strings.Contains(m.View().Content, "overdue by 5m") {
		t.Errorf("overdue timer should be flagged:\n%s", m.View().Content)
	}
}

func TestSummaryShowsError(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0, 0)
	m = update(m, snapshotMsg{err: context.DeadlineExceeded})
	if !strings.Contains(m.View().Content, "error:") {
		t.Errorf("expected error banner, got:\n%s", m.View().Content)
	}
}

func TestNavigationBounds(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0, 0)
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
	m := New(context.Background(), collect.Collector{}, 0, 0, 0)
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
	m := New(context.Background(), collect.Collector{}, 0, 0, 0)
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
	m := New(context.Background(), collect.Collector{}, 0, 0, 0)
	m = update(m, snapshotMsg{snap: sampleSnapshot()})
	m = withStats(m)
	m = withLogs(m, "seasoning.service", "one")
	m = update(m, logStreamEndMsg{unit: "seasoning.service", gen: m.logGen})

	if m.logStreamOn {
		t.Errorf("stream should have ended")
	}
	if !m.logDone {
		t.Errorf("stream should be marked done")
	}
	view := m.View().Content
	if !strings.Contains(view, "● done") {
		t.Errorf("expected a done indicator:\n%s", view)
	}
	if !strings.Contains(view, "finished successfully") {
		t.Errorf("expected a completion banner:\n%s", view)
	}
	if m.View().ProgressBar != nil {
		t.Errorf("a finished stream should not look busy")
	}
}

func TestStreamFailureMarksFailed(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0, 0)
	m = withLogs(m, "seasoning.service", "boom")
	m = update(m, logStreamEndMsg{unit: "seasoning.service", gen: m.logGen, err: errors.New("exit status 1")})

	view := m.View().Content
	for _, want := range []string{"● failed", "stream failed: exit status 1", "boom"} {
		if !strings.Contains(view, want) {
			t.Errorf("failed stream view missing %q\n%s", want, view)
		}
	}
	if strings.Contains(view, "finished successfully") {
		t.Errorf("a failed stream must not claim success:\n%s", view)
	}
}

func TestDeployCompletionBanner(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0, 0)
	m = update(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = update(m, snapshotMsg{snap: sampleSnapshot()})
	m = update(m, runeKey('d'))
	m, _ = updateCmd(m, runeKey('y'))

	m = update(m, logStreamEndMsg{unit: "deploy", gen: m.logGen})
	if !strings.Contains(m.View().Content, "deploy finished successfully") {
		t.Errorf("expected a deploy completion banner:\n%s", m.View().Content)
	}

	m = update(m, logStreamEndMsg{unit: "deploy", gen: m.logGen, err: errors.New("exit status 1")})
	if !strings.Contains(m.View().Content, "deploy failed: exit status 1") {
		t.Errorf("expected a deploy failure banner:\n%s", m.View().Content)
	}
}

func TestStaleGenerationIgnored(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0, 0)
	m = withLogs(m, "seasoning.service", "one")
	m = update(m, logLineMsg{unit: "seasoning.service", gen: m.logGen + 1, line: "stale"})

	if strings.Contains(strings.Join(m.logLines, "\n"), "stale") {
		t.Errorf("stale line was appended: %v", m.logLines)
	}
}

func TestLogsViewBack(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0, 0)
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
	m := New(context.Background(), collect.Collector{}, 0, 0, 0)
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
	m := New(context.Background(), collect.Collector{}, 0, 0, 0)
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
	m := New(context.Background(), collect.Collector{}, 0, 0, 0)
	m = update(m, tea.WindowSizeMsg{Width: 80, Height: 40})
	m = withLogs(m, "seasoning.service", "12345")

	for range 10 {
		m = update(m, key(tea.Key{Code: tea.KeyRight}))
	}
	if m.logX != 5 {
		t.Errorf("logX = %d, want clamped to 5", m.logX)
	}
}

func runeKey(r rune) tea.Msg {
	return key(tea.Key{Text: string(r), Code: r})
}

func TestDeployAsksForConfirmation(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0, 0)
	m = update(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = update(m, snapshotMsg{snap: sampleSnapshot()})

	m, cmd := updateCmd(m, runeKey('d'))
	if m.mode != modeConfirm {
		t.Fatalf("d should open the confirmation, mode = %v", m.mode)
	}
	if cmd != nil {
		t.Errorf("the confirmation step must not start work yet")
	}
	view := m.View().Content
	for _, want := range []string{"deploy seasoning?", "restarts the Rails service", collect.DeployCommand, "y confirm"} {
		if !strings.Contains(view, want) {
			t.Errorf("confirmation missing %q\n%s", want, view)
		}
	}
}

func TestDeployCancelReturnsToSummary(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0, 0)
	m = update(m, snapshotMsg{snap: sampleSnapshot()})
	m = update(m, runeKey('d'))
	m = update(m, key(tea.Key{Code: tea.KeyEscape}))

	if m.mode != modeSummary {
		t.Errorf("escape should cancel the deploy, mode = %v", m.mode)
	}
	if m.logUnit == "deploy" {
		t.Errorf("cancelling must not start the deploy stream")
	}
}

func TestDeployConfirmStartsStream(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0, 0)
	m = update(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = update(m, snapshotMsg{snap: sampleSnapshot()})
	m = update(m, runeKey('d'))

	m, cmd := updateCmd(m, runeKey('y'))
	if cmd == nil {
		t.Fatalf("confirming should start the deploy stream")
	}
	if m.mode != modeLogs {
		t.Errorf("deploy should open the stream pane, mode = %v", m.mode)
	}
	if m.logUnit != "deploy" || m.logTitle != "deploy: seasoning" {
		t.Errorf("deploy stream identity = %q / %q", m.logUnit, m.logTitle)
	}
	if !strings.Contains(m.View().Content, "deploy: seasoning") {
		t.Errorf("deploy pane missing its title:\n%s", m.View().Content)
	}
}

func TestDeployOnlyOfferedOnAppRow(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0, 0)
	m = update(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = update(m, snapshotMsg{snap: sampleSnapshot()})

	m = update(m, key(tea.Key{Code: tea.KeyDown}))
	if !strings.Contains(m.View().Content, "enter logs") || strings.Contains(m.View().Content, "d deploy") {
		t.Errorf("only the app row should advertise deploy:\n%s", m.View().Content)
	}

	m = update(m, runeKey('d'))
	if m.mode != modeSummary {
		t.Errorf("d on a non-app row must do nothing, mode = %v", m.mode)
	}
}

func TestDeployRestartKeepsDeploying(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0, 0)
	m = update(m, snapshotMsg{snap: sampleSnapshot()})
	m = update(m, runeKey('d'))
	m, _ = updateCmd(m, runeKey('y'))

	if !m.logDeploy {
		t.Fatalf("deploy pane should be flagged as a deploy")
	}
	m, cmd := updateCmd(m, runeKey('r'))
	if cmd == nil {
		t.Errorf("r should restart the deploy stream")
	}
	if !m.logDeploy || m.logTitle != "deploy: seasoning" {
		t.Errorf("r should keep deploying, got %q deploy=%v", m.logTitle, m.logDeploy)
	}
}

func TestOpeningUnitLogsClearsDeployFlag(t *testing.T) {
	m := New(context.Background(), collect.Collector{}, 0, 0, 0)
	m = update(m, snapshotMsg{snap: sampleSnapshot()})
	m = update(m, runeKey('d'))
	m, _ = updateCmd(m, runeKey('y'))
	m = update(m, key(tea.Key{Code: tea.KeyEscape}))

	m, _ = updateCmd(m, key(tea.Key{Code: tea.KeyEnter}))
	if m.logDeploy {
		t.Errorf("journal logs must not be treated as a deploy")
	}
	if m.logTitle != "logs: seasoning.service" {
		t.Errorf("log title = %q", m.logTitle)
	}
}

// closingReader stands in for an ssh stream: it yields the recorded output and
// reports the command's exit status from Close.
type closingReader struct {
	r   *strings.Reader
	err error
}

func (c *closingReader) Read(p []byte) (int, error) { return c.r.Read(p) }
func (c *closingReader) Close() error               { return c.err }

type fakeSource struct {
	deploy io.ReadCloser
}

func (f fakeSource) Snapshot(context.Context) (collect.Snapshot, error) {
	return collect.Snapshot{}, nil
}
func (f fakeSource) Stats(context.Context) (collect.Stats, error)   { return collect.Stats{}, nil }
func (f fakeSource) Backup(context.Context) (collect.Backup, error) { return collect.Backup{}, nil }
func (f fakeSource) StreamLogs(context.Context, string, int) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (f fakeSource) StreamDeploy(context.Context) (io.ReadCloser, error) { return f.deploy, nil }

// drain runs a command and the chain of commands it schedules, so a streamed
// deploy is exercised end to end.
func drain(m Model, cmd tea.Cmd) Model {
	for cmd != nil {
		next, c := m.Update(cmd())
		m = next.(Model)
		cmd = c
	}
	return m
}

func TestDeployStreamReportsExitStatus(t *testing.T) {
	src := fakeSource{deploy: &closingReader{r: strings.NewReader("pulling\n"), err: errors.New("exit status 1")}}
	m := New(context.Background(), src, 0, 0, 0)
	m = update(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = update(m, snapshotMsg{snap: sampleSnapshot()})
	m = update(m, runeKey('d'))
	m, cmd := updateCmd(m, runeKey('y'))

	m = drain(m, cmd)
	if !m.logDone || m.logErr == nil {
		t.Fatalf("expected the stream to finish with an error, done=%v err=%v", m.logDone, m.logErr)
	}
	if !strings.Contains(m.View().Content, "deploy failed: exit status 1") {
		t.Errorf("expected a failure banner:\n%s", m.View().Content)
	}
}

func TestDeployStreamReportsSuccess(t *testing.T) {
	src := fakeSource{deploy: &closingReader{r: strings.NewReader("pulling\n")}}
	m := New(context.Background(), src, 0, 0, 0)
	m = update(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = update(m, snapshotMsg{snap: sampleSnapshot()})
	m = update(m, runeKey('d'))
	m, cmd := updateCmd(m, runeKey('y'))

	m = drain(m, cmd)
	if !m.logDone || m.logErr != nil {
		t.Fatalf("expected a clean finish, done=%v err=%v", m.logDone, m.logErr)
	}
	if !strings.Contains(m.View().Content, "deploy finished successfully") {
		t.Errorf("expected a success banner:\n%s", m.View().Content)
	}
}

func TestNginxFreshnessDrivesColour(t *testing.T) {
	snap := sampleSnapshot()
	s := sampleStats()
	// Isolate nginx: clear the other sources of red in the stats panel.
	s.Traffic.S5xx = 0
	s.FailedUnits = nil

	m := New(context.Background(), collect.Collector{}, 0, 0, 0)
	m = update(m, snapshotMsg{snap: snap})
	m = withBackup(m, sampleBackup())

	s.Nginx = collect.Nginx{Count: 1, LastAt: snap.CollectedAt.Add(-2 * time.Minute), LastOK: true}
	m = update(m, statsMsg{stats: s})
	if !strings.Contains(m.View().Content, "1 error · 2m ago") {
		t.Errorf("nginx line missing age:\n%s", m.View().Content)
	}
	if !strings.Contains(m.View().Content, "38;5;203") {
		t.Errorf("a recent nginx error should be red:\n%s", m.View().Content)
	}

	s.Nginx.LastAt = snap.CollectedAt.Add(-6 * time.Hour)
	m = update(m, statsMsg{stats: s})
	if !strings.Contains(m.View().Content, "1 error · 6h ago") {
		t.Errorf("nginx line missing age:\n%s", m.View().Content)
	}
	if strings.Contains(m.View().Content, "38;5;203") {
		t.Errorf("a stale nginx error should not be red:\n%s", m.View().Content)
	}
	if !strings.Contains(m.View().Content, "38;5;214") {
		t.Errorf("a stale nginx error should be amber:\n%s", m.View().Content)
	}
}

package collect

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/maxjacobson/seasoning-monitor/internal/pi"
)

const sampleOutput = `@@SM:DISK@@
12% 24G /
@@SM:MEM@@
8192 2048 5500
@@SM:LOAD@@
0.15 0.42 0.30 1/234 5678
@@SM:NPROC@@
4
@@SM:TEMP@@
temp=48.3'C
@@SM:UPTIME@@
up 3 days, 4 hours
@@SM:TIMERS@@
[{"next":1790395200000000,"left":1790395200000000,"last":1790308807915788,"passed":472465858216,"unit":"seasoning-nightly.timer","activates":"seasoning-nightly.service"},{"next":null,"last":null,"unit":"unrelated.timer","activates":"unrelated.service"}]
@@SM:NOW@@
1790394840
@@SM:SVC:seasoning-nightly.service@@
LoadState=loaded
ActiveState=inactive
Result=success
ExecMainStatus=0
ExecMainExitTimestamp=Fri 2026-09-25 00:02:06 EDT
@@SM:SVC:seasoning.service@@
LoadState=loaded
ActiveState=active
Result=success
ExecMainStatus=0
ExecMainExitTimestamp=
@@SM:SVC:seasoning-prune.service@@
LoadState=not-found
ActiveState=inactive
Result=success
ExecMainStatus=0
ExecMainExitTimestamp=
`

func TestParseSnapshot(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	snap, err := parseSnapshot(sampleOutput, now)
	if err != nil {
		t.Fatalf("parseSnapshot: %v", err)
	}

	if snap.Disk.Percent != 12 || snap.Disk.Avail != "24G" || snap.Disk.Mount != "/" {
		t.Errorf("disk = %+v", snap.Disk)
	}
	if snap.Mem.TotalMB != 8192 || snap.Mem.UsedMB != 2048 || snap.Mem.AvailableMB != 5500 {
		t.Errorf("mem = %+v", snap.Mem)
	}
	if snap.Load.One != 0.15 || snap.Load.Five != 0.42 || snap.Load.Fifteen != 0.30 || snap.Load.NProc != 4 {
		t.Errorf("load = %+v", snap.Load)
	}
	if !snap.TempOK || snap.TempC != 48.3 {
		t.Errorf("temp = %v ok=%v", snap.TempC, snap.TempOK)
	}
	if snap.Uptime != "up 3 days, 4 hours" {
		t.Errorf("uptime = %q", snap.Uptime)
	}
	if snap.CollectedAt.Unix() != 1790394840 {
		t.Errorf("CollectedAt = %v, want NOW from the host", snap.CollectedAt.Unix())
	}

	if len(snap.Services) != 3 {
		t.Fatalf("got %d services, want 3", len(snap.Services))
	}
	// The app must be sorted first.
	if snap.Services[0].Unit != "seasoning.service" || !snap.Services[0].IsApp() {
		t.Errorf("first service = %+v", snap.Services[0])
	}
	if !snap.Services[0].Running() || !snap.Services[0].OK() {
		t.Errorf("app should be running and ok: %+v", snap.Services[0])
	}
	if snap.Services[0].Label() != "seasoning" {
		t.Errorf("app label = %q", snap.Services[0].Label())
	}
	if snap.Services[0].Timer != nil {
		t.Errorf("the app has no timer: %+v", snap.Services[0].Timer)
	}

	nightly := snap.Services[1]
	if nightly.Unit != "seasoning-nightly.service" || !nightly.OK() {
		t.Errorf("nightly = %+v", nightly)
	}
	if nightly.ExitTime != "Fri 2026-09-25 00:02:06 EDT" {
		t.Errorf("nightly exit time = %q", nightly.ExitTime)
	}
	if nightly.Timer == nil {
		t.Fatalf("nightly should have a timer attached: %+v", nightly)
	}
	if !nightly.Timer.NextOK || nightly.Timer.Next.Unix() != 1790395200 {
		t.Errorf("nightly timer next = %v ok=%v", nightly.Timer.Next, nightly.Timer.NextOK)
	}
	if !nightly.Timer.LastOK || nightly.Timer.Last.Unix() != 1790308807 {
		t.Errorf("nightly timer last = %v ok=%v", nightly.Timer.Last, nightly.Timer.LastOK)
	}
	if nightly.Timer.Unit != "seasoning-nightly.timer" {
		t.Errorf("nightly timer unit = %q", nightly.Timer.Unit)
	}
	if rel := nightly.Timer.Relative(snap.CollectedAt); !strings.Contains(rel, "next in 6m") || !strings.Contains(rel, "ran 23h ago") {
		t.Errorf("nightly timer relative = %q", rel)
	}
}

func TestMissingUnitIsNotOK(t *testing.T) {
	snap, err := parseSnapshot(sampleOutput, time.Now())
	if err != nil {
		t.Fatalf("parseSnapshot: %v", err)
	}
	prune := snap.Services[2]
	if prune.Unit != "seasoning-prune.service" {
		t.Fatalf("third service = %+v", prune)
	}
	if !prune.Missing() {
		t.Errorf("prune should be reported missing: %+v", prune)
	}
	if prune.OK() {
		t.Errorf("a unit that is not installed must never be OK: %+v", prune)
	}
	if prune.Timer != nil {
		t.Errorf("prune should have no timer: %+v", prune.Timer)
	}
}

func TestParseSnapshotHandlesMissingSections(t *testing.T) {
	snap, err := parseSnapshot("", time.Now())
	if err != nil {
		t.Fatalf("parseSnapshot: %v", err)
	}
	if snap.Disk.Mount != "" {
		t.Errorf("expected empty disk, got %+v", snap.Disk)
	}
	if snap.TempOK {
		t.Errorf("expected temp unknown")
	}
	if len(snap.Services) != 0 {
		t.Errorf("expected no services, got %+v", snap.Services)
	}
}

func TestParseSnapshotMalformedNumbers(t *testing.T) {
	out := "@@SM:DISK@@\ngarbage nope\n@@SM:MEM@@\nnot numbers\n@@SM:TEMP@@\ntemp=abc\n"
	snap, err := parseSnapshot(out, time.Now())
	if err != nil {
		t.Fatalf("parseSnapshot: %v", err)
	}
	if snap.Disk.Percent != 0 || snap.Disk.Mount != "" {
		t.Errorf("disk = %+v", snap.Disk)
	}
	if snap.TempOK {
		t.Errorf("temp should be unknown")
	}
}

func TestSnapshotUsesRunner(t *testing.T) {
	runner := pi.FakeRunner{Outputs: map[string]string{snapshotScript: sampleOutput}}
	c := Collector{Runner: runner}
	snap, err := c.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Disk.Percent != 12 {
		t.Errorf("disk = %+v", snap.Disk)
	}
	if snap.CollectedAt.IsZero() {
		t.Errorf("expected CollectedAt to be set")
	}
}

func TestStreamLogs(t *testing.T) {
	cmd := "journalctl -u seasoning.service -n 300 -f --no-pager --output=short-iso"
	runner := pi.FakeRunner{Streams: map[string]string{cmd: "first\nsecond\n"}}
	c := Collector{Runner: runner}

	rc, err := c.StreamLogs(context.Background(), "seasoning.service", 300)
	if err != nil {
		t.Fatalf("StreamLogs: %v", err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "first\nsecond\n" {
		t.Errorf("stream = %q", string(data))
	}
}

func TestStreamDeploy(t *testing.T) {
	runner := pi.FakeRunner{Streams: map[string]string{DeployCommand: "pulling\nrestarting\n"}}
	c := Collector{Runner: runner}

	rc, err := c.StreamDeploy(context.Background())
	if err != nil {
		t.Fatalf("StreamDeploy: %v", err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "pulling\nrestarting\n" {
		t.Errorf("deploy stream = %q", string(data))
	}
}

func TestStreamNginxErrors(t *testing.T) {
	cmd := "tail -n 300 -f /var/log/nginx/error.log | grep --line-buffered -E '\\[(crit|error|alert|emerg)\\]'"
	runner := pi.FakeRunner{Streams: map[string]string{cmd: "2026-09-25 [error] boom\n"}}
	c := Collector{Runner: runner}

	rc, err := c.StreamNginxErrors(context.Background(), 300)
	if err != nil {
		t.Fatalf("StreamNginxErrors: %v", err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "2026-09-25 [error] boom\n" {
		t.Errorf("error stream = %q", string(data))
	}
}

func TestStreamNginxErrorsDefaultLines(t *testing.T) {
	cmd := "tail -n 200 -f /var/log/nginx/error.log | grep --line-buffered -E '\\[(crit|error|alert|emerg)\\]'"
	runner := pi.FakeRunner{Streams: map[string]string{cmd: ""}}
	c := Collector{Runner: runner}

	rc, err := c.StreamNginxErrors(context.Background(), 0)
	if err != nil {
		t.Fatalf("StreamNginxErrors: %v", err)
	}
	defer rc.Close()
}

func TestParseTimersEdgeCases(t *testing.T) {
	empty := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"malformed", "not json"},
		{"empty array", "[]"},
		{"no activates", `[{"unit":"t.timer","activates":"","next":123}]`},
	}
	for _, c := range empty {
		if got := parseTimers(c.in); len(got) != 0 {
			t.Errorf("%s: expected no timers, got %+v", c.name, got)
		}
	}

	// A timer whose times are null is still indexed, just without a schedule.
	got := parseTimers(`[{"unit":"t.timer","activates":"t.service","next":null,"last":0}]`)
	tm, ok := got["t.service"]
	if !ok {
		t.Fatalf("expected t.service to be indexed, got %+v", got)
	}
	if tm.NextOK || tm.LastOK {
		t.Errorf("null times should not produce a schedule: %+v", tm)
	}
	if tm.Relative(time.Now()) != "" {
		t.Errorf("a schedule-less timer should render as empty, got %q", tm.Relative(time.Now()))
	}
}

func TestParseTimersIndexesByActivates(t *testing.T) {
	in := `[{"unit":"a.timer","activates":"a.service","next":2000000,"last":1000000}]`
	got := parseTimers(in)
	tm, ok := got["a.service"]
	if !ok {
		t.Fatalf("expected a.service in %+v", got)
	}
	if tm.Unit != "a.timer" || !tm.NextOK || !tm.LastOK {
		t.Errorf("timer = %+v", tm)
	}
	if tm.Next.Unix() != 2 || tm.Last.Unix() != 1 {
		t.Errorf("next=%v last=%v", tm.Next, tm.Last)
	}
}

func TestTimerRelative(t *testing.T) {
	now := time.Unix(1000, 0)

	overdue := Timer{Next: now.Add(-5 * time.Minute), NextOK: true}
	if !overdue.Overdue(now) {
		t.Errorf("expected the timer to be overdue")
	}
	if got := overdue.Relative(now); !strings.Contains(got, "overdue by 5m") {
		t.Errorf("relative = %q", got)
	}

	upcoming := Timer{Next: now.Add(6 * time.Minute), NextOK: true, Last: now.Add(-23 * time.Hour), LastOK: true}
	if got := upcoming.Relative(now); got != "ran 23h ago  ·  next in 6m" {
		t.Errorf("relative = %q", got)
	}
}

func TestSectionMarker(t *testing.T) {
	cases := []struct {
		in    string
		want  string
		match bool
	}{
		{"@@SM:DISK@@", "DISK", true},
		{"@@SM:SVC:seasoning-prune.service@@", "SVC:seasoning-prune.service", true},
		{"  @@SM:MEM@@  ", "MEM", true},
		{"plain output", "", false},
		{"@@SM:MISSING", "", false},
	}
	for _, c := range cases {
		got, ok := sectionMarker(c.in)
		if ok != c.match || got != c.want {
			t.Errorf("sectionMarker(%q) = %q,%v want %q,%v", c.in, got, ok, c.want, c.match)
		}
	}
}

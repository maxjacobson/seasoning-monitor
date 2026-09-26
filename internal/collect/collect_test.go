package collect

import (
	"context"
	"io"
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
@@SM:TIMER@@
Sat 2026-09-26 00:00:00 EDT 43min Fri 2026-09-25 00:00:07 EDT 23h ago seasoning-nightly.timer seasoning-nightly.service
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

	nightly := snap.Services[1]
	if nightly.Unit != "seasoning-nightly.service" || !nightly.OK() {
		t.Errorf("nightly = %+v", nightly)
	}
	if nightly.ExitTime != "Fri 2026-09-25 00:02:06 EDT" {
		t.Errorf("nightly exit time = %q", nightly.ExitTime)
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

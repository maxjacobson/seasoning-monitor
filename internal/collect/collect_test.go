package collect

import (
	"context"
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
NEXT                        LEFT       LAST                        PASSED  UNIT                     ACTIVATES
Thu 2026-09-25 00:00:00 EDT 5h left    Wed 2026-09-24 00:00:00 EDT 18h ago seasoning-nightly.timer seasoning-nightly.target
@@SM:SVC:app@@
ActiveState=active
Result=success
ExecMainStatus=0
ExecMainExitTimestamp=
@@SM:SVC:prune@@
ActiveState=inactive
Result=success
ExecMainStatus=0
ExecMainExitTimestamp=Wed 2026-09-24 00:00:01 EDT
@@SM:SVC:tmdb-refresh-config@@
ActiveState=inactive
Result=success
ExecMainStatus=0
ExecMainExitTimestamp=Wed 2026-09-24 00:00:03 EDT
@@SM:SVC:tmdb-refresh-shows@@
ActiveState=inactive
Result=success
ExecMainStatus=0
ExecMainExitTimestamp=Wed 2026-09-24 00:00:05 EDT
@@SM:SVC:new-season-checker@@
ActiveState=failed
Result=failed
ExecMainStatus=1
ExecMainExitTimestamp=Wed 2026-09-24 00:00:07 EDT
@@SM:SVC:trim-sessions@@
ActiveState=inactive
Result=success
ExecMainStatus=0
ExecMainExitTimestamp=Wed 2026-09-24 00:00:09 EDT
@@SM:SVC:backup-db@@
ActiveState=inactive
Result=success
ExecMainStatus=0
ExecMainExitTimestamp=Wed 2026-09-24 00:00:11 EDT
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
	if snap.App.ActiveState != "active" || !snap.App.OK() {
		t.Errorf("app = %+v", snap.App)
	}

	if len(snap.Services) != len(ServiceNames) {
		t.Fatalf("got %d services, want %d", len(snap.Services), len(ServiceNames))
	}
	byName := map[string]Service{}
	for _, s := range snap.Services {
		byName[s.Name] = s
	}
	if !byName["prune"].OK() {
		t.Errorf("prune should be ok: %+v", byName["prune"])
	}
	failed, ok := byName["new-season-checker"]
	if !ok {
		t.Fatal("missing new-season-checker")
	}
	if failed.OK() || failed.ExitStatus != 1 || failed.Result != "failed" {
		t.Errorf("new-season-checker = %+v", failed)
	}
	if failed.ExitTime != "Wed 2026-09-24 00:00:07 EDT" {
		t.Errorf("exit time = %q", failed.ExitTime)
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
	if len(snap.Services) != len(ServiceNames) {
		t.Fatalf("got %d services, want %d", len(snap.Services), len(ServiceNames))
	}
	for _, s := range snap.Services {
		if s.OK() {
			t.Errorf("missing service should not be ok: %+v", s)
		}
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

func TestSectionMarker(t *testing.T) {
	cases := []struct {
		in    string
		want  string
		match bool
	}{
		{"@@SM:DISK@@", "DISK", true},
		{"@@SM:SVC:prune@@", "SVC:prune", true},
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

// Package collect gathers a health snapshot from the host running Seasoning.
package collect

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/maxjacobson/seasoning-monitor/internal/pi"
)

// ServiceNames are the nightly systemd services defined in Seasoning's
// PRODUCTION.md. Each runs as its own oneshot service.
var ServiceNames = []string{
	"prune",
	"tmdb-refresh-config",
	"tmdb-refresh-shows",
	"new-season-checker",
	"trim-sessions",
	"backup-db",
}

const sectionPrefix = "@@SM:"

// Snapshot is a point-in-time health reading of the Seasoning host.
type Snapshot struct {
	CollectedAt time.Time
	Disk        Disk
	Mem         Mem
	Load        Load
	TempC       float64
	TempOK      bool
	Uptime      string
	Timer       string
	App         Service
	Services    []Service
}

// Disk is filesystem usage for the root mount.
type Disk struct {
	Percent int
	Avail   string
	Mount   string
}

// Mem is memory usage in megabytes.
type Mem struct {
	TotalMB     int
	UsedMB      int
	AvailableMB int
}

// Load is the system load average plus CPU count.
type Load struct {
	One     float64
	Five    float64
	Fifteen float64
	NProc   int
}

// Service is the last known state of a nightly task.
type Service struct {
	Name        string
	ActiveState string
	Result      string
	ExitStatus  int
	ExitTime    string
}

// OK reports whether the service's last run succeeded.
func (s Service) OK() bool {
	return s.Result == "success" && (s.ExitStatus == 0)
}

// Collector produces snapshots and logs from a Runner.
type Collector struct {
	Runner pi.Runner
}

// Snapshot runs one batched command over ssh and parses the result.
func (c Collector) Snapshot(ctx context.Context) (Snapshot, error) {
	out, err := c.Runner.Run(ctx, snapshotScript)
	if err != nil {
		if trimmed := strings.TrimSpace(out); trimmed != "" {
			return Snapshot{}, fmt.Errorf("%w: %s", err, trimmed)
		}
		return Snapshot{}, err
	}
	return parseSnapshot(out, time.Now())
}

// Logs returns the most recent journal lines for a systemd unit.
func (c Collector) Logs(ctx context.Context, unit string, lines int) (string, error) {
	if lines <= 0 {
		lines = 200
	}
	cmd := fmt.Sprintf("journalctl -u %s -n %d --no-pager --output=short-iso", unit, lines)
	return c.Runner.Run(ctx, cmd)
}

const snapshotScript = `echo '@@SM:DISK@@'; df -hP / | awk 'NR==2 {print $5, $4, $6}'
echo '@@SM:MEM@@'; free -m | awk '/^Mem:/ {print $2, $3, $7}'
echo '@@SM:LOAD@@'; cat /proc/loadavg
echo '@@SM:NPROC@@'; nproc
echo '@@SM:TEMP@@'; vcgencmd measure_temp 2>/dev/null || echo 'temp=N/A'
echo '@@SM:UPTIME@@'; uptime -p 2>/dev/null || uptime
echo '@@SM:TIMER@@'; systemctl list-timers seasoning-nightly.timer --no-pager --no-legend 2>/dev/null || true
echo '@@SM:SVC:app@@'
systemctl show seasoning -p ActiveState -p Result -p ExecMainStatus -p ExecMainExitTimestamp 2>/dev/null || true
for s in prune tmdb-refresh-config tmdb-refresh-shows new-season-checker trim-sessions backup-db; do
  echo "@@SM:SVC:$s@@"
  systemctl show "seasoning-$s" -p ActiveState -p Result -p ExecMainStatus -p ExecMainExitTimestamp 2>/dev/null || true
done`

type sections struct {
	plain map[string]string
	svcs  map[string]map[string]string
}

func parseSections(out string) sections {
	s := sections{plain: map[string]string{}, svcs: map[string]map[string]string{}}
	current := ""
	var buf []string

	flush := func() {
		if current == "" {
			return
		}
		body := strings.TrimSpace(strings.Join(buf, "\n"))
		if name, ok := strings.CutPrefix(current, "SVC:"); ok {
			s.svcs[name] = parseKV(body)
		} else {
			s.plain[current] = body
		}
	}

	for _, line := range strings.Split(out, "\n") {
		if marker, ok := sectionMarker(line); ok {
			flush()
			current = marker
			buf = buf[:0]
			continue
		}
		buf = append(buf, line)
	}
	flush()
	return s
}

func sectionMarker(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	inner, ok := strings.CutPrefix(trimmed, sectionPrefix)
	if !ok {
		return "", false
	}
	inner, ok = strings.CutSuffix(inner, "@@")
	if !ok {
		return "", false
	}
	return inner, true
}

func parseKV(body string) map[string]string {
	kv := map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		kv[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return kv
}

func parseSnapshot(out string, now time.Time) (Snapshot, error) {
	s := parseSections(out)
	snap := Snapshot{CollectedAt: now, Uptime: s.plain["UPTIME"], Timer: s.plain["TIMER"]}

	snap.Disk = parseDisk(s.plain["DISK"])
	snap.Mem = parseMem(s.plain["MEM"])
	snap.Load = parseLoad(s.plain["LOAD"], s.plain["NPROC"])
	snap.TempC, snap.TempOK = parseTemp(s.plain["TEMP"])
	snap.App = serviceFromKV("app", s.svcs["app"])

	for _, name := range ServiceNames {
		snap.Services = append(snap.Services, serviceFromKV(name, s.svcs[name]))
	}
	return snap, nil
}

func serviceFromKV(name string, kv map[string]string) Service {
	if kv == nil {
		return Service{Name: name, ActiveState: "unknown", Result: "unknown", ExitStatus: -1}
	}
	exit, _ := strconv.Atoi(kv["ExecMainStatus"])
	return Service{
		Name:        name,
		ActiveState: kv["ActiveState"],
		Result:      kv["Result"],
		ExitStatus:  exit,
		ExitTime:    kv["ExecMainExitTimestamp"],
	}
}

func parseDisk(s string) Disk {
	fields := strings.Fields(s)
	if len(fields) < 3 {
		return Disk{}
	}
	pct, _ := strconv.Atoi(strings.TrimSuffix(fields[0], "%"))
	return Disk{Percent: pct, Avail: fields[1], Mount: fields[2]}
}

func parseMem(s string) Mem {
	fields := strings.Fields(s)
	if len(fields) < 3 {
		return Mem{}
	}
	total, _ := strconv.Atoi(fields[0])
	used, _ := strconv.Atoi(fields[1])
	avail, _ := strconv.Atoi(fields[2])
	return Mem{TotalMB: total, UsedMB: used, AvailableMB: avail}
}

func parseLoad(s, nproc string) Load {
	fields := strings.Fields(s)
	var load Load
	if len(fields) >= 3 {
		load.One, _ = strconv.ParseFloat(fields[0], 64)
		load.Five, _ = strconv.ParseFloat(fields[1], 64)
		load.Fifteen, _ = strconv.ParseFloat(fields[2], 64)
	}
	load.NProc, _ = strconv.Atoi(strings.TrimSpace(nproc))
	return load
}

func parseTemp(s string) (float64, bool) {
	value, ok := strings.CutPrefix(strings.TrimSpace(s), "temp=")
	if !ok {
		return 0, false
	}
	value = strings.TrimSuffix(value, "'C")
	c, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, false
	}
	return c, true
}

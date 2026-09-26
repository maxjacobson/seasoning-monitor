// Package collect gathers a health snapshot from the host running Seasoning.
package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maxjacobson/seasoning-monitor/internal/pi"
)

const (
	sectionPrefix = "@@SM:"
	appUnit       = "seasoning.service"
)

// Snapshot is a point-in-time health reading of the Seasoning host.
type Snapshot struct {
	CollectedAt time.Time
	Disk        Disk
	Mem         Mem
	Load        Load
	TempC       float64
	TempOK      bool
	Uptime      string
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

// Timer is the state of the nightly systemd timer.
type Timer struct {
	Unit      string
	Activates string
	Next      time.Time
	NextOK    bool
	Last      time.Time
	LastOK    bool
}

// Overdue reports whether the timer's next elapse has already passed.
func (t Timer) Overdue(now time.Time) bool {
	return t.NextOK && !t.Next.After(now)
}

// Relative renders a compact description of the timer relative to now, e.g.
// "ran 23h ago  ·  next in 24m".
func (t Timer) Relative(now time.Time) string {
	var parts []string
	if t.LastOK {
		parts = append(parts, fmt.Sprintf("ran %s ago", shortDuration(now.Sub(t.Last))))
	}
	switch {
	case !t.NextOK:
		// no scheduled elapse to mention
	case t.Next.After(now):
		parts = append(parts, fmt.Sprintf("next in %s", shortDuration(t.Next.Sub(now))))
	default:
		parts = append(parts, fmt.Sprintf("overdue by %s", shortDuration(now.Sub(t.Next))))
	}
	return strings.Join(parts, "  ·  ")
}

func shortDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// Service is the observed state of a `seasoning*` systemd unit.
type Service struct {
	Unit        string
	LoadState   string
	ActiveState string
	Result      string
	ExitStatus  int
	ExitTime    string
	// Timer is the timer that fires this service, if any.
	Timer *Timer
}

// Label is the unit name without its `.service` suffix.
func (s Service) Label() string {
	return strings.TrimSuffix(s.Unit, ".service")
}

// IsApp reports whether this is the long-running Rails application unit.
func (s Service) IsApp() bool {
	return s.Unit == appUnit
}

// Missing reports whether the unit isn't installed on the host. This matters
// because `systemctl show` reports Result=success even for absent units, which
// would otherwise look healthy.
func (s Service) Missing() bool {
	return s.LoadState != "loaded"
}

// Running reports whether the unit is currently active.
func (s Service) Running() bool {
	return s.ActiveState == "active" || s.ActiveState == "activating"
}

// OK reports whether the unit is healthily running or last completed cleanly.
func (s Service) OK() bool {
	if s.Missing() {
		return false
	}
	if s.Running() {
		return true
	}
	return s.Result == "success" && s.ExitStatus == 0
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

// StreamLogs returns a reader over the unit's journal that keeps yielding new
// lines as they are written (`journalctl -f`). Closing the reader stops it.
func (c Collector) StreamLogs(ctx context.Context, unit string, lines int) (io.ReadCloser, error) {
	if lines <= 0 {
		lines = 200
	}
	cmd := fmt.Sprintf("journalctl -u %s -n %d -f --no-pager --output=short-iso", unit, lines)
	return c.Runner.Stream(ctx, cmd)
}

const snapshotScript = `echo '@@SM:DISK@@'; df -hP / | awk 'NR==2 {print $5, $4, $6}'
echo '@@SM:MEM@@'; free -m | awk '/^Mem:/ {print $2, $3, $7}'
echo '@@SM:LOAD@@'; cat /proc/loadavg
echo '@@SM:NPROC@@'; nproc
echo '@@SM:TEMP@@'; vcgencmd measure_temp 2>/dev/null || echo 'temp=N/A'
echo '@@SM:UPTIME@@'; uptime -p 2>/dev/null || uptime
echo '@@SM:TIMERS@@'; systemctl list-timers --all --output=json --no-pager 2>/dev/null || true
echo '@@SM:NOW@@'; date +%s
for u in $(systemctl list-units --all --no-legend --no-pager 'seasoning*.service' | awk '$1 ~ /^seasoning.*\.service$/ {print $1}'); do
  echo "@@SM:SVC:$u@@"
  systemctl show "$u" -p LoadState -p ActiveState -p Result -p ExecMainStatus -p ExecMainExitTimestamp 2>/dev/null || true
done`

type sections struct {
	plain   map[string]string
	svcs    map[string]map[string]string
	svcList []string
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
			s.svcList = append(s.svcList, name)
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
	if secs, err := strconv.ParseInt(strings.TrimSpace(s.plain["NOW"]), 10, 64); err == nil && secs > 0 {
		now = time.Unix(secs, 0)
	}
	snap := Snapshot{CollectedAt: now, Uptime: s.plain["UPTIME"]}

	snap.Disk = parseDisk(s.plain["DISK"])
	snap.Mem = parseMem(s.plain["MEM"])
	snap.Load = parseLoad(s.plain["LOAD"], s.plain["NPROC"])
	snap.TempC, snap.TempOK = parseTemp(s.plain["TEMP"])

	timers := parseTimers(s.plain["TIMERS"])
	for _, unit := range s.svcList {
		svc := serviceFromKV(unit, s.svcs[unit])
		if t, ok := timers[unit]; ok {
			timer := t
			svc.Timer = &timer
		}
		snap.Services = append(snap.Services, svc)
	}
	sortServices(snap.Services)
	return snap, nil
}

// timerJSON mirrors the fields systemd emits for `list-timers --output=json`.
// Next and Last are absolute times in microseconds since the epoch.
type timerJSON struct {
	Unit      string `json:"unit"`
	Activates string `json:"activates"`
	Next      *int64 `json:"next"`
	Last      *int64 `json:"last"`
}

// parseTimers indexes timers by the service unit they activate, so each
// service can find its own schedule.
func parseTimers(s string) map[string]Timer {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var entries []timerJSON
	if err := json.Unmarshal([]byte(s), &entries); err != nil {
		return nil
	}
	out := make(map[string]Timer, len(entries))
	for _, e := range entries {
		if e.Activates == "" {
			continue
		}
		t := Timer{Unit: e.Unit, Activates: e.Activates}
		if e.Next != nil && *e.Next > 0 {
			t.Next = microsToTime(*e.Next)
			t.NextOK = true
		}
		if e.Last != nil && *e.Last > 0 {
			t.Last = microsToTime(*e.Last)
			t.LastOK = true
		}
		out[e.Activates] = t
	}
	return out
}

func microsToTime(us int64) time.Time {
	return time.Unix(us/1_000_000, (us%1_000_000)*1000)
}

// sortServices puts the Rails app first, then the rest alphabetically.
func sortServices(services []Service) {
	sort.SliceStable(services, func(i, j int) bool {
		ai, aj := services[i].IsApp(), services[j].IsApp()
		if ai != aj {
			return ai
		}
		return services[i].Unit < services[j].Unit
	})
}

func serviceFromKV(unit string, kv map[string]string) Service {
	if kv == nil {
		return Service{Unit: unit, LoadState: "not-found", ActiveState: "unknown", Result: "unknown", ExitStatus: -1}
	}
	exit, _ := strconv.Atoi(kv["ExecMainStatus"])
	return Service{
		Unit:        unit,
		LoadState:   kv["LoadState"],
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

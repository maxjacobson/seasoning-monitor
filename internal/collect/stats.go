package collect

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Stats are the slower-moving numbers: traffic, errors, database size, and the
// last backup. They cost more to gather (they scan the journal), so the UI
// refreshes them less often than a Snapshot.
type Stats struct {
	Traffic     Traffic
	FailedUnits []string
	Nginx       Nginx
	Database    Database
	Backup      Backup
}

// Traffic is how many requests Rails served in the window, by status class.
type Traffic struct {
	Window   time.Duration
	Requests int
	S2xx     int
	S3xx     int
	S4xx     int
	S5xx     int
}

// Database is the production database's size and connection count.
type Database struct {
	Size        string
	Connections int
	OK          bool
}

// Nginx is the nginx error log: how many errors the current log holds and when
// the newest one happened. nginx rotates its log daily, so Count is scoped to
// roughly the last day and LastAt is what lets the UI judge freshness.
type Nginx struct {
	Count  int
	LastAt time.Time
	LastOK bool
}

// Summary describes the errors with the newest one's age, e.g. "1 error ·
// 6h ago". It falls back to a bare count when the age couldn't be read.
func (n Nginx) Summary(now time.Time) string {
	if n.Count == 0 {
		return "0 errors"
	}
	noun := "errors"
	if n.Count == 1 {
		noun = "error"
	}
	if !n.LastOK {
		return fmt.Sprintf("%d %s", n.Count, noun)
	}
	return fmt.Sprintf("%d %s · %s ago", n.Count, noun, shortDuration(now.Sub(n.LastAt)))
}

// Backup records the most recent successful database dump.
type Backup struct {
	At  time.Time
	Key string
	OK  bool
}

// Stale reports whether the newest backup is older than expected for a nightly
// job.
func (b Backup) Stale(now time.Time) bool {
	return b.OK && now.Sub(b.At) > 26*time.Hour
}

// Stats runs the traffic/health script over ssh and parses the result.
func (c Collector) Stats(ctx context.Context) (Stats, error) {
	out, err := c.Runner.Run(ctx, statsScript)
	if err != nil {
		if trimmed := strings.TrimSpace(out); trimmed != "" {
			return Stats{}, fmt.Errorf("%w: %s", err, trimmed)
		}
		return Stats{}, err
	}
	return parseStats(out), nil
}

const statsScript = `echo '@@SM:TRAFFIC@@'
journalctl -u seasoning --since "24 hours ago" --no-pager 2>/dev/null | awk '
  /Processing by/ { req++ }
  match($0, /Completed [0-9][0-9][0-9]/) { c = substr($0, RSTART + 10, 3); if (c ~ /^2/) s2++; else if (c ~ /^3/) s3++; else if (c ~ /^4/) s4++; else if (c ~ /^5/) s5++ }
  END { printf "requests=%d s2=%d s3=%d s4=%d s5=%d\n", req + 0, s2 + 0, s3 + 0, s4 + 0, s5 + 0 }
'
echo '@@SM:FAILED@@'
systemctl --failed --no-legend --no-pager 2>/dev/null | awk '{for (i=1;i<=NF;i++) if ($i ~ /\.(service|timer|socket|mount|target|path|device)$/) {print $i; break}}'
echo '@@SM:NGINX@@'
n=$(grep -cE '\[(crit|error|alert|emerg)\]' /var/log/nginx/error.log 2>/dev/null || true); printf 'count=%s\n' "${n:-0}"
last=$(grep -E '\[(crit|error|alert|emerg)\]' /var/log/nginx/error.log 2>/dev/null | tail -1 | cut -d' ' -f1,2)
if [ -n "$last" ]; then printf 'last_epoch=%s\n' "$(date -d "$last" +%s)"; fi
echo '@@SM:DB@@'
psql seasoning_production -h /var/run/postgresql -tAc "select pg_size_pretty(pg_database_size(current_database())) || ' ' || (select count(*) from pg_stat_activity)" 2>/dev/null || true
echo '@@SM:BACKUP@@'
journalctl -u seasoning-nightly --since "7 days ago" -o short-iso --no-pager 2>/dev/null | grep "Backed up to" | tail -1 || true
true`

func parseStats(out string) Stats {
	s := parseSections(out)
	return Stats{
		Traffic:     parseTraffic(s.plain["TRAFFIC"]),
		FailedUnits: parseFailedUnits(s.plain["FAILED"]),
		Nginx:       parseNginx(s.plain["NGINX"]),
		Database:    parseDatabase(s.plain["DB"]),
		Backup:      parseBackup(s.plain["BACKUP"]),
	}
}

func parseNginx(s string) Nginx {
	kv := parseKV(s)
	n := Nginx{Count: atoiTrim(kv["count"])}
	if secs, err := strconv.ParseInt(strings.TrimSpace(kv["last_epoch"]), 10, 64); err == nil && secs > 0 {
		n.LastAt = time.Unix(secs, 0)
		n.LastOK = true
	}
	return n
}

func parseTraffic(s string) Traffic {
	t := Traffic{Window: 24 * time.Hour}
	for _, field := range strings.Fields(s) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		n := atoiTrim(value)
		switch key {
		case "requests":
			t.Requests = n
		case "s2":
			t.S2xx = n
		case "s3":
			t.S3xx = n
		case "s4":
			t.S4xx = n
		case "s5":
			t.S5xx = n
		}
	}
	return t
}

func parseFailedUnits(s string) []string {
	var units []string
	for _, line := range strings.Split(s, "\n") {
		if name := strings.TrimSpace(line); name != "" {
			units = append(units, name)
		}
	}
	return units
}

func parseDatabase(s string) Database {
	fields := strings.Fields(s)
	if len(fields) < 2 {
		return Database{}
	}
	return Database{
		Size:        strings.Join(fields[:len(fields)-1], " "),
		Connections: atoiTrim(fields[len(fields)-1]),
		OK:          true,
	}
}

func parseBackup(s string) Backup {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return Backup{}
	}
	at, err := time.Parse(time.RFC3339, fields[0])
	if err != nil {
		return Backup{}
	}
	key := ""
	for _, field := range fields {
		if rest, ok := strings.CutPrefix(field, "s3://"); ok {
			key = rest
			break
		}
	}
	return Backup{At: at, Key: key, OK: true}
}

func atoiTrim(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// Command seasoning-monitor watches the health of the Seasoning host over ssh.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/maxjacobson/seasoning-monitor/internal/collect"
	"github.com/maxjacobson/seasoning-monitor/internal/pi"
	"github.com/maxjacobson/seasoning-monitor/internal/ui"
)

func main() {
	host := flag.String("host", "pi", "ssh host to query")
	probe := flag.Bool("probe", false, "print a snapshot as plain text and exit")
	once := flag.Bool("once", false, "print a rendered summary once and exit")
	logs := flag.String("logs", "", "print journal for a unit (e.g. seasoning-prune) and exit")
	lines := flag.Int("lines", 300, "number of journal lines to fetch with --logs")
	execCmd := flag.String("exec", "", "run an arbitrary read-only shell command over ssh and exit")
	refresh := flag.Duration("refresh", 5*time.Second, "how often the summary auto-refreshes")
	statsRefresh := flag.Duration("stats-refresh", 30*time.Second, "how often traffic/health stats refresh")
	timeout := flag.Duration("timeout", 30*time.Second, "per-command timeout")
	flag.Parse()

	ctx := context.Background()
	collector := collect.Collector{Runner: pi.SSHRunner{Host: *host}}

	if *execCmd != "" {
		ctx, cancel := context.WithTimeout(ctx, *timeout)
		defer cancel()
		out, err := collector.Runner.Run(ctx, *execCmd)
		fmt.Print(out)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}

	if *logs != "" {
		ctx, cancel := context.WithTimeout(ctx, *timeout)
		defer cancel()
		out, err := collector.Logs(ctx, *logs, *lines)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Print(out)
		return
	}

	if *probe || *once {
		ctx, cancel := context.WithTimeout(ctx, 2**timeout)
		defer cancel()
		snap, err := collector.Snapshot(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		stats, statsErr := collector.Stats(ctx)
		if *probe {
			printProbe(snap, stats, statsErr)
		} else {
			printSummary(snap, stats, statsErr)
		}
		return
	}

	p := tea.NewProgram(ui.New(ctx, collector, *refresh, *statsRefresh))
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func printProbe(s collect.Snapshot, stats collect.Stats, statsErr error) {
	fmt.Printf("collected: %s\n", s.CollectedAt.Format(time.RFC3339))
	fmt.Printf("disk:      %d%% used, %s free on %s\n", s.Disk.Percent, s.Disk.Avail, s.Disk.Mount)
	fmt.Printf("mem:       %d/%d MB used, %d MB available\n", s.Mem.UsedMB, s.Mem.TotalMB, s.Mem.AvailableMB)
	fmt.Printf("load:      %.2f %.2f %.2f across %d cpus\n", s.Load.One, s.Load.Five, s.Load.Fifteen, s.Load.NProc)
	if s.TempOK {
		fmt.Printf("temp:      %.1fF\n", s.TempF())
	} else {
		fmt.Printf("temp:      unknown\n")
	}
	fmt.Printf("uptime:    %s\n", s.Uptime)
	for _, svc := range s.Services {
		state := svc.Result
		if svc.Missing() {
			state = "not installed"
		} else if svc.Running() {
			state = "running"
		}
		line := fmt.Sprintf("service:   %-28s %-13s %s (exit %d)", svc.Unit, state, svc.ActiveState, svc.ExitStatus)
		if svc.Timer != nil {
			if rel := svc.Timer.Relative(s.CollectedAt); rel != "" {
				line += "  ·  " + rel
			}
		}
		fmt.Println(line)
	}
	printStats(stats, statsErr)
}

func printStats(stats collect.Stats, statsErr error) {
	if statsErr != nil {
		fmt.Printf("stats:     error: %s\n", statsErr)
		return
	}
	t := stats.Traffic
	fmt.Printf("traffic:   %d requests (24h): 2xx %d, 3xx %d, 4xx %d, 5xx %d\n", t.Requests, t.S2xx, t.S3xx, t.S4xx, t.S5xx)
	if len(stats.FailedUnits) == 0 {
		fmt.Printf("failed:    0 units\n")
	} else {
		fmt.Printf("failed:    %d units: %s\n", len(stats.FailedUnits), strings.Join(stats.FailedUnits, ", "))
	}
	if stats.Database.OK {
		fmt.Printf("postgres:  %s, %d connections\n", stats.Database.Size, stats.Database.Connections)
	}
	if stats.Backup.OK {
		fmt.Printf("backup:    %s (%s)\n", stats.Backup.At.Format(time.RFC3339), stats.Backup.Key)
	} else {
		fmt.Printf("backup:    none found in 7 days\n")
	}
	fmt.Printf("nginx:     %s\n", stats.Nginx.Summary(time.Now()))
}

func printSummary(s collect.Snapshot, stats collect.Stats, statsErr error) {
	fmt.Printf("seasoning health @ %s\n", s.CollectedAt.Format("15:04:05"))
	fmt.Printf("  disk   %d%% used (%s free on %s)\n", s.Disk.Percent, s.Disk.Avail, s.Disk.Mount)
	if s.Mem.TotalMB > 0 {
		fmt.Printf("  mem    %d%% used (%d/%d MB)\n", s.Mem.UsedMB*100/s.Mem.TotalMB, s.Mem.UsedMB, s.Mem.TotalMB)
	}
	fmt.Printf("  load   %.2f %.2f %.2f (%d cpus)\n", s.Load.One, s.Load.Five, s.Load.Fifteen, s.Load.NProc)
	if s.TempOK {
		fmt.Printf("  temp   %.1fF\n", s.TempF())
	}
	for _, svc := range s.Services {
		status := "FAIL"
		switch {
		case svc.Missing():
			status = "MISSING"
		case svc.Running():
			status = "running"
		case svc.OK():
			status = "ok"
		}
		line := fmt.Sprintf("  %-7s %-28s %s", status, svc.Unit, svc.Result)
		if svc.Timer != nil {
			if rel := svc.Timer.Relative(s.CollectedAt); rel != "" {
				line += "  ·  " + rel
			}
		}
		fmt.Println(line)
	}
	if statsErr != nil {
		fmt.Printf("  stats  error: %s\n", statsErr)
		return
	}
	t := stats.Traffic
	fmt.Printf("  traffic  %d requests (24h): 2xx %d, 3xx %d, 4xx %d, 5xx %d\n", t.Requests, t.S2xx, t.S3xx, t.S4xx, t.S5xx)
	fmt.Printf("  failed   %d units\n", len(stats.FailedUnits))
	if stats.Database.OK {
		fmt.Printf("  postgres %s, %d conns\n", stats.Database.Size, stats.Database.Connections)
	}
	if stats.Backup.OK {
		fmt.Printf("  backup   %s\n", humanAgo(stats.Backup.At, s.CollectedAt))
	} else {
		fmt.Printf("  backup   none found in 7 days\n")
	}
	fmt.Printf("  nginx    %s\n", stats.Nginx.Summary(s.CollectedAt))
}

func humanAgo(at, now time.Time) string {
	d := now.Sub(at)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

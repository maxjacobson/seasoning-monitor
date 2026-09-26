// Command seasoning-monitor watches the health of the Seasoning host over ssh.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
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
		ctx, cancel := context.WithTimeout(ctx, *timeout)
		defer cancel()
		snap, err := collector.Snapshot(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		if *probe {
			printProbe(snap)
		} else {
			printSummary(snap)
		}
		return
	}

	p := tea.NewProgram(ui.New(ctx, collector, *refresh))
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func printProbe(s collect.Snapshot) {
	fmt.Printf("collected: %s\n", s.CollectedAt.Format(time.RFC3339))
	fmt.Printf("disk:      %d%% used, %s free on %s\n", s.Disk.Percent, s.Disk.Avail, s.Disk.Mount)
	fmt.Printf("mem:       %d/%d MB used, %d MB available\n", s.Mem.UsedMB, s.Mem.TotalMB, s.Mem.AvailableMB)
	fmt.Printf("load:      %.2f %.2f %.2f across %d cpus\n", s.Load.One, s.Load.Five, s.Load.Fifteen, s.Load.NProc)
	if s.TempOK {
		fmt.Printf("temp:      %.1fC\n", s.TempC)
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
}

func printSummary(s collect.Snapshot) {
	fmt.Printf("seasoning health @ %s\n", s.CollectedAt.Format("15:04:05"))
	fmt.Printf("  disk   %d%% used (%s free on %s)\n", s.Disk.Percent, s.Disk.Avail, s.Disk.Mount)
	if s.Mem.TotalMB > 0 {
		fmt.Printf("  mem    %d%% used (%d/%d MB)\n", s.Mem.UsedMB*100/s.Mem.TotalMB, s.Mem.UsedMB, s.Mem.TotalMB)
	}
	fmt.Printf("  load   %.2f %.2f %.2f (%d cpus)\n", s.Load.One, s.Load.Five, s.Load.Fifteen, s.Load.NProc)
	if s.TempOK {
		fmt.Printf("  temp   %.1fC\n", s.TempC)
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
}

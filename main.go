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
	timeout := flag.Duration("timeout", 30*time.Second, "per-command timeout")
	flag.Parse()

	ctx := context.Background()
	collector := collect.Collector{Runner: pi.SSHRunner{Host: *host}}

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

	p := tea.NewProgram(ui.New(ctx, collector))
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
	fmt.Printf("timer:     %s\n", strings.Join(strings.Fields(s.Timer), " "))
	fmt.Printf("app:       %s\n", s.App.ActiveState)
	for _, svc := range s.Services {
		fmt.Printf("service:   %-22s %s (exit %d)\n", svc.Name, svc.Result, svc.ExitStatus)
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
	fmt.Printf("  app    %s\n", s.App.ActiveState)
	for _, svc := range s.Services {
		status := "FAIL"
		if svc.OK() {
			status = "ok"
		}
		fmt.Printf("  %-6s %-22s %s\n", status, svc.Name, svc.Result)
	}
}

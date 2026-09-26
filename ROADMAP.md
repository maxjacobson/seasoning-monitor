# Roadmap

Ideas and planned work for seasoning-monitor. Rough order, not a commitment.

## Next

_(nothing queued)_

## Later

- [ ] **Pi throttling / undervoltage** — `vcgencmd get_throttled`, red when the
      current run has been throttled. A common Raspberry Pi failure mode.
- [ ] **Disk growth per day** — track GB/day so a slowly filling disk is
      visible before it's a problem.
- [ ] **Error log view** — a key that opens a filtered (`5xx`, `FATAL`) journal
      or nginx error view, rather than only counts.
- [ ] **Watch the Cloudflare tunnel** (`cloudflared`) alongside nginx.
- [ ] **Pending system package upgrades** — show whether the Pi has OS package
      updates waiting (e.g. `apt list --upgradable`) and how many. Read-only for
      now; installing them is a bigger, mutating step.
- [ ] **Historical traffic** — the journal only keeps ~3 days for this unit, so
      anything past that needs nginx access logs (~14 days) or an app-side
      rollup.

## Waiting on application changes

These are ideas for MJ to consider on the Seasoning side; the monitor can use
them once they exist. Deliberately described, not implemented, here.

- **Nightly status file.** Have the nightly job write a small JSON after it
  finishes (`finished_at`, `duration`, `ok`, and per-step results: backup
  target/bytes, shows refreshed, sessions trimmed). The monitor would read one
  file instead of scanning the journal, and we'd see *what* it did, not just
  that it exited 0.
- **Structured request logging** (e.g. lograge): one JSON line per request,
  making traffic/error counts cheap and exact instead of regexing Rails text.
- **A `/health` endpoint** the monitor can time through nginx (DB connectivity,
  queue depth), giving an end-to-end latency signal.
- **nginx `real_ip` + `$upstream_response_time`** in `log_format`: today every
  request appears as `127.0.0.1` (cloudflared), and there's no latency.
- **Backup verification:** the `seasoning-pi-backer-upper` IAM user can only
  `PutObject`, so we can't confirm an object exists from the Pi. Granting
  `s3:ListBucket` (or `s3:GetObject` on the prefix) would let us verify the
  newest dump is really there.

## Done

- [x] Disk, memory, load, and CPU temperature panel.
- [x] Rails app and `seasoning*` unit health, discovered dynamically.
- [x] Per-unit journal viewer with vertical and horizontal scrolling.
- [x] Live log streaming (`journalctl -f`) with follow, jump to top/bottom.
- [x] A `justfile` for common tasks (`just` runs the TUI).
- [x] Auto-refreshing summary, with the scheduled next run on each unit's row.
- [x] Traffic (24h) with a status breakdown, and 5xx highlighted.
- [x] Health checks: failed units, Postgres size/connections, backup freshness,
      and nginx errors with the newest error's age (red only when recent).
- [x] Deploy the app from the TUI: a `d` binding on the app row, gated behind a
      confirmation, streams `bin/pull-and-release` into the log pane and can be
      cancelled mid-flight.

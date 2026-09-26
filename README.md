# seasoning-monitor

A small TUI that watches the health of the Raspberry Pi that runs
[Seasoning](https://www.seasoning.tv). It shells out to `ssh pi` for read-only
diagnostics, so nothing needs to be installed on the Pi.

## What it shows

- **Disk** usage on `/`
- **Memory** usage
- **Load average** and CPU count
- **CPU temperature** (`vcgencmd measure_temp`)
- **`seasoning*` systemd units** — discovered dynamically, each with its load
  state, active state, and last run result. The Rails app is listed first.
- **Each unit's schedule** — if a timer fires a unit, its row shows when it
  last ran and when it next will (e.g. `ran 23h ago · next in 17m`), turning
  amber if the next run is overdue. Timers are matched to the units they
  activate, so this scales to multiple timers.

Press `enter` on a row to follow its journal live (`journalctl -f`). New lines
stream in as they are written; the header shows `● live` until the stream ends.

## Usage

```sh
just                              # interactive TUI
just run                          # same thing
just list                         # show all recipes
just probe                        # raw snapshot as plain text, then exit
just once                         # one rendered summary, then exit
just logs seasoning-nightly       # follow a unit's journal in the terminal
just exec 'df -h'                 # run a read-only command over ssh, then exit
```

The same commands work through `go run .` with the flags `--probe`, `--once`,
`--logs <unit>`, `--exec '<cmd>'`, `--host <ssh-host>` (default `pi`), and
`--refresh <duration>` (default `5s`).

The summary reloads itself every few seconds (see `--refresh`), showing a
`refreshing...` hint while a load is in flight. Press `r` to refresh on demand.

### Keys

| Key         | Action                          |
| ----------- | ------------------------------- |
| `j` / `k`   | move selection (or scroll logs) |
| `h` / `l`   | pan logs left/right             |
| `g` / `G`   | jump to top / bottom (follow)   |
| `enter`     | follow logs for the selected unit |
| `esc`       | back to the summary             |
| `r`         | refresh (or restart the log stream) || `q`         | quit                            |

## How it works

Everything is gathered in a single `ssh` call that runs one batched shell
script and prints `@@SM:<section>@@` delimited output. The script is piped to
`sh` on the remote side over stdin, so the Pi's login shell (fish) doesn't
matter. Services are listed with `systemctl list-units 'seasoning*.service'`
rather than hardcoded names, and each is checked with `LoadState` so a unit
that isn't installed shows up as missing instead of falsely healthy
(`systemctl show` reports `Result=success` even for absent units). Timers come
from `systemctl list-timers --output=json` and are indexed by the unit they
activate, so a unit's schedule travels with its row. See
`internal/collect/collect.go` for the script and parser.

Logs are streamed, not polled: opening a unit starts `journalctl -f` over ssh,
a goroutine scans its output into a buffered channel, and bubbletea reads one
line at a time. Closing the view cancels the stream. See `internal/ui/ui.go`.

## Development

Common tasks live in the `justfile`. Run `just` to launch the TUI, or
`just list` to see everything.

```sh
just check    # formatting, vet, and tests
just test     # tests only
just fmt      # format
just build    # build the binary
```

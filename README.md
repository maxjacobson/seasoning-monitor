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

Press `enter` on a row to read its journal (`journalctl -u <unit>`).

## Usage

```sh
go run .                          # interactive TUI
go run . --probe                  # raw snapshot as plain text, then exit
go run . --once                   # one rendered summary, then exit
go run . --logs seasoning-nightly # journal for a unit, then exit
go run . --exec 'df -h'           # run a read-only command over ssh, then exit
go run . --host pi                # override the ssh host (default: pi)
```

### Keys

| Key         | Action                          |
| ----------- | ------------------------------- |
| `j` / `k`   | move selection (or scroll logs) |
| `h` / `l`   | pan logs left/right             |
| `enter`     | open logs for the selected unit |
| `esc`       | back to the summary             |
| `r`         | refresh now                     |
| `q`         | quit                            |

## How it works

Everything is gathered in a single `ssh` call that runs one batched shell
script and prints `@@SM:<section>@@` delimited output. The script is piped to
`sh` on the remote side over stdin, so the Pi's login shell (fish) doesn't
matter. Services are listed with `systemctl list-units 'seasoning*.service'`
rather than hardcoded names, and each is checked with `LoadState` so a unit
that isn't installed shows up as missing instead of falsely healthy
(`systemctl show` reports `Result=success` even for absent units). See
`internal/collect/collect.go` for the script and parser.

## Development

```sh
go test ./...
go vet ./...
```

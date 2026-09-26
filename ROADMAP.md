# Roadmap

Ideas and planned work for seasoning-monitor. Rough order, not a commitment.

## Next

- [ ] **Deploy the app from the TUI.** Add a binding (say `d`, only when the
      Rails app row is selected) that runs
      `cd /home/maxwell/Documents/seasoning && bin/pull-and-release` over ssh
      and streams the output into the log pane.
      - That script runs `git pull`, `bundle install`, `npm`, `db:prepare`,
        `assets:precompile`, and `sudo systemctl restart seasoning`, so it
        mutates the host. Gate it behind a confirmation prompt.
      - It ends with `sudo systemctl restart seasoning`; verify the `maxwell`
        user can run that without a TTY/password prompt, otherwise it will
        hang when driven over ssh.

## Later

- [ ] Follow/tail mode for logs (keep appending instead of a fixed snapshot).
- [ ] Watch the Cloudflare tunnel (`cloudflared`) and nginx alongside the app.
- [ ] Show the nightly timer's last fire and next fire as their own rows.
- [ ] Historical view of nightly task results over time.

## Done

- [x] Disk, memory, load, and CPU temperature panel.
- [x] Rails app and `seasoning*` unit health, discovered dynamically.
- [x] Per-unit journal viewer with vertical and horizontal scrolling.

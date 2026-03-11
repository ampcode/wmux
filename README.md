# wmux

`wmux` exposes a tmux session in the browser.

It runs a tmux control-mode client (`tmux -CC`), serves a small web UI, and lets you open a specific pane route like `/p/0`.

## Prerequisites

- Go `1.24+`
- `tmux` installed and available in `PATH`

## Quick Start

1. Start (or create) a tmux session you want to control:

```bash
tmux new-session -d -s webui
```

2. Run wmux:

```bash
go run ./cmd/wmux
```

By default it listens on `127.0.0.1:8080`, targets session `webui`, and uses `ghostty` as the terminal renderer.

3. Open pane links in your browser:

- `http://127.0.0.1:8080/api/state.html` to list panes
- Click a pane link (for example `/p/0`) to open that pane terminal

## Common Run Modes

Use a different tmux target session:

```bash
go run ./cmd/wmux --target-session dev
```

Change listen address:

```bash
go run ./cmd/wmux --listen 0.0.0.0:8080
```

Use a custom tmux binary path:

```bash
go run ./cmd/wmux --tmux-bin /opt/homebrew/bin/tmux
```

Attach wmux to an existing tmux server/socket (for example Overmind):

```bash
go run ./cmd/wmux --target-session dev --tmux-socket-name overmind
```

Or via explicit socket path:

```bash
go run ./cmd/wmux --target-session dev --tmux-socket-path /tmp/overmind.sock
```

## Flags

- `--listen` (default `127.0.0.1:8080`)
- `--target-session` (default `webui`; the only tmux session wmux manages and serves)
- `--static-dir` (optional override for web assets)
- `--tmux-bin` (default `tmux`)
- `--tmux-socket-name` (optional; maps to `tmux -L <name>`)
- `--tmux-socket-path` (optional; maps to `tmux -S <path>`)
- `--term` (default `ghostty`; accepted values: `ghostty`, `xterm`)
- `--restart-backoff` (default `500ms`)
- `--restart-max-backoff` (default `10s`)

`--tmux-socket-name` and `--tmux-socket-path` are mutually exclusive.

## Environment Variables

Every flag can be provided by env var:

- `WMUX_LISTEN`
- `WMUX_TARGET_SESSION`
- `WMUX_STATIC_DIR`
- `WMUX_TMUX_BIN`
- `WMUX_TMUX_SOCKET_NAME`
- `WMUX_TMUX_SOCKET_PATH`
- `WMUX_TERM`
- `WMUX_RESTART_BACKOFF`
- `WMUX_RESTART_MAX_BACKOFF`

Example:

```bash
WMUX_TARGET_SESSION=dev WMUX_LISTEN=127.0.0.1:9090 go run ./cmd/wmux
```

## Notes

- In default socket mode, `wmux` ensures the configured `target-session` exists (same behavior as before).
- When `--tmux-socket-name` or `--tmux-socket-path` is set, `wmux` attaches to that tmux server and serves the existing target session without auto-creating it.
- `wmux` always runs on top of exactly one tmux session.
- If the target socket/session is unavailable, `wmux` keeps running and reports `unavailable` in `/api/state.json` until tmux becomes reachable again.
- Static web assets are embedded by default; `--static-dir` is only needed if you want to serve local asset files.

## Development

Run tests:

```bash
go test ./...
```

Run headless end-to-end tests (real tmux + Playwright):

```bash
npm ci
npx playwright install chromium
npm run test:e2e
```

The E2E harness uses `scripts/setup-e2e-tmux-fixture.sh` to create a deterministic tmux session and pane worker.

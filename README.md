# wmux

`wmux` exposes a tmux session in the browser.

It runs a tmux control-mode client (`tmux -CC`), serves a small web UI, and lets you open a specific pane route like `/t/%13`.

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

By default it listens on `127.0.0.1:8080` and targets session `webui`.

3. Open pane links in your browser:

- `http://127.0.0.1:8080/api/state.html` to list panes
- Click a pane link (for example `/t/%13`) to open that pane terminal

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

## Flags

- `--listen` (default `127.0.0.1:8080`)
- `--target-session` (default `webui`; the only tmux session wmux manages and serves)
- `--static-dir` (optional override for web assets)
- `--tmux-bin` (default `tmux`)
- `--restart-backoff` (default `500ms`)
- `--restart-max-backoff` (default `10s`)

## Environment Variables

Every flag can be provided by env var:

- `WMUX_LISTEN`
- `WMUX_TARGET_SESSION`
- `WMUX_STATIC_DIR`
- `WMUX_TMUX_BIN`
- `WMUX_RESTART_BACKOFF`
- `WMUX_RESTART_MAX_BACKOFF`

Example:

```bash
WMUX_TARGET_SESSION=dev WMUX_LISTEN=127.0.0.1:9090 go run ./cmd/wmux
```

## Notes

- `wmux` ensures the configured `target-session` exists at startup.
- `wmux` always runs on top of exactly one tmux session.
- Static web assets are embedded by default; `--static-dir` is only needed if you want to serve local asset files.

## Development

Run tests:

```bash
go test ./...
```

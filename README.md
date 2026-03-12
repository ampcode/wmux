# wmux

`wmux` exposes one tmux session in the browser.

This README is organized using Diataxis:

- Tutorial: learn by doing.
- How-to guides: solve specific tasks.
- Reference: exact interface details.
- Explanation: design and behavior rationale.

## Tutorial

### Open Your First Pane In The Browser

Prerequisites:

- Go `1.24+`
- `tmux` installed and available in `PATH`

1. Create the default target session if it does not already exist:

```bash
tmux has-session -t webui 2>/dev/null || tmux new-session -d -s webui
```

2. Start wmux:

```bash
go run ./cmd/wmux
```

3. Open the pane index in your browser:

`http://127.0.0.1:8080/api/state.html`

4. Click a pane link (for example `/p/0`).

You should now see and control that tmux pane in the browser.

## How-To Guides

### Serve A Different tmux Session

```bash
go run ./cmd/wmux --target-session dev
```

### Attach To An Existing tmux Socket

Use a socket name (maps to `tmux -L`):

```bash
go run ./cmd/wmux --target-session dev --tmux-socket-name overmind
```

Use an explicit socket path (maps to `tmux -S`):

```bash
go run ./cmd/wmux --target-session dev --tmux-socket-path /tmp/overmind.sock
```

### Change The Listen Address

```bash
go run ./cmd/wmux --listen 0.0.0.0:8080
```

### Use A Different Default Terminal Renderer

```bash
go run ./cmd/wmux --term xterm
```

### Use A Custom tmux Binary

```bash
go run ./cmd/wmux --tmux-bin /opt/homebrew/bin/tmux
```

### Run Automated Tests

```bash
go test ./...
npm ci
npx playwright install chromium
npm run test:e2e
```

The E2E harness uses `scripts/setup-e2e-tmux-fixture.sh` to create and tear down its own deterministic tmux fixture.

### Check Whether The tmux Target Is Currently Unavailable

```bash
curl -s http://127.0.0.1:8080/api/state.json | jq '.unavailable'
```

If the target is unavailable, `wmux` stays up and keeps retrying until tmux is reachable again.

## Reference

### CLI Flags

| Flag | Env Var | Default | Description |
| --- | --- | --- | --- |
| `--listen` | `WMUX_LISTEN` | `127.0.0.1:8080` | HTTP listen address |
| `--target-session` | `WMUX_TARGET_SESSION` | `webui` | tmux session to serve |
| `--static-dir` | `WMUX_STATIC_DIR` | embedded assets | Optional static assets directory |
| `--tmux-bin` | `WMUX_TMUX_BIN` | `tmux` | Path to tmux binary |
| `--tmux-socket-name` | `WMUX_TMUX_SOCKET_NAME` | empty | tmux socket name (`tmux -L`) |
| `--tmux-socket-path` | `WMUX_TMUX_SOCKET_PATH` | empty | tmux socket path (`tmux -S`) |
| `--term` | `WMUX_TERM` | `ghostty` | Default pane-link renderer (`ghostty` or `xterm`) |
| `--restart-backoff` | `WMUX_RESTART_BACKOFF` | `500ms` | Restart backoff base |
| `--restart-max-backoff` | `WMUX_RESTART_MAX_BACKOFF` | `10s` | Restart backoff maximum |

`--tmux-socket-name` and `--tmux-socket-path` are mutually exclusive.

Environment-only example:

```bash
WMUX_TARGET_SESSION=dev WMUX_LISTEN=127.0.0.1:9090 go run ./cmd/wmux
```

### Behavior By Socket Mode

| Mode | Session Auto-Creation |
| --- | --- |
| No socket flag (`default` socket) | `wmux` ensures `target-session` exists |
| `--tmux-socket-name` or `--tmux-socket-path` | `wmux` serves existing target session only |

### HTTP Endpoints

- `GET /`, `GET /api/state`, `GET /api/state.json`, `GET /api/state.html`: target-session hypermedia document.
- `GET /p/{pane_id}`: terminal UI for one pane.
- `GET /api/panes/{pane_id}`: single pane hypermedia document.
- `POST /api/panes`: create a pane in target session.
- `GET /api/contents/{pane_id}`: plain pane capture.
- `GET /api/contents/{pane_id}?escapes=1`: escape-decorated pane capture.
- `GET /ws`: WebSocket endpoint for tmux command/output flow.

### Key State Fields (`/api/state.json`)

`panes[]` entries include:

- `pane_id`
- `pane_index`
- `window_index`
- `window_name`

`unavailable` is optional and appears when tmux is unreachable:

```json
{
  "resource": "wmux",
  "panes": [
    {
      "pane_id": "13",
      "pane_index": 0,
      "window_index": 0,
      "window_name": "editor"
    }
  ],
  "unavailable": {
    "reason": "tmux target unavailable"
  }
}
```

For complete protocol and payload details, see `docs/spec.md`.

## Explanation

`wmux` runs one long-lived `tmux -CC` control-mode client process and projects a single target session into a browser UI.

The one-session model is intentional: pane URLs stay stable, command routing remains unambiguous, and reconnect behavior is deterministic.

Socket targeting supports two operational modes:

- Default socket mode is convenience-oriented and ensures the target session exists.
- Explicit socket mode (`-L` or `-S`) is integration-oriented (for tools like Overmind) and avoids creating sessions on tmux servers that `wmux` does not own.

When tmux is temporarily unavailable, `wmux` does not exit. It continues serving HTTP, reports unavailability in API state, and recovers automatically after backend reconnection.

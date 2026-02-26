# wmux Implementation Spec (Current Behavior)

This document describes what the codebase currently implements.

## Goal

`wmux` runs a single `tmux -CC` control-mode client and exposes a browser terminal for tmux panes over HTTP + WebSocket.

Current UI scope:

- Single full-screen terminal per browser page (`ghostty` by default, `xterm` optional via `term` query parameter).
- Page targets exactly one pane (from URL path `/p/<pane_id>`).
- Multiple web clients are supported concurrently.

## Terminology

- **Target session**: the one tmux session `wmux` ensures at startup and attaches to.
- **tmux pane id**: tmux-native id with `%` prefix (for example `%13`).
- **Public pane id**: tmux pane id without `%` (for example `13`), exposed by HTTP APIs.

## Configuration

Flags (with env var equivalents):

- `--listen` (`WMUX_LISTEN`, default `127.0.0.1:8080`)
- `--target-session` (`WMUX_TARGET_SESSION`, default `webui`)
- `--static-dir` (`WMUX_STATIC_DIR`, default embedded assets)
- `--tmux-bin` (`WMUX_TMUX_BIN`, default `tmux`)
- `--term` (`WMUX_TERM`, default `ghostty`; allowed: `ghostty`, `xterm`)
- `--restart-backoff` (`WMUX_RESTART_BACKOFF`, default `500ms`)
- `--restart-max-backoff` (`WMUX_RESTART_MAX_BACKOFF`, default `10s`)

## Startup Sequence

1. Validate tmux binary with `tmux -V`.
2. Ensure the target session exists (`has-session`, then `new-session -d -s <name>` if missing).
3. Build `wshub` and bind it to a `tmuxproc.Manager`.
4. Start manager loop for `tmux -CC attach-session -t <target-session>`.
5. Start HTTP server.
6. Trigger initial state sync (`list-panes` model query with retry).

## tmux Control-Mode Backend

- Exactly one long-lived `tmux -CC` child process per `wmux` process.
- Process is attached through a PTY (`github.com/creack/pty`).
- Stdout is scanned line-by-line and fed into a parser.
- Client commands are written as newline-terminated tmux command lines.
- On child exit, manager restarts with exponential backoff up to `restart-max-backoff`.

Restart side effects:

- Hub resets parser and in-memory model.
- Hub broadcasts `tmux_state` (empty snapshot) and `tmux_restarted`.
- Hub re-requests pane model state.

## HTTP Endpoints

- `GET /ws`
  - WebSocket endpoint.
- `GET /`
  - Hypermedia API document for the target session.
  - Negotiated by `Accept`:
    - default: `application/json`
    - `text/html` if requested
- `GET /api/state`, `/api/state.json`, `/api/state.html`
  - Same hypermedia document shape as `/`, filtered to target-session panes.
  - `.html` forces HTML representation.
  - `.json` forces JSON representation.
- `GET /api/panes/{pane_id}`
  - Hypermedia document for a single pane.
  - Returns `404` when pane id does not exist in target session.
- `POST /api/panes`
  - Creates a new pane in target session.
  - Request body (`application/json`):
    - `env` (optional object of string values)
    - `cwd` (optional non-blank string)
    - `cmd` (optional `[]string`)
  - Validation:
    - `cwd` cannot be only whitespace
    - env keys must match `[A-Za-z_][A-Za-z0-9_]*`
  - Response:
    - `201 Created`
    - `Location: /api/panes/{pane_id}`
    - body is a pane hypermedia document (`resource: "wmux-pane"`)
- `GET /api/contents/{pane_id}`
  - Raw `text/plain` pane capture for a specific target-session pane id.
  - `?escapes=1|true|yes` returns escape-decorated output.
  - default (no escapes flag): plain capture.
  - returns `404` for unknown pane.
- `GET /api/debug/unicode`
  - Returns latest captured unicode debug report.
- `POST /api/debug/unicode`
  - Stores a unicode debug report payload and augments it with server-side pane captures.
- `GET /p` and `GET /p/{pane_id}`
  - Serves terminal UI (`index.html`).
  - Adds/normalizes `?term=` query (allowed: `ghostty`, `xterm`) via `302` redirect when missing/invalid.
- Other static paths (`/index.html`, `/styles.css`, `/vendor/...`)
  - Served from `--static-dir` or embedded assets.

## Hypermedia JSON Format

Collection-style resources (`/`, `/api/state*`) use:

```json
{
  "resource": "wmux",
  "default_term": "ghostty|xterm",
  "links": [...],
  "actions": [...],
  "panes": [...]
}
```

Pane-style resources (`/api/panes/{pane_id}`, `POST /api/panes`) use:

```json
{
  "resource": "wmux-pane",
  "default_term": "ghostty|xterm",
  "links": [...],
  "actions": [...],
  "panes": [ { ...single pane... } ]
}
```

Link behavior:

- Supports URI templates for follow-up requests:
  - `/p/{pane_id}{?term}`
  - `/api/panes/{pane_id}`
  - `/api/contents/{pane_id}{?escapes}`
- Templated links include concrete examples (`example`) in JSON representation.

Action behavior:

- `create-pane` action is always present and includes:
  - field descriptions (`env`, `cwd`, `cmd`)
  - machine-readable JSON Schema (`actions[].schema`, draft 2020-12)

Per-pane links in `panes[].links`:

- `self` -> `/api/panes/{pane_id}`
- `terminal` -> `/p/{pane_id}?term=<default>`
- `contents` -> `/api/contents/{pane_id}`
- `contents-escaped` -> `/api/contents/{pane_id}?escapes=1`

## Hypermedia HTML Format

The HTML representation for `/` and `/api/state.html` renders:

- link list (templated links shown as code + concrete example links)
- action list
- an interactive Create Pane form (`id="create-pane-form"`)
- per-pane links and metadata

Create Pane form behavior:

- Collects `cwd`, `env` JSON object, and `cmd` JSON string-array fields.
- Submits JSON via `fetch` to `POST /api/panes`.
- Renders response/error text in `#create-pane-result`.

## Path Parameter Rules

`pane_id` parsing for `/api/contents/{pane_id}` and `/api/panes/{pane_id}`:

- segment must exist and be exactly one path segment
- trimmed value must be non-empty
- must not start with `%`

Public pane id is treated as opaque by HTTP handlers (not parsed as integer).

## WebSocket Protocol

All frames are JSON text messages.

### Client -> Server

Only one message type is accepted:

```json
{ "t": "cmd", "argv": ["send-keys", "-t", "%13", "-l", "ls"] }
```

Rules:

- `argv` is converted to one tmux command line using shell-safe quoting.
- Command name is lowercased before dispatch.
- Empty command or invalid command token is rejected.

### Server -> Client

- `tmux_state`
  - Snapshot of parsed model (`windows`, `panes`).
  - Sent immediately on connect and after model changes.
- `tmux_command`
  - Parsed `%begin/%end/%error` command block with header and output lines.
- `tmux_notification`
  - Parsed `%...` notification fields (`name`, `args`, `text`, `value`).
- `pane_output`
  - Decoded pane output stream for `%output` / `%extended-output` notifications.
- `pane_snapshot`
  - Emitted when a pending `capture-pane` response completes.
- `pane_cursor`
  - Emitted when a pending `display-message` cursor query returns the expected marker format.
- `tmux_restarted`
  - Emitted when control process restarts.
- `error`
  - Validation, backend, parse, or JSON decoding errors.

## Command Policy

Server enforces a strict allowlist. Any other command is blocked.

Allowed commands:

- `send-keys`
- `refresh-client`
- `kill-window`
- `list-windows`
- `list-panes`
- `display-message`
- `capture-pane`
- `show-options`

The policy validates command name only; argument-level constraints are not enforced.

## State Model and Sync

The hub keeps an in-memory model (`windows`, `panes`) updated from specially formatted command output lines.

Built-in sync command format:

- `list-panes -a -F "__WMUX___pane\t#{session_name}\t#{pane_id}\t#{window_id}\t#{pane_index}\t#{pane_active}\t#{pane_left}\t#{pane_top}\t#{pane_width}\t#{pane_height}\t#{pane_current_command}\t#{pane_title}"`

Client behavior:

- On WS open, it requests model sync via the same `list-panes` command.
- On tmux notifications related to layout/window/pane/session changes, it schedules another sync.

## Browser UI Behavior

- `index.html` renders one terminal host, no tab bar and no pane grid.
- Route token is read from `/p/<pane_id>`.
- Pane resolution is by exact public pane id.
- If no pane matches, UI logs a warning and does not attach terminal input/output.

Terminal behavior:

- Preferred renderer is ghostty-web with xterm fallback.
- On pane change:
  - Reset terminal.
  - Request `capture-pane -p -e -N -t <tmux-pane-id>` for snapshot.
  - Request `display-message -p -t <tmux-pane-id> "__WMUX_CURSOR\t#{pane_cursor_x}\t#{pane_cursor_y}"`.
- `pane_snapshot` seeds terminal content.
- `pane_cursor` moves cursor with ANSI `CSI row;col H`.
- `pane_output` appends live data for current pane only.

Input and resize:

- Terminal input is translated to `send-keys` commands, usually character-by-character.
- Special mappings:
  - `ESC` -> `Escape`
  - `CR/LF` -> `Enter`
  - space -> `Space`
  - tab -> `Tab`
  - `DEL` -> `BSpace`
- Other characters use `send-keys -l <char>`.
- Window resize triggers `fit()` and then:
  - `refresh-client -C <cols>x<rows>`

## Multi-Client Semantics (Current)

- Multiple browser clients may connect simultaneously.
- Each browser page is independently bound to the pane id in its own URL.
- There is no server-side per-client focus model; pane targeting is explicit in each command from the client.

## Security Model

- No built-in authentication/authorization.
- Deployment is expected behind external access control.
- Command allowlist is still enforced server-side.

## Known Gaps vs Original Full Vision

Not currently implemented:

- Window tab UI.
- Multi-pane grid rendering in one page.
- Raw tmux line passthrough protocol.
- Binary WebSocket input frames.
- Rich UI actions beyond terminal interaction and command-level support in allowlisted tmux commands.

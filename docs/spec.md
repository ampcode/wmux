# wmux Implementation Spec (Current Behavior)

This document describes what the codebase currently implements.

## Goal

`wmux` runs a single `tmux -CC` control-mode client and exposes a browser terminal for tmux panes over HTTP + WebSocket.

Current UI scope:

- Single full-screen terminal per browser page (`ghostty` by default, `xterm` optional via `term` query param).
- Page targets exactly one pane (from URL path `/p/<n>`).
- Multiple web clients are supported concurrently.

## Terminology

- **Target session**: the one tmux session `wmux` ensures at startup and attaches to.
- **Pane number**: numeric pane index from tmux (`#{pane_index}`), used in URLs and HTTP API responses.

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

- `GET /ws`: WebSocket endpoint.
- `GET /`: static assets (embedded or `--static-dir`).
- `GET /p` and `GET /p/*`: serve `index.html` for terminal route.
- `GET /api/state`, `/api/state.json`: JSON pane list for the target session.
- `GET /api/state.html`: simple HTML list of pane links.
- `GET /api/contents/{pane}`: raw `text/plain` pane capture for a specific target-session pane number.
  - Default (`?escapes` omitted or falsey): output from `capture-pane -p -t <pane-id>`.
  - Escaped (`?escapes=1`, `true`, or `yes`): output from `capture-pane -p -e -t <pane-id>`.

`/api/state*` responses are filtered to panes where `session_name == target-session`.

`/api/state*` pane entries expose `pane` (number), not the absolute tmux pane id token.

`/api/contents/{pane}` requires `pane` to exist in the target session and returns `404` otherwise.

### Route Parameter Types and Matchers

The API should treat path parameters as typed values, not raw strings.

Current parameter types:

- `PaneNumber`
  - Used by: `/p/{pane}` and `/api/contents/{pane}`
  - Canonical form: non-negative base-10 integer (examples: `0`, `1`, `12`).
  - Matcher (decoded token): `^[0-9]+$`
  - Accepted URL path forms:
    - raw token in path segment: `0`

Validation and normalization rules for `PaneNumber`:

- Reject empty values.
- Reject values containing `/` after decoding.
- Parse as integer and reject negative values.
- Validate normalized value against `^[0-9]+$`.

### `net/http` Integration (flag-inspired)

Because `net/http` does not provide typed path params directly, route handlers should use a small adapter that is conceptually similar to `flag.Value`:

- Define a parser interface for path params:
  - `Set(string) error` for parsing + validation
  - `String() string` for canonical serialization
- Each parameter type (currently `PaneNumber`) implements this interface.
- Handler flow:
  1. Extract raw segment from request path.
  2. Decode URL escaping once.
  3. Call `Set(raw)` on the typed parameter.
  4. Use typed value in handler logic.
  5. Return `404` for route mismatch (missing/invalid segment shape), `400` for syntactically invalid parameter where route matched but value is invalid.

This keeps parsing logic centralized, testable, and consistent with how `flag` delegates parsing to typed values.

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
- `resize-pane`
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
- Route token is read from `/p/<n>` where `n` is a non-negative integer.
- Pane resolution is by exact pane number (`pane_index`).
- If no pane matches, UI logs a warning and does not attach terminal input/output.

Terminal behavior:

- xterm.js with fit addon and scrollback 10000.
- On pane change:
  - Reset terminal.
  - Request `capture-pane -p -e -t <pane>` for snapshot.
  - Request `display-message -p -t <pane> "__WMUX_CURSOR\t#{pane_cursor_x}\t#{pane_cursor_y}"`.
- `pane_snapshot` seeds terminal content.
- `pane_cursor` moves cursor with ANSI `CSI row;col H`.
- `pane_output` appends live data for current pane only.

Input and resize:

- xterm `onData` is translated to `send-keys` commands, typically one character at a time.
- Special mappings:
  - `ESC` -> `Escape`
  - `CR/LF` -> `Enter`
  - space -> `Space`
  - tab -> `Tab`
  - `DEL` -> `BSpace`
- Other characters use `send-keys -l <char>`.
- Window resize triggers `fit()` and then:
  - `resize-pane -t <pane> -x <cols> -y <rows>`

## Multi-Client Semantics (Current)

- Multiple browser clients may connect simultaneously.
- Each browser page is independently bound to the pane number in its own URL.
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

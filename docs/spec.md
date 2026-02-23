# tmux Control-Mode Web Client (Go + xterm.js) — Full Specification

## Goal

Provide a web client for an existing `tmux` server on the same machine:

- Connect via **tmux control mode** (`tmux -CC ...`)
- Serve an **xterm.js UI over HTTP**
- Use **WebSockets** for live interaction
- Render **one xterm.js instance per tmux pane** (grid view)
- Support **multiple simultaneous web clients** with **per-client focus** that does not interfere across tabs/users
- Allow **killing windows**, otherwise window/pane management is read-only
- Use **xterm.js native scrollback** (do not forward scroll events to tmux)

Non-goals:

- Authentication/authorization handled by an external layer (reverse proxy, VPN, etc.)
- Server does not invent a new API/command language; it speaks tmux commands 1:1

---

## Terminology

- **Target session**: the single tmux session wmux ensures, attaches to, and displays.

---

## Configuration

Command-line flags (or env equivalents):

- `--listen` (default `127.0.0.1:8080`)
- `--target-session <name>` (default `webui`)
- `--static-dir <path>` (bundle or embed by default)
- `--tmux-bin <path>` (default `tmux`)
- `--restart-backoff` (e.g. exponential, max cap)

---

## Startup Sequence

1. Ensure tmux server reachable:
   - Execute `tmux -V` (optional sanity check)
2. Ensure target session exists:
   - If missing: `tmux new-session -d -s <target-session>`
3. Spawn a single long-lived tmux control client for the target session:
   - `tmux -CC attach-session -t <target-session>`
4. Establish stdout line reader + stdin writer for the tmux control client.
5. Start HTTP server:
   - `GET /` serves the web app
   - `GET /ws` upgrades to WebSocket

---

## tmux Control-Mode Backend

### Process Model

- Exactly **one** `tmux -CC` process per server instance (single target session).
- If it dies, **restart**, re-attach, and broadcast a resync event to all web clients.

### I/O Rules

- Read tmux stdout **line-by-line** (newline-delimited protocol).
- Forward client-issued tmux command lines to tmux stdin (newline-terminated).

### Output Broadcasting

- Server broadcasts raw control-mode lines to connected web clients.
- Optional: server may also emit a small wrapper message type for lifecycle events (e.g. `tmux_restarted`), but should not translate tmux protocol.

---

## WebSocket Protocol

### Message Types

All JSON messages are UTF-8 text frames unless noted.

#### Client → Server

1. **tmux command line**
   - `{ "t": "cmd", "line": "send-keys -t %7 -l \"ls -la\\r\"" }`
2. **binary tmux bytes (optional)**
   - Binary WS frames MAY be supported, but by default mouse/app input should be represented as `send-keys` commands (see Mouse section).

#### Server → Client

1. **tmux protocol line**
   - `{ "t": "tmux", "line": "%output %7 ..." }`
2. **lifecycle**
   - `{ "t": "tmux_restarted" }`
   - `{ "t": "error", "message": "..." }`

### Policy / Validation (Server-Side)

To preserve multi-client semantics, the server MUST block commands that change the tmux _client_’s global selection/focus.

- **Denylist (default block):**
  - `select-pane`, `select-window`, `switch-client`, `attach-session` (from WS), `detach-client`, `new-session`, `kill-session`, `rename-session`
  - Any command that changes server-side tmux client state in a way that would cause tabs to fight
- **Allowlist (default allow):**
  - Input & interaction: `send-keys`, `resize-pane`, `kill-window`
  - Read-only queries: `list-windows`, `list-panes`, `display-message`, `capture-pane`, `show-options`
  - Window switching is performed by client-side state + read-only queries (not `select-window`)

Validation can be lightweight (prefix match / tokenization) but MUST be enforced.

---

## Multi-Client Model (Per-Client Focus)

### Requirement

Two clients viewing the same target session:

- Client A focuses pane `%1` and types -> pane `%1` receives input
- Client B focuses pane `%2` and types -> pane `%2` receives input
- Neither client’s focus should affect the other

### Rule

All input MUST be explicitly targeted by pane id:

- `send-keys -t %<pane> ...`
- `resize-pane -t %<pane> ...`
- `kill-window -t @<win> ...`

### Prohibition

Do not rely on tmux’s notion of “current pane/window” in the `-CC` process:

- Do not use `select-pane` / `select-window` to represent web focus

### Where Focus Lives

- Focus is purely **client-side** UI state: `focusedPaneId`.
- Server does not need per-client focus state for correctness.

---

## UI Behavior

### Window Display

- Show **only windows from the target session** in a **tab bar**.
- No UI session windows are displayed.

### Pane Display

- For the currently selected window, render a **grid of xterm.js terminals**, one per pane.
- Click in a pane sets client-local focus to that pane.
- Keyboard input is routed to the focused pane via `send-keys -t %pane ...`.

### Window Kill

- UI includes “kill window” action:
  - `kill-window -t @<window_id>`
- All other window operations are read-only in the UI.

---

## Scrolling & Mouse

### Scrolling

- Use **xterm.js native scrollback**.
- Do **not** forward scroll wheel events to tmux.
- Recommendation: do **not** enable tmux `mouse` option globally, because it tends to capture scrolling for copy-mode.

### Mouse Support (Now)

There are two categories:

1. **UI mouse (focus, split resize)**

- Focus handled locally by the web app.
- Split resize handled by UI -> compute rows/cols -> `resize-pane -t %pane -x <cols> -y <rows>`.

1. **Application mouse (inside terminal apps)**

- xterm.js can emit mouse reporting sequences (often SGR, ASCII).
- Client forwards these sequences to tmux via `send-keys` without requiring tmux mouse mode:
  - For sequences starting with ESC:
    - `send-keys -t %PANE Escape`
    - `send-keys -t %PANE -l "[<...rest...>"`
- If a sequence cannot be represented safely in JSON/UTF-8, client MAY send WS binary, but the preferred baseline is `send-keys`.

---

## Resize Model (Web UI Decides)

- UI computes pane rectangles and converts pixel size to terminal rows/cols using xterm’s measured cell size.
- On layout change or window resize, client emits resize commands:
  - `resize-pane -t %<pane> -x <cols> -y <rows>`
- If tmux layout changes externally (splits/resizes by another tmux client), the web UI updates based on control-mode notifications and/or periodic queries.

---

## State Acquisition & Sync

### Initial Load (Client)

On connect, client should query state using tmux commands (server forwards):

- `list-windows -t <target-session> -F "<format>"`
- `list-panes -t <target-session> -a -F "<format>"`
- `display-message -p -t @<win> "#{window_layout}"` (or equivalent)
- Optional: `capture-pane -t %<pane> -p` to seed terminal content

Client then builds:

- window tab list
- current window pane layout tree/grid mapping
- terminal instances and attaches output streams by pane id

### Live Updates

- Client processes async control-mode notifications to keep state current.
- Pane output lines `%output %<pane> ...` are decoded/handled by client and appended to that pane’s xterm.

### Server Restart / tmux Restart

If the `tmux -CC` process restarts:

- Server sends `{ "t":"tmux_restarted" }`
- Clients re-run initial load queries and re-bind terminals

---

## Error Handling

- Malformed/blocked commands:
  - Server returns `{ "t":"error", "message":"blocked command: select-pane" }`
- tmux command errors (from control-mode `%error`):
  - Server forwards raw tmux line; client surfaces in UI

---

## Security

- This system assumes authn/z is handled externally.
- Server must still enforce the denylist/allowlist to preserve multi-client correctness and reduce blast radius.

---

## Implementation Notes (Suggested Structure)

Go packages:

- `tmuxproc`: spawn/restart, stdin writer, stdout reader
- `wshub`: client connections, broadcast, command forwarding
- `policy`: command validation/allowlist-denylist
- `httpd`: static files + ws endpoint

Client:

- xterm.js per pane
- tab bar for windows
- local focus state
- emits tmux commands (1:1) over WS
- parses tmux control-mode lines to update UI model

# How to develop

We have no users.

Make changes fearlessly instead of deprecating features.

## E2E testing

Use the Playwright test to validate the browser-to-tmux flow headlessly.

Prerequisites:
- `tmux` installed and available in `PATH`
- Node.js/npm available
- Go toolchain available

Commands:
- `go test ./...`
- `npm ci`
- `npx playwright install chromium`
- `npm run test:e2e`

Notes:
- The E2E harness creates and tears down its own tmux session via `scripts/setup-e2e-tmux-fixture.sh`.
- Keep the test headless and deterministic; do not depend on manual browser interaction.

## Interactive testing with `agent-browser`

Use `agent-browser` when you need to manually validate the running web UI in an already-open browser session.

Quick flow:
- Attach to the running browser: `agent-browser --auto-connect tab list`
- If the app is on `/`, switch to pane discovery: `agent-browser --auto-connect open http://localhost:8080/api/state.html`
- Snapshot interactive controls and click a pane link (for example `@e1`):
  - `agent-browser --auto-connect snapshot -i`
  - `agent-browser --auto-connect click @e1`
- Confirm you are on a pane route (`/p/<n>`) and terminal input exists:
  - `agent-browser --auto-connect get url`
  - `agent-browser --auto-connect snapshot -i`

Important behavior:
- `/` alone is usually not interactive for terminal input because the UI needs a pane target from the URL.
- Pane routes look like `/p/0` and are required for terminal interaction.

Minimal interaction assertion:
- `agent-browser --auto-connect fill @e1 "echo AGENT_BROWSER_OK"`
- `agent-browser --auto-connect press Enter`
- `agent-browser --auto-connect get text ".xterm-rows"`
- Verify `AGENT_BROWSER_OK` appears in the terminal output.

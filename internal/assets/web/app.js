const terminalHostEl = document.getElementById("terminal-host");
const terminalRenderer = parseTerminalRenderer(location.search);

const initialTargetPaneId = parseTargetPaneId(location.pathname);

const state = {
  terminalRuntime: null,
  ws: null,
  panes: new Map(),
  targetPaneId: initialTargetPaneId,
  currentPaneId: null,
  termBundle: null,
  resizeTimer: null,
  refreshTimer: null,
};

boot();

async function boot() {
  state.terminalRuntime = await loadTerminalRuntime(terminalRenderer);
  connect();
  window.addEventListener("resize", schedulePaneResize);
}

function parseTargetPaneId(pathname) {
  const m = pathname.match(/^\/p\/([^/]+)$/);
  if (!m) return "";
  return normalizePublicPaneId(m[1]);
}

function parseTerminalRenderer(search) {
  const params = new URLSearchParams(search);
  const value = (params.get("term") || "").trim().toLowerCase();
  return value === "ghostty" ? "ghostty" : "xterm";
}

async function loadTerminalRuntime(renderer) {
  if (renderer === "ghostty") {
    try {
      const ghosttyModule = await import("/vendor/ghostty/ghostty-web.js");
      const ghostty = await ghosttyModule.Ghostty.load("/vendor/ghostty/ghostty-vt.wasm");
      return {
        renderer: "ghostty",
        createTerminal(options) {
          return new ghosttyModule.Terminal({ ...options, ghostty });
        },
        createFitAddon() {
          return new ghosttyModule.FitAddon();
        },
      };
    } catch (err) {
      console.warn("failed to initialize ghostty terminal backend, falling back to xterm", err);
    }
  }
  return createXtermRuntime();
}

function createXtermRuntime() {
  if (typeof Terminal !== "function" || !FitAddon?.FitAddon) {
    throw new Error("xterm runtime is not available");
  }
  return {
    renderer: "xterm",
    createTerminal(options) {
      return new Terminal(options);
    },
    createFitAddon() {
      return new FitAddon.FitAddon();
    },
  };
}

function connect() {
  const proto = location.protocol === "https:" ? "wss" : "ws";
  const ws = new WebSocket(`${proto}://${location.host}/ws`);
  state.ws = ws;

  ws.addEventListener("open", () => {
    requestModelSync();
  });

  ws.addEventListener("close", () => {
    setTimeout(connect, 1000);
  });

  ws.addEventListener("message", (event) => {
    let msg;
    try {
      msg = JSON.parse(event.data);
    } catch {
      return;
    }
    handleServerMessage(msg);
  });
}

function handleServerMessage(msg) {
  if (msg.t === "tmux_command") {
    if (msg.command?.success === false && Array.isArray(msg.command.output) && msg.command.output.length) {
      console.warn(msg.command.output.join("\n"));
    }
    return;
  }

  if (msg.t === "tmux_notification") {
    const name = msg.notification?.name || "";
    if (
      name === "layout-change" ||
      name.startsWith("window-") ||
      name.startsWith("pane-") ||
      name === "sessions-changed" ||
      name === "session-changed" ||
      name === "client-session-changed"
    ) {
      scheduleModelRefresh();
    }
    return;
  }

  if (msg.t === "tmux_state") {
    if (!state.terminalRuntime) return;
    applyState(msg.state);
    return;
  }

  if (msg.t === "pane_snapshot") {
    const snap = msg.pane_snapshot;
    if (!snap || normalizePublicPaneId(snap.pane_id) !== state.currentPaneId || !state.termBundle) return;
    state.termBundle.term.reset();
    const seeded = normalizeSnapshotData(snap.data || "").replace(/\n/g, "\r\n");
    state.termBundle.term.write(seeded);
    return;
  }

  if (msg.t === "pane_cursor") {
    const c = msg.pane_cursor;
    if (!c || normalizePublicPaneId(c.pane_id) !== state.currentPaneId || !state.termBundle) return;
    const row = Math.max(1, Number(c.y || 0) + 1);
    const col = Math.max(1, Number(c.x || 0) + 1);
    state.termBundle.term.write(`\u001b[${row};${col}H`);
    return;
  }

  if (msg.t === "pane_output") {
    const out = msg.pane_output;
    if (!out || normalizePublicPaneId(out.pane_id) !== state.currentPaneId || !state.termBundle) return;
    state.termBundle.term.write(out.data || "");
    return;
  }

  if (msg.t === "tmux_restarted") {
    requestModelSync();
    return;
  }

  if (msg.t === "error") {
    console.warn(msg.message || "unknown error");
  }
}

function applyState(snapshot) {
  state.panes.clear();
  for (const p of snapshot?.panes || []) {
    const paneId = normalizePublicPaneId(p.pane_id);
    if (!paneId) continue;
    state.panes.set(paneId, {
      paneId,
      paneIndex: Number(p.pane_index || 0),
      name: p.name || p.title || "",
      width: Number(p.width || 0),
      height: Number(p.height || 0),
      active: !!p.active,
    });
  }

  let resolved = resolveTargetPane(state.targetPaneId, state.panes);
  if (!resolved) {
    resolved = resolveFallbackPane(state.panes);
    if (resolved) {
      state.targetPaneId = resolved.paneId;
      history.replaceState(null, "", paneURLFor(resolved.paneId));
    }
  }

  if (!resolved) {
    state.currentPaneId = null;
    console.warn(`pane not found: ${state.targetPaneId === "" ? "(missing in URL)" : state.targetPaneId}`);
    return;
  }

  if (!state.termBundle) {
    state.termBundle = createTerminal();
  }

  if (state.currentPaneId !== resolved.paneId) {
    state.currentPaneId = resolved.paneId;
    state.termBundle.term.reset();
    const tmuxPaneId = tmuxPaneTarget(resolved.paneId);
    sendArgv(["capture-pane", "-p", "-e", "-N", "-t", tmuxPaneId]);
    sendArgv(["display-message", "-p", "-t", tmuxPaneId, "__WMUX_CURSOR\t#{pane_cursor_x}\t#{pane_cursor_y}"]);
    schedulePaneResize();
  }
}

function resolveTargetPane(paneId, panes) {
  if (!paneId) return null;
  return panes.get(paneId) || null;
}

function resolveFallbackPane(panes) {
  const all = [...panes.values()];
  if (all.length === 0) return null;
  const active = all.find((pane) => pane.active);
  if (active) return active;
  return all[0];
}

function createTerminal() {
  terminalHostEl.innerHTML = "";

  const node = document.createElement("section");
  node.className = "pane";

  const termNode = document.createElement("div");
  termNode.className = "term";

  node.appendChild(termNode);
  terminalHostEl.appendChild(node);

  const term = state.terminalRuntime.createTerminal({
    convertEol: false,
    fontFamily: '"JetBrains Mono NF", "JetBrains Mono", Menlo, monospace',
    fontSize: 13,
    scrollback: 10000,
    cursorBlink: true,
  });
  const fit = state.terminalRuntime.createFitAddon();
  term.loadAddon(fit);
  term.open(termNode);
  fit.fit();
  term.focus();

  term.onData((data) => {
    if (!state.currentPaneId) return;
    sendInputData(state.currentPaneId, data);
  });

  return { node, term, fit };
}

function sendInputData(paneId, data) {
  if (!data) return;
  const tmuxPaneId = tmuxPaneTarget(paneId);
  for (const ch of data) {
    sendKeyStroke(tmuxPaneId, ch);
  }
}

function sendKeyStroke(tmuxPaneId, ch) {
  if (ch === "\u001b") return sendArgv(["send-keys", "-t", tmuxPaneId, "Escape"]);
  if (ch === "\r" || ch === "\n") return sendArgv(["send-keys", "-t", tmuxPaneId, "Enter"]);
  if (ch === " ") return sendArgv(["send-keys", "-t", tmuxPaneId, "Space"]);
  if (ch === "\t") return sendArgv(["send-keys", "-t", tmuxPaneId, "Tab"]);
  if (ch === "\u007f") return sendArgv(["send-keys", "-t", tmuxPaneId, "BSpace"]);
  sendArgv(["send-keys", "-t", tmuxPaneId, "-l", ch]);
}

function requestModelSync() {
  sendArgv(["list-panes", "-a", "-F", "__WMUX___pane\t#{session_name}\t#{pane_id}\t#{window_id}\t#{pane_index}\t#{pane_active}\t#{pane_left}\t#{pane_top}\t#{pane_width}\t#{pane_height}\t#{pane_current_command}\t#{pane_title}"]);
}

function paneURLFor(paneId) {
  const query = location.search || "";
  return `/p/${encodeURIComponent(paneId)}${query}`;
}

function scheduleModelRefresh() {
  if (state.refreshTimer) return;
  state.refreshTimer = setTimeout(() => {
    state.refreshTimer = null;
    requestModelSync();
  }, 120);
}

function schedulePaneResize() {
  if (!state.termBundle || !state.currentPaneId) return;
  if (state.resizeTimer) clearTimeout(state.resizeTimer);
  state.resizeTimer = setTimeout(() => {
    state.resizeTimer = null;
    state.termBundle.fit.fit();
    const { cols, rows } = state.termBundle.term;
    if (cols > 0 && rows > 0) {
      // Resize the tmux control-mode client itself so pane size tracks browser viewport.
      sendArgv(["refresh-client", "-C", `${cols}x${rows}`]);
    }
  }, 120);
}

function sendArgv(argv) {
  if (!state.ws || state.ws.readyState !== WebSocket.OPEN) return;
  state.ws.send(JSON.stringify({ t: "cmd", argv }));
}

function normalizeSnapshotData(raw) {
  const lines = raw.split("\n");
  while (lines.length > 0 && lines[lines.length - 1] === "") {
    lines.pop();
  }
  return lines.join("\n");
}

function normalizePublicPaneId(paneId) {
  const raw = String(paneId || "").trim();
  if (raw === "") return "";
  return raw.startsWith("%") ? raw.slice(1) : raw;
}

function tmuxPaneTarget(paneId) {
  const normalized = normalizePublicPaneId(paneId);
  if (normalized === "") return "";
  return `%${normalized}`;
}

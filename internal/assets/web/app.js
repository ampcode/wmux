const terminalHostEl = document.getElementById("terminal-host");

const targetPaneNumber = parseTargetPaneNumber(location.pathname);

const state = {
  ws: null,
  panes: new Map(),
  currentPaneId: null,
  termBundle: null,
  resizeTimer: null,
  refreshTimer: null,
};

connect();
window.addEventListener("resize", schedulePaneResize);

function parseTargetPaneNumber(pathname) {
  const m = pathname.match(/^\/p\/(\d+)$/);
  if (!m) return "";
  const num = Number(m[1]);
  if (!Number.isInteger(num) || num < 0) return "";
  return num;
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
    if (name === "layout-change" || name.startsWith("window-") || name.startsWith("pane-") || name === "sessions-changed") {
      scheduleModelRefresh();
    }
    return;
  }

  if (msg.t === "tmux_state") {
    applyState(msg.state);
    return;
  }

  if (msg.t === "pane_snapshot") {
    const snap = msg.pane_snapshot;
    if (!snap || snap.pane_id !== state.currentPaneId || !state.termBundle) return;
    state.termBundle.term.reset();
    const seeded = normalizeSnapshotData(snap.data || "").replace(/\n/g, "\r\n");
    state.termBundle.term.write(seeded);
    return;
  }

  if (msg.t === "pane_cursor") {
    const c = msg.pane_cursor;
    if (!c || c.pane_id !== state.currentPaneId || !state.termBundle) return;
    const row = Math.max(1, Number(c.y || 0) + 1);
    const col = Math.max(1, Number(c.x || 0) + 1);
    state.termBundle.term.write(`\u001b[${row};${col}H`);
    return;
  }

  if (msg.t === "pane_output") {
    const out = msg.pane_output;
    if (!out || out.pane_id !== state.currentPaneId || !state.termBundle) return;
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
    const paneNumber = Number(p.pane_index);
    if (!Number.isInteger(paneNumber) || paneNumber < 0) continue;
    state.panes.set(paneNumber, {
      pane: paneNumber,
      id: p.id,
      name: p.name || p.title || "",
      width: Number(p.width || 0),
      height: Number(p.height || 0),
      active: !!p.active,
    });
  }

  const resolved = resolveTargetPane(targetPaneNumber, state.panes);
  if (!resolved) {
    console.warn(`pane not found: ${targetPaneNumber === "" ? "(missing in URL)" : targetPaneNumber}`);
    return;
  }

  if (!state.termBundle) {
    state.termBundle = createTerminal(resolved.id);
  }

  if (state.currentPaneId !== resolved.id) {
    state.currentPaneId = resolved.id;
    state.termBundle.term.reset();
    sendArgv(["capture-pane", "-p", "-e", "-t", resolved.id]);
    sendArgv(["display-message", "-p", "-t", resolved.id, "__WMUX_CURSOR\t#{pane_cursor_x}\t#{pane_cursor_y}"]);
    schedulePaneResize();
  }
}

function resolveTargetPane(paneNumber, panes) {
  if (!Number.isInteger(paneNumber)) return null;
  return panes.get(paneNumber) || null;
}

function createTerminal(initialPaneId) {
  terminalHostEl.innerHTML = "";

  const node = document.createElement("section");
  node.className = "pane";

  const termNode = document.createElement("div");
  termNode.className = "term";

  node.appendChild(termNode);
  terminalHostEl.appendChild(node);

  const term = new Terminal({
    convertEol: false,
    fontFamily: '"JetBrains Mono NF", "JetBrains Mono", Menlo, monospace',
    fontSize: 13,
    scrollback: 10000,
    allowTransparency: true,
    cursorBlink: true,
  });
  const fit = new FitAddon.FitAddon();
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
  for (const ch of data) {
    sendKeyStroke(paneId, ch);
  }
}

function sendKeyStroke(paneId, ch) {
  if (ch === "\u001b") return sendArgv(["send-keys", "-t", paneId, "Escape"]);
  if (ch === "\r" || ch === "\n") return sendArgv(["send-keys", "-t", paneId, "Enter"]);
  if (ch === " ") return sendArgv(["send-keys", "-t", paneId, "Space"]);
  if (ch === "\t") return sendArgv(["send-keys", "-t", paneId, "Tab"]);
  if (ch === "\u007f") return sendArgv(["send-keys", "-t", paneId, "BSpace"]);
  sendArgv(["send-keys", "-t", paneId, "-l", ch]);
}

function requestModelSync() {
  sendArgv(["list-panes", "-a", "-F", "__WMUX___pane\t#{session_name}\t#{pane_id}\t#{window_id}\t#{pane_index}\t#{pane_active}\t#{pane_left}\t#{pane_top}\t#{pane_width}\t#{pane_height}\t#{pane_current_command}\t#{pane_title}"]);
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
      sendArgv(["resize-pane", "-t", state.currentPaneId, "-x", String(cols), "-y", String(rows)]);
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

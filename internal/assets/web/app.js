const statusEl = document.getElementById("status");
const errorsEl = document.getElementById("errors");
const tabsEl = document.getElementById("tabs");
const paneGridEl = document.getElementById("pane-grid");

const state = {
  ws: null,
  windows: new Map(),
  panes: new Map(),
  terminals: new Map(),
  selectedWindowId: null,
  focusedPaneId: null,
  refreshTimer: null,
  resizeTimer: null,
};

connect();
window.addEventListener("resize", () => {
  schedulePaneResize();
});

function connect() {
  const proto = location.protocol === "https:" ? "wss" : "ws";
  const ws = new WebSocket(`${proto}://${location.host}/ws`);
  state.ws = ws;

  ws.addEventListener("open", () => {
    setStatus("connected");
    clearError();
    requestModelSync();
  });

  ws.addEventListener("close", () => {
    setStatus("disconnected; retrying…");
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
    handleTmuxCommand(msg.command);
    return;
  }
  if (msg.t === "tmux_notification") {
    handleTmuxNotification(msg.notification);
    return;
  }
  if (msg.t === "pane_output") {
    const paneOutput = msg.pane_output;
    if (!paneOutput || !paneOutput.pane_id) return;
    const termBundle = state.terminals.get(paneOutput.pane_id);
    if (!termBundle) return;
    termBundle.term.write(paneOutput.data || "");
    return;
  }
  if (msg.t === "pane_snapshot") {
    const snap = msg.pane_snapshot;
    if (!snap || !snap.pane_id) return;
    const termBundle = state.terminals.get(snap.pane_id);
    if (!termBundle) return;
    termBundle.term.reset();
    const seeded = (snap.data || "").replace(/\n/g, "\r\n");
    termBundle.term.write(seeded);
    return;
  }
  if (msg.t === "tmux_state") {
    applyState(msg.state);
    return;
  }
  if (msg.t === "tmux_restarted") {
    setStatus("tmux restarted; syncing…");
    requestModelSync();
    return;
  }
  if (msg.t === "error") {
    setError(msg.message || "unknown error");
  }
}

function handleTmuxCommand(command) {
  if (!command || !Array.isArray(command.output)) return;
  if (command.success === false && command.output.length) {
    setError(command.output.join("\n"));
  }
}

function handleTmuxNotification(notification) {
  if (!notification || !notification.name) return;
  const name = notification.name;
  if (name === "layout-change" || name.startsWith("window-") || name.startsWith("pane-") || name === "sessions-changed") {
    scheduleModelRefresh();
    return;
  }

  if (name === "window-renamed") {
    const [windowId, newName] = [notification.args?.[0], notification.text || ""];
    if (windowId && state.windows.has(windowId)) {
      const win = state.windows.get(windowId);
      state.windows.set(windowId, { ...win, name: newName || win.name });
      renderTabs();
    }
  }
}

function applyState(snapshot) {
  if (!snapshot) return;

  state.windows.clear();
  state.panes.clear();

  for (const w of snapshot.windows || []) {
    state.windows.set(w.id, {
      id: w.id,
      index: Number(w.index || 0),
      name: w.name || "",
    });
  }

  for (const p of snapshot.panes || []) {
    state.panes.set(p.id, {
      id: p.id,
      windowId: p.window_id,
      paneIndex: Number(p.pane_index || 0),
      active: !!p.active,
      left: Number(p.left || 0),
      top: Number(p.top || 0),
      width: Number(p.width || 0),
      height: Number(p.height || 0),
      title: p.title || "",
    });
  }

  if (state.focusedPaneId && !state.panes.has(state.focusedPaneId)) {
    state.focusedPaneId = null;
  }
  if (!state.focusedPaneId) {
    const activePane = [...state.panes.values()].find((p) => p.active);
    if (activePane) {
      state.focusedPaneId = activePane.id;
    }
  }
  if (state.selectedWindowId && !state.windows.has(state.selectedWindowId)) {
    state.selectedWindowId = null;
  }
  if (!state.selectedWindowId) {
    const first = [...state.windows.values()].sort((a, b) => a.index - b.index)[0];
    if (first) {
      state.selectedWindowId = first.id;
    }
  }

  renderTabs();
  renderPanes();
}

function requestModelSync() {
  sendArgv(["list-windows", "-F", "__WMUX___win\t#{window_id}\t#{window_index}\t#{window_name}"]);
  sendArgv(["list-panes", "-a", "-F", "__WMUX___pane\t#{pane_id}\t#{window_id}\t#{pane_index}\t#{pane_active}\t#{pane_left}\t#{pane_top}\t#{pane_width}\t#{pane_height}\t#{pane_title}"]);
}

function scheduleModelRefresh() {
  if (state.refreshTimer) return;
  state.refreshTimer = setTimeout(() => {
    state.refreshTimer = null;
    requestModelSync();
  }, 120);
}

function renderTabs() {
  const wins = [...state.windows.values()].sort((a, b) => a.index - b.index);
  if (!wins.length) {
    tabsEl.innerHTML = "";
    return;
  }
  if (!state.selectedWindowId || !state.windows.has(state.selectedWindowId)) {
    state.selectedWindowId = wins[0].id;
  }

  tabsEl.innerHTML = "";
  for (const win of wins) {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "tab" + (win.id === state.selectedWindowId ? " active" : "");
    btn.textContent = `${win.index}: ${win.name}`;
    btn.addEventListener("click", () => {
      state.selectedWindowId = win.id;
      renderTabs();
      renderPanes();
    });

    const kill = document.createElement("span");
    kill.className = "kill";
    kill.textContent = "×";
    kill.title = "kill window";
    kill.addEventListener("click", (ev) => {
      ev.stopPropagation();
      sendArgv(["kill-window", "-t", win.id]);
    });

    btn.appendChild(kill);
    tabsEl.appendChild(btn);
  }
}

function renderPanes() {
  const panes = [...state.panes.values()]
    .filter((p) => p.windowId === state.selectedWindowId)
    .sort((a, b) => a.paneIndex - b.paneIndex);

  for (const [id, bundle] of state.terminals) {
    if (!panes.find((p) => p.id === id)) {
      bundle.term.dispose();
      bundle.node.remove();
      state.terminals.delete(id);
    }
  }

  if (!panes.length) {
    paneGridEl.innerHTML = "";
    state.terminals.clear();
    return;
  }

  const maxW = Math.max(...panes.map((p) => p.left + p.width));
  const maxH = Math.max(...panes.map((p) => p.top + p.height));
  paneGridEl.style.minWidth = `${maxW * 9 + 20}px`;
  paneGridEl.style.minHeight = `${maxH * 19 + 28}px`;

  for (const pane of panes) {
    let bundle = state.terminals.get(pane.id);
    if (!bundle) {
      bundle = createPaneTerminal(pane);
      state.terminals.set(pane.id, bundle);
    }

    bundle.node.classList.toggle("focused", pane.id === state.focusedPaneId);
    bundle.node.style.left = `${pane.left * 9}px`;
    bundle.node.style.top = `${pane.top * 19}px`;
    bundle.node.style.width = `${pane.width * 9}px`;
    bundle.node.style.height = `${pane.height * 19 + 24}px`;
    bundle.label.textContent = `${pane.id} · ${pane.title || "pane"}`;
  }

  schedulePaneResize();
}

function createPaneTerminal(pane) {
  const node = document.createElement("section");
  node.className = "pane";

  const header = document.createElement("div");
  header.className = "pane-header";
  const label = document.createElement("span");
  header.appendChild(label);

  const termNode = document.createElement("div");
  termNode.className = "term";

  node.appendChild(header);
  node.appendChild(termNode);
  paneGridEl.appendChild(node);

  const term = new Terminal({
    convertEol: false,
    fontFamily: "JetBrains Mono, Menlo, monospace",
    fontSize: 13,
    scrollback: 10000,
    allowTransparency: true,
    cursorBlink: true,
  });
  const fit = new FitAddon.FitAddon();
  term.loadAddon(fit);
  term.open(termNode);
  fit.fit();

  node.addEventListener("mousedown", () => {
    state.focusedPaneId = pane.id;
    renderPanes();
    term.focus();
  });

  term.onData((data) => {
    if (state.focusedPaneId !== pane.id) {
      return;
    }
    sendInputData(pane.id, data);
  });

  sendArgv(["capture-pane", "-p", "-S", "-2000", "-E", "-", "-t", pane.id]);

  return { node, term, fit, label, paneId: pane.id };
}

function sendInputData(paneId, data) {
  if (!data) return;
  for (const ch of data) {
    sendKeyStroke(paneId, ch);
  }
}

function sendKeyStroke(paneId, ch) {
  if (ch === "\u001b") {
    sendArgv(["send-keys", "-t", paneId, "Escape"]);
    return;
  }
  if (ch === "\r" || ch === "\n") {
    sendArgv(["send-keys", "-t", paneId, "Enter"]);
    return;
  }
  if (ch === " ") {
    sendArgv(["send-keys", "-t", paneId, "Space"]);
    return;
  }
  if (ch === "\t") {
    sendArgv(["send-keys", "-t", paneId, "Tab"]);
    return;
  }
  if (ch === "\u007f") {
    sendArgv(["send-keys", "-t", paneId, "BSpace"]);
    return;
  }
  sendArgv(["send-keys", "-t", paneId, "-l", ch]);
}

function schedulePaneResize() {
  if (state.resizeTimer) clearTimeout(state.resizeTimer);
  state.resizeTimer = setTimeout(() => {
    state.resizeTimer = null;
    for (const [paneId, bundle] of state.terminals) {
      bundle.fit.fit();
      const { cols, rows } = bundle.term;
      if (cols > 0 && rows > 0) {
        sendArgv(["resize-pane", "-t", paneId, "-x", String(cols), "-y", String(rows)]);
      }
    }
  }, 180);
}

function sendArgv(argv) {
  if (!state.ws || state.ws.readyState !== WebSocket.OPEN) return;
  state.ws.send(JSON.stringify({ t: "cmd", argv }));
}

function setStatus(msg) {
  statusEl.textContent = msg;
}

function setError(msg) {
  errorsEl.textContent = msg;
}

function clearError() {
  errorsEl.textContent = "";
}

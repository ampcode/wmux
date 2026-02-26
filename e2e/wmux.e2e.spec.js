const { test, expect } = require('@playwright/test');
const { execFileSync, spawn } = require('node:child_process');
const net = require('node:net');
const path = require('node:path');

function sh(command, args, opts = {}) {
  return execFileSync(command, args, {
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
    ...opts,
  }).trim();
}

function hasCommand(name) {
  try {
    sh('bash', ['-lc', `command -v ${name}`]);
    return true;
  } catch {
    return false;
  }
}

async function waitFor(check, timeoutMs, intervalMs = 200) {
  const deadline = Date.now() + timeoutMs;
  let lastError = null;
  while (Date.now() < deadline) {
    try {
      const value = await check();
      if (value) return value;
    } catch (err) {
      lastError = err;
    }
    await new Promise((resolve) => setTimeout(resolve, intervalMs));
  }
  if (lastError) throw lastError;
  throw new Error('timed out waiting for condition');
}

async function getFreePort() {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.on('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const address = server.address();
      if (!address || typeof address === 'string') {
        server.close(() => reject(new Error('failed to resolve dynamic port')));
        return;
      }
      const { port } = address;
      server.close((err) => {
        if (err) {
          reject(err);
          return;
        }
        resolve(port);
      });
    });
  });
}

test.describe.configure({ mode: 'serial' });

const missing = [];
if (!hasCommand('tmux')) missing.push('tmux');
if (!hasCommand('go')) missing.push('go');

const shouldSkip = missing.length > 0;
const skipReason = `missing required commands: ${missing.join(', ')}`;

let sessionName;
let paneId;
let publicPaneId;
let port;
let baseURL;
let wmuxProc;

test.beforeAll(async () => {
  test.skip(shouldSkip, skipReason);

  const nonce = `${Date.now()}-${Math.floor(Math.random() * 1_000_000)}`;
  sessionName = `wmux-e2e-${nonce}`;
  paneId = sh('bash', ['./scripts/setup-e2e-tmux-fixture.sh', sessionName]);
  publicPaneId = paneId.replace(/^%/, '');

  port = await getFreePort();
  baseURL = `http://127.0.0.1:${port}`;

  wmuxProc = spawn(
    'go',
    ['run', './cmd/wmux', '--listen', `127.0.0.1:${port}`, '--target-session', sessionName],
    { stdio: ['ignore', 'pipe', 'pipe'] }
  );

  let logs = '';
  wmuxProc.stdout.on('data', (chunk) => {
    logs += chunk.toString();
  });
  wmuxProc.stderr.on('data', (chunk) => {
    logs += chunk.toString();
  });

  let wmuxExited = false;
  wmuxProc.on('exit', (code, signal) => {
    wmuxExited = true;
    logs += `\n[wmux exited] code=${code} signal=${signal}`;
  });

  await waitFor(async () => {
    if (wmuxExited) return false;
    try {
      const res = await fetch(`${baseURL}/api/state.json`);
      if (!res.ok) return false;
      const body = await res.json();
      return (
        Array.isArray(body.panes) &&
        body.panes.some((p) => p.pane_id === publicPaneId && p.session_name === sessionName)
      );
    } catch {
      return false;
    }
  }, 20_000, 250).catch((err) => {
    throw new Error(`wmux failed to become ready: ${err.message}\n${logs}`);
  });
});

test.afterAll(async () => {
  if (wmuxProc && !wmuxProc.killed) {
    wmuxProc.kill('SIGTERM');
    await new Promise((resolve) => setTimeout(resolve, 500));
  }

  if (sessionName) {
    try {
      sh('tmux', ['kill-session', '-t', sessionName]);
    } catch {
      // Session may already be gone.
    }
  }
});

test('headless flow covers state, pane attach, input mapping, and resize', async ({ page, request }) => {
  test.skip(shouldSkip, skipReason);

  await page.addInitScript(() => {
    const NativeWebSocket = window.WebSocket;
    const sent = [];
    class TrackingWebSocket extends NativeWebSocket {
      send(data) {
        try {
          sent.push(JSON.parse(String(data)));
        } catch {
          // Ignore frames that are not JSON command envelopes.
        }
        return super.send(data);
      }
    }
    window.WebSocket = TrackingWebSocket;
    window.__wmuxSent = sent;
  });

  const stateRes = await request.get(`${baseURL}/api/state.json`);
  expect(stateRes.ok()).toBeTruthy();
  const state = await stateRes.json();
  expect(Array.isArray(state.panes)).toBeTruthy();
  expect(state.panes.some((p) => p.pane_id === publicPaneId)).toBeTruthy();

  const rootRes = await request.get(`${baseURL}/`);
  expect(rootRes.ok()).toBeTruthy();
  const root = await rootRes.json();
  expect(Array.isArray(root.links)).toBeTruthy();
  expect(root.links.some((l) => l.rel === 'create-pane' && l.href === '/api/panes' && l.method === 'POST')).toBeTruthy();
  expect(Array.isArray(root.actions)).toBeTruthy();
  expect(root.actions.some((a) => a.name === 'create-pane' && a.href === '/api/panes' && a.method === 'POST')).toBeTruthy();
  expect(Array.isArray(root.panes)).toBeTruthy();
  expect(root.panes.some((p) => p.pane_id === publicPaneId)).toBeTruthy();

  const rootHTML = await request.get(`${baseURL}/`, { headers: { Accept: 'text/html' } });
  expect(rootHTML.ok()).toBeTruthy();
  await expect(rootHTML.text()).resolves.toContain('id="create-pane-form"');

  const stateHTML = await request.get(`${baseURL}/api/state.html`);
  expect(stateHTML.ok()).toBeTruthy();
  await expect(stateHTML.text()).resolves.toContain(`/p/${publicPaneId}`);

  const browserLogs = [];
  page.on('console', (msg) => browserLogs.push(`[console:${msg.type()}] ${msg.text()}`));
  page.on('pageerror', (err) => browserLogs.push(`[pageerror] ${err.message}`));

  await page.goto(`${baseURL}/p/${publicPaneId}?term=xterm`);
  const termReady = await waitFor(async () => {
    const count = await page.locator('#terminal-host .term').count();
    return count > 0;
  }, 15_000).catch(() => false);

  if (!termReady) {
    throw new Error(`terminal never mounted; browser logs:\n${browserLogs.join('\n')}`);
  }

  await expect
    .poll(async () => page.locator('.xterm-rows').count())
    .toBeGreaterThan(0);

  const baselineTerminalText = await page.locator('.xterm-rows').innerText();
  const baselineCapture = sh('tmux', ['capture-pane', '-pt', paneId, '-S', '-120']);

  const externalMarker = `__WMUX_EXTERNAL_${Date.now()}__`;
  sh('tmux', ['send-keys', '-t', paneId, externalMarker, 'Enter']);

  await expect
    .poll(async () => {
      const txt = await page.locator('.xterm-rows').innerText();
      return txt;
    })
    .not.toBe(baselineTerminalText);

  await expect
    .poll(async () => page.locator('.xterm-rows').innerText())
    .toContain(externalMarker);

  await waitFor(() => {
    const cap = sh('tmux', ['capture-pane', '-pt', paneId, '-S', '-120']);
    return cap !== baselineCapture && cap.includes(externalMarker);
  }, 10_000);

  const inputMarker = `WMUXE2E${Date.now()}`;
  const expectedQuotedInput = `__WMUX_INPUT_Q__:$'${inputMarker}ac\\td'`;

  await page.keyboard.type(`${inputMarker}ab`);
  await page.keyboard.press('Backspace');
  await page.keyboard.type('c');
  await page.keyboard.press('Tab');
  await page.keyboard.type('d');
  await page.keyboard.press('Enter');

  await waitFor(() => {
    const cap = sh('tmux', ['capture-pane', '-pt', paneId, '-S', '-80']);
    return cap.includes(expectedQuotedInput);
  }, 10_000);

  const sentArgv = await page.evaluate(() => window.__wmuxSent.map((m) => m.argv || []));
  expect(sentArgv.some((argv) => argv[0] === 'send-keys' && argv.includes('BSpace'))).toBeTruthy();
  expect(sentArgv.some((argv) => argv[0] === 'send-keys' && argv.includes('Tab'))).toBeTruthy();
  expect(sentArgv.some((argv) => argv[0] === 'send-keys' && argv.includes('Enter'))).toBeTruthy();
  expect(sentArgv.some((argv) => argv[0] === 'send-keys' && argv.includes('-l') && argv.includes('a'))).toBeTruthy();

  await page.setViewportSize({ width: 1400, height: 900 });
  await expect
    .poll(async () => {
      const messages = await page.evaluate(() => window.__wmuxSent.map((m) => m.argv || []));
      return messages.some((argv) => argv[0] === 'refresh-client' && argv.includes('-C'));
    })
    .toBeTruthy();
});

test('api can create pane with env cwd and cmd', async ({ request }) => {
  test.skip(shouldSkip, skipReason);

  const workerPath = path.resolve(__dirname, '..', 'scripts', 'e2e-pane-worker.sh');
  const marker = `WMUX_CREATE_${Date.now()}`;
  const cwd = path.resolve(__dirname, '..');

  const createRes = await request.post(`${baseURL}/api/panes`, {
    data: {
      env: { WMUX_E2E_ENV: marker },
      cwd,
      cmd: ['bash', '-lc', `printf '__WMUX_CREATE__:%s|%s\\n' "$PWD" "$WMUX_E2E_ENV"; exec bash "${workerPath}"`],
    },
  });
  expect(createRes.status()).toBe(201);

  const created = await createRes.json();
  expect(created.resource).toBe('wmux-pane');
  expect(Array.isArray(created.panes)).toBeTruthy();
  expect(created.panes.length).toBe(1);
  const createdPaneId = created.panes[0].pane_id;
  expect(typeof createdPaneId).toBe('string');
  expect(createdPaneId.length).toBeGreaterThan(0);

  const location = createRes.headers()['location'];
  expect(location).toBe(`/api/panes/${createdPaneId}`);

  await expect
    .poll(async () => {
      const res = await request.get(`${baseURL}/api/state.json`);
      if (!res.ok()) return false;
      const body = await res.json();
      return Array.isArray(body.panes) && body.panes.some((p) => p.pane_id === createdPaneId);
    })
    .toBeTruthy();

  await expect
    .poll(async () => {
      const res = await request.get(`${baseURL}/api/contents/${createdPaneId}`);
      if (!res.ok()) return '';
      return await res.text();
    })
    .toContain(`__WMUX_CREATE__:${cwd}|${marker}`);
});

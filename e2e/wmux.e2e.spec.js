const { test, expect } = require('@playwright/test');
const { execFileSync, spawn } = require('node:child_process');

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

test.describe.configure({ mode: 'serial' });

const missing = [];
if (!hasCommand('tmux')) missing.push('tmux');
if (!hasCommand('go')) missing.push('go');

const shouldSkip = missing.length > 0;
const skipReason = `missing required commands: ${missing.join(', ')}`;

let sessionName;
let paneId;
let port;
let baseURL;
let wmuxProc;

test.beforeAll(async () => {
  test.skip(shouldSkip, skipReason);

  const nonce = `${Date.now()}-${Math.floor(Math.random() * 1_000_000)}`;
  sessionName = `wmux-e2e-${nonce}`;
  paneId = sh('bash', ['./scripts/setup-e2e-tmux-fixture.sh', sessionName]);

  port = 19000 + Math.floor(Math.random() * 1000);
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

  await waitFor(async () => {
    try {
      const res = await fetch(`${baseURL}/api/state.json`);
      if (!res.ok) return false;
      const body = await res.json();
      return Array.isArray(body.panes) && body.panes.some((p) => p.id === paneId);
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
  expect(state.panes.some((p) => p.id === paneId)).toBeTruthy();

  const stateHTML = await request.get(`${baseURL}/api/state.html`);
  expect(stateHTML.ok()).toBeTruthy();
  await expect(stateHTML.text()).resolves.toContain(`/t/${paneId}`);

  const browserLogs = [];
  page.on('console', (msg) => browserLogs.push(`[console:${msg.type()}] ${msg.text()}`));
  page.on('pageerror', (err) => browserLogs.push(`[pageerror] ${err.message}`));

  await page.goto(`${baseURL}/t/${paneId}`);
  const termReady = await waitFor(async () => {
    const count = await page.locator('#terminal-host .term').count();
    return count > 0;
  }, 15_000).catch(() => false);

  if (!termReady) {
    throw new Error(`terminal never mounted; browser logs:\n${browserLogs.join('\n')}`);
  }

  await expect
    .poll(async () => {
      const txt = await page.locator('.xterm-rows').innerText();
      return txt;
    })
    .toContain('__WMUX_E2E_READY__');

  sh('tmux', ['send-keys', '-t', paneId, '__WMUX_EXTERNAL__', 'Enter']);
  await expect
    .poll(async () => {
      const txt = await page.locator('.xterm-rows').innerText();
      return txt;
    })
    .toContain('__WMUX_INPUT_Q__:__WMUX_EXTERNAL__');

  await page.keyboard.type('ab');
  await page.keyboard.press('Backspace');
  await page.keyboard.type('c');
  await page.keyboard.press('Tab');
  await page.keyboard.type('d');
  await page.keyboard.press('Enter');

  await waitFor(() => {
    const cap = sh('tmux', ['capture-pane', '-pt', paneId, '-S', '-80']);
    return cap.includes("__WMUX_INPUT_Q__:$'ac\\td'");
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
      return messages.some((argv) => argv[0] === 'resize-pane' && argv.includes('-x') && argv.includes('-y'));
    })
    .toBeTruthy();
});

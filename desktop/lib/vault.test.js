'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const net = require('net');
const os = require('os');
const path = require('path');

const { localPorts, freePort, vaultState, vaultEnv, startVault } = require('./vault');
const { createBoundedLogWriter } = require('./boundedLog');

function listenOn(port) {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.once('error', reject);
    server.listen(port, '127.0.0.1', () => resolve(server));
  });
}

function closeServer(server) {
  return new Promise((resolve) => server.close(() => resolve()));
}

async function findFreePortBlock(start, count) {
  for (let base = start; base < start + 1000; base += count) {
    const servers = [];
    try {
      for (let i = 0; i < count; i++) {
        servers.push(await listenOn(base + i));
      }
      for (const s of servers) await closeServer(s);
      return base;
    } catch (_) {
      for (const s of servers) await closeServer(s);
    }
  }
  throw new Error(`Could not find ${count} consecutive free ports from ${start}`);
}

test('localPorts reserves distinct agent, workspace, and Vault ports even when defaults are busy', async () => {
  const base = await findFreePortBlock(48100, 6);
  const defaults = { agent: base, workspace: base + 1, vault: base + 2 };

  // All defaults free.
  const clean = await localPorts({}, defaults);
  assert.deepEqual(clean, { agent: base, workspace: base + 1, vault: base + 2, moved: false });

  // Workspace default busy: workspace must skip Vault's port instead of clashing,
  // and saved agent/Vault ports do not move.
  const holdWorkspace = await listenOn(base + 1);
  try {
    const res = await localPorts({ agent: base, vault: base + 2 }, defaults);
    assert.equal(res.agent, base);
    assert.equal(res.vault, base + 2);
    assert.equal(res.workspace, base + 3);
    assert.equal(res.moved, false);
    assert.equal(new Set([res.agent, res.workspace, res.vault]).size, 3);
  } finally {
    await closeServer(holdWorkspace);
  }

  // Agent default busy: agent shifts onto workspace's default, so workspace must
  // skip both agent and Vault.
  const holdAgent = await listenOn(base);
  try {
    const res = await localPorts({}, defaults);
    assert.equal(res.agent, base + 1);
    assert.equal(res.vault, base + 2);
    assert.equal(res.workspace, base + 3);
    assert.equal(new Set([res.agent, res.workspace, res.vault]).size, 3);
  } finally {
    await closeServer(holdAgent);
  }

  // Saved Vault port on workspace's default: workspace must pick another free port.
  const res = await localPorts({ agent: base, vault: base + 1 }, defaults);
  assert.equal(res.agent, base);
  assert.equal(res.vault, base + 1);
  assert.equal(res.workspace, base + 2);
  assert.equal(res.moved, false);
});

test('startVault spawns Vault once without GATEWAY_BOOTSTRAP_ONLY, waits for /healthz, and logs output', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'awvault-'));
  try {
    const port = await freePort(48200);
    const bin = path.join(dir, 'fake-vault.sh');
    const countFile = path.join(dir, 'spawn-count');
    const script = path.join(dir, 'fake-vault.js');
    fs.writeFileSync(script, `
      const fs = require('fs');
      const http = require('http');
      fs.appendFileSync(${JSON.stringify(countFile)}, 'spawn:' + (process.env.GATEWAY_BOOTSTRAP_ONLY || '') + '\\n');
      let healthy = false;
      const srv = http.createServer((req, res) => {
        res.statusCode = req.url === '/healthz' && healthy ? 200 : 503;
        res.end();
      });
      setTimeout(() => {
        srv.listen(Number(process.env.GATEWAY_PORT), '127.0.0.1', () => {
          console.log('vault listening');
          setTimeout(() => { healthy = true; }, 80);
        });
      }, 60);
      process.on('SIGTERM', () => srv.close(() => process.exit(0)));
    `);
    fs.writeFileSync(bin, `#!/bin/sh\nexec "${process.execPath}" "${script}"\n`, { mode: 0o755 });
    const logFile = path.join(dir, 'vault.log');
    const fileLog = createBoundedLogWriter(logFile);
    let exitCalled = false;
    const child = await startVault({
      bin,
      env: { ...process.env, GATEWAY_PORT: String(port) },
      log: fileLog,
      pollMs: 20,
      readyTimeoutMs: 5000,
      onExit: () => { exitCalled = true; },
    });
    // Vault must already be answering 200 on /healthz by the time startVault resolves.
    const status = await new Promise((resolve, reject) => {
      const req = require('http').get(`http://127.0.0.1:${port}/healthz`, (res) => {
        res.resume();
        resolve(res.statusCode);
      });
      req.on('error', reject);
    });
    assert.equal(status, 200);
    assert.equal(exitCalled, false);

    const exited = new Promise((r) => child.on('exit', r));
    child.kill('SIGTERM');
    await exited;

    const lines = fs.readFileSync(countFile, 'utf8').trim().split('\n');
    assert.deepEqual(lines, ['spawn:']);
    assert.equal(exitCalled, true);
    assert.match(fs.readFileSync(logFile, 'utf8'), /vault listening/);
    assert.match(fs.readFileSync(logFile, 'utf8'), /vault exited/);

    // Early crash before /healthz rejects immediately and does not trigger onExit.
    const crashBin = path.join(dir, 'crash-vault.sh');
    fs.writeFileSync(crashBin, '#!/bin/sh\necho "fatal init error" >&2\nexit 2\n', { mode: 0o755 });
    let crashOnExit = false;
    await assert.rejects(
      startVault({
        bin: crashBin,
        env: { ...process.env, GATEWAY_PORT: String(port) },
        log: fileLog,
        pollMs: 20,
        readyTimeoutMs: 5000,
        onExit: () => { crashOnExit = true; },
      }),
      /Vault stopped before startup completed \(exit 2/,
    );
    assert.equal(crashOnExit, false);

    // Timeout kills the child and does not trigger onExit.
    const hangBin = path.join(dir, 'hang-vault.sh');
    fs.writeFileSync(hangBin, '#!/bin/sh\nexec sleep 30\n', { mode: 0o755 });
    let hangOnExit = false;
    await assert.rejects(
      startVault({
        bin: hangBin,
        env: { ...process.env, GATEWAY_PORT: String(port) },
        log: fileLog,
        pollMs: 20,
        readyTimeoutMs: 120,
        onExit: () => { hangOnExit = true; },
      }),
      /Not ready: Vault/,
    );
    await new Promise((r) => setTimeout(r, 60));
    assert.equal(hangOnExit, false);

    await assert.rejects(
      startVault({ bin: path.join(dir, 'missing-vault'), env: process.env, log: fileLog }),
      /ENOENT/,
    );
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('vaultState and vaultEnv strip inherited gateway vars and keep private permissions', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'awvault-state-'));
  try {
    const state = vaultState(dir);
    assert.equal(fs.readFileSync(state.tokenFile, 'utf8').length, 64);
    const env = vaultEnv({
      inherited: { PATH: '/usr/bin', GATEWAY_PORT: '9999', VAULT_OLD: '1', CAPLAYER_X: '2' },
      port: 45680,
      agentPort: 45678,
      stateDir: state.dir,
      tokenFile: state.tokenFile,
      docsDir: '/tmp/docs',
    });
    assert.equal(env.GATEWAY_PORT, '45680');
    assert.equal(env.GATEWAY_PRODUCT_URL, 'http://127.0.0.1:45678');
    assert.equal(env.VAULT_OLD, undefined);
    assert.equal(env.CAPLAYER_X, undefined);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');

const { vaultState, vaultEnv, startVault } = require('./vault');
const { createBoundedLogWriter } = require('./boundedLog');

test('startVault spawns Vault once without GATEWAY_BOOTSTRAP_ONLY and logs output', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'awvault-'));
  try {
    const bin = path.join(dir, 'fake-vault.sh');
    const countFile = path.join(dir, 'spawn-count');
    fs.writeFileSync(bin, `#!/bin/sh
echo "spawn:$GATEWAY_BOOTSTRAP_ONLY" >> "${countFile}"
echo "vault listening"
while true; do sleep 1; done
`, { mode: 0o755 });
    const logFile = path.join(dir, 'vault.log');
    const fileLog = createBoundedLogWriter(logFile);
    let resolveReady;
    const ready = new Promise((r) => { resolveReady = r; });
    const log = {
      write(chunk) {
        fileLog.write(chunk);
        if (String(chunk).includes('vault listening')) resolveReady();
      },
    };
    let exitCalled = false;
    const child = await startVault({
      bin,
      env: { ...process.env },
      log,
      onExit: () => { exitCalled = true; },
    });
    await Promise.race([
      ready,
      new Promise((_, rej) => setTimeout(() => rej(new Error(`vault did not log readiness; log=${fs.existsSync(logFile) ? fs.readFileSync(logFile, 'utf8') : '<missing>'}`)), 5000)),
    ]);
    const exited = new Promise((r) => child.on('exit', r));
    child.kill('SIGTERM');
    await exited;

    const lines = fs.readFileSync(countFile, 'utf8').trim().split('\n');
    assert.deepEqual(lines, ['spawn:']);
    assert.equal(exitCalled, true);
    assert.match(fs.readFileSync(logFile, 'utf8'), /vault listening/);
    assert.match(fs.readFileSync(logFile, 'utf8'), /vault exited/);

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

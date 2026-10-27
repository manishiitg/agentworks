'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const http = require('http');

const { parseEnvBlock, applyLoginEnv, BEGIN, END } = require('./loginEnv');
const { createBoundedLogWriter, trimLogFileToTail } = require('./boundedLog');
const { fetchHealth, waitForHealth } = require('./health');
const { normalizeExternalUrl, isInternalNavUrl, isLoopbackUrl } = require('./externalNav');
const { spawnServer, killServer } = require('./servers');

test('parseEnvBlock ignores shell banners around the markers', () => {
  const stdout = `Agent pid 123\nIdentity added: key\n${BEGIN}PATH=/opt/homebrew/bin:/usr/bin\0HOME=/Users/x\0BROKEN\0=novalue\0${END}\ntrailing`;
  assert.deepEqual(parseEnvBlock(stdout), { PATH: '/opt/homebrew/bin:/usr/bin', HOME: '/Users/x' });
  assert.deepEqual(parseEnvBlock('no markers here'), {});
});

test('applyLoginEnv fills gaps but PATH always wins', () => {
  const target = { PATH: '/usr/bin:/bin', KEEP: 'electron' };
  applyLoginEnv({ PATH: '/opt/homebrew/bin:/usr/bin', KEEP: 'login', NEW: 'x' }, target);
  assert.deepEqual(target, { PATH: '/opt/homebrew/bin:/usr/bin', KEEP: 'electron', NEW: 'x' });
});

test('bounded log keeps the tail under the cap', () => {
  const file = path.join(fs.mkdtempSync(path.join(os.tmpdir(), 'awlog-')), 'agent.log');
  const log = createBoundedLogWriter(file, { maxBytes: 2000, appName: 'TestApp' });
  for (let i = 0; i < 50; i++) log.write(`line ${String(i).padStart(4, '0')} ${'x'.repeat(80)}\n`);
  const size = fs.statSync(file).size;
  assert.ok(size <= 2000, `size ${size} over cap`);
  const content = fs.readFileSync(file, 'utf8');
  assert.match(content, /Log truncated by TestApp/);
  assert.match(content, /line 0049/);
  assert.doesNotMatch(content, /line 0000 /);
  // An oversized single chunk keeps its final bytes.
  log.write('y'.repeat(5000));
  assert.ok(fs.statSync(file).size <= 2000);
  assert.match(fs.readFileSync(file, 'utf8'), /Oversized log chunk truncated by TestApp/);
  assert.equal(trimLogFileToTail(path.join(os.tmpdir(), 'does-not-exist.log')), 0);
});

test('health polling resolves when every check is 200 and fails fast when a server died', async () => {
  const server = http.createServer((req, res) => { res.statusCode = req.url === '/ok' ? 200 : 503; res.end(); });
  await new Promise((r) => server.listen(0, '127.0.0.1', r));
  const base = `http://127.0.0.1:${server.address().port}`;
  assert.equal(await fetchHealth(`${base}/ok`), true);
  assert.equal(await fetchHealth(`${base}/nope`), false);
  await waitForHealth({ checks: [{ name: 'a', url: `${base}/ok` }, { name: 'b', url: `${base}/ok` }], timeoutMs: 2000, pollMs: 20 });
  await assert.rejects(
    waitForHealth({ checks: [{ name: 'agent', url: `${base}/nope` }], timeoutMs: 100, pollMs: 20, hint: 'Rebuild it.' }),
    /Not ready: agent\. Rebuild it\./,
  );
  await assert.rejects(
    waitForHealth({ checks: [{ name: 'a', url: `${base}/nope` }], timeoutMs: 5000, pollMs: 20, isAlive: () => 'agent exited before it became ready' }),
    /agent exited before it became ready/,
  );
  server.close();
});

test('external navigation rules', () => {
  assert.equal(normalizeExternalUrl(' https://example.com/x '), 'https://example.com/x');
  assert.equal(normalizeExternalUrl('javascript:alert(1)'), null);
  assert.equal(normalizeExternalUrl('mailto:a@b.c'), 'mailto:a@b.c');
  assert.equal(isLoopbackUrl('http://127.0.0.1:45678/'), true);
  assert.equal(isLoopbackUrl('http://example.com'), false);
  assert.equal(isInternalNavUrl('file:///x'), true);
  assert.equal(isInternalNavUrl('https://example.com'), false);
});

test('spawnServer resolves on the DynamicPort line, substitutes the port and logs output', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'awspawn-'));
  const script = path.join(dir, 'fake-server.js');
  fs.writeFileSync(script, `
    const port = process.argv[process.argv.indexOf('--port') + 1];
    console.error('warming');
    console.log('DynamicPort: ' + port);
    setInterval(() => {}, 1000);
  `);
  const log = createBoundedLogWriter(path.join(dir, 'fake.log'));
  const { child, port } = await spawnServer({
    name: 'fake', bin: process.execPath, args: [script, '--port', spawnServer.PORT_PLACEHOLDER],
    preferredPort: 47654, cwd: dir, env: process.env, log,
  });
  assert.ok(port >= 47654, `port ${port}`);
  const exited = new Promise((r) => child.on('exit', r));
  killServer(child);
  await exited;
  const content = fs.readFileSync(path.join(dir, 'fake.log'), 'utf8');
  assert.match(content, /warming/);
  assert.match(content, new RegExp(`DynamicPort: ${port}`));
  assert.match(content, /fake-server exited/);
  // PLAT-856-383: a failed startup migration exits before DynamicPort; do not hang the desktop.
  await assert.rejects(spawnServer({ name: 'migration-failed', bin: process.execPath, args: ['-e', 'process.exit(1)'], preferredPort: 47657, cwd: dir, env: process.env, log }), /stopped before startup completed/);
  await assert.rejects(spawnServer({ name: 'missing', bin: path.join(dir, 'nope'), args: [], preferredPort: 47655, cwd: dir, env: process.env, log }));
});

test('spawnServer can derive process environment from the selected port', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'awspawn-env-'));
  const script = path.join(dir, 'fake-server.js');
  fs.writeFileSync(script, `
    const port = process.argv[process.argv.indexOf('--port') + 1];
    if (process.env.PUBLIC_URL !== 'http://127.0.0.1:' + port) process.exit(2);
    console.log('DynamicPort: ' + port);
    setInterval(() => {}, 1000);
  `);
  const log = createBoundedLogWriter(path.join(dir, 'fake.log'));
  const { child, port } = await spawnServer({
    name: 'fake-env', bin: process.execPath, args: [script, '--port', spawnServer.PORT_PLACEHOLDER],
    preferredPort: 47656, cwd: dir,
    env: (selectedPort) => ({ ...process.env, PUBLIC_URL: `http://127.0.0.1:${selectedPort}` }),
    log,
  });
  assert.ok(port >= 47656, `port ${port}`);
  const exited = new Promise((r) => child.on('exit', r));
  killServer(child);
  await exited;
});

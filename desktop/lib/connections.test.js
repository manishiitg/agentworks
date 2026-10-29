const { test } = require('node:test');
const { once } = require('node:events');
const assert = require('node:assert/strict');
const { connectionRequest, installDesktopConnections } = require('./connections');

test('saved remote sessions are available only to the app top frame, never dashboard frames or popups', () => {
  const frame = { url: 'http://127.0.0.1:18743/crew' };
  const contents = { id: 4, mainFrame: frame };
  const origin = 'http://127.0.0.1:18743';
  const request = { webContentsId: 4, frame, resourceType: 'xhr', url: origin + '/api/connections/abc/api/events' };
  assert.equal(connectionRequest(request, contents, origin), true);
  for (const change of [
    { frame: { url: frame.url } }, // same-origin iframe, different frame
    { webContentsId: 5 }, { resourceType: 'mainFrame' }, { resourceType: 'subFrame' },
    { url: 'https://example.com/api/connections/abc/api/events' },
    { url: origin + '/api/auth/me' },
  ]) assert.equal(connectionRequest({ ...request, ...change }, contents, origin), false);

  let beforeHeaders;
  installDesktopConnections({ session: { webRequest: { onBeforeSendHeaders: (_, cb) => { beforeHeaders = cb; } } },
    ipcMain: { on() {}, handle() {} }, getWindow: () => ({ webContents: contents }), getOrigin: () => origin, secret: 'app-proof' });
  beforeHeaders({ ...request, frame: { url: frame.url }, requestHeaders: { 'x-agentworks-desktop': 'forged' } }, result => {
    assert.equal(Object.keys(result.requestHeaders).length, 0);
  });
  beforeHeaders({ ...request, requestHeaders: {} }, result => {
    assert.equal(result.requestHeaders['X-AgentWorks-Desktop'], 'app-proof');
  });
});

// PLAT-746-207: a restart must never stamp the retired port or the startup gap.
test('connection proof follows the live server and is absent during restart', () => {
  let origin = 'http://127.0.0.1:18743', beforeHeaders;
  const frame = { url: origin + '/crew' }, contents = { id: 4, mainFrame: frame };
  installDesktopConnections({ session: { webRequest: { onBeforeSendHeaders: (_, cb) => { beforeHeaders = cb; } } },
    ipcMain: { on() {}, handle() {} }, getWindow: () => ({ webContents: contents }), getOrigin: () => origin, secret: 'app-proof' });
  const request = url => new Promise(resolve => beforeHeaders({ webContentsId: 4, frame, resourceType: 'xhr', url,
    requestHeaders: { 'X-AgentWorks-Desktop': 'forged' } }, value => resolve(value.requestHeaders['X-AgentWorks-Desktop'])));
  return (async () => {
    const retired = origin + '/api/desktop-connections';
    assert.equal(await request(retired), 'app-proof');
    origin = null;
    assert.equal(await request(retired), undefined);
    origin = 'http://127.0.0.1:18744'; frame.url = origin + '/crew';
    assert.equal(await request(retired), undefined);
    assert.equal(await request(origin + '/api/desktop-connections'), 'app-proof');
  })();
});

// Inspect the real initial child environment with ps, not just process.env after cleanup.
test('desktop bootstrap goes over fd 3 and is absent from process lists and workspace children', async () => {
  const { withoutDesktopBootstrap, spawnWithBootstrap } = require('./connections');
  const { spawnSync } = require('node:child_process');
  const env = { PATH: process.env.PATH, AUTH_SECRET: 'inherited-auth', AGENTWORKS_DESKTOP_SECRET: 'inherited', AGENTWORKS_DESKTOP_HOST: 'stale' };
  const workspaceEnv = withoutDesktopBootstrap(env);
  const workspace = spawnSync(process.execPath, ['-e', "process.exit(Object.keys(process.env).some(k => k.startsWith('AGENTWORKS_DESKTOP_') || k === 'AUTH_SECRET') ? 1 : 0)"], { env: workspaceEnv });
  assert.equal(workspace.status, 0);
  const payload = { secret: 'proof-canary-917', host: '127.0.0.1:49152', auth_secret: 'auth-canary-582' };
  const child = spawnWithBootstrap(process.execPath, ['-e', `
    const fs = require('node:fs');
    const data = JSON.parse(fs.readFileSync(3, 'utf8')); fs.closeSync(3);
    if (!data.secret || !data.auth_secret || data.host !== '127.0.0.1:49152') process.exit(1);
    process.stdout.write('ready\\n'); setInterval(() => {}, 1000);
  `], { env }, payload);
  try {
    const [ready] = await once(child.stdout, 'data');
    assert.match(ready.toString(), /ready/);
    const listing = spawnSync('ps', ['eww', '-p', String(child.pid)], { encoding: 'utf8' });
    assert.equal(listing.status, 0);
    for (const value of ['AGENTWORKS_DESKTOP_', 'AUTH_SECRET=', payload.secret, payload.auth_secret, 'inherited-auth']) {
      assert.equal(listing.stdout.includes(value), false, 'bootstrap exposed by ps');
    }
  } finally { child.kill(); await once(child, 'exit'); }
});

// PLAT-746-211: Vite and the agent listen on different origins in the local launcher.
test('development window selects connections on Vite and sends proof only to its backend', async () => {
  const origin = 'http://127.0.0.1:18743', appOrigin = 'http://127.0.0.1:51733';
  const frame = { url: appOrigin + '/crew' }, contents = { id: 4, mainFrame: frame };
  let beforeHeaders, getConnection, openConnection, loaded;
  installDesktopConnections({ session: { webRequest: { onBeforeSendHeaders: (_, cb) => { beforeHeaders = cb; } } },
    ipcMain: { on: (_, cb) => { getConnection = cb; }, handle: (_, cb) => { openConnection = cb; } },
    getWindow: () => ({ webContents: contents, loadURL: async url => { loaded = url; } }),
    getOrigin: () => origin, getAppOrigin: () => appOrigin, secret: 'app-proof' });
  const event = { sender: contents, senderFrame: frame };
  getConnection(event);
  assert.equal(event.returnValue, 'local');
  const request = { webContentsId: 4, frame, resourceType: 'xhr', requestHeaders: {} };
  for (const [url, expected] of [[origin + '/api/desktop-connections', 'app-proof'], [appOrigin + '/api/desktop-connections', undefined], ['http://127.0.0.1:18744/api/desktop-connections', undefined]]) {
    beforeHeaders({ ...request, url }, result => assert.equal(result.requestHeaders['X-AgentWorks-Desktop'], expected));
  }
  beforeHeaders({ ...request, frame: { url: frame.url }, url: origin + '/api/desktop-connections' }, result => assert.equal(result.requestHeaders['X-AgentWorks-Desktop'], undefined));
  await openConnection(event, 'a'.repeat(32), '/crew/projects/example');
  assert.equal(loaded, appOrigin + '/crew/projects/example');
});

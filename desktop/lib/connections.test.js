const { test } = require('node:test');
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
    ipcMain: { on() {}, handle() {} }, getWindow: () => ({ webContents: contents }), origin, secret: 'app-proof' });
  beforeHeaders({ ...request, frame: { url: frame.url }, requestHeaders: { 'x-agentworks-desktop': 'forged' } }, result => {
    assert.equal(Object.keys(result.requestHeaders).length, 0);
  });
  beforeHeaders({ ...request, requestHeaders: {} }, result => {
    assert.equal(result.requestHeaders['X-AgentWorks-Desktop'], 'app-proof');
  });
});

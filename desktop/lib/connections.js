// Only the trusted app's top frame can ask the backend to use saved accounts.
// A same-origin dashboard iframe is deliberately not an app caller.
function isAppFrame(frame, contents, origin) {
  try { return !!frame && frame === contents.mainFrame && new URL(frame.url).origin === origin; }
  catch { return false; }
}

function connectionRequest(details, contents, origin) {
  if (!contents || details.webContentsId !== contents.id ||
      !['xhr', 'fetch'].includes(details.resourceType) || !isAppFrame(details.frame, contents, origin)) return false;
  try {
    const url = new URL(details.url);
    return url.origin === origin && (url.pathname === '/api/desktop-connections' ||
      url.pathname.startsWith('/api/desktop-connections/') || url.pathname.startsWith('/api/connections/'));
  } catch { return false; }
}

function installDesktopConnections({ session, ipcMain, getWindow, getOrigin, secret }) {
  let connection = 'local';
  session.webRequest.onBeforeSendHeaders({ urls: ['http://127.0.0.1/*'] }, (details, done) => {
    const origin = getOrigin();
    const headers = { ...details.requestHeaders };
    // Never accept a renderer-supplied version, even in a subframe.
    for (const key of Object.keys(headers)) if (key.toLowerCase() === 'x-agentworks-desktop') delete headers[key];
    if (connectionRequest(details, getWindow()?.webContents, origin)) headers['X-AgentWorks-Desktop'] = secret;
    done({ requestHeaders: headers });
  });
  ipcMain.on('get-desktop-connection', event => {
    const origin = getOrigin();
    event.returnValue = event.sender === getWindow()?.webContents && isAppFrame(event.senderFrame, event.sender, origin) ? connection : null;
  });
  ipcMain.handle('open-desktop-connection', async (event, id, route) => {
    const origin = getOrigin();
    if (event.sender !== getWindow()?.webContents || !isAppFrame(event.senderFrame, event.sender, origin)) throw new Error('Use the main app window.');
    if (id !== 'local' && !/^[a-f0-9]{32}$/.test(id)) throw new Error('Unknown connection.');
    if (typeof route !== 'string' || !/^\/(?:crew(?:\/projects\/[a-zA-Z0-9_-]+)?|workflows(?:\/[a-zA-Z0-9_-]+)?|goals|home)$/.test(route)) throw new Error('Unknown project.');
    connection = id;
    // Reopening discards per-server singleton caches and event readers. Work
    // continues on its server. The connection never comes from an app URL.
    await getWindow().loadURL(origin + route);
  });
}
// Neither child starts with signing credentials or app proof in its environment.
// Unsetting them inside Go is too late: macOS ps can read the initial environment.
function withoutDesktopBootstrap(env) {
  return Object.fromEntries(Object.entries(env).filter(([key]) =>
    !key.startsWith('AGENTWORKS_DESKTOP_') && key !== 'AUTH_SECRET' && key !== 'AGENTWORKS_BOOTSTRAP_FD'));
}
function spawnWithBootstrap(bin, args, options, bootstrap) {
  const { spawn } = require('node:child_process');
  const child = spawn(bin, args, { ...options,
    env: { ...withoutDesktopBootstrap(options.env), AGENTWORKS_BOOTSTRAP_FD: '3' },
    stdio: ['ignore', 'pipe', 'pipe', 'pipe'],
  });
  // fd 3 is private to this child; close it after the one startup message.
  child.stdio[3].on('error', () => child.kill());
  child.stdio[3].end(JSON.stringify(bootstrap) + '\n');
  return child;
}
module.exports = { isAppFrame, connectionRequest, installDesktopConnections, withoutDesktopBootstrap, spawnWithBootstrap };

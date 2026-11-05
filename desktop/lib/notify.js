'use strict';

// Notifications in the Mac app (owner, 5 Nov). The app's own server decides every send and relays its company
// servers' messages; this main-process stream shows each one natively (with the short sound when this Mac has it on),
// whether or not a window is open. Clicking opens that chat.

const http = require('http');
const crypto = require('crypto');

// sessionToken is a short session for this Mac's person, signed with the local server's own signing key, kept only
// in this process: the stream needs it as well as the app's start secret.
function sessionToken(authSecret, userId) {
  const now = Math.floor(Date.now() / 1000);
  const part = value => Buffer.from(JSON.stringify(value)).toString('base64url');
  const body = `${part({ alg: 'HS256', typ: 'JWT' })}.${part({ user_id: userId, username: userId, iss: 'mcp-agent-builder', sub: userId, iat: now, nbf: now - 5, exp: now + 3600 })}`;
  return `${body}.${crypto.createHmac('sha256', authSecret).update(body).digest('base64url')}`;
}

// startNotifyStream keeps one stream to the local server open, reconnecting, until stop() is called.
function startNotifyStream({ getPort, secret, authSecret, userId, show, log }) {
  let stopped = false;
  let request = null;
  let timer = null;
  const connect = () => {
    if (stopped) return;
    const port = getPort();
    if (!port) { timer = setTimeout(connect, 3000); return; }
    request = http.get({ host: '127.0.0.1', port, path: '/api/desktop/notify-stream', headers: { 'X-AgentWorks-Notify': secret, Authorization: `Bearer ${sessionToken(authSecret, userId)}`, Accept: 'text/event-stream' } }, res => {
      if (res.statusCode !== 200) { res.resume(); retry(); return; }
      res.setEncoding('utf8');
      let buffer = '';
      res.on('data', chunk => {
        buffer += chunk;
        let at;
        while ((at = buffer.indexOf('\n\n')) >= 0) {
          const block = buffer.slice(0, at);
          buffer = buffer.slice(at + 2);
          const event = /^event: (.*)$/m.exec(block)?.[1];
          const data = /^data: (.*)$/m.exec(block)?.[1];
          if (event !== 'notify' || !data) continue;
          try { show(JSON.parse(data)); } catch (err) { log(`[notify] unreadable message: ${err.message}`); }
        }
      });
      res.on('end', retry);
      res.on('error', retry);
    });
    request.on('error', retry);
  };
  const retry = () => {
    if (stopped || timer) return;
    timer = setTimeout(() => { timer = null; connect(); }, 3000);
  };
  connect();
  return () => {
    stopped = true;
    clearTimeout(timer);
    request?.destroy();
  };
}

// notifyRoute keeps only routes the app can open; a company server's chat opens its Crew.
function notifyRoute(route, remote) {
  const value = typeof route === 'string' && route.startsWith('/') ? route : '/';
  if (!remote) return value;
  const crew = /^\/crew\/projects\/[a-zA-Z0-9_-]+/.exec(value);
  return crew ? crew[0] : '/home';
}

module.exports = { startNotifyStream, notifyRoute };

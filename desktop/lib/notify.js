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

// The split launcher already owns a private desktop proof. Pair it with the
// ordinary local-person session; no server signing key leaves the backend.
async function developmentNotifyHeaders(socketPath, apiOrigin, appOrigin) {
  const proof = await require('./devConnection').readDevelopmentProof(socketPath, apiOrigin, appOrigin);
  const token = await new Promise((resolve, reject) => {
    const req = http.request(new URL('/api/auth/login', apiOrigin), { method: 'POST', agent: false, headers: { 'Content-Type': 'application/json' } }, res => {
      let body = '';
      res.setEncoding('utf8');
      res.on('data', chunk => { body += chunk; if (body.length > 65536) req.destroy(new Error('Local notification sign-in unavailable.')); });
      res.on('error', reject);
      res.on('end', () => {
        try {
          const value = JSON.parse(body);
          if (res.statusCode !== 200 || typeof value.token !== 'string' || !value.token) throw new Error();
          resolve(value.token);
        } catch { reject(new Error('Local notification sign-in unavailable.')); }
      });
    });
    req.on('error', reject);
    req.setTimeout(3000, () => req.destroy(new Error('Local notification sign-in unavailable.')));
    req.end('{}');
  });
  return { Origin: appOrigin, 'X-AgentWorks-Desktop': proof, Authorization: `Bearer ${token}` };
}

// One window-independent stream. Every reconnect gets the current origin and
// fresh credentials, including after a separately launched backend restarts.
function startNotifyStream({ getOrigin, getHeaders, show, log }) {
  let stopped = false;
  let request = null;
  let timer = null;
  const connect = async () => {
    if (stopped) return;
    let retrying = false;
    let current = null;
    const retry = reason => {
      if (stopped || retrying) return;
      retrying = true;
      log(`[notify] stream=retry reason=${reason}`);
      current?.destroy();
      timer = setTimeout(() => { timer = null; connect(); }, 3000);
    };
    try {
      const origin = getOrigin();
      if (!origin) { retry('backend_unavailable'); return; }
      const headers = await getHeaders(origin);
      if (stopped) return;
      if (getOrigin() !== origin) { retry('backend_changed'); return; }
      current = request = http.get(new URL('/api/desktop/notify-stream', origin), { headers, agent: false }, res => {
        if (res.statusCode !== 200) { res.resume(); retry(`http_${res.statusCode}`); return; }
        log('[notify] stream=connected');
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
            let message;
            try { message = JSON.parse(data); } catch { log('[notify] delivery=failed reason=invalid_message'); continue; }
            try { show(message); } catch { log('[notify] delivery=failed reason=display_failed'); }
          }
        });
        res.once('end', () => retry('ended'));
        res.once('close', () => retry('closed'));
        res.once('error', () => retry('disconnected'));
      });
      current.once('error', () => retry('unreachable'));
    } catch { retry('credentials_unavailable'); }
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

module.exports = { startNotifyStream, notifyRoute, sessionToken, developmentNotifyHeaders };

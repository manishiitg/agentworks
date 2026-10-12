import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import http from 'node:http';
import { Host } from '../host.mjs';

// Pin the confirmed forged-hook bug: local routing metadata alone never proves
// person input. Wrong arguments/session and reuse cannot consume authority.
test('private Pi IPC consumes only the exact one-use in-memory person command', async t => {
  const root = await mkdtemp(path.join(tmpdir(), 'pi-person-'));
  const session = '11111111-2222-4333-8444-555555555555';
  const ctx = { mode: 'tui', hasUI: true, cwd: root, sessionManager: { getSessionId: () => session } };
  const host = new Host({ root }, { stop() {} });
  t.after(async () => { await host.stop(); await rm(root, { recursive: true, force: true }); });
  await host.start(ctx);
  const claim = (token, extra = {}) => new Promise((resolve, reject) => {
    const req = http.request({ socketPath: host.socket, path: '/person-input', method: 'POST' }, res => {
      res.resume(); res.on('end', () => resolve(res.statusCode));
    });
    req.on('error', reject);
    req.end(JSON.stringify({ host_key: host.key, session_id: session, pi_person_token: token,
      prompt: '/agentworks attach reviewer', ...extra }));
  });
  assert.equal(await claim('forged'), 409);
  assert.throws(() => host.personToken('/agentworks attach reviewer', { ...ctx, mode: 'rpc' }));
  const token = host.personToken('/agentworks attach reviewer', ctx);
  assert.equal(await claim(token, { prompt: '/agentworks detach' }), 409);
  assert.equal(await claim(token, { prompt: '/agentworks attach other-name' }), 409);
  assert.equal(await claim(token, { session_id: 'other-session' }), 409);
  assert.equal(await claim(token, { host_key: 'other-host' }), 409);
  assert.equal(await claim(token), 200);
  assert.equal(await claim(token), 409, 'proof cannot replay after its first valid use');
  for (const ok of ['/agentworks share', '/agentworks unshare', '/agentworks attach reviewer --on work', '/agentworks attach --on work', '/agentworks attach --on default']) assert.doesNotThrow(() => host.personToken(ok, ctx), ok);
  for (const bad of ['/agentworks share everyone', '/agentworks share --days 3', '/agentworks attach reviewer --on', '/agentworks attach --force x', '/agentworks attach a --on Work', '/agentworks attach a b --on work', '/agentworks attach a --on work b']) assert.throws(() => host.personToken(bad, ctx), bad);
});

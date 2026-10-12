import { nativeUsageStats } from './usage.mjs';
import { execFileSync, spawn } from 'node:child_process';
import { createHash, randomUUID } from 'node:crypto';
import { constants } from 'node:fs';
import { mkdir, lstat, open, rename, unlink } from 'node:fs/promises';
import http from 'node:http';
import path from 'node:path';

export function hostKey() {
  const started = execFileSync('ps', ['-p', String(process.pid), '-o', 'lstart='], {
    encoding: 'utf8', timeout: 1000, stdio: ['ignore', 'pipe', 'ignore'],
    env: { ...process.env, LC_ALL: 'C', TZ: 'UTC' },
  }).trim().split(/\s+/).join(' ');
  if (!started) throw new Error('Cannot identify this exact Pi process');
  return createHash('sha256').update(`${process.pid} ${started}`).digest('hex');
}

async function privateDirectory(dir) {
  await mkdir(dir, { recursive: true, mode: 0o700 });
  const stat = await lstat(dir);
  if (!stat.isDirectory() || stat.uid !== process.getuid() || stat.mode & 0o077)
    throw new Error('AgentWorks Pi discovery directory is not private');
}

// This SDK-owned record proves which live process owns the current session.
// Activation cannot attach: it only checks an already created bridge lease.
export class Host {
  #personTokens = new Map();
  constructor(bridge, delivery, usageClass) { this.bridge = bridge; this.delivery = delivery; this.usageClass = usageClass; }

  async start(ctx) {
    await this.stop();
    const session = ctx.sessionManager.getSessionId();
    if (!/^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$/i.test(session))
      throw new Error('Pi did not supply a native session UUID');
    this.key = hostKey();
    this.delivery.host = this.key;
    this.session = session;
    await privateDirectory(this.bridge.root);
    const dir = path.join(this.bridge.root, 'pi-sessions');
    await privateDirectory(dir);
    this.socket = path.join(this.bridge.root, `pi-${process.pid}-${randomUUID().slice(0, 8)}.sock`);
    this.server = http.createServer(async (req, res) => {
      let body = '';
      try {
        if (req.method !== 'POST' || !['/activate', '/person-input', '/usage-stats'].includes(req.url)) throw new Error();
        for await (const chunk of req) { body += chunk; if (body.length > 4096) throw new Error(); }
        const input = JSON.parse(body);
        if (req.url === '/person-input') {
          const proof = this.#personTokens.get(input.pi_person_token);
          if (input.host_key !== this.key || input.session_id !== session ||
              ctx.sessionManager.getSessionId() !== session || !proof ||
              proof.host !== this.key || proof.session !== session || proof.text !== input.prompt || proof.expires < Date.now()) throw new Error();
          this.#personTokens.delete(input.pi_person_token);
          res.end('{"person_input":true}'); return;
        }
        if (input.host_key !== this.key || input.session_id !== session ||
            typeof input.attachment_id !== 'string' || !input.attachment_id ||
            ctx.sessionManager.getSessionId() !== session) throw new Error();
        const status = await this.bridge.call({ action: 'status', session_id: session,
          host_key: this.key, attachment_id: input.attachment_id });
        if (!status.attached || status.attachment?.provider !== 'pi' ||
            status.attachment.native_session_id !== session ||
            status.attachment.attachment_id !== input.attachment_id) throw new Error();
        if (req.url === '/usage-stats') {
          const usage = await this.bridge.call({ action: 'usage_status', session_id: session, host_key: this.key, attachment_id: input.attachment_id });
          if (!usage.result?.enabled) throw new Error();
          const stats = await nativeUsageStats(ctx, this.usageClass);
          if (ctx.sessionManager.getSessionId() !== session) throw new Error();
          const still = await this.bridge.call({ action: 'usage_status', session_id: session, host_key: this.key, attachment_id: input.attachment_id });
          if (!still.result?.enabled || still.result.generation !== usage.result.generation) throw new Error();
          res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify(stats)); return;
        }
        if (this.delivery.attachment?.attachment_id !== input.attachment_id ||
            !this.delivery.current(this.delivery.generation)) await this.delivery.start(ctx, input.attachment_id);
        if (this.delivery.attachment?.attachment_id !== input.attachment_id) throw new Error();
        res.end('{"active":true}');
      } catch { res.statusCode = 409; res.end('{"active":false}'); }
    });
    this.server.requestTimeout = 5000;
    await new Promise((resolve, reject) => { this.server.once('error', reject); this.server.listen(this.socket, resolve); });
    const { chmod } = await import('node:fs/promises');
    await chmod(this.socket, 0o600);
    this.file = path.join(dir, `${process.pid}.json`);
    const temp = this.file + '.' + randomUUID() + '.tmp';
    const handle = await open(temp, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY | constants.O_NOFOLLOW, 0o600);
    try {
      await handle.writeFile(JSON.stringify({ pid: process.pid, host_key: this.key, session_id: session,
        socket: this.socket, cwd: ctx.cwd, mode: ctx.mode }));
      await handle.sync(); await rename(temp, this.file);
    } finally { await handle.close(); await unlink(temp).catch(() => {}); }
  }

  personToken(text, ctx) {
    if (ctx.mode !== 'tui' || !ctx.hasUI || ctx.sessionManager.getSessionId() !== this.session ||
        !/^\/agentworks[ \t]+(?:attach(?:[ \t]+(?!--)\S+)?(?:[ \t]+--on[ \t]+(?:default|[a-z0-9][a-z0-9._-]{0,31}))?|detach|share[ \t]+[a-z0-9][a-z0-9._-]{0,63}(?:[ \t]+--purpose[ \t]+[^\r\n]{1,300})?|unshare(?:[ \t]+[a-z0-9][a-z0-9._-]{0,63})?|groups|group[ \t]+[a-z0-9][a-z0-9._-]{0,63})[ \t]*$/.test(text)) throw new Error('Pi person input requires the TUI');
    const token = randomUUID();
    this.#personTokens.set(token, { text, host: this.key, session: ctx.sessionManager.getSessionId(), expires: Date.now() + 12000 });
    return token;
  }

  forgetPersonToken(token) { this.#personTokens.delete(token); }

  async stop() {
    this.#personTokens.clear();
    const server = this.server;
    this.server = undefined;
    if (server) { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); }
    if (this.file) await unlink(this.file).catch(() => {});
    if (this.socket) await unlink(this.socket).catch(() => {});
    this.file = this.socket = undefined;
  }
}

// No shell, selected session, credentials or model-visible attach tool.
export function personCommand(text, ctx, host) {
  const token = host.personToken(text, ctx);
  return new Promise((resolve, reject) => {
    const child = spawn('agentworks', ['plugin', 'hook', 'pi'], { cwd: ctx.cwd, stdio: ['pipe', 'pipe', 'pipe'] });
    let output = '';
    const timer = setTimeout(() => { child.kill(); reject(new Error('AgentWorks person command timed out')); }, 12000);
    child.stdout.on('data', chunk => { output += chunk; if (output.length > 128 * 1024) child.kill(); });
    child.stderr.resume();
    child.on('error', () => { host.forgetPersonToken(token); clearTimeout(timer); reject(new Error('AgentWorks CLI unavailable; install or update it before attaching Pi')); });
    child.on('close', code => {
      host.forgetPersonToken(token); clearTimeout(timer);
      try { if (code !== 0) throw new Error(); resolve(JSON.parse(output)); }
      catch { reject(new Error('AgentWorks person command failed; check your existing CLI connection')); }
    });
    child.stdin.on('error', () => {});
    child.stdin.end(JSON.stringify({ session_id: ctx.sessionManager.getSessionId(), cwd: ctx.cwd,
      hook_event_name: 'UserPromptSubmit', prompt: text, pi_person_token: token }));
  });
}

import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, chmod, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import http from 'node:http';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { LocalBridge } from '../bridge.mjs';

const dir = path.dirname(fileURLToPath(import.meta.url));
const until = async (fn, label) => {
  const deadline = Date.now() + 12000;
  while (!fn()) {
    if (Date.now() > deadline) throw new Error(`Timed out: ${label}`);
    await new Promise(resolve => setTimeout(resolve, 20));
  }
};

async function harness(t, options = {}) {
  const root = await mkdtemp(path.join(tmpdir(), 'plat868-'));
  await chmod(root, 0o700);
  const requests = [], offers = [], waiters = [];
  let attached = options.attached ?? true, session, lease = 'lease-one';
  const attachment = () => ({ attachment_id: lease, agent_name: 'receiver', provider: 'pi', native_session_id: session });
  const respond = res => res.end(JSON.stringify({ attached, attachment: attachment(), messages: offers.splice(0) }));
  const server = http.createServer(async (req, res) => {
    let body = ''; for await (const chunk of req) body += chunk;
    const input = JSON.parse(body); requests.push(input);
    if (!session) session = input.session_id;
    res.setHeader('Content-Type', 'application/json');
    if (input.action === 'next' && !offers.length) {
      const timer = setTimeout(() => respond(res), 300);
      const wake = () => { clearTimeout(timer); if (!res.writableEnded) respond(res); };
      waiters.push(wake); res.on('close', () => { const i = waiters.indexOf(wake); if (i >= 0) waiters.splice(i, 1); });
    } else if (input.action === 'detach') {
      attached = false; respond(res);
    } else res.end(JSON.stringify({ attached, attachment: attachment(), messages: input.action === 'next' ? offers.splice(0) : [],
      result: { inbox_id: 'original-inbox' } }));
  });
  await new Promise(resolve => server.listen(path.join(root, 'bridge.sock'), resolve));
  await chmod(path.join(root, 'bridge.sock'), 0o600);
  const events = [], stderr = [];
  const child = spawn(process.env.PI_BIN || 'pi', ['--mode', 'rpc', '--offline', '--no-extensions', '--no-mcp',
    '--no-skills', '--no-themes', '--no-context-files', '--no-prompt-templates', '--no-builtin-tools',
    '-e', path.join(dir, '../index.ts'), '-e', path.join(dir, 'provider.ts'),
    '--model', 'plat868-fixture/fixture', '--session', path.join(root, 'session.jsonl')], {
    cwd: root, env: { ...process.env, PI_CODING_AGENT_DIR: path.join(root, 'agent'), AGENTWORKS_BRIDGE_ROOT: root },
    stdio: ['pipe', 'pipe', 'pipe'],
  });
  let buffer = '';
  child.stdout.on('data', chunk => {
    buffer += chunk;
    while (buffer.includes('\n')) {
      const end = buffer.indexOf('\n'), line = buffer.slice(0, end); buffer = buffer.slice(end + 1);
      try { events.push(JSON.parse(line)); } catch { stderr.push(line); }
    }
  });
  child.stderr.on('data', chunk => stderr.push(String(chunk)));
  t.after(async () => {
    child.stdin.end();
    await Promise.race([new Promise(resolve => child.once('exit', resolve)), new Promise(resolve => setTimeout(resolve, 2000))]);
    child.kill('SIGKILL');
    server.closeAllConnections(); await new Promise(resolve => server.close(resolve));
    await rm(root, { recursive: true, force: true });
  });
  child.on('error', error => stderr.push(error.message));
  let counter = 0;
  const command = async (type, args = {}) => {
    const id = `command-${++counter}`; child.stdin.write(JSON.stringify({ id, type, ...args }) + '\n');
    await until(() => events.some(e => e.id === id && e.type === 'response'), `Pi ${type}: ${stderr.join('')}`);
    const result = events.find(e => e.id === id && e.type === 'response');
    assert.equal(result.success, true, JSON.stringify(result)); return result.data;
  };
  await command('get_state');
  await until(() => requests.some(r => r.action === (attached ? 'next' : 'status')), `extension loaded: ${stderr.join('')}`);
  const offer = (id, extra = {}) => {
    for (const messageId of Array.isArray(id) ? id : [id]) {
      offers.push({ attachment_id: lease, message_id: messageId, inbox_id: 'original-inbox', sender: 'sender',
        message: `FIXTURE_MESSAGE_${messageId}`, sequence: counter, ...extra });
    }
    for (const wake of [...waiters]) wake();
  };
  return { root, child, events, requests, command, offer,
    revoke: () => { attached = false; for (const wake of [...waiters]) wake(); },
    stderr };
}

const userMessage = (events, id) => events.find(e => e.type === 'message_start' && e.message.role === 'user' &&
  JSON.stringify(e.message.content).includes(`FIXTURE_MESSAGE_${id}`));
const admitted = (requests, id) => requests.find(r => r.action === 'ack' && r.message_id === id && r.delivery === 'admitted');

test('real Pi: idle push, busy tool follow-up, exact receipt, replay and revoke', async t => {
  const h = await harness(t);
  h.offer(['idle', 'idle-second']);
  await until(() => admitted(h.requests, 'idle'), 'idle native admission');
  assert.ok(userMessage(h.events, 'idle'));
  await until(() => admitted(h.requests, 'idle-second'), 'ordered batch native admission');
  const batchText = JSON.stringify(userMessage(h.events, 'idle').message.content);
  assert.ok(batchText.indexOf('FIXTURE_MESSAGE_idle') < batchText.indexOf('FIXTURE_MESSAGE_idle-second'));
  assert.equal(userMessage(h.events, 'idle'), userMessage(h.events, 'idle-second'), 'one native prompt for a catch-up batch');
  await until(() => h.events.some(e => e.type === 'agent_settled'), 'idle settled');
  assert.equal(h.requests.some(r => r.delivery === 'processed' || r.action === 'reply'), false);

  h.events.length = 0;
  await h.command('prompt', { message: 'BUSY_TOOL' });
  await until(() => h.events.some(e => e.type === 'tool_execution_start'), 'tool in flight');
  h.offer('busy');
  await until(() => h.events.some(e => e.type === 'queue_update' && e.followUp?.length), 'native follow-up queue');
  assert.equal(admitted(h.requests, 'busy'), undefined, 'queued is not admitted');
  await h.command('prompt', { message: '/agentworks reconnect' });
  await until(() => admitted(h.requests, 'busy'), 'busy native admission');
  const toolEnd = h.events.findIndex(e => e.type === 'tool_execution_end');
  const delivery = h.events.indexOf(userMessage(h.events, 'busy'));
  assert.ok(toolEnd >= 0 && delivery > toolEnd, 'message delivered after the busy tool');
  h.offer('busy');
  await new Promise(resolve => setTimeout(resolve, 700));
  assert.equal(h.events.filter(e => e.type === 'message_start' && e.message.role === 'user' &&
    JSON.stringify(e.message.content).includes('FIXTURE_MESSAGE_busy')).length, 1, 'same ID is not reinjected');

  await h.command('prompt', { message: 'PROCESSED:busy' });
  await until(() => h.requests.some(r => r.delivery === 'processed' && r.message_id === 'busy'), 'explicit processed receipt');
  await until(() => h.events.filter(e => e.type === 'agent_settled').length >= 2, 'processed tool settled');
  await h.command('prompt', { message: 'REPLY:busy' });
  await until(() => h.requests.some(r => r.action === 'reply' && r.message_id === 'busy'), 'explicit same-inbox reply');
  assert.equal(h.requests.find(r => r.action === 'reply').submission_id, 'fixture-reply-one');
  assert.equal(h.requests.some(r => r.action === 'attach'), false, 'model tools cannot attach');
  h.offer('wrong', { attachment_id: 'other-session-lease' });
  await new Promise(resolve => setTimeout(resolve, 500));
  assert.equal(userMessage(h.events, 'wrong'), undefined);
  h.revoke();
  await new Promise(resolve => setTimeout(resolve, 500));
  const before = h.requests.length;
  await new Promise(resolve => setTimeout(resolve, 500));
  assert.equal(h.requests.length, before, 'revoked receiver stops requesting offers');

  const unattached = await harness(t, { attached: false });
  await new Promise(resolve => setTimeout(resolve, 400));
  assert.equal(unattached.requests.length, 1, 'unattached startup performs only local status');
  assert.equal(unattached.requests[0].action, 'status');
  assert.equal(unattached.events.some(e => e.type === 'extension_ui_request'), false, 'unattached startup is silent');
  await unattached.command('prompt', { message: '/agentworks attach receiver' });
  assert.equal(unattached.requests.some(r => r.action === 'attach'), false, 'RPC cannot attach without a person in TUI');
});

test('private Unix bridge refuses a public directory and an unattached receiver', async t => {
  const root = await mkdtemp(path.join(tmpdir(), 'plat868-private-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const server = http.createServer((_req, res) => res.end('{"attached":false}'));
  await new Promise(resolve => server.listen(path.join(root, 'bridge.sock'), resolve));
  t.after(() => new Promise(resolve => server.close(resolve)));
  await chmod(path.join(root, 'bridge.sock'), 0o600); await chmod(root, 0o755);
  const bridge = new LocalBridge(root);
  assert.equal(await bridge.available(), false);
  await assert.rejects(bridge.call({ action: 'status', session_id: 'fixture' }));
  await chmod(root, 0o700);
  assert.deepEqual(await bridge.call({ action: 'status', session_id: 'fixture' }), { attached: false });
});

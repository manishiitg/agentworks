import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, chmod, rm, readFile, writeFile, readdir } from 'node:fs/promises';
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
  const history = new Map();
  const requests = [], offers = [], waiters = [], failures = new Map(), processed = new Set(), nativeAdmitted = new Set();
  let heldNext = false, heldDelivered = false, nextWait = 300, usageEnabled = false;
  let attached = options.attached ?? true, session, lease = 'lease-one';
  const attachment = () => ({ attachment_id: lease, agent_name: 'receiver', provider: 'pi', native_session_id: session });
  const respond = (res, input) => {
    if (!attached || input.attachment_id !== lease) return res.end('{"attached":false}');
    const messages = offers.splice(0);
    if (heldNext && messages.length) { heldDelivered = true; return; } // Bridge recorded offered; provider never receives it.
    res.end(JSON.stringify({ attached, attachment: attachment(), messages }));
  };
  const server = http.createServer(async (req, res) => {
    let body = ''; for await (const chunk of req) body += chunk;
    const input = JSON.parse(body); requests.push(input);
    assert.match(input.host_key || '', /^[a-f0-9]{64}$/, 'each native request binds the exact process');
    if (!session) session = input.session_id;
    res.setHeader('Content-Type', 'application/json');
    if (input.action === 'usage_status') { res.end(JSON.stringify({ result: { enabled: usageEnabled, generation: 'fixture-generation' } })); return; }
    if (input.attachment_id && input.attachment_id !== lease) { res.end('{"attached":false}'); return; }
    if (['next', 'ack', 'reply', 'detach'].includes(input.action)) assert.equal(input.attachment_id, lease, 'active requests must carry the lease');
    if (input.action === 'read') { res.end(JSON.stringify({ attached, attachment: attachment(), result: { messages: [...history.values()].filter(m => m.attachment_id === lease) } })); return; }
    if (input.action === 'ack') {
      if (input.delivery === 'processed') {
        nativeAdmitted.add(input.message_id);
        processed.add(input.message_id);
      }
      else {
        const failure = failures.get(input.message_id);
        if (failure === 'lost') { nativeAdmitted.add(input.message_id); failures.delete(input.message_id); res.destroy(); return; }
        if (failure === 'transient') { failures.delete(input.message_id); res.end(JSON.stringify({ error: 'temporary fixture failure' })); return; }
        if (failure || processed.has(input.message_id)) {
          res.end(JSON.stringify({ error: failure === 'not_offered' ? 'message was not offered to this exact session' : `AgentWorks ${failure || 'receipt_refused'} (HTTP 409): PRIVATE_ERROR_TAIL` })); return;
        }
        nativeAdmitted.add(input.message_id);
      }
    }
    if (input.action === 'next' && !offers.length) {
      const timer = setTimeout(() => respond(res, input), nextWait);
      const wake = () => { clearTimeout(timer); if (!res.writableEnded) respond(res, input); };
      waiters.push(wake); res.on('close', () => { const i = waiters.indexOf(wake); if (i >= 0) waiters.splice(i, 1); });
    } else if (input.action === 'detach') {
      attached = false; respond(res, input);
    } else if (input.action === 'next') respond(res, input);
    else res.end(JSON.stringify({ attached, attachment: attachment(), result: { inbox_id: 'original-inbox' } }));
  });
  await new Promise(resolve => server.listen(path.join(root, 'bridge.sock'), resolve));
  await chmod(path.join(root, 'bridge.sock'), 0o600);
  const events = [], stderr = [];
  let child;
  const launch = () => { child = spawn(process.env.PI_BIN || 'pi', ['--mode', 'rpc', '--offline', '--no-extensions', '--no-mcp',
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
  child.on('error', error => stderr.push(error.message));
  };
  launch();
  const kill = async () => {
    if (child.exitCode === null && child.signalCode === null) {
      const exited = new Promise(resolve => child.once('exit', resolve));
      child.kill('SIGKILL'); await exited;
    }
  };
  t.after(async () => {
    child.stdin.end();
    await Promise.race([new Promise(resolve => child.once('exit', resolve)), new Promise(resolve => setTimeout(resolve, 2000))]);
    await kill();
    server.closeAllConnections(); await new Promise(resolve => server.close(resolve));
    await rm(root, { recursive: true, force: true });
  });
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
    for (const msg of offers) history.set(msg.message_id, msg);
    for (const wake of [...waiters]) wake();
  };
  const activate = async (override = {}, expected = 200, endpoint = '/activate') => {
    const record = JSON.parse(await readFile(path.join(root, 'pi-sessions', `${child.pid}.json`), 'utf8'));
    return new Promise((resolve, reject) => {
      const req = http.request({ socketPath: record.socket, path: endpoint, method: 'POST' }, res => {
        let body = ''; res.on('data', c => { body += c; }); res.on('end', () => { assert.equal(res.statusCode, expected, body); resolve(JSON.parse(body)); });
      });
      req.on('error', reject); req.end(JSON.stringify({ host_key: record.host_key, session_id: record.session_id, attachment_id: lease, ...override }));
    });
  };
  return { root, activate, usageStats: status => activate({}, status, '/usage-stats'), setUsageEnabled: value => { usageEnabled = value; }, get child() { return child; }, events, requests, command, offer,
    failures, processed, kill,
    restart: async () => { await kill(); events.length = 0; heldNext = false; launch(); await command('get_state'); },
    holdNext: () => { heldNext = true; },
    isHeld: () => heldDelivered,
    waitLong: () => { nextWait = 10000; },
    reattach: () => { lease = 'lease-two'; for (const wake of [...waiters]) wake(); },
    revoke: () => { attached = false; for (const wake of [...waiters]) wake(); },
    stderr };
}

const userMessage = (events, id) => events.find(e => e.type === 'message_start' && e.message.role === 'user' &&
  JSON.stringify(e.message.content).includes(`FIXTURE_MESSAGE_${id}`));
const admitted = (requests, id) => requests.find(r => r.action === 'ack' && r.message_id === id && r.delivery === 'admitted');

test('real Pi: idle push, busy tool follow-up, exact receipt, replay and revoke', async t => {
  const h = await harness(t);
  await h.activate({ attachment_id: 'wrong-lease' }, 409);
  assert.equal(h.requests.some(r => r.action === 'attach'), false, 'activation cannot create a lease');
  h.offer(['idle', 'idle-second']);
  await until(() => admitted(h.requests, 'idle'), 'idle native admission');
  assert.ok(userMessage(h.events, 'idle'));
  await until(() => admitted(h.requests, 'idle-second'), 'ordered batch native admission');
  const batchText = JSON.stringify(userMessage(h.events, 'idle').message.content);
  assert.ok(batchText.indexOf('FIXTURE_MESSAGE_idle') < batchText.indexOf('FIXTURE_MESSAGE_idle-second'));
  assert.equal(userMessage(h.events, 'idle'), userMessage(h.events, 'idle-second'), 'one native prompt for a catch-up batch');
  await until(() => h.events.some(e => e.type === 'agent_settled'), 'idle settled');
  assert.equal(h.requests.some(r => r.delivery === 'processed' || r.action === 'reply'), false);
  h.offer('unacknowledged', { message: 'FIXTURE_MESSAGE_unacknowledged NO_ACK' });
  await until(() => userMessage(h.events, 'unacknowledged'), 'display without receiver acknowledgement');
  await new Promise(resolve => setTimeout(resolve, 300));
  assert.equal(admitted(h.requests, 'unacknowledged'), undefined, 'native display alone does not admit');
  await h.command('prompt', { message: 'PROCESSED:unacknowledged' });
  await until(() => h.processed.has('unacknowledged'), 'one explicit processed receipt also admits');
  assert.equal(admitted(h.requests, 'unacknowledged'), undefined, 'processed needs no separate admitted call');

  h.events.length = 0;
  await h.command('prompt', { message: 'BUSY_TOOL' });
  await until(() => h.events.some(e => e.type === 'tool_execution_start'), 'tool in flight');
  h.offer('busy');
  await until(() => h.events.some(e => e.type === 'queue_update' && e.followUp?.length), 'native follow-up queue');
  assert.equal(admitted(h.requests, 'busy'), undefined, 'queued is not admitted');
  await h.activate();
  await h.activate(); // Repeated person activation retains one consumer.
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
  assert.equal(unattached.requests.some(r => r.action === 'attach'), false, 'RPC cannot attach without literal person input');
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

const notice = (events, text) => events.some(e => e.type === 'extension_ui_request' &&
  JSON.stringify(e).includes(text));
const sessionEntries = async h => (await readFile(path.join(h.root, 'session.jsonl'), 'utf8')).trim().split('\n').map(JSON.parse);

test('real Pi: refused/lost receipts do not block delivery or downgrade processed', async t => {
  const h = await harness(t);
  h.failures.set('refused', 'receipt_refused');
  h.failures.set('unoffered', 'not_offered');
  h.failures.set('unadmitted', 'not_admitted');
  h.offer(['refused', 'unoffered', 'unadmitted', 'later']);
  await until(() => admitted(h.requests, 'later'), 'later receipt survives final per-ID refusals');
  await until(() => notice(h.events, 'refused a delivery receipt'), 'person sees refusal');
  h.failures.set('lost', 'lost');
  h.offer('lost');
  await until(() => admitted(h.requests, 'lost'), 'admitted response deliberately lost');
  h.offer('after-lost');
  await until(() => admitted(h.requests, 'after-lost'), 'delivery proceeds while response is lost');
  await h.command('prompt', { message: 'PROCESSED:lost' });
  await until(() => h.processed.has('lost'), 'explicit processing after lost admission reply');
  const processedIndex = h.requests.findIndex(r => r.message_id === 'lost' && r.delivery === 'processed');
  await until(() => h.events.filter(e => e.type === 'agent_settled').length >= 4, 'processing settled');
  await new Promise(resolve => setTimeout(resolve, 3500));
  assert.equal(h.requests.slice(processedIndex + 1).some(r => r.message_id === 'lost' && r.delivery === 'admitted'), false);
  for (const id of ['refused', 'unoffered', 'unadmitted']) {
    assert.equal(h.requests.filter(r => r.message_id === id && r.delivery === 'admitted').length, 1, `${id} final refusal is not retried`);
  }
  h.failures.set('transient', 'transient');
  h.offer('transient');
  await until(() => admitted(h.requests, 'transient'), 'first admission temporarily fails');
  await h.command('prompt', { message: 'PROCESSED:transient' });
  await until(() => h.processed.has('transient'), 'processing replaces the pending admission retry');
  const transit = h.requests.filter(r => r.message_id === 'transient' && r.action === 'ack');
  assert.deepEqual(transit.map(r => r.delivery), ['admitted', 'processed']);
  assert.equal(JSON.stringify(h.events).includes('PRIVATE_ERROR_TAIL'), false);
  h.waitLong();
  h.failures.set('timer', 'transient'); h.offer('timer');
  await until(() => admitted(h.requests, 'timer'), 'timer message first attempt');
  const retryStart = Date.now();
  await until(() => h.requests.filter(r => r.message_id === 'timer' && r.delivery === 'admitted').length === 2, 'timer retries while next is blocked');
  assert.ok(Date.now() - retryStart < 5000, 'retry does not wait for the ten-second next response');
  const changed = await harness(t);
  changed.failures.set('changed', 'attachment_changed'); changed.offer('changed');
  await until(() => notice(changed.events, 'attachment changed'), 'lease-wide refusal stops receiver');
  const count = changed.requests.length;
  await new Promise(resolve => setTimeout(resolve, 700));
  assert.equal(changed.requests.length, count, 'lease-wide refusal stops all requests');
});

test('real Pi: multiple busy batches, exact frame admission and old-lease fencing', async t => {
  const h = await harness(t);
  await h.command('set_follow_up_mode', { mode: 'all-at-once' });
  await h.command('prompt', { message: 'BUSY_TOOL' });
  await until(() => h.events.some(e => e.type === 'tool_execution_start'), 'busy tool');
  h.offer(['batch-one', 'batch-one-second']);
  await until(() => h.events.some(e => e.type === 'queue_update' && e.followUp?.length === 1), 'first batch queued');
  h.offer(['batch-two', 'batch-two-second', 'altered']);
  await until(() => h.events.some(e => e.type === 'queue_update' && e.followUp?.length >= 2), 'second batch queued');
  const ids = ['batch-one', 'batch-one-second', 'batch-two', 'batch-two-second'];
  for (const id of ids) assert.equal(admitted(h.requests, id), undefined, 'queue alone is not admission');
  for (const id of ids) {
    await until(() => admitted(h.requests, id), `native frame ${id}`);
    assert.ok(userMessage(h.events, id));
  }
  await until(() => h.events.some(e => e.type === 'agent_settled'), 'all batches settled');
  assert.equal(admitted(h.requests, 'altered'), undefined, 'unchanged marker with changed body is not admitted');
  await until(() => notice(h.events, 'unconfirmed delivery'), 'changed frame recovery notice');
  const entries = await sessionEntries(h);
  assert.equal(entries.filter(e => e.customType === 'agentworks-pi-admitted').length, 4);
  assert.ok(h.requests.filter(r => r.action === 'next').every(r => r.attachment_id === 'lease-one'));
  // A request already waiting under the old lease must not drain the new one.
  h.reattach();
  h.offer('new-lease');
  await new Promise(resolve => setTimeout(resolve, 700));
  assert.equal(userMessage(h.events, 'new-lease'), undefined);
  await h.activate();
  // A stopped receiver may discover a person-created lease on explicit reconnect.
  await until(() => admitted(h.requests, 'new-lease'), 'new lease retains its offer');
  assert.ok(h.requests.filter(r => r.action === 'next').some(r => r.attachment_id === 'lease-two'));
});

test('real Pi: crash before offer response warns; saved native evidence restores tools', async t => {
  const h = await harness(t);
  await h.command('prompt', { message: 'ESTABLISH_EXISTING_CONVERSATION' });
  await until(() => h.events.some(e => e.type === 'agent_settled'), 'conversation persisted');
  h.holdNext();
  h.offer('interrupted');
  await until(h.isHeld, 'offer removed by bridge without reply');
  await h.kill();
  const markers = await readdir(path.join(h.root, 'pi-recovery'));
  assert.ok(markers.length, 'durable uncertainty exists outside buffered native entries');
  await h.restart();
  await until(() => notice(h.events, 'unconfirmed delivery'), 'crash recovery notice');
  assert.equal(userMessage(h.events, 'interrupted'), undefined, 'ambiguous offer is never reinjected');
  assert.equal((await readdir(path.join(h.root, 'pi-recovery'))).length, 1, 'reported previous-process marker is retired');
  assert.ok((await readdir(path.join(h.root, 'pi-recovery'))).every(name => !markers.includes(name)), 'notice acknowledges the stale marker');
  await h.command('prompt', { message: 'RECOVER:interrupted' });
  await until(() => admitted(h.requests, 'interrupted'), 'explicit receiver acknowledges a bounded mailbox read');
  assert.equal(userMessage(h.events, 'interrupted'), undefined, 'manual recovery never reinjects missing input');
  h.offer('recorded');
  await until(() => admitted(h.requests, 'recorded'), 'real native admission before simulated journal loss');
  await until(() => h.events.some(e => e.type === 'agent_settled'), 'native transcript persisted');
  await h.kill();
  // Reproduce a lost admission-entry write with the actual saved user message intact.
  const entries = (await sessionEntries(h)).filter(e => !(e.type === 'custom' &&
    ['agentworks-pi-admitted', 'agentworks-pi-acknowledged'].includes(e.customType) && e.data?.message_id === 'recorded'));
  assert.ok(entries.some(e => e.type === 'message' && e.message.role === 'user' && JSON.stringify(e).includes('FIXTURE_MESSAGE_recorded')));
  await writeFile(path.join(h.root, 'session.jsonl'), entries.map(e => JSON.stringify(e)).join('\n') + '\n');
  await h.restart();
  await h.command('prompt', { message: 'PROCESSED:recorded' });
  await until(() => h.processed.has('recorded'), 'recovered native evidence permits explicit processing');
  assert.equal(userMessage(h.events, 'recorded'), undefined, 'recovery does not resubmit the saved native message');
  await h.command('prompt', { message: 'REPLY:recorded' });
  await until(() => h.requests.some(r => r.action === 'reply' && r.message_id === 'recorded'), 'recovered native evidence permits explicit same-inbox reply');
});


test('real Pi: native SDK usage is exact-session numeric-only and opt-in gated', async t => {
  const h = await harness(t);
  await h.usageStats(409);
  // Pi persists a new session only after its first completed conversation.
  // This offline fixture turn creates the native header and branch entries.
  await h.command('prompt', { message: 'ESTABLISH_USAGE_FIXTURE' });
  await until(() => h.events.some(e => e.type === 'agent_settled'), 'usage fixture persisted');
  await h.kill();
  const entries = await sessionEntries(h);
  entries.push({ type: 'usage', id: 'abcdef12', parentId: entries.at(-1)?.id || null, timestamp: new Date().toISOString(), kind: 'cache_warm', provider: 'plat868-fixture', model: 'fixture', usage: { input: 4, output: 9, cacheRead: 20, cacheWrite: 3, totalTokens: 36, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } } });
  await writeFile(path.join(h.root, 'session.jsonl'), entries.map(e => JSON.stringify(e)).join('\n') + '\n');
  await h.restart();
  h.setUsageEnabled(true);
  const expected = await h.command('get_session_stats');
  assert.equal(expected.tokens.total, 36, 'native SDK includes the recorded cache-warming usage');
  const observed = await h.usageStats(200);
  assert.deepEqual(observed.tokens, expected.tokens);
  assert.equal(observed.sessionId, expected.sessionId);
  assert.equal(observed.userMessages, expected.userMessages);
  assert.deepEqual(Object.keys(observed).sort(), ['model', 'sessionId', 'tokens', 'userMessages']);
  h.setUsageEnabled(false);
  await h.usageStats(409);
});

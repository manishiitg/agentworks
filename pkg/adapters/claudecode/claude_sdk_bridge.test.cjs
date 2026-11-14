const { test } = require('node:test');
const assert = require('node:assert/strict');
const { mkdtempSync, writeFileSync, rmSync } = require('node:fs');
const { tmpdir } = require('node:os');
const { join } = require('node:path');
const { spawn } = require('node:child_process');
const { createHelperTracker } = require('./claude_sdk_bridge.cjs');

test('PLAT-746-1008 explicit skill occupies the last text block without losing images or arguments', () => {
  const { sdkUserContent } = require('./claude_sdk_bridge.cjs');
  assert.equal(sdkUserContent({ skill_name: 'review', text: 'Inspect this change\n/another is an argument' }), '/review\nInspect this change\n/another is an argument');
  const blocks = sdkUserContent({ skill_name: 'review', text: 'Inspect this image', images: [{ mediaType: 'image/png', data: 'pixels' }] });
  assert.equal(blocks[0].type, 'image');
  assert.deepEqual(blocks.at(-1), { type: 'text', text: '/review\nInspect this image' });
});

test('PLAT-872 native helper bookends ignore shell/ambient tasks and late runs', () => {
  const events = [], tracker = createHelperTracker(event => events.push(event.helper));
  const system = (subtype, extra) => tracker.observe({ type: 'system', subtype, ...extra });
  system('background_tasks_changed', { tasks: [
    { task_id: 'shell', task_type: 'local_bash' },
    { task_id: 'watcher', task_type: 'local_agent', ambient: true },
    { task_id: 'helper', run_id: '1', task_type: 'local_agent', description: 'Check parser' },
  ] });
  assert.equal(tracker.running(), true);
  system('task_notification', { task_id: 'helper', run_id: '1', status: 'completed' });
  system('background_tasks_changed', { tasks: [{ task_id: 'helper', run_id: '1' }] });
  assert.equal(tracker.running(), false);
  system('task_started', { task_id: 'helper', run_id: '2', task_type: 'local_agent' });
  system('task_notification', { task_id: 'helper', run_id: '1', status: 'failed' });
  assert.equal(tracker.running(), true);
  system('task_progress', { task_id: 'helper', run_id: '2' });
  system('task_notification', { task_id: 'helper', run_id: '2', status: 'stopped' });
  assert.deepEqual(events.map(event => event.state), ['started', 'finished', 'started', 'progress', 'interrupted']);
});

test('PLAT-872 async Agent without run_in_background survives parent result, accepts steer, and delivers completion', { timeout: 5000 }, async t => {
  const folder = mkdtempSync(join(tmpdir(), 'sdk-helper-'));
  t.after(() => rmSync(folder, { recursive: true, force: true }));
  // Same official query interface and native messages; no model or credentials.
  writeFileSync(join(folder, 'sdk.mjs'), `
export function query({ prompt, options }) {
  if (options.thinking?.type !== 'adaptive' || options.thinking?.display !== 'summarized' || options.extraArgs?.['thinking-display'] !== 'summarized') throw new Error('thinking summaries not requested');
  const iterator = (async function* () {
    const first = await prompt.next();
    yield { type: 'system', subtype: 'init', session_id: 'native-test' };
    yield { type: 'stream_event', event: { type: 'content_block_delta', delta: { type: 'thinking_delta', thinking: 'Checking the parser.' } } };
    yield { type: 'assistant', message: { content: [{ type: 'tool_use', id: 'agent-tool', name: 'Agent', input: { description: 'Check parser' } }] } };
    yield { type: 'system', subtype: 'task_started', task_type: 'local_agent', task_id: 'helper', run_id: '1', tool_use_id: 'agent-tool', description: 'Check parser' };
    yield { type: 'result', result: 'Helper is working', user_message_uuids: [first.value.uuid] };
    const steer = await prompt.next();
    if (steer.done) throw new Error('parent closed before helper completion');
    yield { type: 'user', uuid: steer.value.uuid };
    yield { type: 'system', subtype: 'task_notification', task_id: 'helper', run_id: '1', status: 'completed' };
    yield { type: 'result', result: 'Parser reviewed with your steer', user_message_uuids: [steer.value.uuid] };
  })();
  iterator.close = () => {};
  return iterator;
}`);
  const child = spawn(process.execPath, [join(__dirname, 'claude_sdk_bridge.cjs'), join(folder, 'sdk.mjs'), 'unused'], { stdio: ['pipe', 'pipe', 'pipe'], env: { ...process.env, AGENTWORKS_CLI_LAUNCH: '' } });
  t.after(() => child.kill());
  const events = []; let pending = '', stderr = '';
  child.stderr.on('data', data => stderr += data);
  child.stdout.on('data', data => {
    pending += data;
    while (pending.includes('\n')) {
      const end = pending.indexOf('\n'), line = pending.slice(0, end); pending = pending.slice(end + 1);
      const event = JSON.parse(line); events.push(event);
      if (event.type === 'result' && event.result === 'Helper is working') {
        assert.equal(event.sdk_continues, true);
        child.stdin.write(JSON.stringify({ type: 'steer', id: 'new-direction', text: 'Also check errors' }) + '\n');
      }
    }
  });
  const finished = new Promise((resolve, reject) => { child.once('error', reject); child.once('exit', code => code === 0 ? resolve() : reject(new Error(stderr || 'bridge failed'))); });
  child.stdin.write(JSON.stringify({ type: 'prompt', text: 'Delegate parser review' }) + '\n');
  await finished;
  assert.ok(events.some(event => event.type === 'stream_event' && event.event?.delta?.thinking === 'Checking the parser.'));
  assert.deepEqual(events.filter(event => event.type === 'sdk_helper').map(event => event.helper.state), ['started', 'finished']);
  assert.ok(events.some(event => event.type === 'sdk_control_result' && event.id === 'new-direction' && !event.is_error));
  const result = events.filter(event => event.type === 'result').at(-1);
  assert.equal(result.result, 'Parser reviewed with your steer');
  assert.equal(result.sdk_continues, false);
  assert.notEqual(result.sdk_parked_turn, true);
});

test('PLAT-746-899 Claude wakes itself after its background command; the host adopts that turn', { timeout: 5000 }, async t => {
  const folder = mkdtempSync(join(tmpdir(), 'sdk-wake-'));
  t.after(() => rmSync(folder, { recursive: true, force: true }));
  // The official SDK starts a turn on its own when a background task finishes (seen live 12 Nov).
  writeFileSync(join(folder, 'sdk.mjs'), `
export function query({ prompt }) {
  const iterator = (async function* () {
    const first = await prompt.next();
    yield { type: 'system', subtype: 'init', session_id: 'native-test' };
    yield { type: 'assistant', message: { content: [{ type: 'tool_use', id: 'bg-tool', name: 'Bash', input: { command: 'sleep 25', run_in_background: true } }] } };
    yield { type: 'system', subtype: 'background_tasks_changed', tasks: [{ task_id: 'bg', run_id: '1', tool_use_id: 'bg-tool' }] };
    yield { type: 'result', result: 'Started it', user_message_uuids: [first.value.uuid] };
    yield { type: 'system', subtype: 'task_notification', task_id: 'bg', run_id: '1', status: 'completed' };
    yield { type: 'system', subtype: 'background_tasks_changed', tasks: [] };
    yield { type: 'assistant', message: { content: [{ type: 'text', text: 'Its output was DONE' }] } };
    await new Promise(resolve => setTimeout(resolve, 200));
    yield { type: 'result', result: 'Its output was DONE' };
  })();
  iterator.close = () => {};
  return iterator;
}`);
  const child = spawn(process.execPath, [join(__dirname, 'claude_sdk_bridge.cjs'), join(folder, 'sdk.mjs'), 'unused'], { stdio: ['pipe', 'pipe', 'pipe'], env: { ...process.env, AGENTWORKS_CLI_LAUNCH: '' } });
  t.after(() => child.kill());
  const events = []; let pending = '', stderr = '';
  child.stderr.on('data', data => stderr += data);
  child.stdout.on('data', data => {
    pending += data;
    while (pending.includes('\n')) {
      const end = pending.indexOf('\n'), line = pending.slice(0, end); pending = pending.slice(end + 1);
      const event = JSON.parse(line); events.push(event);
      // The host sees Claude's own turn start and adopts it, as the Go session does.
      if (event.type === 'assistant' && event.sdk_parked_turn) child.stdin.write(JSON.stringify({ type: 'adopt' }) + '\n');
    }
  });
  const finished = new Promise((resolve, reject) => { child.once('error', reject); child.once('exit', code => code === 0 ? resolve() : reject(new Error(stderr || 'bridge failed'))); });
  child.stdin.write(JSON.stringify({ type: 'prompt', text: 'Run sleep 25 in the background' }) + '\n');
  await finished;
  const results = events.filter(event => event.type === 'result');
  assert.equal(results[0].sdk_parked, true);
  // Adopted: the wake's result is the host's turn (not dropped as a parked turn), and with no task left the session ends.
  assert.equal(results.at(-1).result, 'Its output was DONE');
  assert.notEqual(results.at(-1).sdk_parked_turn, true);
  assert.equal(results.at(-1).sdk_parked, undefined);
});

// PLAT-746-941: preserve per-task reports; a shell command has no invented
// model count and must not change the existing foreground hold decision.
test('background task progress retains only its own usage and real bookends', () => {
  const updates = [], tasks = createHelperTracker(event => updates.push(event.helper), true);
  const send = (subtype, task_id, extra = {}) => tasks.observe({ type: 'system', subtype, task_id, run_id: '1', ...extra });
  tasks.observe({ type: 'assistant', message: { content: [{ type: 'tool_use', id: 'shell-tool', name: 'Bash', input: { command: 'deploy.sh verify', run_in_background: true } }] } });
  send('task_started', 'shell', { task_type: 'local_bash', tool_use_id: 'shell-tool' });
  assert.equal(tasks.running(), false);
  send('task_started', 'agent-a', { task_type: 'local_agent' });
  send('task_started', 'agent-b', { task_type: 'local_agent' });
  send('task_progress', 'agent-a', { usage: { total_tokens: 18400, duration_ms: 134000 } });
  send('task_progress', 'agent-b', { usage: { total_tokens: 500, duration_ms: 1000 } });
  send('task_notification', 'agent-a', { status: 'completed', usage: { total_tokens: 24100, duration_ms: 182000 } });
  send('task_notification', 'shell', { status: 'completed' });
  assert.equal(updates.findLast(task => task.task_id === 'agent-a').total_tokens, 24100);
  assert.equal(updates.findLast(task => task.task_id === 'agent-b').total_tokens, 500);
  assert.equal(updates.findLast(task => task.task_id === 'shell').total_tokens, undefined);
  assert.equal(updates.findLast(task => task.task_id === 'shell').state, 'finished');
  assert.equal(tasks.running(), true); // agent-b, not the finished shell
});

// Ordinary CLI-managed Bash ids must not become extra background rows.
test('shell rows require explicit background or an observed timeout', () => {
  for (const mode of ['ordinary', 'explicit', 'auto']) {
    const updates = [], tracker = createHelperTracker(e => updates.push(e.helper), true);
    tracker.observe({ type: 'assistant', message: { content: [{ type: 'tool_use', id: 'bash', name: 'Bash', input: { command: 'deploy.sh verify', run_in_background: mode === 'explicit' } }] } });
    tracker.observe({ type: 'user', toolUseResult: { backgroundTaskId: 'bg', ...(mode === 'auto' ? { timedOutAfterMs: 1000 } : {}) }, message: { content: [{ type: 'tool_result', tool_use_id: 'bash' }] } });
    tracker.observe({ type: 'system', subtype: 'task_started', task_type: 'local_bash', tool_use_id: 'bash', task_id: 'bg', run_id: '1' });
    tracker.observe({ type: 'system', subtype: 'task_notification', task_type: 'local_bash', tool_use_id: 'bash', task_id: 'bg', run_id: '1', status: 'completed' });
    assert.equal(updates.length, mode === 'ordinary' ? 0 : 2);
    for (const task of updates) { assert.equal(task.command, 'deploy.sh verify'); assert.equal(task.background_mode, mode); }
    assert.equal(tracker.running(), false);
  }
});

test('helper type and description follow task-owned live progress without a poller', () => {
  const updates = [], tracker = createHelperTracker(e => updates.push(e.helper), true);
  tracker.observe({ type: 'assistant', message: { content: [{ type: 'tool_use', id: 'explore-tool', name: 'Agent', input: { subagent_type: 'Explore', description: 'Find routing code', run_in_background: true } }] } });
  tracker.observe({ type: 'system', subtype: 'task_started', task_id: 'explore', tool_use_id: 'explore-tool', task_type: 'local_agent', run_id: '1' });
  tracker.observe({ type: 'system', subtype: 'task_progress', task_id: 'explore', tool_use_id: 'explore-tool', run_id: '1', description: 'Grepping coding.go', usage: { total_tokens: 154200, duration_ms: 288000 } });
  const latest = updates.at(-1);
  assert.equal(latest.helper_type, 'Explore');
  assert.equal(latest.helper_description, 'Find routing code');
  assert.equal(latest.current_step, 'Grepping coding.go');
  assert.equal(latest.total_tokens, 154200);
});

test('PLAT-746-941 task-free warm turns reuse one query and Stop closes its input', { timeout: 5000 }, async t => {
  const folder = mkdtempSync(join(tmpdir(), 'sdk-warm-'));
  t.after(() => rmSync(folder, { recursive: true, force: true }));
  writeFileSync(join(folder, 'sdk.mjs'), `
let queries = 0;
export function query({ prompt }) {
  if (++queries !== 1) throw new Error('reopened the query');
  const iterator = (async function* () {
    yield { type: 'system', subtype: 'init', session_id: 'same-native-session' };
    for await (const message of prompt) {
      yield { type: 'result', result: message.message.content, user_message_uuids: [message.uuid] };
    }
  })();
  iterator.close = () => {};
  return iterator;
}`);
  const child = spawn(process.execPath, [join(__dirname, 'claude_sdk_bridge.cjs'), join(folder, 'sdk.mjs'), 'unused'], { stdio: ['pipe', 'pipe', 'pipe'], env: { ...process.env, AGENTWORKS_CLI_LAUNCH: '' } });
  t.after(() => child.kill());
  const events = []; let pending = '', stderr = '';
  const send = request => child.stdin.write(JSON.stringify(request) + '\n');
  child.stderr.on('data', data => stderr += data);
  child.stdout.on('data', data => {
    pending += data;
    while (pending.includes('\n')) {
      const end = pending.indexOf('\n'), line = pending.slice(0, end); pending = pending.slice(end + 1);
      const event = JSON.parse(line); events.push(event);
      if (event.type === 'result') {
        if (event.result === 'first') send({ type: 'prompt', text: 'second', keep_warm: true });
        else send({ type: 'stop_background', id: 'stop' });
      }
    }
  });
  const finished = new Promise((resolve, reject) => { child.once('error', reject); child.once('exit', code => code === 0 ? resolve() : reject(new Error(stderr || 'bridge failed'))); });
  send({ type: 'prompt', text: 'first', keep_warm: true });
  await finished;
  assert.equal(events.filter(event => event.type === 'system' && event.subtype === 'init').length, 1);
  assert.deepEqual(events.filter(event => event.type === 'result').map(event => event.result), ['first', 'second']);
  for (const result of events.filter(event => event.type === 'result')) {
    assert.equal(result.sdk_continues, false);
    assert.equal(result.sdk_parked, true);
    assert.ok(result.sdk_warm_memory.bridge_rss_bytes > 0);
  }
  assert.ok(events.some(event => event.type === 'sdk_control_result' && event.id === 'stop'));
});

test('PLAT-746-1000 native images reach prompt and steer but never echo into host events', { timeout: 5000 }, async t => {
  const folder = mkdtempSync(join(tmpdir(), 'sdk-images-'));
  t.after(() => rmSync(folder, { recursive: true, force: true }));
  const data = Buffer.from('native-image-fixture').toString('base64');
  writeFileSync(join(folder, 'sdk.mjs'), `
export function query({ prompt }) {
  const iterator = (async function* () {
    for (const expected of ['Inspect this file', 'A follow-up']) {
      const next = await prompt.next();
      const content = next.value.message.content;
      if (!Array.isArray(content) || content[0]?.text !== expected || content[1]?.type !== 'image' || content[1]?.source?.type !== 'base64' || content[1]?.source?.media_type !== 'image/png' || content[1]?.source?.data !== '${data}') throw new Error('native image content lost');
      yield { type: 'user', uuid: next.value.uuid, message: next.value.message };
    }
    yield { type: 'result', result: 'Images received' };
  })();
  iterator.close = () => {};
  return iterator;
}`);
  const child = spawn(process.execPath, [join(__dirname, 'claude_sdk_bridge.cjs'), join(folder, 'sdk.mjs'), 'unused'], { stdio: ['pipe', 'pipe', 'pipe'], env: { ...process.env, AGENTWORKS_CLI_LAUNCH: '' } });
  t.after(() => child.kill());
  const events = []; let pending = '', stderr = '', raw = '', sent = false;
  child.stderr.on('data', value => stderr += value);
  child.stdout.on('data', value => {
    raw += value; pending += value;
    while (pending.includes('\n')) {
      const end = pending.indexOf('\n'), event = JSON.parse(pending.slice(0, end)); pending = pending.slice(end + 1); events.push(event);
      if (event.type === 'user' && !sent) {
        sent = true;
        child.stdin.write(JSON.stringify({ type: 'steer', id: 'image-steer', text: 'A follow-up', images: [{ mediaType: 'image/png', data }] }) + '\n');
      }
    }
  });
  const finished = new Promise((resolve, reject) => { child.once('error', reject); child.once('exit', code => code === 0 ? resolve() : reject(new Error(stderr || 'bridge failed'))); });
  child.stdin.write(JSON.stringify({ type: 'prompt', text: 'Inspect this file', images: [{ mediaType: 'image/png', data }] }) + '\n');
  await finished;
  assert.equal(raw.includes(data), false, 'image bytes leaked through the user echo');
  assert.ok(events.some(event => event.type === 'sdk_control_result' && event.id === 'image-steer' && !event.is_error));
  assert.ok(events.some(event => event.type === 'result' && event.result === 'Images received'));
});

test('PLAT-746-1000 a kept SDK prompt iterator releases delivered image blocks', { timeout: 8000 }, async t => {
  const folder = mkdtempSync(join(tmpdir(), 'sdk-image-retention-'));
  t.after(() => rmSync(folder, { recursive: true, force: true }));
  writeFileSync(join(folder, 'sdk.mjs'), String.raw`
const tick = () => new Promise(resolve => setImmediate(resolve));
export function query({ prompt }) {
  let written = 0, firstID, secondID, imageRef;
  const transport = { write(serialized) {
    const message = JSON.parse(serialized);
    if (++written === 1) {
      if (message.message.content[1]?.source?.data?.length !== 1048576) throw new Error('image not written');
      firstID = message.uuid;
    } else {
      if (message.message.content !== 'Second warm turn') throw new Error('warm text changed');
      secondID = message.uuid;
    }
  } };
  // Mirror installed Agent SDK 0.3.311 streamInput: serialize/write each
  // yielded message, with no pending-message cache beyond that write.
  const pump = (async () => {
    for await (let message of prompt) {
      if (!imageRef) imageRef = new WeakRef(message.message.content[1]);
      await Promise.resolve(transport.write(JSON.stringify(message) + '\n'));
    }
  })();
  const iterator = (async function* () {
    while (written < 1) await tick();
    yield { type: 'user', uuid: firstID };
    yield { type: 'result', result: 'First image delivered', user_message_uuids: [firstID] };
    // Resume the input iterator and collect while the same native session is
    // parked, BEFORE a second prompt can overwrite its previous message.
    for (let i = 0; i < 8; i++) { await tick(); global.gc(); }
    await tick();
    if (imageRef.deref() !== undefined) throw new Error('delivered image retained while parked');
    yield { type: 'system', subtype: 'image_release_checked' };
    while (written < 2) await tick();
    yield { type: 'result', result: 'Second warm turn finished', user_message_uuids: [secondID] };
  })();
  iterator.close = () => {};
  return iterator;
}`);
  const child = spawn(process.execPath, ['--expose-gc', join(__dirname, 'claude_sdk_bridge.cjs'), join(folder, 'sdk.mjs'), 'unused'], { stdio: ['pipe', 'pipe', 'pipe'], env: { ...process.env, AGENTWORKS_CLI_LAUNCH: '' } });
  t.after(() => child.kill());
  let pending = '', stderr = '', released = false, finishedWarm = false;
  child.stderr.on('data', data => stderr += data);
  // The exit verdict reports the bridge error even if it closes a large write.
  child.stdin.on('error', () => {});
  child.stdout.on('data', data => {
    pending += data;
    while (pending.includes('\n')) {
      const end = pending.indexOf('\n'), event = JSON.parse(pending.slice(0, end)); pending = pending.slice(end + 1);
      if (event.type === 'system' && event.subtype === 'image_release_checked') {
        released = true;
        child.stdin.write(JSON.stringify({ type: 'prompt', keep_warm: true, text: 'Second warm turn' }) + '\n');
      }
      if (event.type === 'result' && event.result === 'Second warm turn finished') finishedWarm = true;
    }
  });
  const finished = new Promise((resolve, reject) => { child.once('error', reject); child.once('exit', code => code === 0 ? resolve() : reject(new Error(stderr || 'bridge failed'))); });
  child.stdin.write(JSON.stringify({ type: 'prompt', keep_warm: true, text: 'Inspect the image', images: [{ mediaType: 'image/png', data: 'A'.repeat(1048576) }] }) + '\n');
  await finished;
  assert.equal(released, true, 'image blocks were not released before the next warm prompt');
  assert.equal(finishedWarm, true, 'the same kept session failed its next turn');
});

test('PLAT-746-992 cached SDK suggestions arrive after result, skip first/unscoped/near-limit and never send themselves', { timeout: 5000 }, async t => {
  const folder = mkdtempSync(join(tmpdir(), 'sdk-next-prompt-'));
  t.after(() => rmSync(folder, { recursive: true, force: true }));
  writeFileSync(join(folder, 'sdk.mjs'), `
export function query({prompt,options}) {
  if (options.promptSuggestions !== true) throw new Error('native prediction option missing');
  let enabled = false;
  const iterator = (async function*(){
    for (let turn=1; turn<=4; turn++) {
      const input = await prompt.next();
      if (input.done) throw new Error('input closed early');
      if (input.value.message.content !== 'explicit input '+turn) throw new Error('prediction sent as input');
      yield {type:'result',result:'done '+turn,sample_enabled:enabled,user_message_uuids:[input.value.uuid]};
      if (enabled) yield {type:'prompt_suggestion',suggestion:'Check the diff',uuid:'suggestion-'+turn,session_id:'native'};
      if (turn===3) yield {type:'rate_limit_event',rate_limit_info:{status:'allowed_warning',rateLimitType:'five_hour',utilization:0.95}};
    }
  })();
  iterator.applyFlagSettings = async settings => {enabled=settings.promptSuggestionEnabled;};
  iterator.close = () => {};
  return iterator;
}`);
  const child=spawn(process.execPath,[join(__dirname,'claude_sdk_bridge.cjs'),join(folder,'sdk.mjs'),'unused'],{stdio:['pipe','pipe','pipe'],env:{...process.env,AGENTWORKS_CLI_LAUNCH:''}});
  t.after(()=>child.kill());
  const events=[];let pending='',stderr='';
  const send=turn=>child.stdin.write(JSON.stringify({type:'prompt',text:'explicit input '+turn,keep_warm:true,next_prompt_capable:true,next_prompt:turn>1&&turn!==3,next_prompt_turn:turn===3?'':'host-'+turn})+'\n');
  child.stderr.on('data',data=>stderr+=data);
  child.stdout.on('data',data=>{pending+=data;while(pending.includes('\n')){
    const end=pending.indexOf('\n'),event=JSON.parse(pending.slice(0,end));pending=pending.slice(end+1);events.push(event);
    if(event.type==='result'&&event.result==='done 1')send(2);
    if(event.type==='prompt_suggestion')send(3);
    if(event.type==='rate_limit_event')send(4);
  }});
  const finished=new Promise((resolve,reject)=>{child.once('error',reject);child.once('exit',code=>code===0?resolve():reject(new Error(stderr||'bridge failed')))});
  send(1);await finished;
  const results=events.filter(event=>event.type==='result');
  assert.deepEqual(results.map(event=>event.sample_enabled),[false,true,false,false]);
  const suggestions=events.filter(event=>event.type==='prompt_suggestion');
  assert.equal(suggestions.length,1);
  assert.equal(suggestions[0].sdk_prompt_turn,'host-2');
  assert.ok(events.indexOf(suggestions[0])>events.indexOf(results[1]));
});

test('warm turn setters await acknowledgements and patch only effortLevel', async () => {
  const { applySDKTurnSettings } = require('./claude_sdk_bridge.cjs');
  const calls = [], flags = { promptSuggestionEnabled: true };
  const run = {
    async setModel(value) { await Promise.resolve(); calls.push(['model', value]); },
    async setPermissionMode(value) { await Promise.resolve(); calls.push(['permission', value]); },
    async applyFlagSettings(value) { await Promise.resolve(); Object.assign(flags, value); calls.push(['flags', value]); },
  };
  const current = { model: 'one', permission_mode: 'dontAsk', effort: 'high' };
  const wanted = { model: 'two', permission_mode: 'plan', effort: null };
  await applySDKTurnSettings(run, current, wanted);
  assert.deepEqual(calls, [['model', 'two'], ['permission', 'plan'], ['flags', { effortLevel: null }]]);
  assert.deepEqual(current, wanted);
  assert.equal(flags.promptSuggestionEnabled, true);
  await applySDKTurnSettings(run, current, wanted);
  assert.equal(calls.length, 3);
  await assert.rejects(applySDKTurnSettings({ setModel: async () => { throw new Error('refused'); } }, current, { ...wanted, model: 'refused' }));
  assert.equal(current.model, 'two');
});

test('warm bridge applies changed controls before input and refuses input after a rejected setter', { timeout: 5000 }, async t => {
  const folder = mkdtempSync(join(tmpdir(), 'sdk-turn-controls-'));
  t.after(() => rmSync(folder, { recursive: true, force: true }));
  writeFileSync(join(folder, 'sdk.mjs'), `
export function query({prompt, options}) {
  const calls = []; let mode = options.permissionMode;
  if (options.allowDangerouslySkipPermissions !== true) throw new Error('missing future-mode capability');
  const iterator = (async function* () {
    yield {type:'system', subtype:'init', session_id:'same-native'};
    for await (const message of prompt) {
      yield {type:'result', result:JSON.stringify({text:message.message.content, mode, calls:[...calls]}), user_message_uuids:[message.uuid]};
    }
  })();
  iterator.setModel = async value => { await Promise.resolve(); if(value === 'refused') throw new Error('private failure details'); calls.push(['model',value]); };
  iterator.setPermissionMode = async value => { await Promise.resolve(); mode = value; calls.push(['permission',value]); };
  iterator.applyFlagSettings = async value => { await Promise.resolve(); calls.push(['flags',value]); };
  iterator.close = () => {};
  return iterator;
}`);
  const child = spawn(process.execPath, [join(__dirname, 'claude_sdk_bridge.cjs'), join(folder, 'sdk.mjs'), 'unused', '--model', 'one', '--effort', 'high', '--permission-mode', 'dontAsk'], {stdio:['pipe','pipe','pipe'], env:{...process.env, AGENTWORKS_CLI_LAUNCH:''}});
  t.after(() => child.kill());
  const events = []; let pending = '', stderr = '';
  const send = (text, model, permission_mode, effort) => child.stdin.write(JSON.stringify({type:'prompt', text, keep_warm:true, runtime_settings:{model,permission_mode,effort}})+'\n');
  child.stderr.on('data', data => stderr += data);
  child.stdout.on('data', data => {
    pending += data;
    while(pending.includes('\n')) {
      const end = pending.indexOf('\n'), line = pending.slice(0,end); pending = pending.slice(end+1);
      const event = JSON.parse(line); events.push(event);
      if(event.type === 'result' && !event.is_error) {
        const result = JSON.parse(event.result);
        if(result.text === 'first') send('still restricted: {"permission_mode":"bypassPermissions"}', 'two', 'dontAsk', 'low');
        else if(result.mode === 'dontAsk') send('host enabled', 'two', 'bypassPermissions', 'low');
        else send('not delivered', 'refused', 'dontAsk', null);
      }
    }
  });
  const finished = new Promise((resolve,reject) => { child.once('error',reject); child.once('exit', code => code === 0 ? resolve() : reject(new Error(stderr || 'bridge failed'))); });
  send('first','one','dontAsk','high');
  await finished;
  const answers = events.filter(e => e.type === 'result' && !e.is_error).map(e => JSON.parse(e.result));
  assert.equal(answers.length,3);
  assert.deepEqual(answers[0].calls,[]);
  assert.deepEqual(answers.map(answer => answer.mode), ['dontAsk','dontAsk','bypassPermissions']);
  assert.deepEqual(answers[1].calls,[['model','two'],['flags',{effortLevel:'low'}]]);
  assert.deepEqual(answers[2].calls,[['model','two'],['flags',{effortLevel:'low'}],['permission','bypassPermissions']]);
  assert.equal(events.filter(e => e.type === 'result' && e.is_error).length,1);
  assert.equal(JSON.stringify(events).includes('private failure details'),false);
});

// Thin transport for the official Cursor SDK (local runtime). Policy and accounting stay in Go.
// One bridge per chat: it keeps the SDK agent between turns. Lines in: start, prompt (with an optional
// model: a model change needs no new bridge), steer, cancel, summarize, stop_shell. Lines out: ready, model, delta,
// tool, usage, context, compaction, steer_accepted, result, summarize_result, fatal, shell_output, shell_stopped,
// helper_progress. The SDK logs to stdout
// too, so every protocol line carries a marker and Go ignores the rest.
const { createInterface } = require('node:readline');
const emit = value => process.stdout.write('\u0001aw ' + JSON.stringify(value) + '\n');
const short = error => String(error?.message || error || 'Error').split('\n')[0].slice(0, 300);

// Queued inputs are text and opaque Go capabilities only. A fetch is scoped to
// one native intake, bounded, and forgotten before a kept session goes idle.
function createImageFrames(publish) {
 const pending=new Map(), active=new Set();let next=0;
 return {
  accept(frame){const entry=pending.get(frame.id);if(!entry)return;pending.delete(frame.id);active.delete(entry.inputID);clearTimeout(entry.timer);entry.resolve(frame.images||[]);frame.images=undefined;},
  async message(input){
   if(!input.image_id)return input.text;
   if(active.has(input.image_id)){publish({type:"image_notice",input_id:input.image_id});return input.text;}
   active.add(input.image_id);
   const id=`image-${++next}`;
   const images=await new Promise(resolve=>{
    const entry={resolve,inputID:input.image_id,timer:setTimeout(()=>{pending.delete(id);active.delete(input.image_id);publish({type:'image_notice',input_id:input.image_id});resolve([]);},6000)};
    pending.set(id,entry);publish({type:'image_fetch',id,input_id:input.image_id});
   });
   return images.length?{text:input.text,images:images.map(image=>({data:image.data,mimeType:image.mimeType}))}:input.text;
  }
 };
}

// Live output of Cursor's own background shells (#3131). The SDK reports a shell call's start and end, not
// the output of one that keeps running after its call returned; Cursor writes that output to
// <project>/terminals/<shell id>.txt under its data folder: a header (pid, cwd, command, status) rewritten in
// place, the output, then an exit footer. The bridge follows each new file whose command matches a shell call
// of this chat and reports bounded chunks; Stop signals the pid in that header, only for a file it follows.
// Best effort: a file that cannot be found only means no live output; the final result is still Cursor's own.
const TERMINAL_POLL_MS = 1000, TERMINAL_READ_MAX = 64 * 1024, TERMINAL_CHUNK_MAX = 8 * 1024;
const TERMINAL_MAX_TAILS = 16, TERMINAL_CALL_WINDOW_MS = 10 * 60 * 1000, TERMINAL_HEADER_MAX = 8 * 1024;
const cursorProjectSlug = path => String(path || '').replace(/[^a-zA-Z0-9]/g, '-').replace(/-+/g, '-').replace(/^-+|-+$/g, '');
function cursorTerminalDirs(cwd, env = process.env, home = require('node:os').homedir()) {
  const { join } = require('node:path');
  const { realpathSync } = require('node:fs');
  const roots = env.CURSOR_DATA_DIR?.trim() ? [join(env.CURSOR_DATA_DIR.trim(), 'projects')] : home ? [join(home, '.cursor', 'projects')] : [];
  const paths = [cwd];
  try { const real = realpathSync(cwd); if (real !== cwd) paths.push(real); } catch { /* A missing folder has no terminals. */ }
  const dirs = [];
  for (const root of roots) for (const path of paths) { const slug = cursorProjectSlug(path); if (slug) dirs.push(join(root, slug, 'terminals')); }
  return [...new Set(dirs)];
}
// parseTerminalHeader reads Cursor's header block: "---", key: value lines, "---".
function parseTerminalHeader(text) {
  if (!text.startsWith('---\n')) return undefined;
  const end = text.indexOf('\n---\n', 3);
  if (end < 0) return undefined;
  const header = { end: end + 5 };
  for (const line of text.slice(4, end).split('\n')) {
    const at = line.indexOf(': ');
    if (at < 0) continue;
    const key = line.slice(0, at), raw = line.slice(at + 2).trim();
    if (key === 'command' || key === 'cwd') { try { header[key] = JSON.parse(raw); } catch { header[key] = raw; } }
    else if (key === 'pid') header.pid = Number.parseInt(raw, 10) || undefined;
    else if (key === 'status') header.status = raw;
  }
  return header;
}
// Cursor's exit footer: "\n---\nexit_code: N\n...ended_at: ...\n---\n" (or "error: ..."); not output.
const terminalFooter = /\n?---\n(?:exit_code|error): [\s\S]*$/;
function createTerminalTail(publish, { dirs = [], pollMs = TERMINAL_POLL_MS, now = Date.now, kill = process.kill.bind(process) } = {}) {
  const fs = require('node:fs'), { join } = require('node:path');
  const calls = [], tails = new Map(), known = new Set();
  let timer, baselined = false;
  const send = (tail, text, extra = {}) => publish({ type: 'shell_output', call_id: tail.callId, shell_id: tail.shellId,
    label: tail.command.slice(0, 300), text, ...(tail.dropped ? { dropped: true } : {}), ...extra });
  const list = () => {
    const files = [];
    for (const dir of dirs) {
      let names = [];
      try { names = fs.readdirSync(dir); } catch { continue; }
      for (const name of names) if (/^\d+\.txt$/.test(name)) files.push(join(dir, name));
    }
    return files;
  };
  // Files there before this bridge's first shell belong to earlier chats or runs.
  const baseline = () => { if (baselined) return; baselined = true; for (const file of list()) known.add(file); };
  const readHeader = path => {
    let fd;
    try {
      fd = fs.openSync(path, 'r');
      const bytes = Buffer.alloc(TERMINAL_HEADER_MAX);
      const n = fs.readSync(fd, bytes, 0, bytes.length, 0);
      return parseTerminalHeader(bytes.subarray(0, n).toString());
    } catch { return undefined; } finally { if (fd !== undefined) fs.closeSync(fd); }
  };
  const read = tail => {
    let size;
    try { size = fs.statSync(tail.path).size; } catch { return ''; }
    if (size <= tail.offset) return '';
    const start = size - tail.offset > TERMINAL_READ_MAX ? size - TERMINAL_READ_MAX : tail.offset;
    const bytes = Buffer.alloc(size - start);
    let fd;
    try { fd = fs.openSync(tail.path, 'r'); fs.readSync(fd, bytes, 0, bytes.length, start); } catch { return ''; } finally { if (fd !== undefined) fs.closeSync(fd); }
    let first = 0;
    if (start > tail.offset) { tail.dropped = true; tail.decoder = new TextDecoder(); while (first < bytes.length && (bytes[first] & 0xc0) === 0x80) first++; }
    tail.offset = size;
    return tail.decoder.decode(bytes.subarray(first), { stream: true });
  };
  const flush = (tail, text) => {
    if (Buffer.byteLength(text) > TERMINAL_CHUNK_MAX) {
      const buffer = Buffer.from(text);
      let cut = buffer.length - TERMINAL_CHUNK_MAX;
      while (cut < buffer.length && (buffer[cut] & 0xc0) === 0x80) cut++;
      text = buffer.subarray(cut).toString(); tail.dropped = true;
    }
    if (text) { send(tail, text); tail.dropped = false; }
  };
  const finish = tail => {
    tails.delete(tail.shellId);
    flush(tail, (tail.held + read(tail) + tail.decoder.decode()).replace(terminalFooter, ''));
    send(tail, '', { done: true });
  };
  const match = header => {
    const command = String(header.command || '').trim();
    let index = calls.findIndex(call => call.command === command);
    if (index < 0) return undefined;
    return calls.splice(index, 1)[0];
  };
  const poll = () => {
    const cutoff = now() - TERMINAL_CALL_WINDOW_MS;
    while (calls.length && calls[0].at < cutoff) calls.shift();
    if (calls.length) {
      for (const path of list()) {
        if (known.has(path) || tails.size >= TERMINAL_MAX_TAILS) continue;
        const header = readHeader(path);
        if (!header) continue; // Not written yet: Cursor writes the header with the first output.
        known.add(path);
        const call = match(header);
        if (!call) continue;
        const shellId = /(\d+)\.txt$/.exec(path)[1];
        const tail = { path, callId: call.callId, shellId, command: String(header.command || ''), pid: header.pid, offset: header.end, held: '', dropped: false, decoder: new TextDecoder() };
        tails.set(shellId, tail);
        send(tail, ''); // The host learns at once that this command runs on its own and can be stopped.
      }
    }
    for (const tail of [...tails.values()]) {
      const header = readHeader(tail.path);
      if (header?.pid) tail.pid = header.pid;
      if (!header || header.status !== 'running') { finish(tail); continue; }
      // Hold a possible partial footer line back until the command ends.
      const text = tail.held + read(tail);
      const cut = text.lastIndexOf('\n---');
      if (cut >= 0 && text.length - cut < 512) { tail.held = text.slice(cut); flush(tail, text.slice(0, cut)); }
      else { tail.held = ''; flush(tail, text); }
    }
    if (!calls.length && !tails.size) { clearInterval(timer); timer = undefined; }
  };
  const ensure = () => { if (!timer) { timer = setInterval(poll, pollMs); timer.unref?.(); } };
  return {
    // call registers a shell call of this chat; its file, if it goes to the background, is followed.
    call(callId, command) {
      if (typeof callId !== 'string' || !callId || typeof command !== 'string' || !command.trim()) return;
      baseline();
      calls.push({ callId, command: command.trim(), at: now() });
      if (calls.length > 64) calls.shift();
      ensure();
    },
    // stop signals one followed shell; false when it is not followed or already gone.
    stop(shellId) {
      const tail = tails.get(String(shellId));
      if (!tail?.pid) return false;
      try { kill(-tail.pid, 'SIGTERM'); return true; } catch { /* Not a group leader: signal the process. */ }
      try { kill(tail.pid, 'SIGTERM'); return true; } catch { return false; }
    },
    poll,
    running: () => [...tails.keys()],
    close() { clearInterval(timer); timer = undefined; for (const tail of [...tails.values()]) send(tail, '', { done: true }); tails.clear(); },
  };
}

async function main() {
  // The SDK finds its bundled programs (ripgrep for grep/glob, the tree-sitter shell parser) in
  // node_modules above the running script. This script sits in a temp folder, so point argv[1] at
  // the SDK itself; otherwise every grep/glob fails "Ripgrep path not configured".
  process.argv[1] = process.argv[2];
  const { Agent, Cursor, createAgentPlatform } = require(process.argv[2]);
  // The installed SDK's own version, for the start log line (the provisioner pins it; this shows what ran).
  let sdkVersion;
  try {
    const metadata = require(require('node:path').join(require('node:path').dirname(process.argv[2]), '..', '..', 'package.json'));
    if (metadata?.name === '@cursor/sdk' && typeof metadata.version === 'string') sdkVersion = metadata.version.slice(0, 40);
  } catch { /* A test double or a moved entry has no package file. */ }
  const imageFrames=createImageFrames(emit);
  let terminals; // follows this chat's background shells once start names its folder (#3131)
  let agent, run, start, store, requestedModel, followups = [], early = [], busy = false;
  // PLAT-746-999: observe only metadata after the native turn boundary. Keep
  // the last run's status listener across the foreground result: a completion
  // arriving while the bridge is idle must still be visible. No wake policy.
  let stopRunObservation;
  const eventTypes = new Set(['system', 'user', 'assistant', 'tool_call', 'thinking', 'status', 'request', 'task', 'usage',
    'text-delta', 'thinking-delta', 'thinking-completed', 'tool-call-started', 'tool-call-completed',
    'tool-call-delta', 'user-message-appended', 'partial-tool-call', 'token-delta', 'summary',
    'summary-started', 'summary-completed', 'shell-output-delta', 'turn-ended', 'step-started',
    'step-completed', 'tool-requests-listed', 'snapshot', 'changed', 'wait-resolved', 'subscribe-unavailable']);
  const statuses = new Set(['running', 'finished', 'error', 'cancelled', 'CREATING', 'RUNNING', 'FINISHED', 'ERROR', 'CANCELLED', 'EXPIRED']);
  const metaID = value => typeof value === 'string' && /^[a-zA-Z0-9_:.-]{1,160}$/.test(value) ? value : undefined;
  const observe = (state, kind, event, status) => {
    if (!state.afterEnd) return;
    emit({ type: 'background_meta', kind, event_type: eventTypes.has(event?.type) ? event.type : 'unknown',
      agent_id: metaID(event?.agent_id) ?? metaID(agent?.agentId), run_id: metaID(event?.run_id) ?? metaID(state.run?.id),
      parent_run_id: metaID(state.run?.id),
      call_id: metaID(event?.call_id ?? event?.callId),
      status: statuses.has(status) ? status : undefined, phase: state.phase });
  };

  // This process belongs to one account; keep a successful discovery across starts/resumes. The lookup begins as
  // the bridge starts, so a start rarely waits, and never keeps a failed or empty result: the next start asks again
  // (PLAT-872-64: an empty result cached for the bridge's life sent the bare model without the account's fixed
  // settings, and a Plus account refused it). A start waits up to 15 s, asking again after a quick failure: the
  // earlier 3 s limit started about one Cursor chat in six without the settings, and Plus refused it (PLAT-872-93).
  // Go remembers the settings a start found, so later chats of this account skip the lookup.
  let accountModels, catalog = 'not_needed', lookup, lookupMs;
  const listModels = () => lookup ??= Cursor.models.list({ apiKey: process.env.CURSOR_API_KEY || undefined })
    .then(models => { if (Array.isArray(models) && models.length) accountModels = models; else catalog = 'empty'; return models; },
          error => { catalog = 'error: ' + short(error); })
    .finally(() => { lookup = undefined; });
  listModels();
  const modelDefaults = async (limit = 15000) => { // limit: tests shorten the wait
    const began = Date.now();
    while (!accountModels) {
      const left = limit - (Date.now() - began);
      if (left <= 0) { catalog = 'timeout'; break; }
      let timer;
      const waited = await Promise.race([listModels().then(() => 'done'), new Promise(resolve => { timer = setTimeout(() => resolve('timeout'), left); })]);
      clearTimeout(timer);
      if (waited === 'timeout') { catalog = 'timeout'; break; }
      if (!accountModels) await new Promise(resolve => setTimeout(resolve, Math.min(1000, Math.max(0, limit - (Date.now() - began)))));
    }
    lookupMs = Date.now() - began;
    if (!accountModels) return []; // Discovery trouble must not prevent an otherwise valid chat.
    catalog = 'ok';
    return accountModels;
  };
  // PLAT-872-64: an omitted parameter list is not the account's default variant.
  // Composer's account catalog, for example, supplies explicit fast=false.
  // Preserve explicit selections; resolve bare IDs with this account only.
  const resolveModel = async (model, limit) => {
    if (model.params?.length) return model;
    const models = await modelDefaults(limit);
    const variant = models.find(entry => entry.id === model.id)?.variants?.find(entry => entry.isDefault);
    return variant?.params?.length ? { ...model, params: variant.params } : model;
  };
  const paramsText = model => (model.params || []).map(p => p.id + '=' + p.value).join(',');
  // Typed SDK failures (RateLimitError, codes like PRO_USER_USAGE_LIMIT, HTTP 429) go to Go as fields:
  // Go decides what a usage limit is. Only short, plain values are passed on.
  const errorInfo = error => {
    if (!error || typeof error !== 'object') return {};
    const code = typeof error.code === 'string' && /^[A-Za-z0-9_.:-]{1,80}$/.test(error.code) ? error.code : undefined;
    const status = Number.isInteger(error.status) && error.status > 0 && error.status < 1000 ? error.status : undefined;
    const name = typeof error.name === 'string' && /^[A-Za-z]{1,60}$/.test(error.name) && error.name !== 'Error' ? error.name : undefined;
    return { error_code: code, error_status: status, error_name: name };
  };
  // Context fill: Cursor keeps it on the agent's latest checkpoint (tokenDetails: usedTokens of maxTokens), read
  // through the SDK's public createAgentPlatform. Only with this chat's private store: the shared store is opened
  // one bridge at a time ("database is locked"). Never fails or holds a turn: no answer within 3 s is unknown.
  let platform;
  const contextFill = async () => {
    if (!store || !agent || typeof createAgentPlatform !== 'function') return undefined;
    let timer;
    try {
      return await Promise.race([(async () => {
        platform ??= await createAgentPlatform({ workspaceRef: start.cwd, localStore: store });
        const details = (await platform.checkpointStore.loadLatest(agent.agentId))?.tokenDetails;
        const used = Number(details?.usedTokens), max = Number(details?.maxTokens);
        return Number.isSafeInteger(used) && used > 0 && Number.isSafeInteger(max) && max > 0 ? { used, max } : undefined;
      })(), new Promise(resolve => { timer = setTimeout(resolve, 3000); })]);
    } catch { return undefined; } finally { clearTimeout(timer); }
  };
  // Cancels this agent's runs still marked running; true when there was one. Throws only when a stale
  // run could not be cancelled; if the runs cannot be listed, the agent is kept as before.
  const cancelStale = async agentId => {
    const where = { runtime: 'local', cwd: start.cwd };
    let cursor, found = false;
    do {
      let page;
      try { page = await Agent.listRuns(agentId, { ...where, cursor }); } catch { return found; }
      for (const old of page.items) {
        if (old.status === 'running') { await Agent.cancelRun(old.id, where); found = true; }
      }
      cursor = page.nextCursor;
    } while (cursor);
    return found;
  };
  // Never leave a run active at Cursor when this bridge is stopped: a server restart (SIGTERM) or Go
  // closing stdin cancels the in-flight run first, so the next resume of this agent can send again.
  const cancelRun = () => {
    let timer;
    return Promise.race([run?.cancel().catch(() => {}), new Promise(resolve => { timer = setTimeout(resolve, 3000); })]).finally(() => clearTimeout(timer));
  };
  // agentworks#3128: the SDK can reject a promise nothing awaits (its stall detector aborts a request
  // and leaves the rejection behind) or throw from a timer. Node would end this bridge and fail the
  // chat. Log each one instead. The stall abort is the SDK's own recovery (it retries), so the run goes
  // on; anything else fails only the active run, with a typed error, and the bridge keeps serving.
  let failRun;
  const isStallAbort = reason => reason instanceof Error && reason.name === 'AbortError' &&
    typeof reason.stack === 'string' && /^\s+at (?:\S+\.)?onStall\b/m.test(reason.stack);
  const sdkFault = origin => reason => {
    const stall = isStallAbort(reason);
    emit({ type: 'sdk_rejection', kind: origin, action: stall ? 'recovering' : failRun ? 'run_failed' : 'logged', error: short(reason) });
    if (!stall) failRun?.(reason);
  };
  process.on('unhandledRejection', sdkFault('unhandled_rejection'));
  process.on('uncaughtException', sdkFault('uncaught_exception'));
  for (const signal of ['SIGTERM', 'SIGHUP', 'SIGINT']) {
    process.on(signal, () => { drop(); cancelRun().finally(() => process.exit(143)); });
  }
  const lines = createInterface({ input: process.stdin });
  const queue = [];
  let wake;

  const next = async () => {
    while (!queue.length) await new Promise(resolve => { wake = resolve; });
    wake = undefined;
    return queue.shift();
  };
  const options = () => ({
    apiKey: process.env.CURSOR_API_KEY || undefined,
    model: start.model,
    // Rules (system prompt) and skills come from this chat's own folder Go prepared (start.dirs); its MCP
    // servers arrive explicitly in start.mcp_servers.
    local: { cwd: start.cwd, ...(store ? { store } : {}), ...(start.dirs ? { dirs: start.dirs } : {}), settingSources: ['project', 'user'],
      // Task subagents get this agent's tool allowlist; without it a child gets Cursor's full toolset.
      subagentInherit: {},
      // Retry transport errors and stalls inside the SDK (1.3 reads this; pinned so a default change cannot drop it).
      enableAgentRetries: true },
    ...(start.mode ? { mode: start.mode } : {}),
    ...(start.tools ? { tools: start.tools } : {}),
    ...(start.mcp_servers ? { mcpServers: start.mcp_servers } : {}),
  });
  // Steers handed to a running run. The SDK settles a steer only when the server answers, and one it
  // had queued is settled "revert_to_followup" only as its run ends, often after run.wait() returned.
  // So a turn waits for every steer's outcome before it ends, and answers each one the run did not
  // take (or never confirmed) as a follow-up: an accepted message is never dropped.
  let steers = [], steerIntakes = Promise.resolve();
  const settle = (entry, outcome) => {
    if (entry.settled) return;
    entry.settled = true;
    emit({ type: 'steer_outcome', id: entry.id, status: outcome });
    if (outcome !== 'complete_delivered') followups.push({text:entry.text,image_id:entry.image_id});
  };
  const drop = () => { for (const entry of steers) entry.settled = true; steers = []; followups = []; steerIntakes = Promise.resolve(); };
  // Steering and cancel arrive while a run streams, so they are handled as they are read.
  let summarizing = false;
  const control = request => {
    if (request.type === 'steer') {
      // Go only steers during its turn; before this turn's prompt arrives, hold it for that prompt.
      // A late image capability cannot survive this host turn's cleanup.
      // Return it to the durable host lane rather than accepting a stale ID.
      if (!busy && request.image_id) { emit({type:'steer_accepted',id:request.id,accepted:false});return true; }
      if (!busy) { early.push({text:request.text,image_id:request.image_id}); emit({ type: 'steer_accepted', id: request.id, accepted: true }); return true; }
      // The prompt arrived but its run is still starting: answer it right after that run, same turn.
      if (!run) { followups.push({text:request.text,image_id:request.image_id}); emit({ type: 'steer_accepted', id: request.id, accepted: true }); return true; }
      emit({ type: 'steer_accepted', id: request.id, accepted: true });
      const entry = { id: request.id, text: request.text, image_id:request.image_id };
      steers.push(entry);
      const target=run;
      // Keep queued entries as IDs/text; fetch at the head asynchronously,
      // then invoke native steers in receipt order.
      // Box the outcome promise: ordering intake must NOT wait for delivery
      // outcomes, which Cursor may settle only after this run ends.
      emit({type:'image_order',id:entry.id,action:'queued'});
      const submitted = steerIntakes.then(async () => {
        if (entry.settled) { emit({type:'image_order',id:entry.id,action:'skipped',status:'cancelled'});return {outcome:'cancelled'}; }
        const message = await imageFrames.message(entry);
        if (entry.settled) { emit({type:'image_order',id:entry.id,action:'skipped',status:'cancelled'});return {outcome:'cancelled'}; }
        const outcome = target.steer ? target.steer(message) : 'revert_to_followup';
        emit({type:'image_order',id:entry.id,action:'submitted'});
        return {outcome};
      });
      steerIntakes = submitted.then(() => {}, () => {});
      entry.done = submitted.then(value => value.outcome)
        .then(outcome => settle(entry, outcome), () => settle(entry, 'error'));
      return true;
    }
    if (request.type === 'cancel') { drop(); run?.cancel().catch(() => {}); return true; }
    return false;
  };
  lines.on('line', line => {
    const request = JSON.parse(line);
    if(request.type==='image_frame'){imageFrames.accept(request);return;}
    if (request.type === 'stop_shell') { emit({ type: 'shell_stopped', id: request.id, accepted: terminals?.stop(request.shell_id) === true }); return; }
    // A compaction is not a person's turn: the host keeps the message for the next one.
    if (summarizing && request.type === 'steer') { emit({ type: 'steer_accepted', id: request.id, accepted: false }); return; }
    if ((busy || request.type === 'steer') && control(request)) return;
    queue.push(request); wake?.();
  });
  lines.on('close', () => { if (busy) { drop(); cancelRun(); } queue.push({ type: 'close' }); wake?.(); });
  for (;;) {
    const request = await next();
    if (request.type === 'close') break;
    if (control(request)) continue;
    if (request.type === 'start') {
      start = request;
      terminals ??= createTerminalTail(emit, { dirs: cursorTerminalDirs(start.cwd) });
      if (start.store_root) {
        // One private store per AgentWorks chat. The shared SDK index may
        // contain other conversations and must never enter a transfer.
        const { SqliteLocalAgentStore } = require(require('node:path').join(require('node:path').dirname(process.argv[2]), 'sqlite.js'));
        store = await SqliteLocalAgentStore.open({ workspaceRef: start.cwd, stateRoot: start.store_root });
      }
      if (!process.env.CURSOR_API_KEY && (await Cursor.auth.status()).status !== 'logged-in') {
        throw new Error('Sign in again to use Cursor. Open Providers, select this account, and choose Sign in.');
      }
      requestedModel = JSON.stringify(start.model);
      start.model = await resolveModel(start.model, start.catalog_wait_ms);
      if (start.agent_id) {
        try { agent = await Agent.resume(start.agent_id, options()); }
        catch (error) {
          // A saved agent this store does not have: start fresh and let Go send the history.
          if (start.require_native_resume || !/not.?found|unknown agent|does not exist/i.test(short(error))) throw error;
          agent = undefined;
        }
      }
      // A run this chat's earlier bridge left active (killed by a restart before run.cancel landed) blocks
      // every later send with "already has active run". This bridge has started no run yet, so any
      // running run is stale: cancel it, or start a new agent and let Go send the history.
      let stale;
      if (agent) {
        try { stale = (await cancelStale(agent.agentId)) ? 'cancelled' : undefined; }
        catch { stale = 'new_agent'; agent = undefined; }
      }
      const resumed = Boolean(agent);
      if (start.require_native_resume && !resumed) throw new Error('The transferred native Cursor agent could not be resumed. Its saved history was kept.');
      if (start.require_native_resume && agent.agentId !== start.agent_id) throw new Error('Cursor returned a different native agent. The transferred chat was kept.');
      if (!agent) agent = await Agent.create(options());
      emit({ type: 'ready', agent_id: agent.agentId, resumed, ...(stale ? { stale, stale_agent: start.agent_id } : {}), model_catalog: catalog, ...(lookupMs !== undefined ? { model_lookup_ms: lookupMs } : {}), model_params: paramsText(start.model), sdk_version: sdkVersion });
      continue;
    }
    if (request.type === 'summarize') {
      // Idle compaction (PLAT-872): Cursor's own /summarize (SDK 1.3 agent.summarize()). It starts a run like
      // send(); its stream reports the summary's status and wait() ends it. No person message is sent.
      busy = summarizing = true;
      try {
        if (typeof agent?.summarize !== 'function') { emit({ type: 'summarize_result', status: 'unsupported' }); continue; }
        const before = await contextFill();
        let summaryStatus, fallback;
        run = await agent.summarize();
        const current = run;
        for await (const event of current.stream()) {
          if (event.type !== 'summary') continue;
          summaryStatus = event.status;
          fallback = event.fallback === true || undefined;
          emit({ type: 'compaction', status: event.status, fallback });
        }
        const result = await current.wait();
        const after = result.status === 'finished' ? await contextFill() : undefined;
        if (after) emit({ type: 'context', used: after.used, max: after.max });
        emit({ type: 'summarize_result', status: result.status, summary_status: summaryStatus, fallback,
          before_used: before?.used, after_used: after?.used, usage: result.usage,
          error: result.error ? short(result.error) : undefined, ...errorInfo(result.error) });
      } catch (error) {
        emit({ type: 'summarize_result', status: 'error', error: short(error), ...errorInfo(error) });
      } finally { busy = summarizing = false; run = undefined; drop(); }
      continue;
    }
    if (request.type !== 'prompt') continue;
    busy = true;
    followups = early;
    early = [];
    let text = {text:request.text,image_id:request.image_id}, last;
    try {
      // A model chosen for this message: same bridge, same native agent; send({ model }) switches it.
      if (request.model && JSON.stringify(request.model) !== requestedModel) {
        const from = start.model.id;
        requestedModel = JSON.stringify(request.model);
        start.model = await resolveModel(request.model, start.catalog_wait_ms);
        emit({ type: 'model', model_id: start.model.id, from, model_params: paramsText(start.model), model_catalog: request.model.params?.length ? 'not_needed' : catalog });
      }
      while (text !== undefined) {
        // After Cursor says its turn ended, a brief stream error can make the SDK resume the same run
        // from an older checkpoint; those resumes save nothing and the run only ends much later, if at
        // all (RTS 12 Nov: the answer was out, then 15 silent minutes). So once the turn has ended and
        // nothing new starts for a short while, the answer already streamed is the reply: the run is
        // cancelled and the turn finishes. A run that ends with the SDK's own "no progress" error after
        // the turn ended has finished too.
        const observation = { afterEnd: false, phase: 'foreground', run: undefined };
        let turnEnded = false, quietEnded = false, faulted = false, quiet, giveUp, fail;
        const sdkFailed = new Promise(resolve => { fail = resolve; });
        failRun = reason => {
          failRun = undefined;
          faulted = true;
          fail({ status: 'error', result: '', error: { message: 'Cursor SDK internal error: ' + short(reason) } });
        };
        const outstandingWork = new Set(), detachedWork = new Set();
        const pendingHelpers = [];
        const helper = (callId, status, durationMs) => {
          if (!metaID(callId)) return;
          const row = {type:'helper',agent_id:metaID(agent.agentId),call_id:callId,status,
            duration_ms:Number.isFinite(durationMs) && durationMs>=0 && durationMs<=Number.MAX_SAFE_INTEGER ? Math.floor(durationMs) : undefined};
          if (!observation.run) pendingHelpers.push(row);
          else emit({...row,run_id:metaID(observation.run.id)});
        };
        // A helper's own steps, streamed on its task call: its live conversation (#3132).
        const helperProgress = (callId, step) => {
          if (!metaID(callId) || !step) return;
          const base = { type: 'helper_progress', call_id: callId, agent_id: metaID(agent.agentId) };
          if (step.type === 'text-delta' && step.text) emit({ ...base, kind: 'text', text: step.text });
          else if (step.type === 'tool-call-started' && step.toolCall && metaID(step.callId)) emit({ ...base, kind: 'tool_started', id: step.callId, name: step.toolCall.type, args: step.toolCall.args });
          else if (step.type === 'tool-call-completed' && step.toolCall && metaID(step.callId)) emit({ ...base, kind: 'tool_completed', id: step.callId, name: step.toolCall.type, args: step.toolCall.args, result: step.toolCall.result });
        };
        const hasWork = () => outstandingWork.size > 0 || detachedWork.size > 0;
        const stopQuiet = () => { clearTimeout(quiet); quiet = undefined; };
        const quietMs = start.turn_end_quiet_ms ?? 30000;
        const gaveUp = new Promise(resolve => { giveUp = resolve; });
        const armQuiet = () => {
          stopQuiet();
          if (!turnEnded || hasWork()) return;
          quiet = setTimeout(() => {
            if (!turnEnded || hasWork()) return;
            quietEnded = true;
            emit({ type: 'turn_end_settled', action: 'quiet', status: String(quietMs) });
            run?.cancel().catch(() => {});
            // A cancel the SDK never answers must not hold a truly idle turn.
            setTimeout(() => giveUp({ status: 'finished', result: '' }), 10000);
          }, quietMs);
        };
        const sendOptions = {
          // Reassert the selected variant on every turn, including resumed/follow-up runs.
          model: start.model,
          onDelta: ({ update }) => {
            if (update.type === 'turn-ended') observation.afterEnd = true;
            observe(observation, 'delta', update);
            if (update.type === 'tool-call-delta') helperProgress(update.callId, update.taskUpdate);
            if (update.type === 'tool-call-started' && update.toolCall?.type === 'task') emit({ type: 'helper_progress', call_id: update.callId, agent_id: metaID(agent.agentId), kind: 'started', args: { description: update.toolCall.args?.description, prompt: update.toolCall.args?.prompt } });
            if (update.toolCall?.type === 'task') {
              if (update.type === 'tool-call-started') helper(update.callId,'started');
              if (update.type === 'tool-call-completed') {
                const result = update.toolCall.result;
                if (result?.status === 'error') helper(update.callId,'failed');
                else if (result?.status === 'success' && result.value?.isBackground === false) helper(update.callId,'finished',result.value.durationMs);
                else if (result?.status === 'success' && result.value?.isBackground === true) helper(update.callId,'launch_ack');
                else helper(update.callId,'unreported');
              }
            }
            // The SDK exposes call lifetimes, not a separate background roster.
            // A detached task remains known work until the native run finishes.
            if (update.type === 'tool-call-started' && update.toolCall?.type === 'shell') terminals?.call(update.callId, update.toolCall.args?.command);
            if (update.type === 'tool-call-started' && ['shell', 'task'].includes(update.toolCall?.type)) {
              outstandingWork.add(update.callId);
              stopQuiet();
            } else if (update.type === 'tool-call-delta') {
              outstandingWork.add(update.callId);
              stopQuiet();
            } else if (update.type === 'tool-call-completed') {
              outstandingWork.delete(update.callId);
              if (update.toolCall?.type === 'task' && update.toolCall.result?.status === 'success' && update.toolCall.result.value?.isBackground === true) detachedWork.add(update.callId);
              else detachedWork.delete(update.callId);
              armQuiet();
            }
            if (update.type === 'turn-ended') {
              turnEnded = true;
              emit({ type: 'turn_ended' });
              armQuiet();
            } else if (['step-started', 'user-message-appended', 'text-delta', 'thinking-delta', 'partial-tool-call', 'tool-call-started'].includes(update.type)) {
              turnEnded = false;
              stopQuiet();
            }
            // The successful result is the complete list, including SDK merges.
            if (update.type === 'tool-call-completed' && update.toolCall?.type === 'updateTodos' && update.toolCall.result?.status === 'success' && Array.isArray(update.toolCall.result.value?.todos)) {
              emit({ type: 'plan', args: { todos: update.toolCall.result.value.todos } });
            }
            if (update.type === 'text-delta') emit({ type: 'delta', kind: 'text', text: update.text });
            else if (update.type === 'thinking-delta') emit({ type: 'delta', kind: 'thinking', text: update.text });
          },
        };
        try { run = await agent.send(await imageFrames.message(text), sendOptions); }
        catch (error) {
          // Same stale run as at start, if it appeared after: cancel it and retry once. A run the store still marks
          // active that cannot be found or cancelled (its process crashed) is expired by the SDK's own recovery,
          // send({ local: { force: true } }), instead of failing every later message of the chat.
          if (!/already has active run/i.test(short(error)) && error?.name !== 'AgentBusyError') throw error;
          const cancelled = await cancelStale(agent.agentId).catch(() => false);
          emit({ type: 'stale', agent_id: agent.agentId, action: cancelled ? 'cancelled' : 'forced' });
          run = await agent.send(await imageFrames.message(text), cancelled ? sendOptions : { ...sendOptions, local: { force: true } });
        }
        const current = run;
        observation.run = current;
        for (const row of pendingHelpers) emit({...row,run_id:metaID(current.id)}); pendingHelpers.length=0;
        stopRunObservation?.();
        stopRunObservation = undefined;
        // Run.onDidChangeStatus is part of the official SDK Run interface (1.1 and later).
        // Missing support in a fixture/older bridge is metadata, not a failure.
        if (typeof current.onDidChangeStatus === 'function') {
          try {
            const stop = current.onDidChangeStatus(status => observe(observation, 'status', { type: 'changed' }, status));
            stopRunObservation = () => { try { stop?.(); } catch { /* Observation cannot change run behavior. */ } };
          } catch { observe(observation, 'status', { type: 'subscribe-unavailable' }); }
        } else observe(observation, 'status', { type: 'subscribe-unavailable' });
        observe(observation, 'status', { type: 'snapshot' }, current.status);
        last = await Promise.race([(async () => {
          for await (const event of current.stream()) {
            observe(observation, 'stream', event, event.type === 'status' ? event.status : undefined);
            if (event.type === 'tool_call') emit({ type: 'tool', call_id: event.call_id, name: event.name, status: event.status, args: event.args, result: event.result });
            else if (event.type === 'usage') emit({ type: 'usage', usage: event.usage });
            // Cursor's automatic compaction (SDK 1.3 summary messages): running, then completed or error.
            else if (event.type === 'summary') emit({ type: 'compaction', status: event.status, fallback: event.fallback === true || undefined });
          }
          const result = await current.wait();
          observe(observation, 'status', { type: 'wait-resolved' }, result.status);
          return result;
        })(), gaveUp, sdkFailed]);
        failRun = undefined;
        // The failed run is left to Cursor otherwise, and would block this agent's next send.
        if (faulted) current.cancel().catch(() => {});
        observation.phase = 'after_result';
        stopQuiet();
        // An SDK fault after Cursor ended its turn leaves the same finished answer.
        if (turnEnded && !hasWork() && last.status !== 'finished' && (quietEnded || faulted || /repeated resume attempts made no progress/i.test(short(last.error)))) {
          if (faulted && !quietEnded) emit({ type: 'turn_end_settled', action: 'sdk_rejection', status: last.status });
          else if (!quietEnded) emit({ type: 'turn_end_settled', action: 'no_progress', status: last.status });
          last = { ...last, status: 'finished', error: undefined };
        }
        run = undefined;
        if (last.status !== 'finished') break;
        let timer;
        await Promise.race([Promise.all(steers.map(entry => entry.done)), new Promise(resolve => { timer = setTimeout(resolve, 15000); })]);
        clearTimeout(timer);
        for (const entry of steers) settle(entry, 'unconfirmed');
        steers = [];
        text = followups.shift();
      }
      if (last.status === 'finished') {
        const fill = await contextFill();
        if (fill) emit({ type: 'context', used: fill.used, max: fill.max });
      }
      emit({ type: 'result', status: last.status, result: last.result || '', error: last.error ? short(last.error) : undefined, ...errorInfo(last.error), usage: last.usage });
    } catch (error) {
      emit({ type: 'result', status: 'error', error: short(error), ...errorInfo(error) });
    } finally { busy = false; run = undefined; failRun = undefined; drop(); }
  }
  terminals?.close();
  await agent?.[Symbol.asyncDispose]?.();
  stopRunObservation?.();
}
module.exports = { createTerminalTail, parseTerminalHeader, cursorTerminalDirs };
if (require.main === module) main().then(() => process.exit(0), error => { emit({ type: 'fatal', error: short(error) }); process.exit(1); });

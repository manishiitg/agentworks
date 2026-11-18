// Thin transport for the official Anthropic Agent SDK. Policy and accounting stay in Go.
const { createInterface } = require('node:readline');
const { pathToFileURL } = require('node:url');
const { randomUUID } = require('node:crypto');
const { spawn } = require('node:child_process');
const { rmSync, writeFileSync, readdirSync, existsSync, statSync, openSync, readSync, closeSync } = require('node:fs');
const { join, resolve } = require('node:path');
// Native user echoes may include image bytes. They stay inside the SDK's own
// conversation, never in the host event stream or its JSON/history buffers.
const emit = value => process.stdout.write(JSON.stringify(value, (key, entry) =>
  key === 'source' && entry?.type === 'base64' && typeof entry.data === 'string'
    ? { ...entry, data: '[native image omitted]' } : entry) + '\n');

function sdkUserContent(request) {
  const images = request.images || [];
  // Exactly one host-admitted explicit skill. Claude expands a leading slash
  // in the final text block; other slash text remains that skill's arguments.
  // This requests native expansion, never reports it as executed.
  if (request.skill_name) {
    const text = `/${request.skill_name}${request.text ? '\n' + request.text : ''}`;
    if (!images.length) return text;
    return [...images.map(image => ({ type: 'image', source: { type: 'base64', media_type: image.mediaType, data: image.data } })), { type: 'text', text }];
  }
  if (!images.length) return request.text;
  return [...(request.text ? [{ type: 'text', text: request.text }] : []),
    ...images.map(image => ({ type: 'image', source: { type: 'base64', media_type: image.mediaType, data: image.data } }))];
}
function takeSDKContent(input) {
  const content = input.content;
  // Only the SDK consumer owns the yielded content; our waiting queue and
  // generator bookkeeping retain no image data after this delivery.
  input.content = undefined;
  return content;
}
let nativeErrorReported = false;
const deny = () => ({ behavior: 'deny', message: 'The request was cancelled. Use the chat or account setup to continue.' });

// Native Agent launches can be asynchronous without a run_in_background input.
// Track their real task bookends (including resumed runs), never prose/tool output.
function createHelperTracker(publish, includeShell = false) {
  const tools = new Map(), helpers = new Map(), shells = new Map(), helperTools = new Map();
  // nestedTools: Agent calls made inside a subagent. backgrounded: foreground calls already moved aside.
  const nestedTools = new Set(), backgrounded = new Set();
  const isHelper = event => event.task_type === 'local_agent' || ['Agent', 'Task'].includes(tools.get(event.tool_use_id));
  const observe = event => {
    if (event.type === 'assistant') {
      for (const block of event.message?.content || []) {
        if (block.type === 'tool_use' && ['Agent', 'Task'].includes(block.name)) {
          tools.set(block.id, block.name);
          if (event.parent_tool_use_id) nestedTools.add(block.id);
          helperTools.set(block.id, { helper_type: typeof block.input?.subagent_type === 'string' ? block.input.subagent_type : '',
            helper_description: typeof block.input?.description === 'string' ? block.input.description : '',
            helper_mode: block.input?.run_in_background === true ? 'async' : 'foreground' });
        }
        if (block.type === 'tool_use' && block.name === 'Bash') shells.set(block.id, {
          command: typeof block.input?.command === 'string' ? block.input.command : '',
          mode: block.input?.run_in_background === true ? 'explicit' : ''
        });
      }
    }
    if (event.type === 'user' && event.toolUseResult && Object.hasOwn(event.toolUseResult, 'timedOutAfterMs')) {
      for (const block of event.message?.content || []) {
        const shell = shells.get(block.tool_use_id);
        if (block.type === 'tool_result' && shell && !shell.mode) shell.mode = 'auto';
      }
    }
    if (event.type !== 'system') return;
    const entries = event.subtype === 'background_tasks_changed' ? (event.tasks || []) :
      ['task_started', 'task_progress', 'task_notification'].includes(event.subtype) ? [event] : [];
    for (const entry of entries) {
      if (!entry.task_id || entry.ambient || entry.skip_transcript) continue;
      const previous = helpers.get(entry.task_id);
      if (previous?.run_id && entry.run_id && entry.run_id < previous.run_id) continue;
      const shell = shells.get(entry.tool_use_id || previous?.tool_use_id);
      if (!previous && !isHelper(entry) && !(includeShell && entry.task_type === 'local_bash' && shell?.mode)) continue;
      const helperInfo = helperTools.get(entry.tool_use_id || previous?.tool_use_id) || {};
      const usage = entry.usage;
      const measured = usage && Number.isSafeInteger(usage.total_tokens) && usage.total_tokens >= 0 ? { total_tokens: usage.total_tokens } : {};
      const duration = usage && Number.isSafeInteger(usage.duration_ms) && usage.duration_ms >= 0 ? { duration_ms: usage.duration_ms } : {};
      if (event.subtype === 'task_progress') {
        if (previous?.state === 'started') {
          const updated = { ...previous, ...measured, ...duration, ...(typeof entry.description === 'string' && entry.description ? { current_step: entry.description } : {}), updated_at: Date.now() };
          helpers.set(entry.task_id, updated);
          publish({ type: 'sdk_helper', helper: { ...updated, state: 'progress' } });
        }
        continue;
      }
      const sameRun = previous && previous.run_id === (entry.run_id || '');
      const state = event.subtype === 'task_notification' ?
        ({ completed: 'finished', failed: 'failed', stopped: 'interrupted' }[entry.status]) : 'started';
      if (!state || (sameRun && previous.state !== 'started' && state === 'started')) continue;
      const helper = { ...(sameRun ? previous : {}), ...helperInfo, ...measured, ...duration,
        ...(shell?.mode ? { command: shell.command, background_mode: shell.mode } : {}),
        kind: (sameRun && previous.kind) || (isHelper(entry) ? 'helper' : 'background_command'), updated_at: Date.now(), task_id: entry.task_id, run_id: entry.run_id || '',
        tool_use_id: entry.tool_use_id || (sameRun ? previous.tool_use_id : '') || '',
        description: entry.description || (sameRun ? previous.description : '') || 'Helper agent',
        started_at: sameRun ? previous.started_at : Date.now(), state };
      helpers.set(entry.task_id, helper);
      if (!sameRun || previous.state !== state) publish({ type: 'sdk_helper', helper });
    }
  };
  // Top-level foreground subagents still running: a priority-now steer would cancel them (#3128).
  const foreground = () => [...helpers.values()].filter(helper => helper.state === 'started' && helper.kind === 'helper' &&
    helper.helper_mode === 'foreground' && helper.tool_use_id && !nestedTools.has(helper.tool_use_id) &&
    !backgrounded.has(helper.tool_use_id)).map(helper => helper.tool_use_id);
  return { observe, foreground, markBackgrounded: id => backgrounded.add(id),
    running: () => [...helpers.values()].some(helper => helper.state === 'started' && helper.kind !== 'background_command') };
}

// Before a priority-now steer, ask the SDK to move each foreground subagent to
// the background so it survives the steer (as T3 Code does). Best effort and
// bounded: the steer goes in after timeoutMs whatever the SDK answered. Counts
// only; the host logs them.
async function moveForegroundHelpersAside(run, toolUseIds, timeoutMs = 5000) {
  const report = { moved: 0, unmatched: 0, failed: 0, timed_out: false };
  if (typeof run?.backgroundTasks !== 'function') return { ...report, failed: toolUseIds.length };
  let timer;
  const moves = Promise.allSettled(toolUseIds.map(id => Promise.resolve().then(() => run.backgroundTasks(id))
    .then(moved => { if (moved) report.moved++; else report.unmatched++; }, () => { report.failed++; })));
  const timeout = new Promise(resolve => { timer = setTimeout(() => { report.timed_out = true; resolve(); }, timeoutMs); });
  await Promise.race([moves, timeout]);
  clearTimeout(timer);
  return { ...report };
}

// Claude's ScheduleWakeup reports no task: its tool result is the only sign
// Claude will start a turn on its own later. Returns the pending wake's epoch
// ms (0: none or stopped) once a ScheduleWakeup call returns, else undefined.
function createScheduledWakeTracker() {
  const calls = new Set();
  return event => {
    if (event.parent_tool_use_id) return undefined;
    if (event.type === 'assistant') {
      for (const block of event.message?.content || []) if (block?.type === 'tool_use' && block.name === 'ScheduleWakeup') calls.add(block.id);
      return undefined;
    }
    if (event.type !== 'user' || !Array.isArray(event.message?.content)) return undefined;
    const returned = event.message.content.filter(block => block?.type === 'tool_result' && calls.delete(block.tool_use_id));
    if (!returned.length) return undefined;
    const result = event.tool_use_result ?? event.toolUseResult;
    const at = returned.length === 1 && !returned[0].is_error && result && typeof result.scheduledFor === 'number' ? result.scheduledFor : 0;
    return at > 0 ? at : 0;
  };
}

// Live output of Claude's own background shell commands (#3131). The SDK
// withholds such a command's output until Claude reads it; the command writes
// it to its task output file, which the bridge follows while the task runs and
// reports in bounded chunks: at most one per poll, holding the newest
// TASK_OUTPUT_CHUNK_MAX bytes. Best effort: Claude's file layout is private, so
// a file that cannot be found only means no live output; the final result is
// still Claude's own.
const TASK_OUTPUT_POLL_MS = 1000, TASK_OUTPUT_READ_MAX = 64 * 1024, TASK_OUTPUT_CHUNK_MAX = 8 * 1024;
const TASK_OUTPUT_MAX_TAILS = 16, TASK_OUTPUT_LOCATE_TRIES = 30;
function taskOutputPathFromResult(text) {
  const match = /Output is being written to:?\s*(\S+\.output)/.exec(typeof text === 'string' ? text : '');
  return match ? match[1] : '';
}
function toolResultText(content) {
  if (typeof content === 'string') return content;
  return Array.isArray(content) ? content.map(block => typeof block?.text === 'string' ? block.text : '').join('\n') : '';
}
// Claude Code's per-user temp folder (as T3 Code finds it): CLAUDE_CODE_TMPDIR,
// else /tmp on macOS and the usual temp variables elsewhere.
function claudeTaskOutputRoot(env = process.env, platform = process.platform) {
  const uid = process.getuid?.();
  if (uid === undefined) return '';
  const fallback = platform === 'darwin' ? '/tmp' : env.TMPDIR || env.TMP || env.TEMP || '/tmp';
  return resolve(process.cwd(), env.CLAUDE_CODE_TMPDIR?.trim() || fallback, `claude-${uid}`);
}
function locateTaskOutput(root, sessionId, taskId) {
  if (!root || !sessionId || !taskId || /[\\/]/.test(sessionId + taskId)) return '';
  let projects = [];
  try { projects = readdirSync(root); } catch { return ''; }
  for (const project of projects) {
    const candidate = join(root, project, sessionId, 'tasks', `${taskId}.output`);
    if (existsSync(candidate)) return candidate;
  }
  return '';
}
function createTaskOutputTail(publish, { pollMs = TASK_OUTPUT_POLL_MS, root = claudeTaskOutputRoot() } = {}) {
  const shells = new Set(), paths = new Map(), taskTool = new Map(), tails = new Map();
  let sessionId = '';
  const send = (tail, text, extra = {}) => publish({ type: 'sdk_task_output', task_output: { task_id: tail.taskId, tool_use_id: tail.toolId, text, ...(tail.dropped ? { dropped: true } : {}), ...extra } });
  const read = tail => {
    if (!tail.path) {
      tail.path = paths.get(tail.toolId) || locateTaskOutput(root, sessionId, tail.taskId);
      if (!tail.path) return '';
    }
    let size;
    try { size = statSync(tail.path).size; } catch { return ''; }
    if (size < tail.offset) { tail.offset = 0; tail.decoder = new TextDecoder(); }
    if (size === tail.offset) return '';
    const start = size - tail.offset > TASK_OUTPUT_READ_MAX ? size - TASK_OUTPUT_READ_MAX : tail.offset;
    const bytes = Buffer.alloc(size - start);
    let fd;
    try { fd = openSync(tail.path, 'r'); readSync(fd, bytes, 0, bytes.length, start); } catch { return ''; } finally { if (fd !== undefined) closeSync(fd); }
    let first = 0;
    if (start > tail.offset) {
      // Skipped output: start on a whole character and say so.
      tail.dropped = true; tail.decoder = new TextDecoder();
      while (first < bytes.length && (bytes[first] & 0xc0) === 0x80) first++;
    }
    tail.offset = size;
    return tail.decoder.decode(bytes.subarray(first), { stream: true });
  };
  const flush = (tail, text) => {
    if (Buffer.byteLength(text) > TASK_OUTPUT_CHUNK_MAX) {
      const buffer = Buffer.from(text);
      let cut = buffer.length - TASK_OUTPUT_CHUNK_MAX;
      while (cut < buffer.length && (buffer[cut] & 0xc0) === 0x80) cut++;
      text = buffer.subarray(cut).toString(); tail.dropped = true;
    }
    if (text) { send(tail, text); tail.dropped = false; }
  };
  const poll = tail => {
    if (!tail.path && ++tail.tries > TASK_OUTPUT_LOCATE_TRIES) { clearInterval(tail.timer); tail.timer = undefined; return; }
    flush(tail, read(tail));
  };
  const start = (taskId, toolId) => {
    if (!taskId || !toolId || tails.has(taskId) || tails.size >= TASK_OUTPUT_MAX_TAILS) return;
    const tail = { taskId, toolId, path: paths.get(toolId) || '', offset: 0, tries: 0, dropped: false, decoder: new TextDecoder() };
    tail.timer = setInterval(() => poll(tail), pollMs);
    tail.timer.unref?.();
    tails.set(taskId, tail);
    // The host learns at once that this command runs on its own and can be stopped.
    send(tail, '');
  };
  const finish = (taskId, outputFile) => {
    const tail = tails.get(taskId);
    if (!tail) return;
    tails.delete(taskId);
    clearInterval(tail.timer);
    if (!tail.path && typeof outputFile === 'string') tail.path = outputFile;
    flush(tail, read(tail) + tail.decoder.decode());
    send(tail, '', { done: true });
  };
  const observe = event => {
    if (typeof event.session_id === 'string' && event.session_id) sessionId = event.session_id;
    if (event.type === 'assistant' && !event.parent_tool_use_id) {
      for (const block of event.message?.content || []) {
        if (block?.type === 'tool_use' && block.name === 'Bash' && block.input?.run_in_background === true) shells.add(block.id);
      }
    }
    if (event.type === 'user' && Array.isArray(event.message?.content)) {
      const result = event.tool_use_result ?? event.toolUseResult;
      for (const block of event.message.content) {
        if (block?.type !== 'tool_result' || !shells.has(block.tool_use_id)) continue;
        const path = taskOutputPathFromResult(toolResultText(block.content));
        if (path) paths.set(block.tool_use_id, path);
        const taskId = typeof result?.backgroundTaskId === 'string' ? result.backgroundTaskId : '';
        if (taskId) { taskTool.set(taskId, block.tool_use_id); start(taskId, block.tool_use_id); }
        for (const tail of tails.values()) if (tail.toolId === block.tool_use_id && !tail.path && path) tail.path = path;
      }
    }
    if (event.type !== 'system') return;
    if (event.subtype === 'task_started' && shells.has(event.tool_use_id)) {
      taskTool.set(event.task_id, event.tool_use_id);
      start(event.task_id, event.tool_use_id);
    }
    if (event.subtype === 'background_tasks_changed') {
      for (const task of event.tasks || []) if (shells.has(task.tool_use_id)) start(task.task_id, task.tool_use_id);
    }
    if (event.subtype === 'task_notification') finish(event.task_id, event.output_file);
  };
  const close = () => { for (const tail of tails.values()) clearInterval(tail.timer); tails.clear(); };
  return { observe, close, running: () => [...tails.keys()] };
}

// Await each native acknowledgement before the next user input can be yielded.
// Patch only our effort key; prompt-suggestion eligibility belongs to its own control.
async function applySDKTurnSettings(run, current, wanted) {
  if (!wanted) return [];
  const changed = [];
  for (const [key, method] of [['model', 'setModel'], ['permission_mode', 'setPermissionMode']]) {
    if (wanted[key] !== current[key]) {
      if (typeof run?.[method] !== 'function') throw new Error('unsupported_turn_setting');
      await run[method](wanted[key] || (key === 'model' ? undefined : 'default'));
      current[key] = wanted[key];
      changed.push(key);
    }
  }
  const effort = wanted.effort || null;
  if (effort !== current.effort) {
    if (typeof run?.applyFlagSettings !== 'function') throw new Error('unsupported_turn_setting');
    await run.applyFlagSettings({ effortLevel: effort });
    current.effort = effort;
    changed.push("effort");
  }
  return changed;
}

async function main() {
  const [sdkEntry, executable, ...argv] = process.argv.slice(2);
  const { query } = await import(pathToFileURL(sdkEntry).href);
  const options = {
    pathToClaudeCodeExecutable: executable,
    cwd: process.cwd(), env: process.env,
    includePartialMessages: true,
    // A host-authorized later turn may select bypass; current permissionMode still governs every call.
    allowDangerouslySkipPermissions: true,
    thinking: { type: 'adaptive', display: 'summarized' },
    // Match the CLI's normal Claude Code instructions and project skills.
    systemPrompt: { type: 'preset', preset: 'claude_code' },
    settingSources: ['user', 'project', 'local'],
    extraArgs: { 'replay-user-messages': null, 'thinking-display': 'summarized' },
  };
  // A confined chat: this bridge runs outside Landlock and every Claude Code
  // process the SDK starts is exec'd in place by the launcher, so it keeps its
  // own /proc entry (PLAT-872-51). The policy file goes in this bridge's
  // private folder, which no confined CLI can write; the launcher deletes it.
  const launch = process.env.AGENTWORKS_CLI_LAUNCH && JSON.parse(process.env.AGENTWORKS_CLI_LAUNCH);
  delete process.env.AGENTWORKS_CLI_LAUNCH;
  let nativeProcess;
  options.spawnClaudeCodeProcess = ({ command, args, cwd, env, signal }) => {
    if (launch) {
      const policy = join(__dirname, `policy-${randomUUID()}.json`);
      writeFileSync(policy, JSON.stringify(launch.policy), { mode: 0o600, flag: 'wx' });
      try {
        nativeProcess = spawn(launch.runner, ['--config', policy, '--', ...launch.env_args, command, ...args],
          { cwd, env, signal, stdio: ['pipe', 'pipe', 'pipe'] });
      } catch (error) { rmSync(policy, { force: true }); throw error; }
    } else nativeProcess = spawn(command, args, { cwd, env, signal, stdio: ['pipe', 'pipe', 'pipe'] });
    return nativeProcess;
  };
  for (let i = 0; i < argv.length; i++) {
    const flag = argv[i];
    if (flag === '-p' || flag === '--verbose') continue;
    if (flag === '--output-format') { i++; continue; }
    if (flag === '--dangerously-skip-permissions') {
      options.permissionMode = 'bypassPermissions'; options.allowDangerouslySkipPermissions = true; continue;
    }
    if (flag === '--strict-mcp-config') { options.strictMcpConfig = true; continue; }
    const value = argv[++i];
    switch (flag) {
      case '--model': options.model = value; break;
      case '--effort': options.effort = value; break;
      case '--append-system-prompt': options.systemPrompt.append = value; break;
      case '--permission-mode': options.permissionMode = value; if (value === 'bypassPermissions') options.allowDangerouslySkipPermissions = true; break;
      case '--resume': options.resume = value; break;
      case '--session-id': options.sessionId = value; break;
      case '--tools': options.tools = value ? value.split(',') : []; break;
      case '--allowedTools': options.allowedTools = value.split(','); break;
      case '--disallowedTools': options.disallowedTools = [...(options.disallowedTools || []), ...value.split(',')]; break;
      case '--add-dir': (options.additionalDirectories ||= []).push(value); break;
      case '--settings': options.settings = value; break;
      case '--mcp-config': options.extraArgs['mcp-config'] = value; break;
      default: throw new Error(`Unsupported SDK launch flag ${flag}`);
    }
  }
  let run, wake, ended = false, keepWarm = false;
  const turnSettings = { model: options.model || '', permission_mode: options.permissionMode || 'default', effort: options.effort || null };
  let nextPromptCapable = false, nextPromptTurn = '', settledPromptTurn = '';
  let suggestionNearLimit = false;
  const suggestionWindows = new Map();
  const helpers = createHelperTracker(emit, true);
  const taskOutput = createTaskOutputTail(emit, { root: claudeTaskOutputRoot(options.env) });
  const scheduledWake = createScheduledWakeTracker();
  let scheduledFor = 0, steerChain = Promise.resolve();
  // movedAside: background tasks that appeared while a steer was waiting to be taken, i.e. work a steer moved aside.
  // ownBackground: tool calls Claude itself started with run_in_background (a dev server, a long build). Those
  // were never moved aside, so they never hold the turn, even when they start while a steer waits.
  const input = [], permissions = new Map(), steering = new Map(), unsettled = new Set(), background = new Map(), movedAside = new Set(), ownBackground = new Set();
  // Kept session (PLAT-872): a turn ends at its answer, but while Claude's own background tasks still run the
  // session stays open ("parked") so they keep running; the next turn's prompt goes into this same session.
  // turnActive: a host turn owns the output. Output while no turn is active (a model turn the SDK starts on its own,
  // for example after a task notification) is tagged sdk_parked_turn until the host adopts it ("adopt"): Claude
  // wakes itself when its background work finishes, and that turn is shown in the chat (PLAT-746-899).
  // strayCheck: the prompt went into a kept session while such a model turn was running, so output is ours only
  // once the SDK says it consumed our prompt (its uuid).
  let turnActive = false, parked = false, parkedTurnRunning = false, strayCheck = false, promptUUID, promptConsumed = true, lastBackground = '[]';
  const toolInfo = new Map(), taskTool = new Map(), taskInfo = new Map();
  const ownRunning = () => [...background.keys()].filter(id => ownBackground.has(taskTool.get(id)));
  // Only what the person needs to recognise the task. Go trims the command and removes inline values.
  const reportBackground = () => {
    const tasks = ownRunning().map(id => {
      const tool = toolInfo.get(taskTool.get(id)) || {}, info = taskInfo.get(id) || {};
      return { task_id: id, description: tool.description || info.description || '', command: tool.command || '', started_at: info.started_at || 0 };
    });
    const key = JSON.stringify(tasks.map(task => task.task_id));
    if (key !== lastBackground) { lastBackground = key; emit({ type: 'sdk_background', background: tasks }); }
  };
  const lines = createInterface({ input: process.stdin });
  const finishInput = () => { ended = true; wake?.(); };
  const cancelPermissions = () => { for (const resolve of permissions.values()) resolve(deny()); permissions.clear(); };
  options.canUseTool = async (tool, toolInput, context) => {
    const id = randomUUID();
    const abort = () => { permissions.get(id)?.(deny()); permissions.delete(id); emit({ type: 'sdk_permission_cancel', request_id: id }); };
    if (context.signal.aborted) return deny();
    const response = new Promise(resolve => permissions.set(id, resolve));
    context.signal.addEventListener('abort', abort, { once: true });
    // Claude's own "always allow" rule for this request, offered as a choice; the host scopes it to this chat.
    emit({ type: 'sdk_permission', request_id: id, tool_name: tool, input: toolInput, ...(context.suggestions?.length ? { suggestions: context.suggestions } : {}) });
    try { return await response; }
    finally { context.signal.removeEventListener('abort', abort); permissions.delete(id); }
  };
  const first = new Promise((resolve, reject) => {
    lines.on('line', line => {
      try {
        const request = JSON.parse(line);
        if (request.type === 'prompt') {
          keepWarm = request.keep_warm === true;
          if (!run) nextPromptCapable = request.next_prompt_capable === true;
          nextPromptTurn = request.next_prompt_turn || '';
          const uuid = randomUUID();
          strayCheck = parked && parkedTurnRunning;
          parked = false; turnActive = true; promptUUID = uuid; promptConsumed = !strayCheck;
          input.push({ content: sdkUserContent(request), uuid, nextPrompt: request.next_prompt === true, nextPromptTurn, settings: request.runtime_settings }); wake?.(); resolve();
        }
        else if (request.type === 'adopt') {
          keepWarm = request.keep_warm === true;
          nextPromptTurn = ''; settledPromptTurn = '';
          if (nextPromptCapable && typeof run?.applyFlagSettings === 'function') void run.applyFlagSettings({promptSuggestionEnabled:false}).catch(() => {});
          // The host takes the model turn Claude started on its own: from here its output is the host's turn, and
          // its result ends that turn as usual. One that already ended is reported so the host ends its turn too.
          if (parked && parkedTurnRunning) { parked = false; turnActive = true; strayCheck = false; promptConsumed = true; }
          else emit({ type: 'sdk_adopt_idle' });
        }
        else if (request.type === 'steer') {
          // A kept session between turns takes no steer: the host starts a normal turn instead.
          if (!run || ended || !turnActive) {
            emit({ type: 'sdk_control_result', id: request.id, is_error: true, input_not_sent: true });
          } else {
            // Steers stay in order; one that waits for subagents to move aside waits at most 5 s.
            const foreground = helpers.foreground();
            steerChain = steerChain.then(async () => {
              if (foreground.length) {
                const moved = await moveForegroundHelpersAside(run, foreground);
                foreground.forEach(helpers.markBackgrounded);
                emit({ type: 'sdk_steer_background', steer_background: moved });
                if (ended || !turnActive) { emit({ type: 'sdk_control_result', id: request.id, is_error: true, input_not_sent: true }); return; }
              }
              const uuid = randomUUID();
              steering.set(uuid, request.id);
              unsettled.add(uuid);
              input.push({ content: sdkUserContent(request), uuid, priority: 'now' });
              wake?.();
            }).catch(() => emit({ type: 'sdk_control_result', id: request.id, is_error: true, input_not_sent: true }));
          }
        }
        else if (request.type === 'permission') permissions.get(request.id)?.(request.response);
        else if (request.type === 'interrupt' && run) {
          cancelPermissions();
          run.interrupt().then(() => { unsettled.clear(); background.clear(); movedAside.clear(); finishInput(); emit({ type: 'sdk_control_result', id: request.id }); }, () => emit({ type: 'sdk_control_result', id: request.id, is_error: true }));
        }
        else if (request.type === 'release') {
          // The host ended a turn held open after its answer. What still runs is no longer part of a turn; the
          // session stays only for Claude's own background tasks (keep), otherwise input ends.
          settledPromptTurn = nextPromptTurn;
          turnActive = false; strayCheck = false; promptConsumed = true;
          if (request.keep && (keepWarm || ownRunning().length > 0)) parked = true; else finishInput();
        }
        else if (request.type === 'stop_task') {
          // One of Claude's own tasks, stopped from the chat's background-work list. Its task_notification
          // updates the list; an empty list between turns closes the session.
          if (run && ownRunning().includes(request.task_id)) run.stopTask(request.task_id).catch(() => {});
        }
        else if (request.type === 'stop_background') {
          // Stop Claude's own background tasks; between turns this also closes the session.
          Promise.allSettled(ownRunning().map(id => run ? run.stopTask(id) : undefined)).then(() => {
            if (!turnActive) finishInput();
            emit({ type: 'sdk_control_result', id: request.id });
          });
        }
      } catch (error) { reject(error); }
    });
    lines.on('close', () => { finishInput(); cancelPermissions(); resolve(); });
  });
  await first;
  // This SDK capability reuses the native turn cache. Dynamic flag settings
  // gate each input without restarting a kept query or its background work.
  options.promptSuggestions = nextPromptCapable;
  async function* messages() {
    while (!ended || input.length) {
      if (!input.length) { await new Promise(resolve => { wake = resolve; }); wake = undefined; continue; }
      const next = input.shift();
      // query() returns its control handle before this microtask resumes.
      await Promise.resolve();
      try {
        const fields = await applySDKTurnSettings(run, turnSettings, next.settings);
        if (fields.length) emit({type:'sdk_turn_settings', settings_fields:fields});
      }
      catch {
        nativeErrorReported = true;
        emit({type:'sdk_turn_settings', settings_failed:true});
        emit({type:'result', is_error:true, result:'Claude could not apply the requested turn settings. No new input was sent.'});
        finishInput();
        return;
      }
      if (nextPromptCapable && next.nextPrompt !== undefined) {
        if (typeof run?.applyFlagSettings === 'function') {
          try { await run.applyFlagSettings({ promptSuggestionEnabled: next.nextPrompt && !suggestionNearLimit }); }
          catch { nextPromptCapable = false; emit({type:'sdk_prompt_skipped', sdk_prompt_turn:next.nextPromptTurn, prompt_skip_reason:'unsupported_sdk'}); }
        } else {
          nextPromptCapable = false;
          emit({type:'sdk_prompt_skipped', sdk_prompt_turn:next.nextPromptTurn, prompt_skip_reason:'unsupported_sdk'});
        }
      }
      // Native human priority-now delivery lets movable tools step aside while
      // the running conversation takes the new direction (PLAT-872-47).
      const message = { type: 'user', message: { role: 'user', content: takeSDKContent(next) }, parent_tool_use_id: null,
        uuid: next.uuid, priority: next.priority, origin: { kind: 'human' } };
      try { yield message; }
      finally {
        // Agent SDK streamInput writes the message before requesting next().
        // Once resumed, drop our image blocks even if its loop retains the
        // last yielded envelope while waiting in a kept session. Text stays.
        if (Array.isArray(message.message.content)) message.message.content = message.message.content.filter(block => block.type !== 'image');
      }
    }
  }
  run = query({ prompt: messages(), options });
  try {
    for await (const event of run) {
      if (event.type === 'rate_limit_event') {
        const limit = event.rate_limit_info || {};
        if (typeof limit.utilization === 'number') suggestionWindows.set(limit.rateLimitType || 'primary', { used:limit.utilization, resets:limit.resetsAt });
        for (const [name, window] of Object.entries(limit.unifiedWindows || {})) if (typeof window.utilization === 'number') suggestionWindows.set(name, {used:window.utilization, resets:window.resetsAt});
        suggestionNearLimit = limit.status === 'rejected' || [...suggestionWindows.values()].some(window => window.used >= 0.9 && (!window.resets || window.resets * 1000 > Date.now()));
        if (suggestionNearLimit && nextPromptCapable && typeof run.applyFlagSettings === 'function') {
          try { await run.applyFlagSettings({ promptSuggestionEnabled:false }); } catch { nextPromptCapable = false; }
          emit({type:'sdk_prompt_skipped', sdk_prompt_turn:nextPromptTurn, prompt_skip_reason:'near_limit'});
        }
      }
      if (event.type === 'prompt_suggestion') {
        if (nextPromptCapable && !turnActive && settledPromptTurn && !suggestionNearLimit) emit({...event, sdk_prompt_turn:settledPromptTurn});
        continue;
      }
      helpers.observe(event);
      taskOutput.observe(event);
      const wakeAt = scheduledWake(event);
      if (wakeAt !== undefined && wakeAt !== scheduledFor) { scheduledFor = wakeAt; emit({ type: 'sdk_scheduled_wake', scheduled_for: wakeAt }); }
      // Keep work moved aside by steering alive until its native completion.
      // An empty roster can precede task_notification, so only that terminal
      // notification retires a known task. Ambient watchers do not hold a turn.
      if (event.type === 'assistant') {
        for (const block of event.message?.content || []) {
          if (block?.type === 'tool_use' && block.input?.run_in_background === true) {
            ownBackground.add(block.id);
            toolInfo.set(block.id, { description: typeof block.input.description === 'string' ? block.input.description : '', command: typeof block.input.command === 'string' ? block.input.command : '' });
          }
        }
      }
      if (event.type === 'system' && event.subtype === 'background_tasks_changed') {
        for (const task of event.tasks || []) {
          if (task.ambient) continue;
          if (task.tool_use_id) taskTool.set(task.task_id, task.tool_use_id);
          if (!background.has(task.task_id) && unsettled.size > 0 && !ownBackground.has(taskTool.get(task.task_id))) movedAside.add(task.task_id);
          if (!taskInfo.has(task.task_id)) taskInfo.set(task.task_id, { description: task.description || '', started_at: Date.now() });
          background.set(task.task_id, task.run_id);
        }
      }
      if (event.type === 'system' && event.subtype === 'task_started' && event.tool_use_id) {
        // The roster can list a task before task_started names its tool call.
        taskTool.set(event.task_id, event.tool_use_id);
        if (ownBackground.has(event.tool_use_id)) movedAside.delete(event.task_id);
        if (!taskInfo.has(event.task_id)) taskInfo.set(event.task_id, { description: event.description || '', started_at: Date.now() });
      }
      if (event.type === 'system' && event.subtype === 'task_notification' &&
          background.get(event.task_id) === event.run_id) { background.delete(event.task_id); movedAside.delete(event.task_id); }
      if (event.type === 'system') reportBackground();
      if (event.type === "result" && event.is_error) nativeErrorReported = true;
      const consumed = new Set([...(event.user_message_uuids || []), event.user_message_uuid]);
      if (event.type === 'user' && !event.parent_tool_use_id) consumed.add(event.uuid);
      if (strayCheck && !promptConsumed && (consumed.has(promptUUID) ||
          (event.type === 'result' && !event.user_message_uuids && !event.user_message_uuid))) promptConsumed = true;
      for (const uuid of consumed) {
        if (steering.has(uuid)) {
          emit({ type: 'sdk_control_result', id: steering.get(uuid) });
          steering.delete(uuid);
        }
      }
      if (!turnActive || !promptConsumed) {
        // Not part of the host's turn: a model turn the SDK ran on its own in a kept session.
        if (event.type === 'assistant' || event.type === 'stream_event') parkedTurnRunning = true;
        if (event.type === 'result') parkedTurnRunning = false;
        event.sdk_parked_turn = true;
        emit(event);
        continue;
      }
      let park = false;
      if (event.type === 'result') {
        for (const uuid of consumed) unsettled.delete(uuid);
        // Native helpers keep their parent turn open to receive their result; a steer can still enter it.
        // A finished answer otherwise ends the person's turn. Background work holds it open only when a steer moved
        // that work aside; a command Claude backgrounded on its own (a slow search past its timeout), before or
        // apart from any steer, must not keep the chat "working" (owner, 2026-10-28: an answer means done).
        event.sdk_continues = !ended && (unsettled.size > 0 || movedAside.size > 0 || event.queued_turn_count > 0 || helpers.running());
        // Why a turn stays open, for the host log (counts only).
        event.sdk_hold = { unsettled: unsettled.size, moved_aside: movedAside.size, queued: event.queued_turn_count || 0, background: background.size, helpers: helpers.running() };
        if (!event.sdk_continues) {
          settledPromptTurn = nextPromptTurn;
          turnActive = false; strayCheck = false;
          // Claude's own background tasks still run: keep the session so they keep running.
          park = !ended && (ownRunning().length > 0 || (keepWarm && !event.is_error));
          if (park) {
            parked = true; event.sdk_parked = true; reportBackground();
            if (keepWarm) event.sdk_warm_memory = { bridge_rss_bytes: process.memoryUsage.rss(), native_pid: nativeProcess?.pid };
          }
        }
      }
      emit(event);
      // One host turn can consume several steers. Close only after their native
      // results, then drain transcript writes before the next host resumes.
      if (event.type === 'result' && !event.sdk_continues && !park) finishInput();
    }
  } finally { taskOutput.close(); finishInput(); cancelPermissions(); lines.close(); run.close(); }
}
module.exports = { createHelperTracker, applySDKTurnSettings, sdkUserContent, moveForegroundHelpersAside, createScheduledWakeTracker, createTaskOutputTail, taskOutputPathFromResult, locateTaskOutput };
if (require.main === module) main().then(() => process.stdout.write("", () => process.exit(0)), error => {
  // SDK errors can contain command-line/environment details: report only a
  // stable category here. Native semantic errors still arrive as result events.
  if (!nativeErrorReported) emit({ type: 'result', is_error: true, result: `Claude Agent SDK failed (${error?.name || 'Error'}). Check SDK installation and the selected Claude login.` });
  process.exitCode = 1;
  process.stdin.destroy();
});

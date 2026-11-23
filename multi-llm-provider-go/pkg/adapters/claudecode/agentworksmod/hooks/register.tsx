import type { Register } from 'claude-code'

// AgentWorks runs this mod inside a chat's Claude Code terminal. It reports
// what the terminal does as one small JSON file per event in
// $AGENTWORKS_MOD_DIR/events, which AgentWorks reads in order. Without that
// folder (a person's own Claude Code) it does nothing.

let seq = 0
// The main conversation is answering: input then joins the running turn.
let running = false
let turnId = ''
let delivering = false
let toolsReady = false
const delivered = new Set<string>()
// Streamed pieces of the main conversation's current model request, written
// out about every 150 ms; stepKey names that request for its stored rows.
let stream: Record<string, unknown>[] = []
let flushedAt = 0
let stepKey = ''
// Rows the chat history keeps: prompts, answer blocks, tool results, input
// joined into a running turn.
const ROW_DOORS = new Set(['prompt', 'response', 'tool-result', 'delivery', 'note'])
const ROW_TEXT_LIMIT = 200000

// What the terminal shows besides Claude Code itself: the status line, the band
// above the prompt and the Changes pane.
const CHANGES_PANE = 'agentworks-changes'
const BLOCKED_COMMANDS: Record<string, string> = {
  login: 'AgentWorks signs Claude in with the chat\'s account.',
  logout: 'AgentWorks signs Claude in with the chat\'s account.',
  model: 'Pick the model with the model chip in the chat box.',
  permissions: 'AgentWorks sets what Claude may do in this Crew.',
  'allowed-tools': 'AgentWorks sets what Claude may do in this Crew.',
  hooks: 'AgentWorks sets what Claude may do in this Crew.',
  plugin: 'AgentWorks manages Claude\'s add-ons here.',
  plugins: 'AgentWorks manages Claude\'s add-ons here.',
  mcp: 'Add tools through the Crew\'s Connections.',
  'add-dir': 'Claude works in this Crew\'s folder.',
  cd: 'Claude works in this Crew\'s folder.',
  background: 'Claude stays attached to this chat.',
  bug: 'Use the app to report a problem.',
  feedback: 'Use the app to report a problem.',
  upgrade: 'AgentWorks keeps Claude Code up to date.',
  'terminal-setup': 'The terminal is set up by AgentWorks.',
  ide: 'The terminal is set up by AgentWorks.',
  'install-github-app': 'Connect GitHub from the Crew\'s Connections.',
}
const editedFiles = new Set<string>()
type RepoChanges = { repo: string; branch: string; files: { path: string; added: string; removed: string }[] }
let changes: RepoChanges[] = []
// The files AgentWorks' snapshots saw this chat change (every file, git or not), from $AGENTWORKS_MOD_INBOX/changes.state.
let platformFiles: { path: string; status: string }[] = []
let platformStamp = ''
let lastMessage = ''

async function emit($: any, type: string, fields: Record<string, unknown> = {}) {
  // Numbered before any await, so concurrent events keep their order.
  const n = ++seq
  const at = Date.now()
  // Never fails the hook it reports from: Claude Code's own work goes on.
  try {
    const dir = await $.env.get('AGENTWORKS_MOD_DIR')
    if (!dir) return
    const name = `${String(at).padStart(14, '0')}-${String(n).padStart(6, '0')}-${type}.json`
    const sessionId = await $.session.id()
    await $.fs.write(`${dir}/events/${name}`, JSON.stringify({ type, at, seq: n, session_id: sessionId, ...fields }))
  } catch {
    // A lost event shows as a missing step in AgentWorks' log, not a broken turn.
  }
}

// deliverInbox hands Claude the messages AgentWorks put in $AGENTWORKS_MOD_DIR/inbox,
// in name order, once each: a new turn when Claude is idle, or a row the
// running turn reads at its next step. Each is confirmed with an "input" event.
async function deliverInbox($: any) {
  if (delivering) return
  delivering = true
  try {
    // AgentWorks' requests: a folder the agent in this session can read but not write.
    const inbox = await $.env.get('AGENTWORKS_MOD_INBOX')
    if (!inbox) return
    const entries = (await $.fs.list(inbox).catch(() => [])) as { name: string; kind: string }[]
    const names = entries.filter(entry => entry.kind === 'file' && entry.name.endsWith('.json')).map(entry => entry.name).sort()
    // A stop goes first: a message queued behind it must not hold it up.
    names.sort((a, b) => Number(b.includes('-abort')) - Number(a.includes('-abort')))
    for (const name of names) {
      if (delivered.has(name)) continue
      delivered.add(name)
      // A request that cannot be read is answered with its error, never left to time out.
      let id = name.replace(/\.json$/, '')
      try {
        const request = JSON.parse(await $.fs.read(`${inbox}/${name}`))
        id = String(request.id ?? id)
        await emit($, 'input', { input_id: id, ...(await handle($, request)) })
      } catch (err) {
        await emit($, 'input', { input_id: id, error: String(err) })
      }
    }
  } finally {
    delivering = false
  }
}

async function flushStream($: any, force: boolean) {
  if (stream.length === 0 || (!force && Date.now() - flushedAt < 150)) return
  const items = stream
  stream = []
  flushedAt = Date.now()
  await emit($, 'stream', { step: stepKey, items })
}

// trimRow keeps a stored row small enough for one event file: long text and
// tool output are cut, with a note of how much.
function trimRow(message: any): any {
  const cut = (text: string) => (text.length > ROW_TEXT_LIMIT ? `${text.slice(0, ROW_TEXT_LIMIT)}\n[cut: ${text.length - ROW_TEXT_LIMIT} more characters]` : text)
  const content = Array.isArray(message?.content)
    ? message.content.map((block: any) => {
        if (block?.type === 'text' && typeof block.text === 'string') return { ...block, text: cut(block.text) }
        if (block?.type === 'tool_result') {
          if (typeof block.content === 'string') return { ...block, content: cut(block.content) }
          if (Array.isArray(block.content)) return { ...block, content: block.content.map((part: any) => (part?.type === 'text' ? { ...part, text: cut(String(part.text ?? '')) } : { type: 'text', text: `[${part?.type ?? 'item'} not shown]` })) }
        }
        if (block?.type === 'image' || block?.type === 'document') return { type: 'text', text: `[${block.type} not shown]` }
        return block
      })
    : message?.content
  return { role: message?.role, content }
}

// waitForMcpTools holds the first message until the chat's MCP servers
// (AGENTWORKS_MOD_MCP_SERVERS, comma separated) have listed their tools, at
// most 30 s: they connect after the session starts, and a turn sent before
// them runs without their tools.
async function waitForMcpTools($: any) {
  if (toolsReady) return
  const normalize = (name: string) => name.replace(/[^A-Za-z0-9_-]/g, '_')
  const want = String((await $.env.get('AGENTWORKS_MOD_MCP_SERVERS')) ?? '').split(',').map(name => normalize(name.trim())).filter(Boolean)
  const deadline = Date.now() + 30000
  while (want.length > 0 && Date.now() < deadline) {
    const tools = (await $.tool.list()) as { name: string; mcp: boolean }[]
    const servers = new Set(tools.filter(tool => tool.mcp).map(tool => tool.name.split('__')[1]))
    if (want.every(name => servers.has(name))) break
    await $.clock.sleep(250)
  }
  toolsReady = true
}

// A message AgentWorks framed for Claude: an automatic notice, or a message
// from another agent or Crew. The band names its source; Claude Code draws the
// row itself (a mod's render hooks skip the messages the mod submitted).
function framedMessage(text: string): { label: string; body: string } | null {
  if (text.startsWith('[AUTO-NOTIFICATION]')) return { label: 'Automatic message', body: text.slice('[AUTO-NOTIFICATION]'.length).trim() }
  const agent = text.match(/^\[(?:Agent message|Message from another person's agent) (?:from )?([^.(]+)[^\]]*\]\s*/)
  if (agent) return { label: `Message from ${agent[1].trim()}`, body: text.slice(agent[0].length).replace(/^<<<BEGIN [^>]+>>>\n?|\n?<<<END [^>]+>>>$/g, '').trim() }
  return null
}

// refreshChanges asks git which of the files Claude edited this session
// changed, grouped by repository.
// readPlatformChanges picks up the chat's changed files when AgentWorks writes a new list.
async function readPlatformChanges($: any) {
  const inbox = await $.env.get('AGENTWORKS_MOD_INBOX')
  if (!inbox) return
  const raw = await $.fs.read(`${inbox}/changes.state`).catch(() => '')
  if (!raw || raw === platformStamp) return
  platformStamp = raw
  try {
    const parsed = JSON.parse(raw)
    platformFiles = Array.isArray(parsed.files) ? parsed.files.filter((file: any) => typeof file?.path === 'string') : []
  } catch {
    return
  }
  await refreshChanges($)
}

// shortPaths drops the folder every changed file shares, so the list reads like the app's.
function shortPaths(paths: string[]): Map<string, string> {
  const dirs = paths.map(path => path.split('/').slice(0, -1))
  let common = dirs[0] ?? []
  for (const dir of dirs) {
    let i = 0
    while (i < common.length && i < dir.length && common[i] === dir[i]) i++
    common = common.slice(0, i)
  }
  const prefix = common.length ? common.join('/') + '/' : ''
  return new Map(paths.map(path => [path, path.startsWith(prefix) ? path.slice(prefix.length) : path]))
}

async function refreshChanges($: any) {
  const byRepo = new Map<string, string[]>()
  for (const file of new Set([...editedFiles, ...platformFiles.map(file => file.path)])) {
    const dir = file.slice(0, file.lastIndexOf('/')) || '.'
    const top = await $.process.run(['git', '-C', dir, 'rev-parse', '--show-toplevel']).catch(() => null)
    const repo = top && top.exitCode === 0 ? top.stdout.trim() : ''
    if (!repo) continue
    byRepo.set(repo, [...(byRepo.get(repo) ?? []), file])
  }
  const next: RepoChanges[] = []
  for (const [repo, files] of byRepo) {
    const branch = await $.process.run(['git', '-C', repo, 'branch', '--show-current']).catch(() => null)
    const stat = await $.process.run(['git', '-C', repo, 'diff', '--numstat', 'HEAD', '--', ...files]).catch(() => null)
    const counts = new Map<string, { added: string; removed: string }>()
    for (const line of String(stat?.stdout ?? '').split('\n')) {
      const [added, removed, path] = line.split('\t')
      if (path) counts.set(`${repo}/${path}`, { added, removed })
    }
    const listed = files.map(file => ({ path: file.slice(repo.length + 1), ...(counts.get(file) ?? { added: 'new', removed: '' }) }))
    next.push({ repo, branch: String(branch?.stdout ?? '').trim(), files: listed })
  }
  changes = next
  $.ui.invalidate('ui.render')
}

// modelName turns a model id into the name people know ("claude-sonnet-5-5" -> "Sonnet 5.5").
function modelName(id: string): string {
  const match = /^claude-([a-z]+)-(\d+)(?:-(\d+))?/.exec(id)
  if (!match) return id
  return `${match[1][0].toUpperCase()}${match[1].slice(1)} ${match[2]}${match[3] ? `.${match[3]}` : ''}`
}

function tokens(n: number): string {
  return n >= 1_000_000 ? `${Math.round(n / 100_000) / 10}M` : `${Math.round(n / 1000)}k`
}

// The status line: model, how full the context is, and the plan's 5-hour and weekly use. No cost: the app
// shows spend where it is counted.
async function refreshStatus($: any) {
  const usage = await $.session.usage().catch(() => null)
  if (!usage) return
  const model = await $.session.model().catch(() => '')
  const parts: string[] = []
  const id = String(typeof model === 'string' ? model : model?.id ?? '')
  if (id) parts.push(modelName(id))
  if (usage.context?.tokens) {
    const used = usage.context.tokens
    const window = usage.context.window
    parts.push(window ? `context ${tokens(used)}/${tokens(window)} (${Math.round((used / window) * 100)}%)` : `context ${tokens(used)}`)
  }
  const names: Record<string, string> = { five_hour: '5h', seven_day: '7d', seven_day_opus: '7d Opus', seven_day_sonnet: '7d Sonnet' }
  for (const limit of usage.rateLimits ?? []) parts.push(`${names[limit.kind] ?? String(limit.kind).replace(/_/g, ' ')} ${Math.round(limit.percentUsed)}%`)
  $.ui.status(parts.filter(Boolean).join('  ·  ') || undefined)
}

// slashAllowed: a built-in command only when AGENTWORKS_MOD_SLASH_COMMANDS
// lists it; skills, custom and MCP prompt commands always.
async function slashAllowed($: any, name: string): Promise<boolean> {
  const allowed = String((await $.env.get('AGENTWORKS_MOD_SLASH_COMMANDS')) ?? '').split(',').map(entry => entry.trim().replace(/^\//, '').toLowerCase()).filter(Boolean)
  if (allowed.includes(name.toLowerCase())) return true
  const commands = (await $.command.list().catch(() => [])) as { name: string; source: string }[]
  const found = commands.find(entry => entry.name === name)
  return Boolean(found && found.source !== 'builtin')
}

// handle runs one inbox request and says how it went.
async function handle($: any, request: { kind?: string; text?: string }): Promise<Record<string, unknown>> {
  switch (request.kind ?? 'input') {
    case 'abort':
      if (!running || !turnId) return { delivery: 'idle' }
      await $.turn.abort({ turnId })
      return { delivery: 'aborted' }
    case 'compact': {
      if (running) throw new Error('a turn is running')
      const compacted = await $.session.compact()
      if (compacted.skip) return { delivery: 'skipped', reason: String(compacted.skip) }
      return { delivery: 'compacted', tokens_before: compacted.tokensBefore ?? 0, tokens_after: compacted.tokensAfter ?? 0 }
    }
    default: {
      await waitForMcpTools($)
      const text = String(request.text ?? '')
      if (running) {
        const stored = await $.session.append({ message: { type: 'user', content: [{ type: 'text', text }] } })
        if (stored.deny) throw new Error(String(stored.deny))
        return { delivery: 'running_turn' }
      }
      const framed = framedMessage(text)
      if (framed) {
        lastMessage = `${framed.label}: ${framed.body.split('\n')[0].slice(0, 80)}`
        $.ui.invalidate('ui.render')
      }
      // A slash command runs as the person typing it would: a skill or custom
      // command, or a built-in on the allowed list. A built-in that changes the
      // CLI's login or settings (/login, /config, /permissions, /hooks, ...) never.
      const command = text.match(/^\/([A-Za-z0-9_:.-]+)\s*([\s\S]*)$/)
      if (command) {
        if (!(await slashAllowed($, command[1]))) throw new Error(`/${command[1]} is not allowed here`)
        await $.command.run({ command: command[1], args: command[2] ?? '' })
        return { delivery: 'command' }
      }
      await $.prompt.submit({ text, asUser: true })
      return { delivery: 'new_turn' }
    }
  }
}

export const register: Register = on => {
  on('session.start', async ($, e, next) => {
    const result = await next(e)
    await $.command.register({ name: 'changes', description: "Show the files Claude changed in this chat, with Commit and Pull request" })
    await emit($, 'session')
    $.clock.every(250, () => {
      void deliverInbox($)
      void readPlatformChanges($).catch(() => undefined)
    })
    return result
  })

  // /clear, /resume and /branch end this session; the engine names the one
  // it moves to, and the next turn's events carry its id as well.
  on('session.end', async ($, e, next) => {
    const result = await next(e)
    await emit($, 'session_end', { reason: e.reason, ended_session_id: e.sessionId, next_session_id: e.resume?.id ?? '' })
    return result
  })

  on('command.run', async ($, e, next) => {
    // The few built-ins AgentWorks keeps for itself (it chooses and bills the login and model, sets the
    // tools and rules, keeps Claude in the Crew's folder and attached to the chat). Blocked here, however the
    // command was picked (typed, or chosen from the menu with the arrow keys), so the terminal lets the menu
    // work normally.
    const blocked = BLOCKED_COMMANDS[e.command]
    if (blocked) {
      await emit($, 'command_blocked', { command: e.command })
      return { text: `/${e.command} isn't available in AgentWorks. ${blocked}` }
    }
    const result = await next(e)
    await emit($, 'command', { command: e.command })
    return result
  })

  on('turn.start', async ($, e, next) => {
    if (!e.agentId) {
      running = true
      turnId = e.turnId
      await emit($, 'turn_start', { turn_id: e.turnId })
    }
    return next(e)
  })

  // The main conversation's model requests, streamed as they arrive: text,
  // thinking, each tool call and its input as Claude writes it, and the stop.
  on('turn.step', async function* ($, e, next) {
    const main = !e.agentId
    if (main) stepKey = `${e.turnId}:${e.index}`
    const chunks = next(e)
    while (true) {
      const step = await chunks.next()
      if (step.done) {
        if (main) await flushStream($, true)
        return step.value
      }
      const chunk: any = step.value
      if (main) {
        switch (chunk.kind) {
          case 'text':
          case 'thinking':
            stream.push({ kind: chunk.kind, index: chunk.index, text: chunk.text })
            break
          case 'tool':
            stream.push({ kind: 'tool', index: chunk.index, id: chunk.id, name: chunk.name })
            break
          case 'input':
            stream.push({ kind: 'input', index: chunk.index, json: chunk.json })
            break
          case 'stop':
            stream.push({ kind: 'stop', stop_reason: chunk.stopReason, usage: chunk.usage })
            break
        }
        await flushStream($, chunk.kind === 'stop' || chunk.kind === 'tool')
      }
      yield chunk
    }
  })

  on('session.append', async ($, e, next) => {
    const stored: any = await next(e)
    if (!(e as any).agentId && ROW_DOORS.has(String((e as any).door)) && stored?.message) {
      await emit($, 'row', { door: (e as any).door, uuid: stored.uuid, step: stepKey, message: trimRow(stored.message) })
    }
    return stored
  })

  // Files Claude edits, for the Changes pane.
  on('tool.call', async ($, e, next) => {
    const ran = await next(e)
    const input: any = e
    const path = String(input.file_path ?? input.notebook_path ?? '')
    if (['Edit', 'Write', 'MultiEdit', 'NotebookEdit'].includes(String(input.tool)) && path.startsWith('/') && !ran?.deny) {
      editedFiles.add(path)
      void refreshChanges($).catch(() => undefined)
    }
    return ran
  })

  // Usage changed (after a turn, or a plan limit's use moved): status line and AgentWorks' plan figures.
  on('session.measure', async ($, e, next) => {
    const result = await next(e)
    void refreshStatus($).catch(() => undefined)
    const usage = await $.session.usage().catch(() => null)
    if (usage?.rateLimits?.length) await emit($, 'usage', { rate_limits: usage.rateLimits, context_tokens: usage.context?.tokens ?? 0, context_window: usage.context?.window ?? 0 })
    return result
  })

  on('command.run', { command: 'changes' }, async $ => {
    await refreshChanges($)
    await $.ui.open({ id: CHANGES_PANE, title: 'Changes in this chat' })
    return { text: changes.length ? 'Changes pane opened.' : 'No file changes in this chat yet.' }
  })

  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    // AgentWorks' list counts every changed file; without it, the files Claude edited inside git repositories.
    const files = platformFiles.length || changes.reduce((sum, repo) => sum + repo.files.length, 0)
    if ((e as any).props?.hasSurvey || (!lastMessage && files === 0)) return next(e)
    const { Box, Button, Text } = $.ui.resolve(e)
    return (
      <Box gap={1}>
        {lastMessage && <Text dimColor>{lastMessage}</Text>}
        {files > 0 && <Button key="changes" label={`${files} file${files === 1 ? '' : 's'} changed · Open`} onPress={() => $.ui.open({ id: CHANGES_PANE, title: 'Changes in this chat' })} />}
      </Box>
    )
  })

  on('ui.render', { component: 'Pane', requestId: CHANGES_PANE }, async ($, e) => {
    const { Box, Button, Text } = $.ui.resolve(e)
    if (changes.length === 0 && platformFiles.length === 0) return <Text dimColor>No file changes in this chat yet.</Text>
    const ask = (text: string) => () => $.prompt.submit({ text, asUser: true })
    const short = shortPaths(platformFiles.map(file => file.path))
    return (
      <Box flexDirection="column" gap={1}>
        {platformFiles.length > 0 && (
          <Box key="all" flexDirection="column">
            <Text weight="semibold">Changed in this chat</Text>
            {platformFiles.map(file => (
              <Text key={file.path}>{short.get(file.path)} <Text dimColor>{file.status}</Text></Text>
            ))}
          </Box>
        )}
        {changes.map(repo => (
          <Box key={repo.repo} flexDirection="column">
            <Text weight="semibold">{repo.repo.split('/').pop()}{repo.branch ? ` · ${repo.branch}` : ''}</Text>
            {repo.files.map(file => (
              <Text key={file.path}>{file.path} <Text color="green">{file.added === 'new' ? 'new' : `+${file.added}`}</Text>{file.removed ? <Text color="red"> −{file.removed}</Text> : null}</Text>
            ))}
            <Box gap={1}>
              <Button key={`commit-${repo.repo}`} label="Commit" onPress={ask(`Commit the changes in ${repo.repo} with a clear message.`)} />
              <Button key={`pr-${repo.repo}`} variant="primary" label="Push & open PR" onPress={ask(`Push the branch in ${repo.repo} and open a pull request for it.`)} />
            </Box>
          </Box>
        ))}
      </Box>
    )
  })

  // Every compaction, whoever asked for it: /compact, Claude's own threshold, AgentWorks.
  on('session.compact', async ($, e, next) => {
    const result = await next(e)
    if (!result.skip) await emit($, 'compaction', { trigger: String((e as any).trigger ?? ''), tokens_before: result.tokensBefore ?? 0, tokens_after: result.tokensAfter ?? 0 })
    return result
  })

  on('turn.complete', async ($, e, next) => {
    const result = await next(e)
    if (!(e as any).agentId) {
      running = false
      void refreshStatus($).catch(() => undefined)
      // The turn ends whatever the usage read does.
      const usage: any = (await $.session.usage().catch(() => null)) ?? {}
      await emit($, 'turn_complete', {
        turn_id: e.turnId,
        reason: e.reason,
        aborted: e.isAborted,
        answer: e.answer,
        duration_ms: e.durationMs,
        context_tokens: usage.context?.tokens ?? 0,
        context_window: usage.context?.window ?? 0,
        cost_usd: usage.cost?.usd ?? 0,
        rate_limits: usage.rateLimits ?? [],
      })
    }
    return result
  })
}

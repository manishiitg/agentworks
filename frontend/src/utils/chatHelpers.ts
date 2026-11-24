import type { PollingEvent } from '../services/api-types'
import { pairToolCalls } from './terminalEventTranscript'

/**
 * Every helper agent a chat started, from any coding app, in one list (#3245):
 * Claude Code and Cursor helpers (their native activity records), Codex helpers
 * (their spawnAgent steps) and work handed to another coding app through the
 * coding-agent job tools. Everything here is what the server already sent for
 * the chat; nothing is guessed except a Codex helper's state, which the panel
 * reads from the server.
 */
export type ChatHelperState = 'running' | 'waiting' | 'done' | 'failed' | 'stopped' | 'unknown'

export interface ChatHelper {
  key: string
  /** The coding app that runs the helper (claude-code, codex-cli, cursor-cli, agy-cli, ...). */
  provider: string
  name: string
  task: string
  /** What it is doing now, when the app says. */
  activity?: string
  state: ChatHelperState
  startedAt?: number
  endedAt?: number
  durationMs?: number
  result?: string
  /** Opens the helper's own conversation (the subagent endpoint) when set. */
  toolCallId?: string
  /** A Codex helper's state comes from the subagent endpoint. */
  stateFromServer?: boolean
  /** Background-work item that stops it (Claude SDK tasks). */
  stopId?: string
  /** Work handed to a different coding app as a job. */
  delegated?: boolean
  model?: string
  jobId?: string
  /** Another of the owner's chat tabs doing a task this chat handed it (any coding app). */
  linkedChat?: { key: string; sessionId?: string; agent: string; running: boolean }
}

/** A task this chat handed to another of its tabs, as the server lists it (GET /api/sessions/{id}/linked-helpers). */
export interface ChatLinkedHelper {
  message_id: string
  inbox_id: string
  title: string
  task: string
  state: string
  reply?: string
  reply_at?: string
  sent_at: string
  agent: string
  chat_key: string
  session_id?: string
  provider?: string
  model?: string
  running: boolean
}

const object = (value: unknown): Record<string, unknown> | undefined => value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : undefined
const text = (value: unknown): string => typeof value === 'string' ? value.trim() : ''
const instant = (value: unknown): number | undefined => {
  if (typeof value !== 'string' || !value || value.startsWith('0001-')) return undefined
  const at = Date.parse(value)
  return Number.isFinite(at) ? at : undefined
}
const firstLine = (value: string, max = 120): string => {
  const line = value.split('\n').map(part => part.trim()).find(Boolean) || ''
  return line.length > max ? `${line.slice(0, max - 1)}…` : line
}

function json(value: string | undefined): Record<string, unknown> | undefined {
  if (!value) return undefined
  try { return object(JSON.parse(value)) } catch { /* fall through */ }
  const start = value.indexOf('{'), end = value.lastIndexOf('}')
  if (start < 0 || end <= start) return undefined
  try { return object(JSON.parse(value.slice(start, end + 1))) } catch { return undefined }
}

export function nativeHelperState(state: unknown): ChatHelperState {
  switch (state) {
    case 'started': return 'running'
    case 'finished': case 'completed': return 'done'
    case 'failed': return 'failed'
    case 'stopped': case 'interrupted': return 'stopped'
    default: return 'unknown'
  }
}

function jobState(status: string): ChatHelperState {
  switch (status) {
    case 'queued': case 'running': case 'cancellation_requested': return 'running'
    case 'completed': return 'done'
    case 'failed': case 'timed_out': return 'failed'
    case 'cancelled': return 'stopped'
    default: return 'unknown'
  }
}

const providerNames: Record<string, string> = {
  'claude-code': 'Claude', 'codex-cli': 'Codex', 'cursor-cli': 'Cursor', 'agy-cli': 'agy', agy: 'agy', 'pi-cli': 'Pi', 'muse-cli': 'Muse',
}
export const helperProviderName = (provider: string): string => providerNames[provider] || provider.replace(/-cli$/, '') || 'Agent'

const eventTime = (event: PollingEvent | undefined): number | undefined => instant((event as { timestamp?: unknown } | undefined)?.timestamp)

/**
 * The saved helper events of the whole chat (the server's list) followed by the
 * live ones it does not have yet, each event once (#3245).
 */
export function mergeHelperEvents(saved: PollingEvent[], live: PollingEvent[]): PollingEvent[] {
  if (!saved.length) return live
  const seen = new Set(saved.map(event => event.id).filter(Boolean))
  return [...saved, ...live.filter(event => !event.id || !seen.has(event.id))]
}

/** Builds the list from a chat's (already coalesced) events, oldest first. */
/** One helper's later record over its earlier one. A late start does not undo a finished helper. */
function mergeHelper(previous: ChatHelper, helper: ChatHelper): ChatHelper {
  const keepState = helper.state === 'running' && previous.state !== 'running' && previous.state !== 'unknown'
  const base = keepState ? { ...helper, ...previous } : { ...previous, ...helper }
  return {
    ...base,
    key: previous.key,
    name: helper.task && !keepState ? helper.name : previous.task ? previous.name : helper.name,
    task: (keepState ? previous.task : helper.task) || previous.task || helper.task,
    startedAt: previous.startedAt ?? helper.startedAt,
    toolCallId: helper.toolCallId || previous.toolCallId,
    stopId: helper.stopId || previous.stopId,
  }
}

export function collectChatHelpers(events: PollingEvent[]): ChatHelper[] {
  const helpers: Array<ChatHelper | undefined> = []
  const nativeKeys = new Map<string, number>()
  for (const event of events) {
    if (event.type !== 'coding_agent_native_activity') continue
    const envelope = object(event.data)
    const record = object(object(envelope?.data)?.record ?? envelope?.record)
    if (!record || record.kind !== 'helper') continue
    const toolId = text(record.tool_id), taskId = text(record.task_id), provider = text(record.provider)
    const key = JSON.stringify([provider, text(record.native_session_id), taskId || toolId || text(record.work_id) || text(record.id)])
    // One helper arrives under two ids: Claude's transcript names it by its tool call first, its task records
    // by the task id (with the tool call alongside). Either id finds the same line, or it was listed twice.
    const aliases = [key, ...(taskId ? [`task:${provider}:${taskId}`] : []), ...(toolId ? [`tool:${provider}:${toolId}`] : [])]
    const startedAt = instant(record.started_at), endedAt = instant(record.ended_at)
    const state = nativeHelperState(record.state)
    const durationMs = typeof record.duration_ms === 'number' ? record.duration_ms : startedAt !== undefined && endedAt !== undefined ? endedAt - startedAt : undefined
    // The chat hides a quick foreground helper; so does the list.
    if (record.helper_mode === 'foreground' && state !== 'running' && (durationMs === undefined || durationMs < 3000)) continue
    const description = text(record.helper_description)
    const genericName = ['', 'Agent', 'Task', 'Helper agent', 'Subagent'].includes(text(record.name))
    const helper: ChatHelper = {
      key: `native:${key}`,
      provider,
      name: firstLine(description || (genericName ? text(record.helper_type) : text(record.name)) || 'Helper agent', 80),
      task: description,
      activity: state === 'running' ? text(record.current_step) || undefined : undefined,
      state,
      startedAt, endedAt, durationMs,
      model: text(record.model) || undefined,
      ...(toolId ? { toolCallId: toolId } : {}),
      ...(provider === 'claude-code' && taskId && record.origin === 'sdk_helper' ? { stopId: `claude-task:${taskId}` } : {}),
    }
    // Pieces can arrive task id only, then tool call only, then both: the record with both joins the two lines.
    const found = [...new Set(aliases.map(alias => nativeKeys.get(alias)).filter((at): at is number => at !== undefined))].sort((a, b) => a - b)
    if (!found.length) { for (const alias of aliases) nativeKeys.set(alias, helpers.length); helpers.push(helper); continue }
    const [index, ...joined] = found
    for (const other of joined) {
      helpers[index] = mergeHelper(helpers[index] as ChatHelper, helpers[other] as ChatHelper)
      helpers[other] = undefined
      for (const [alias, at] of nativeKeys) if (at === other) nativeKeys.set(alias, index)
    }
    for (const alias of aliases) nativeKeys.set(alias, index)
    helpers[index] = mergeHelper(helpers[index] as ChatHelper, helper)
  }

  const list = helpers.filter((helper): helper is ChatHelper => helper !== undefined)
  const jobs = new Map<string, ChatHelper>()
  for (const pair of pairToolCalls(events)) {
    const startedAt = eventTime(pair.events[0])
    if (pair.name === 'spawnAgent' && pair.callId) {
      // Already listed: from an earlier step, or from the helper's own records.
      if (list.some(helper => helper.key === `codex:${pair.callId}` || helper.toolCallId === pair.callId)) continue
      const prompt = text(json(pair.args)?.prompt)
      list.push({
        key: `codex:${pair.callId}`, provider: 'codex-cli', name: firstLine(prompt, 80) || 'Helper agent', task: prompt,
        state: 'unknown', stateFromServer: true, startedAt, toolCallId: pair.callId,
      })
      continue
    }
    if (pair.name === 'delegate_coding_agent') {
      const args = json(pair.args) || {}
      const out = json(pair.result) || {}
      const task = text(args.task)
      const jobId = text(out.job_id)
      const status = text(out.status)
      const existing = jobId ? jobs.get(jobId) : undefined
      if (existing) { list.splice(list.indexOf(existing), 1); jobs.delete(jobId) }
      const helper: ChatHelper = {
        key: `job:${jobId || pair.key}`, provider: text(out.provider) || text(args.provider) || 'agent', name: firstLine(task, 80) || 'Coding job', task,
        state: pair.status === 'error' ? 'failed' : pair.status === 'running' ? 'running' : status ? jobState(status) : 'unknown',
        startedAt: instant(out.started_at) ?? instant(out.created_at) ?? startedAt,
        delegated: true, model: text(out.model) || text(args.model) || undefined, jobId: jobId || undefined,
        result: pair.status === 'error' ? firstLine(pair.result || '', 400) || undefined : undefined,
      }
      list.push(helper)
      if (jobId) jobs.set(jobId, helper)
      continue
    }
    if (pair.name === 'get_coding_agent_job' || pair.name === 'cancel_coding_agent_job') {
      const out = json(pair.result)
      const jobId = text(out?.job_id) || text(json(pair.args)?.job_id)
      const helper = jobs.get(jobId)
      if (!helper || !out) continue
      const status = text(out.status)
      if (status) helper.state = jobState(status)
      const progress = text(out.progress)
      helper.activity = helper.state === 'running' && progress ? firstLine(progress) : undefined
      const result = text(out.result) || text(out.error)
      if (result) helper.result = result
      helper.endedAt = instant(out.finished_at) ?? helper.endedAt
      if (typeof out.elapsed_seconds === 'number') helper.durationMs = out.elapsed_seconds * 1000
    }
  }
  return list
}

/** Running first (newest first), then the rest (newest first). */
export function orderChatHelpers(helpers: ChatHelper[]): { running: ChatHelper[]; finished: ChatHelper[] } {
  const newest = (a: ChatHelper, b: ChatHelper) => (b.startedAt ?? 0) - (a.startedAt ?? 0)
  return {
    running: helpers.filter(helper => helper.state === 'running').sort(newest),
    finished: helpers.filter(helper => helper.state !== 'running').sort(newest),
  }
}

/** The tasks this chat handed to its other tabs, as helpers (#3245). */
export function linkedChatHelpers(items: ChatLinkedHelper[]): ChatHelper[] {
  return items.map(item => {
    const state: ChatHelperState = item.state === 'done' ? 'done' : item.state === 'blocked' ? 'failed' : item.state === 'needs_you' ? 'waiting' : 'running'
    const reply = text(item.reply)
    return {
      key: `tab:${item.message_id}`,
      provider: text(item.provider) || 'agent',
      name: firstLine(text(item.title) || text(item.task), 80) || 'Handed-off task',
      task: text(item.task),
      activity: state === 'running' ? (item.running ? `Working in "${item.agent}"` : `Waiting for "${item.agent}" to start`) : undefined,
      state,
      startedAt: instant(item.sent_at),
      endedAt: state === 'running' ? undefined : instant(item.reply_at),
      result: reply || undefined,
      model: text(item.model) || undefined,
      linkedChat: { key: item.chat_key, sessionId: item.session_id || undefined, agent: item.agent, running: item.running },
    }
  })
}

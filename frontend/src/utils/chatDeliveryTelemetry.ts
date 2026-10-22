import type { PollingEvent } from '../services/api-types'

export type ChatDeliveryTelemetryPhase =
  | 'send_decision'
  | 'send_staged'
  | 'receipt_shown'
  | 'submitted'
  | 'api_acknowledged'
  | 'api_failed'
  | 'sse_received'
  | 'poll_received'
  | 'catchup_received'
  | 'processed'
  | 'painted'
  | 'tab_reorder'
  | 'view_switch'
  | 'terminal_ready'

export type ChatDeliveryTelemetryEvent = {
  sequence: number
  phase: ChatDeliveryTelemetryPhase
  session_id: string
  event_id?: string
  event_type?: string
  transport?: string
  tab_id?: string
  target_tab_id?: string
  client_time: string
  performance_ms: number
  server_event_time?: string
  submission_id?: string
  elapsed_ms?: number
  delivery_status?: string
  provider?: string
  delivery_source?: string
  server_received_at?: string
  cli_accepted_at?: string
  server_to_cli_ms?: number
}

export type ChatDeliveryTelemetryBatch = {
  page_id: string
  events: ChatDeliveryTelemetryEvent[]
}

type TelemetryTransport = (batch: ChatDeliveryTelemetryBatch) => Promise<void>

const CHAT_DELIVERY_EVENT_TYPES = new Set([
  'user_message',
  'conversation_thinking',
  'llm_generation_end',
  'unified_completion',
  'conversation_error',
  'agent_error',
])
const MAX_EVENTS_PER_OBSERVATION = 20
const MAX_QUEUED_EVENTS = 500
const MAX_SEEN_MILESTONES = 5000
const queue: ChatDeliveryTelemetryEvent[] = []
const seenMilestones = new Set<string>()
let timer: number | undefined
let sequence = 0
let pageID = ''
let telemetryTransport: TelemetryTransport | undefined

function getPageID(): string {
  if (!pageID) {
    pageID = typeof crypto !== 'undefined' && crypto.randomUUID
      ? crypto.randomUUID()
      : `page-${Date.now()}-${Math.random().toString(36).slice(2)}`
  }
  return pageID
}

export function configureChatDeliveryTelemetryTransport(transport: TelemetryTransport): void {
  telemetryTransport = transport
}

export function isChatDeliveryTelemetryEvent(event: Pick<PollingEvent, 'type'>): boolean {
  return typeof event.type === 'string' && CHAT_DELIVERY_EVENT_TYPES.has(event.type)
}

function milestoneGroup(phase: ChatDeliveryTelemetryPhase): string {
  if (phase === 'sse_received' || phase === 'poll_received' || phase === 'catchup_received') {
    return 'received'
  }
  return phase
}

function shouldRecordMilestone(
  phase: ChatDeliveryTelemetryPhase,
  sessionId: string,
  event: PollingEvent,
): boolean {
  // Legacy events without IDs cannot be safely deduplicated without inspecting
  // content, which this content-free diagnostic intentionally never does.
  if (!event.id) return true
  const key = `${milestoneGroup(phase)}:${sessionId}:${event.id}`
  if (seenMilestones.has(key)) return false
  seenMilestones.add(key)
  if (seenMilestones.size > MAX_SEEN_MILESTONES) {
    const oldest = seenMilestones.values().next().value
    if (oldest) seenMilestones.delete(oldest)
  }
  return true
}

function flush(): void {
  timer = undefined
  const events = queue.splice(0, 100)
  if (events.length === 0) return
  const transport = telemetryTransport
  if (transport) {
    void transport({ page_id: getPageID(), events }).catch(() => {
      // Diagnostics must never delay, retry, or alter chat delivery.
    })
  }
  if (queue.length > 0) timer = window.setTimeout(flush, 100)
}

type ChatSubmissionTelemetryDetails = {
  tabId?: string
  /** The server's message id, so a screenshot can be matched to the server's [DELIVERY] line. */
  eventId?: string
  observedPerformanceMS?: number
  elapsedMS?: number
  deliveryStatus?: string
  provider?: string
  deliverySource?: string
  /** Where a send went: server, server:new, blocked:init, blocked:no-target (send_decision). */
  transport?: string
  serverReceivedAt?: string
  cliAcceptedAt?: string
  serverToCLIMS?: number
}

/** Record content-free send-to-CLI milestones correlated by submission ID. */
export function recordChatSubmissionTelemetry(
  phase: Extract<ChatDeliveryTelemetryPhase, 'send_decision' | 'send_staged' | 'receipt_shown' | 'submitted' | 'api_acknowledged' | 'api_failed'>,
  sessionId: string,
  submissionId: string,
  details: ChatSubmissionTelemetryDetails = {},
): void {
  if (typeof window === 'undefined' || !sessionId || !submissionId) return
  const key = `${phase}:${sessionId}:${submissionId}`
  if (seenMilestones.has(key)) return
  seenMilestones.add(key)
  if (queue.length >= MAX_QUEUED_EVENTS) queue.shift()
  queue.push({
    sequence: ++sequence,
    phase,
    session_id: sessionId,
    event_id: details.eventId,
    event_type: 'chat_submission',
    transport: details.transport,
    tab_id: details.tabId,
    client_time: new Date().toISOString(),
    performance_ms: Math.round((details.observedPerformanceMS ?? performance.now()) * 1000) / 1000,
    submission_id: submissionId,
    elapsed_ms: details.elapsedMS === undefined ? undefined : Math.round(details.elapsedMS * 1000) / 1000,
    delivery_status: details.deliveryStatus,
    provider: details.provider,
    delivery_source: details.deliverySource,
    server_received_at: details.serverReceivedAt,
    cli_accepted_at: details.cliAcceptedAt,
    server_to_cli_ms: details.serverToCLIMS,
  })
  if (timer === undefined) timer = window.setTimeout(flush, 100)
}

/** Drag/drop diagnostics share the existing bounded, content-free transport.
 * IDs only: never chat names, messages, paths or drag payload contents. */
export function recordTabReorderTelemetry(sessionId: string, attemptId: string, status: 'start' | 'drop' | 'cancel' | 'saved' | 'failed' | 'no_change', sourceTabId: string, targetTabId?: string) {
  if (typeof window === 'undefined' || !sessionId) return
  if (queue.length >= MAX_QUEUED_EVENTS) queue.shift()
  queue.push({ sequence: ++sequence, phase: 'tab_reorder', session_id: sessionId, submission_id: attemptId,
    event_type: 'chat_tab', tab_id: sourceTabId, target_tab_id: targetTabId, delivery_status: status,
    client_time: new Date().toISOString(), performance_ms: Math.round(performance.now() * 1000) / 1000 })
  if (timer === undefined) timer = window.setTimeout(flush, 100)
}

let viewSwitchStartedAt = 0
/** The moment a person asks to switch between the chat and the terminal view; the paint after it is timed against this. */
export function markViewSwitchStart(): void {
  viewSwitchStartedAt = typeof performance !== 'undefined' ? performance.now() : 0
}
/** The start mark, read once: 0 when no switch was asked for since the last read. */
export function takeViewSwitchStart(): number {
  const at = viewSwitchStartedAt
  viewSwitchStartedAt = 0
  return at
}

/** Content-free timings of the chat/terminal switch, so a slow switch shows up in the server log with its numbers.
 * view_switch: the click to the first paint of the new view (what = chat or terminal); terminal_ready: one of the terminal's own
 * loads (what = metadata or history). */
export function recordViewTimingTelemetry(phase: 'view_switch' | 'terminal_ready', sessionId: string, tabId: string, what: string, elapsedMs: number) {
  if (typeof window === 'undefined' || !sessionId) return
  if (queue.length >= MAX_QUEUED_EVENTS) queue.shift()
  queue.push({ sequence: ++sequence, phase, session_id: sessionId, tab_id: tabId, transport: what, elapsed_ms: Math.round(elapsedMs),
    client_time: new Date().toISOString(), performance_ms: Math.round(performance.now() * 1000) / 1000 })
  if (timer === undefined) timer = window.setTimeout(flush, 100)
}

/** Record content-free browser milestones for visible chat events. */
export function recordChatDeliveryTelemetry(
  phase: ChatDeliveryTelemetryPhase,
  sessionId: string,
  events: PollingEvent[],
  transport: string,
  tabId?: string,
): void {
  if (typeof window === 'undefined' || !sessionId) return
  // Catch-up can return a 1,500-event durable tail. Timing diagnostics only
  // need the newest relevant milestones; processing every historical event
  // competes with the chat renderer on the browser's main thread.
  const relevantEvents = events
    .filter(isChatDeliveryTelemetryEvent)
    .slice(-MAX_EVENTS_PER_OBSERVATION)
  for (const event of relevantEvents) {
    if (!shouldRecordMilestone(phase, sessionId, event)) continue
    if (queue.length >= MAX_QUEUED_EVENTS) queue.shift()
    queue.push({
      sequence: ++sequence,
      phase,
      session_id: sessionId,
      event_id: event.id || undefined,
      event_type: event.type || undefined,
      transport,
      tab_id: tabId,
      client_time: new Date().toISOString(),
      performance_ms: Math.round(performance.now() * 1000) / 1000,
      server_event_time: event.timestamp || undefined,
    })
  }
  if (queue.length === 0 || timer !== undefined) return
  timer = window.setTimeout(flush, 100)
}

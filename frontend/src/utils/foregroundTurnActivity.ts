import type { PollingEvent } from '../services/api-types'
import { isForegroundSessionEvent } from '../../shared/session/foreground'
import { getEventPayloadParts, getRuntimeEventScope } from './runtimeEventScope'

const completionTypes = new Set([
  'llm_generation_end', 'unified_completion', 'agent_end',
  'conversation_end', 'conversation_error', 'context_cancelled',
])
const startTypes = new Set(['user_message', 'conversation_start', 'llm_generation_start', 'streaming_start'])

function isForegroundTurnEvent(event: PollingEvent): boolean {
  const { eventRecord, agentEvent, innerData } = getEventPayloadParts(event)
  return isForegroundSessionEvent(event,
    eventRecord.component ?? innerData?.component ?? agentEvent?.component,
    eventRecord.correlation_id ?? innerData?.correlation_id ?? agentEvent?.correlation_id,
  ) && getRuntimeEventScope(event).kind === 'session'
}

export function isForegroundTurnCompletion(event: PollingEvent): boolean {
  if (!event.type || !completionTypes.has(event.type) || !isForegroundTurnEvent(event)) return false
  // Restored narration uses a generation-end shape without ending the turn.
  return getEventPayloadParts(event).innerData?.restored_intermediate_update !== true
}

export function foregroundTurnState(events?: readonly PollingEvent[]): 'running' | 'completed' | undefined {
  let latest: PollingEvent | undefined
  let state: 'running' | 'completed' | undefined
  for (const event of events || []) {
    const completed = isForegroundTurnCompletion(event)
    const started = event.type && startTypes.has(event.type) && isForegroundTurnEvent(event)
    if (!completed && !started) continue
    if (latest) {
      // Catch-up/history can append old lifecycle events after the current
      // optimistic message. Arrival order must not settle that newer turn.
      const previousTime = Date.parse(latest.timestamp || '')
      const eventTime = Date.parse(event.timestamp || '')
      if (Number.isFinite(previousTime) && Number.isFinite(eventTime)) {
        if (eventTime < previousTime) continue
        if (eventTime === previousTime && event.sequence !== undefined && latest.sequence !== undefined && event.sequence < latest.sequence) continue
      }
    }
    latest = event
    state = completed ? 'completed' : 'running'
  }
  return state
}

export function foregroundTurnCompleted(events?: readonly PollingEvent[]): boolean {
  return foregroundTurnState(events) === 'completed'
}

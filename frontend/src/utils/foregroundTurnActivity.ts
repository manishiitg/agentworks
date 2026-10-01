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

export function foregroundTurnCompleted(events?: readonly PollingEvent[]): boolean {
  if (!events) return false
  for (let index = events.length - 1; index >= 0; index--) {
    const event = events[index]
    if (isForegroundTurnCompletion(event)) return true
    if (event.type && startTypes.has(event.type) && isForegroundTurnEvent(event)) return false
  }
  return false
}

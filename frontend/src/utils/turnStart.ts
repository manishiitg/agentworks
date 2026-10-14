import type { PollingEvent } from '../services/api-types'

// Events that end a turn. After one of these the next user message starts a new turn.
const TURN_END_EVENTS = new Set(['unified_completion', 'conversation_end', 'conversation_error', 'context_cancelled'])

// The server's own start time of the turn now running: the timestamp the server put on the first user message after
// the last turn ended. Display only; nothing is decided from it. Returns undefined when no such event is known, and
// the caller then shows no elapsed time rather than starting a browser clock.
export function currentTurnStartedAt(events: readonly Pick<PollingEvent, 'type' | 'timestamp'>[] | undefined): number | undefined {
  if (!events) return undefined
  let started: number | undefined
  for (let index = events.length - 1; index >= 0; index -= 1) {
    const event = events[index]
    const type = event.type ?? ''
    if (TURN_END_EVENTS.has(type)) break
    if (type === 'user_message' && event.timestamp) {
      const at = Date.parse(event.timestamp)
      if (Number.isFinite(at)) started = at
    }
  }
  return started
}

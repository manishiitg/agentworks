import type { PollingEvent } from '../services/api-types'

// Events that end a turn. After one of these the next user message starts a new turn.
const TURN_END_EVENTS = new Set(['unified_completion', 'conversation_end', 'conversation_error', 'context_cancelled', 'streaming_end'])

// The server's own start time of the turn now running: the timestamp the server put on the first user message after
// the last turn ended. Display only; nothing is decided from it.
//
// It is returned only when the loaded events PROVE it: a turn-end event is present before that user message. A window
// that starts mid-conversation (older events not loaded, a restored chat, a reload) or a message queued during the
// previous turn gives no such proof, and then this returns undefined so the screen shows no time rather than a wrong
// one. The server should expose the turn start directly (PLAT-746-35); until then this stays conservative.
export function currentTurnStartedAt(events: readonly Pick<PollingEvent, 'type' | 'timestamp'>[] | undefined): number | undefined {
  if (!events) return undefined
  let started: number | undefined
  for (let index = events.length - 1; index >= 0; index -= 1) {
    const event = events[index]
    const type = event.type ?? ''
    if (TURN_END_EVENTS.has(type)) return started
    if (type === 'user_message' && event.timestamp) {
      const at = Date.parse(event.timestamp)
      if (Number.isFinite(at)) started = at
    }
  }
  return undefined
}

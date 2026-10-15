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

// Events that are not the agent's own output: the person's message and delivery receipts.
const NOT_AGENT_OUTPUT = new Set(['user_message', 'live_input_confirmed'])

// The server's own time of the newest thing the agent produced in the turn now running (text, a tool call, thinking,
// a question), so the Working line can say how long it has been quiet. Display only. Like currentTurnStartedAt it
// answers only when the turn start is proven by the loaded events; otherwise it returns undefined and the line shows
// nothing rather than a wrong time. With no output yet it returns the turn start itself.
export function lastAgentOutputAt(events: readonly Pick<PollingEvent, 'type' | 'timestamp'>[] | undefined): number | undefined {
  if (!events) return undefined
  const started = currentTurnStartedAt(events)
  if (started === undefined) return undefined
  let newest = started
  for (let index = events.length - 1; index >= 0; index -= 1) {
    const event = events[index]
    const type = event.type ?? ''
    if (TURN_END_EVENTS.has(type)) continue
    if (type === 'user_message') {
      const at = event.timestamp ? Date.parse(event.timestamp) : NaN
      if (Number.isFinite(at) && at <= started) break
      continue
    }
    if (NOT_AGENT_OUTPUT.has(type) || !event.timestamp) continue
    const at = Date.parse(event.timestamp)
    if (Number.isFinite(at) && at > newest) newest = at
  }
  return newest
}

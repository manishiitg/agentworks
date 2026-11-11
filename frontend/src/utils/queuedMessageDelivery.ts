/**
 * How a queued message should reach an agent whose turn is still running.
 *
 * There is one live-delivery mechanism:
 *
 * - `live-query` — POST /api/query. The backend decides whether to steer the
 *   active provider, persist the message for the next turn, or start a turn.
 * - `wait` — no live path; the queue drain sends it once the turn ends.
 *
 * Structured workflow-step turns are excluded by the caller because they have
 * no retained process and no steer capability.
 *
 * Auto-notifications are never delivered mid-turn. Interrupting a running agent
 * with step-completion noise is not worth it, and they lose nothing by waiting.
 */
export type QueuedMessageRoute = 'live-query' | 'wait'

export function routeForQueuedMessage(params: {
  isStreaming: boolean
  hasSession: boolean
  canUseLiveQuery: boolean
  canSteer: boolean
}): QueuedMessageRoute {
  const { isStreaming, hasSession, canUseLiveQuery, canSteer } = params
  // Only the mid-turn case is decided here. An idle chat is the queue drain's job.
  if (!isStreaming || !hasSession) return 'wait'
  if (canUseLiveQuery || canSteer) return 'live-query'
  return 'wait'
}

/** Auto-notifications wait for idle; everything else is a person expecting an answer. */
export function splitQueuedMessages(messages: string[], autoNotificationPrefix: string): {
  human: string[]
  auto: string[]
} {
  return {
    human: messages.filter(message => !message.startsWith(autoNotificationPrefix)),
    auto: messages.filter(message => message.startsWith(autoNotificationPrefix)),
  }
}

import React from 'react'
import type { ConversationThinkingEvent } from '../../../generated/event-types'

interface ConversationThinkingEventProps {
  event: ConversationThinkingEvent
  compact?: boolean
}

/**
 * A readable progress update emitted by a coding-agent transport. This is a
 * structured event, rather than a rendered terminal/tmux frame.
 *
 * Rendered as plain muted text, not a card: it reads as the agent's running
 * commentary between the user's message and the answer (user request,
 * 2026-09-03). Token-streamed fragments are folded into one event upstream
 * (utils/thinkingDeltas.ts), so one of these is one thinking span.
 */
export const ConversationThinkingEventDisplay: React.FC<ConversationThinkingEventProps> = ({ event }) => {
  const thinking = event.thinking?.trim()
  if (!thinking) return null

  return (
    <p className="whitespace-pre-wrap break-words px-1 text-sm leading-6 text-muted-foreground">
      {thinking}
    </p>
  )
}

import React from 'react'
import { RotateCw } from 'lucide-react'
import type { RetryAttemptEvent } from '../../../generated/events'

interface RetryAttemptEventDisplayProps {
  event: RetryAttemptEvent
}

// A provider retry is a transient notice inside a reply, not an error: one
// compact row in the chat's design tokens (the old boxed amber card predated
// the current chat design). The reason stays readable (connection_error →
// "connection error") and the full text is in the tooltip.
export const RetryAttemptEventDisplay: React.FC<RetryAttemptEventDisplayProps> = ({ event }) => {
  const nextAttempt = (event.attempt_index ?? 0) + 1
  const attempt = event.total_attempts ? Math.min(nextAttempt, event.total_attempts) : nextAttempt
  const attemptText = event.total_attempts ? `attempt ${attempt} of ${event.total_attempts}` : `attempt ${attempt}`
  const reason = event.error?.replace(/\s*-\s*retrying original model\s*$/i, '').trim()
  const readableReason = reason?.replace(/_/g, ' ')
  const model = event.model_id || event.provider || 'model'
  const parts = [`Retrying ${model}`, attemptText, event.duration ? `in ${event.duration}` : '', readableReason || ''].filter(Boolean)

  return (
    <div
      className="flex min-w-0 items-center gap-2 rounded-md border border-border bg-muted/30 px-3 py-1.5 text-xs text-muted-foreground"
      title={reason ? `Reason: ${reason}` : undefined}
      role="status"
    >
      <RotateCw className="h-3.5 w-3.5 shrink-0 text-amber-500" aria-hidden="true" />
      <span className="min-w-0 truncate">
        <span className="font-medium text-foreground">{parts[0]}</span>
        {parts.slice(1).map(part => <span key={part}> · {part}</span>)}
      </span>
      {event.timestamp && (
        <span className="ml-auto shrink-0 tabular-nums">{new Date(event.timestamp).toLocaleTimeString()}</span>
      )}
    </div>
  )
}

import React from 'react'
import type { ContextCancelledEvent } from '../../../generated/events'
import { formatDuration } from '../../../utils/duration'

interface ContextCancelledEventDisplayProps {
  event: ContextCancelledEvent
}

export const ContextCancelledEventDisplay: React.FC<ContextCancelledEventDisplayProps> = ({
  event
}) => {
  const parts: string[] = ['Context cancelled']
  if (event.reason && event.reason !== 'context canceled') parts.push(`— ${event.reason}`)
  if (event.turn !== undefined) parts.push(`T${event.turn}`)
  if (event.duration !== undefined) parts.push(formatDuration(event.duration))

  return (
    <span className="text-[11px] text-muted-foreground italic">
      {parts.join(' · ')}
    </span>
  )
}

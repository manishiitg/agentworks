import React from 'react'
import { ConversationMarkdownRenderer } from '../../ui/MarkdownRenderer'
import { humanReadableAgentResult } from '../system/eventDisplayUtils'
import { interruptedCompletionMessage } from '../../../../shared/session/completionOutcome'

interface UnifiedCompletionEvent {
  timestamp?: string
  trace_id?: string
  span_id?: string
  event_id?: string
  parent_id?: string
  is_end_event?: boolean
  correlation_id?: string
  agent_type?: string
  agent_mode?: string
  question?: string
  final_result?: string
  status?: string
  duration?: number
  turns?: number
  error?: string
  metadata?: Record<string, unknown>
}

interface UnifiedCompletionEventDisplayProps {
  event: UnifiedCompletionEvent
}

export const UnifiedCompletionEventDisplay: React.FC<UnifiedCompletionEventDisplayProps> = ({ event }) => {
  const displayResult = humanReadableAgentResult(event.final_result)

  // Note: event.duration is in nanoseconds from Go time.Duration
  const formatDuration = (durationNs: number) => {
    if (!durationNs || durationNs <= 0) {
      return '0ms'
    }

    // Convert nanoseconds to milliseconds
    const durationMs = durationNs / 1000000

    if (durationMs < 1) {
      // Less than 1ms, show in microseconds
      const durationUs = durationNs / 1000
      return `${Math.round(durationUs)}μs`
    } else if (durationMs < 1000) {
      return `${Math.round(durationMs)}ms`
    } else if (durationMs < 60000) {
      return `${(durationMs / 1000).toFixed(1)}s`
    } else {
      return `${(durationMs / 60000).toFixed(1)}m`
    }
  }

  const isError = event.status === 'error' || event.error

  // Render restored step errors and other completions with content as normal
  // assistant messages. Otherwise an event with status=error and final_result
  // collapses to a generic "Error" box and hides the useful restored summary.
  if (displayResult) {
    return (
      <div className="flex items-start gap-2 py-2">
        <div className="flex-1 min-w-0">
          <div className="border-l-2 border-emerald-400/65 pl-4 pr-2">
            <ConversationMarkdownRenderer content={displayResult} maxHeight="none" framed={false} />
          </div>
          {event.duration != null && (
            <div className="mt-2 pl-4 text-[10px] tabular-nums text-neutral-500">
              {formatDuration(event.duration)}
            </div>
          )}
        </div>
      </div>
    )
  }

  const interrupted = interruptedCompletionMessage(event.metadata)
  if (interrupted) {
    return <div role="status" className="py-2 text-sm text-muted-foreground">{interrupted}</div>
  }

  // Error case without final_result: keep the original compact error display
  if (isError) {
    return (
      <div className="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded p-2">
        <div className="flex items-center justify-between gap-3">
          <div className="text-sm font-medium text-red-700 dark:text-red-300">
            Error
            <span className="text-xs font-normal text-red-600 dark:text-red-400 ml-2">
              {event.duration != null && `${formatDuration(event.duration)}`}
              {event.turns && ` | ${event.turns} turns`}
            </span>
          </div>
          {event.timestamp && (
            <div className="text-xs flex-shrink-0 text-red-600 dark:text-red-400">
              {new Date(event.timestamp).toLocaleTimeString()}
            </div>
          )}
        </div>
        {event.error && (
          <div className="mt-2 bg-red-100 dark:bg-red-800 border border-red-200 dark:border-red-700 rounded-md p-2">
            <div className="text-sm text-red-900 dark:text-red-100 whitespace-pre-wrap break-words">
              {event.error}
            </div>
          </div>
        )}
      </div>
    )
  }

  // No final_result: minimal completion indicator
  return (
    <div className="bg-gray-50 dark:bg-gray-900/20 border border-gray-200 dark:border-gray-800 rounded p-2">
      <div className="flex items-center justify-between gap-3">
        <div className="text-xs text-gray-500 dark:text-gray-400">
          Completed
          {event.duration != null && ` in ${formatDuration(event.duration)}`}
          {event.turns && ` (${event.turns} turns)`}
        </div>
        {event.timestamp && (
          <div className="text-xs text-gray-400 dark:text-gray-500">
            {new Date(event.timestamp).toLocaleTimeString()}
          </div>
        )}
      </div>
    </div>
  )
}

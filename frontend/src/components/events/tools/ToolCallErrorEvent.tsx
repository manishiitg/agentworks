import React from 'react'
import type { ToolCallErrorEvent } from '../../../generated/events'
import { formatDuration } from '../../../utils/duration'
import { useExpandable } from '../useExpandable'

interface ToolCallErrorEventDisplayProps {
  event: ToolCallErrorEvent
}

const ERROR_PREVIEW_LIMIT = 160

function compactError(error?: string): string | null {
  if (!error) return null
  const singleLine = error.replace(/\s+/g, ' ').trim()
  if (!singleLine) return null
  return singleLine.length > ERROR_PREVIEW_LIMIT
    ? `${singleLine.slice(0, ERROR_PREVIEW_LIMIT)}...`
    : singleLine
}

export const ToolCallErrorEventDisplay: React.FC<ToolCallErrorEventDisplayProps> = ({ event }) => {
  const { isExpanded, toggle } = useExpandable(true)
  const errorPreview = compactError(event.error)

  return (
    <div className="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded p-2">
      <div className="flex items-center justify-between gap-3">
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium text-red-700 dark:text-red-300">
              ❌ Tool Call Error
            </span>
            {event.tool_name && (
              <span className="text-xs text-red-600 dark:text-red-400">
                • {event.tool_name}
              </span>
            )}
            {event.turn && (
              <span className="text-xs text-red-600 dark:text-red-400">
                • Turn {event.turn}
              </span>
            )}
            {event.duration && (
              <span className="text-xs text-red-600 dark:text-red-400">
                • {formatDuration(event.duration)}
              </span>
            )}
          </div>
          {!isExpanded && errorPreview && (
            <div className="mt-1 text-xs text-red-700 dark:text-red-300 truncate">
              {errorPreview}
            </div>
          )}
        </div>

        <div className="flex items-center gap-2 flex-shrink-0">
          {event.timestamp && (
            <div className="text-xs text-red-600 dark:text-red-400">
              {new Date(event.timestamp).toLocaleTimeString()}
            </div>
          )}
          <button
            onClick={toggle}
            className="rounded border border-red-200 px-2 py-0.5 text-[10px] font-medium text-red-700 transition-colors hover:bg-red-200 dark:border-red-800 dark:text-red-300 dark:hover:bg-red-800"
            title={isExpanded ? "Collapse error details (Alt+Click for all)" : "Expand error details (Alt+Click for all)"}
          >
            {isExpanded ? 'Close' : 'Open'}
          </button>
        </div>
      </div>
      
      {isExpanded && (
        <div className="mt-2 space-y-2">
          {event.server_name && (
            <div className="text-xs text-red-600 dark:text-red-400">
              <span className="font-medium">Server:</span> {event.server_name}
            </div>
          )}
          
          {/* Error details */}
          {event.error && (
            <div className="mt-1 p-2 bg-card dark:bg-red-950/30 border border-red-200 dark:border-red-800 rounded text-xs text-red-800 dark:text-red-200 font-mono whitespace-pre-wrap break-words max-h-40 overflow-y-auto">
              {event.error}
            </div>
          )}
        </div>
      )}
    </div>
  )
}

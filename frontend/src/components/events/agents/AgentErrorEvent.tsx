import React from 'react'
import type { AgentErrorEvent } from '../../../generated/events-bridge'
import { formatDuration } from '../../../utils/duration'
import { FreeModelHint } from '../../chat/FreeModelHint'

interface AgentErrorEventProps {
  event: AgentErrorEvent
}

export const AgentErrorEventDisplay: React.FC<AgentErrorEventProps> = ({ event }) => {
  return (
    <div className="bg-yellow-50 dark:bg-yellow-900/20 border border-yellow-200 dark:border-yellow-800 rounded p-1.5">
      <div className="flex items-start gap-2">
        <div className="w-1.5 h-1.5 bg-red-500 rounded-full mt-0.5"></div>
        <div className="flex-1 min-w-0 text-xs">
          <div className="flex items-center gap-1.5">
            <span className="font-medium text-yellow-800 dark:text-yellow-200">Agent Error</span>
            {event.timestamp && (
              <span className="text-yellow-600 dark:text-yellow-400 ml-auto opacity-75">
                {new Date(event.timestamp).toLocaleTimeString()}
              </span>
            )}
          </div>

          {/* Error message - full detail preserved */}
          {event.error && (
            <div className="mt-0.5">
              <div className="text-red-700 dark:text-red-300 leading-tight bg-red-100/50 dark:bg-red-800/30 border border-red-200 dark:border-red-700 rounded p-1.5">
                {event.error}
              </div>
            </div>
          )}
          <FreeModelHint error={event.error} />

          {/* Context - full detail preserved */}
          {event.context && (
            <div className="text-yellow-700 dark:text-yellow-300 mt-0.5 opacity-90 leading-tight bg-yellow-100/50 dark:bg-yellow-800/30 border border-yellow-200 dark:border-yellow-700 rounded p-1.5">
              {event.context}
            </div>
          )}

          {/* Compact metadata */}
          <div className="flex items-center gap-2 mt-0.5 text-yellow-600 dark:text-yellow-400 opacity-75">
            {event.turn && <span>Turn: {event.turn}</span>}
            {event.duration && <span>• {formatDuration(event.duration)}</span>}
          </div>

          {/* IDs inline */}
          {(event.trace_id || event.correlation_id) && (
            <div className="text-yellow-600 dark:text-yellow-400 mt-0.5 opacity-75">
              {event.trace_id && <code className="bg-yellow-200/50 dark:bg-yellow-700/30 px-1 rounded mr-2">{event.trace_id}</code>}
              {event.correlation_id && <code className="bg-yellow-200/50 dark:bg-yellow-700/30 px-1 rounded">{event.correlation_id}</code>}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

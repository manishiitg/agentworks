import React, { useState } from 'react'
import type { LLMGenerationWithRetryEvent } from '../../../generated/events'

interface LLMGenerationWithRetryEventDisplayProps {
  event: LLMGenerationWithRetryEvent
  mode?: 'compact' | 'detailed'
}

export const LLMGenerationWithRetryEventDisplay: React.FC<LLMGenerationWithRetryEventDisplayProps> = ({
  event,
  mode = 'compact'
}) => {
  const [isUsageExpanded, setIsUsageExpanded] = useState(false)

  if (mode === 'compact') {
    return (
      <div className="bg-blue-50 dark:bg-blue-900/20 dark-plus:bg-blue-900/20 border border-blue-200 dark:border-blue-800 dark-plus:border-blue-800 rounded p-1.5">
        <div className="flex items-start gap-2">
          <div className="flex-1 min-w-0 text-xs">
            <div className="flex items-center gap-1.5">
              <span className="font-medium text-blue-800 dark:text-blue-200 dark-plus:text-blue-200">LLM Generation</span>
              {event.current_llm && (
                <span className="text-blue-700 dark:text-blue-300 dark-plus:text-blue-300 text-xs">
                  {event.current_llm}
                </span>
              )}
              {event.timestamp && (
                <span className="text-blue-600 dark:text-blue-400 dark-plus:text-blue-400 ml-auto opacity-75">
                  {new Date(event.timestamp).toLocaleTimeString()}
                </span>
              )}
            </div>

            {/* Compact model info */}
            <div className="flex items-center gap-2 mt-0.5 text-blue-600 dark:text-blue-400 dark-plus:text-blue-400 opacity-75">
              {event.primary_model && event.primary_model !== event.current_llm && (
                <span>primary: {event.primary_model}</span>
              )}
              {event.provider && <span>• {event.provider}</span>}
              {event.max_retries && <span>• max {event.max_retries} retries</span>}
            </div>

            {/* Final error - compact */}
            {event.final_error && (
              <div className="text-red-700 dark:text-red-300 dark-plus:text-red-300 mt-0.5 leading-tight bg-red-100/50 dark:bg-red-800/30 dark-plus:bg-red-800/30 border border-red-200 dark:border-red-700 dark-plus:border-red-700 rounded p-1.5">
                {event.final_error.length > 60 ? `${event.final_error.substring(0, 60)}...` : event.final_error}
              </div>
            )}

            {/* Usage with expand functionality */}
            {event.usage && (
              <div className="mt-0.5">
                <div className="flex items-center gap-1.5 mb-0.5">
                  <span className="text-blue-600 dark:text-blue-400 dark-plus:text-blue-400 opacity-75">Usage:</span>
                  <button
                    onClick={() => setIsUsageExpanded(!isUsageExpanded)}
                    className="text-blue-500 dark:text-blue-400 dark-plus:text-blue-400 hover:opacity-80 text-xs"
                  >
                    {isUsageExpanded ? '↑' : '↓'}
                  </button>
                </div>
                <div className="text-blue-600 dark:text-blue-400 dark-plus:text-blue-400 opacity-75">
                  <span className="text-xs font-mono">
                    {typeof event.usage === 'string' ? event.usage : 'usage data available'}
                  </span>
                </div>
                {isUsageExpanded && (
                  <div className="mt-1 bg-card border border-border rounded p-1.5 max-h-32 overflow-y-auto">
                    <div className="text-xs font-mono text-foreground">
                      {typeof event.usage === 'string' ? event.usage : JSON.stringify(event.usage, null, 2)}
                    </div>
                  </div>
                )}
              </div>
            )}
          </div>
        </div>
      </div>
    )
  }

  return (
    <div className="bg-blue-50 dark:bg-blue-900/20 dark-plus:bg-blue-900/20 border border-blue-200 dark:border-blue-800 dark-plus:border-blue-800 rounded p-2">
      <div className="flex items-start gap-2">
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2 mb-2">
            <span className="text-sm font-semibold text-blue-800 dark:text-blue-200 dark-plus:text-blue-200">LLM Generation with Retry</span>
            {event.timestamp && (
              <span className="text-xs text-blue-600 dark:text-blue-400 dark-plus:text-blue-400 ml-auto opacity-75">
                {new Date(event.timestamp).toLocaleString()}
              </span>
            )}
          </div>

          {/* Operation */}
          {event.operation && (
            <div className="mb-2 text-xs text-blue-700 dark:text-blue-300 dark-plus:text-blue-300">
              <span className="font-medium">Operation:</span> {event.operation}
            </div>
          )}

          {/* Models - inline */}
          {(event.primary_model || event.current_llm) && (
            <div className="mb-2">
              <div className="grid grid-cols-2 gap-2">
                {event.primary_model && (
                  <div className="bg-card dark:bg-blue-900/20 dark-plus:bg-blue-900/20 rounded p-2">
                    <span className="text-xs font-medium text-blue-600 dark:text-blue-400 dark-plus:text-blue-400 block">Primary Model</span>
                    <span className="text-sm font-semibold text-blue-700 dark:text-blue-300 dark-plus:text-blue-300">{event.primary_model}</span>
                  </div>
                )}
                {event.current_llm && (
                  <div className="bg-card dark:bg-blue-900/20 dark-plus:bg-blue-900/20 rounded p-2">
                    <span className="text-xs font-medium text-blue-600 dark:text-blue-400 dark-plus:text-blue-400 block">Current LLM</span>
                    <span className="text-sm font-semibold text-blue-700 dark:text-blue-300 dark-plus:text-blue-300">{event.current_llm}</span>
                  </div>
                )}
              </div>
            </div>
          )}

          {/* Compact metrics */}
          <div className="flex items-center gap-3 mb-2 text-xs text-blue-600 dark:text-blue-400 dark-plus:text-blue-400">
            {event.provider && <span>{event.provider}</span>}
            {event.max_retries && <span>• max {event.max_retries} retries</span>}
            {event.turn !== undefined && <span>• turn {event.turn}</span>}
          </div>

          {/* Final error */}
          {event.final_error && (
            <div className="mb-2">
              <div className="text-xs font-medium text-red-700 dark:text-red-300 dark-plus:text-red-300 mb-1">Final Error:</div>
              <div className="text-xs text-red-700 dark:text-red-300 dark-plus:text-red-300 leading-tight bg-red-100/50 dark:bg-red-800/30 dark-plus:bg-red-800/30 border border-red-200 dark:border-red-700 dark-plus:border-red-700 rounded p-2">
                {event.final_error}
              </div>
            </div>
          )}

          {/* Usage */}
          {event.usage && (
            <div>
              <div className="text-xs font-medium text-blue-700 dark:text-blue-300 dark-plus:text-blue-300 mb-1">Usage:</div>
              <div className="text-xs font-mono text-blue-700 dark:text-blue-300 dark-plus:text-blue-300 bg-blue-100/50 dark:bg-blue-800/30 dark-plus:bg-blue-800/30 border border-blue-200 dark:border-blue-700 dark-plus:border-blue-700 rounded p-2 max-h-32 overflow-y-auto">
                {typeof event.usage === 'string' ? event.usage : JSON.stringify(event.usage, null, 2)}
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

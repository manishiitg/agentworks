import React from 'react'
import { CheckCircle, AlertCircle } from 'lucide-react'
import type { LLMGenerationEndEvent } from '../../../generated/events'
import { ConversationMarkdownRenderer } from '../../ui/MarkdownRenderer'
import { formatDuration } from '../../../utils/duration'

interface LLMGenerationEndEventProps {
  event: LLMGenerationEndEvent
}

export const LLMGenerationEndEventDisplay: React.FC<LLMGenerationEndEventProps> = ({ event }) => {
  
  const isSuccess = true // LLM generation end is typically successful
  
  // Type assertion for extended UsageMetrics properties
  const extendedUsageMetrics = event.usage_metrics ? (event.usage_metrics as typeof event.usage_metrics & {
    cache_tokens?: number
    reasoning_tokens?: number
  }) : null

  // Extract context usage from metadata

  // Helper function to format token count (e.g., 1000000 -> "1M", 200000 -> "200k")
  // A successful generation is the common case and gets the neutral card the rest
  // of the UI uses; colour is reserved for failure, so a red card actually stands
  // out in a long transcript instead of competing with a wall of green.
  const bgColor = isSuccess
    ? 'bg-gray-50 dark:bg-gray-900/20 border-gray-200 dark:border-gray-800'
    : 'bg-red-50 dark:bg-red-900/20 border-red-200 dark:border-red-800'

  const textColor = isSuccess
    ? 'text-gray-700 dark:text-gray-300'
    : 'text-red-700 dark:text-red-300'

  const iconColor = isSuccess
    ? 'text-gray-500 dark:text-gray-400'
    : 'text-red-600'

  const Icon = isSuccess ? CheckCircle : AlertCircle

  return (
    <div className={`${bgColor} border rounded p-2`}>
      <div className={`text-xs ${textColor} space-y-1`}>
        {/* Header with key info */}
        <div className="flex items-center justify-between gap-3">
          {/* Left side: Icon and main content */}
          <div className="flex items-center gap-2 min-w-0 flex-1">
            <Icon className={`w-4 h-4 ${iconColor} flex-shrink-0`} />
            <div className="min-w-0 flex-1">
              <div className="text-sm font-medium text-gray-700 dark:text-gray-200">
                LLM Generation End{' '}
                <span className="text-xs font-normal text-gray-500 dark:text-gray-400">
                  {event.turn != null && `• Turn ${event.turn}`}
                  {event.duration != null && ` • ${formatDuration(event.duration)}`}
                  {event.tool_calls !== undefined && ` • ${event.tool_calls} tool calls`}
                  {event.usage_metrics && (
                    <>
                      {' • Tokens: '}
                      {event.usage_metrics.prompt_tokens && (
                        <>Input: {event.usage_metrics.prompt_tokens.toLocaleString()}</>
                      )}
                      {event.usage_metrics.completion_tokens && (
                        <> • Output: {event.usage_metrics.completion_tokens.toLocaleString()}</>
                      )}
                      {event.usage_metrics.total_tokens && (
                        <> • Total: {event.usage_metrics.total_tokens.toLocaleString()}</>
                      )}
                      {extendedUsageMetrics?.cache_tokens && extendedUsageMetrics.cache_tokens > 0 && (
                        <span className="text-cyan-600 dark:text-cyan-400">
                          {' • Cache: '}{extendedUsageMetrics.cache_tokens.toLocaleString()}
                        </span>
                      )}
                      {extendedUsageMetrics?.reasoning_tokens && extendedUsageMetrics.reasoning_tokens > 0 && (
                        <span className="text-purple-600 dark:text-purple-400">
                          {' • Reasoning: '}{extendedUsageMetrics.reasoning_tokens.toLocaleString()}
                        </span>
                      )}
                    </>
                  )}
                  {/* Context / Fixed-threshold percentages removed: the number
                      is no longer used for anything, and it was reporting >100%
                      because cache reads are summed across every internal LLM
                      call in a turn rather than measuring one context. */}
                </span>
              </div>
            </div>
          </div>
          {/* Right side: Time */}
          {event.timestamp && (
            <div className="text-xs text-gray-500 dark:text-gray-400 flex-shrink-0">
              {new Date(event.timestamp).toLocaleTimeString()}
            </div>
          )}
        </div>
        
        {/* Content with markdown rendering - show even if empty */}
        <div className="space-y-2 mt-2">
          <div className="text-xs font-medium text-gray-700 dark:text-gray-300">Content:</div>
          <div className="bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-md p-2">
            {event.content ? (
              <ConversationMarkdownRenderer content={event.content} maxHeight="none" framed={false} />
            ) : (
              <div className="text-xs text-gray-500 dark:text-gray-400 italic">
                {event.tool_calls && event.tool_calls > 0 
                  ? `No text content (${event.tool_calls} tool call${event.tool_calls > 1 ? 's' : ''} made)` 
                  : 'No content generated'}
              </div>
            )}
          </div>
        </div>
        
      </div>
    </div>
  )
}

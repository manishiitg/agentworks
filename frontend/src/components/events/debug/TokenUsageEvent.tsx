import React from 'react'
import type { TokenUsageEvent } from '../../../generated/events'
import { formatDuration } from '../../../utils/duration'
import { formatTokens } from '../../../utils/formatTokens'

interface TokenUsageEventDisplayProps {
  event: TokenUsageEvent
}

export const TokenUsageEventDisplay: React.FC<TokenUsageEventDisplayProps> = ({ event }) => {
  const isTotalEvent = event.context === 'conversation_total'
  
  // Extract cumulative metrics from generation_info for total events
  const cumulativePromptTokens = isTotalEvent 
    ? (event.generation_info?.cumulative_prompt_tokens as number ?? event.prompt_tokens ?? 0)
    : (event.generation_info?.PromptTokens as number ?? event.prompt_tokens ?? 0)
  
  const cumulativeCompletionTokens = isTotalEvent
    ? (event.generation_info?.cumulative_completion_tokens as number ?? event.completion_tokens ?? 0)
    : (event.generation_info?.CompletionTokens as number ?? event.completion_tokens ?? 0)
  
  const cumulativeCacheTokens = isTotalEvent
    ? (event.generation_info?.cumulative_cache_tokens as number ?? 0)
    : 0
  
  const cumulativeReasoningTokens = isTotalEvent
    ? (event.generation_info?.cumulative_reasoning_tokens as number ?? event.reasoning_tokens ?? 0)
    : (event.generation_info?.ReasoningTokens as number ?? event.reasoning_tokens ?? 0)
  
  const llmCallCount = isTotalEvent
    ? (event.generation_info?.llm_call_count as number ?? 0)
    : 0
  
  const cacheEnabledCallCount = isTotalEvent
    ? (event.generation_info?.cache_enabled_call_count as number ?? 0)
    : 0

  // Extract pricing information
  const inputCost = isTotalEvent
    ? (event.generation_info?.cumulative_input_cost as number ?? event.input_cost_usd ?? 0)
    : (event.input_cost_usd ?? 0)
  const outputCost = isTotalEvent
    ? (event.generation_info?.cumulative_output_cost as number ?? event.output_cost_usd ?? 0)
    : (event.output_cost_usd ?? 0)
  const reasoningCost = isTotalEvent
    ? (event.generation_info?.cumulative_reasoning_cost as number ?? event.reasoning_cost_usd ?? 0)
    : (event.reasoning_cost_usd ?? 0)
  const cacheCost = isTotalEvent
    ? (event.generation_info?.cumulative_cache_cost as number ?? event.cache_cost_usd ?? 0)
    : (event.cache_cost_usd ?? 0)
  const totalCost = isTotalEvent
    ? (event.generation_info?.cumulative_total_cost as number ?? event.total_cost_usd ?? 0)
    : (event.total_cost_usd ?? 0)

  // Extract context window usage information
  const contextWindowUsage = isTotalEvent
    ? (event.generation_info?.current_context_window_usage as number ?? event.context_window_usage ?? 0)
    : (event.context_window_usage ?? 0)
  const modelContextWindow = isTotalEvent
    ? (event.generation_info?.model_context_window as number ?? event.model_context_window ?? 0)
    : (event.model_context_window ?? 0)
  const contextUsagePercent = isTotalEvent
    ? (event.generation_info?.context_usage_percent as number ?? event.context_usage_percent ?? 0)
    : (event.context_usage_percent ?? 0)


  // Theme colors - use purple for total events, blue for per-call events
  const bgColor = isTotalEvent 
    ? 'bg-purple-50 dark:bg-purple-900/20'
    : 'bg-blue-50 dark:bg-blue-900/20'
  
  const borderColor = isTotalEvent
    ? 'border-purple-200 dark:border-purple-800'
    : 'border-blue-200 dark:border-blue-800'
  
  const textColor = isTotalEvent
    ? 'text-purple-700 dark:text-purple-300'
    : 'text-blue-700 dark:text-blue-300'
  
  const textSecondaryColor = isTotalEvent
    ? 'text-purple-600 dark:text-purple-400'
    : 'text-blue-600 dark:text-blue-400'

  return (
    <div className={`${bgColor} border ${borderColor} rounded-md p-2`}>
      {/* Compact header with title and metadata */}
      <div className="flex items-center justify-between gap-2 mb-1.5">
        <div className="flex items-center gap-1.5 flex-wrap text-xs">
          <span className={`font-semibold ${textColor}`}>Usage</span>
        </div>
        {event.timestamp && (
          <span className="text-xs text-gray-500 dark:text-gray-400 flex-shrink-0">
            {new Date(event.timestamp).toLocaleTimeString()}
          </span>
        )}
      </div>
      
      {/* Combined metrics: All tokens with costs inline, context, stats - all in one compact line */}
      <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs">
        {/* Token breakdown with costs inline */}
        <span className={textColor}>
          <span className="font-semibold">Tokens:</span>
          <span className="ml-1">
            In:{cumulativePromptTokens.toLocaleString()}
            {inputCost > 0 && (
              <span className="text-green-600 dark:text-green-400 ml-1">(${inputCost.toFixed(4)})</span>
            )}
          </span>
          {cumulativeCacheTokens > 0 && (
            <span className="text-sky-600 dark:text-sky-400 ml-1">
              Cache:{cumulativeCacheTokens.toLocaleString()}
              {cacheCost > 0 && (
                <span className="text-green-600 dark:text-green-400 ml-1">(${cacheCost.toFixed(4)})</span>
              )}
            </span>
          )}
          {cumulativeReasoningTokens > 0 && (
            <span className="text-purple-600 dark:text-purple-400 ml-1">
              Reason:{cumulativeReasoningTokens.toLocaleString()}
              {reasoningCost > 0 && (
                <span className="text-green-600 dark:text-green-400 ml-1">(${reasoningCost.toFixed(4)})</span>
              )}
            </span>
          )}
          <span className="ml-1">
            Out:{cumulativeCompletionTokens.toLocaleString()}
            {outputCost > 0 && (
              <span className="text-green-600 dark:text-green-400 ml-1">(${outputCost.toFixed(4)})</span>
            )}
          </span>
          {totalCost > 0 && (
            <span className="ml-1 font-semibold text-green-600 dark:text-green-400">
              Total:${totalCost.toFixed(4)}
            </span>
          )}
        </span>
        
        {/* Context window */}
        {modelContextWindow > 0 && (
          <span className={textSecondaryColor}>
            <span className="font-semibold">Context:</span>
            <span className="ml-1">{contextWindowUsage.toLocaleString()}/{modelContextWindow.toLocaleString()}</span>
            {contextUsagePercent > 0 && (
              <>
                {contextUsagePercent > 0 && (
                  <span className={contextUsagePercent > 80 ? 'text-red-600 dark:text-red-400' : contextUsagePercent > 50 ? 'text-yellow-600 dark:text-yellow-400' : 'ml-1'}>
                    {' '}({contextUsagePercent.toFixed(1)}% ({formatTokens(modelContextWindow)}))
                  </span>
                )}
              </>
            )}
          </span>
        )}
        
        {/* Stats for total events */}
        {isTotalEvent && llmCallCount > 0 && (
          <span className={textSecondaryColor}>
            <span className="font-semibold">Calls:</span>
            <span className="ml-1">{llmCallCount}</span>
            {cacheEnabledCallCount > 0 && (
              <span className="text-sky-600 dark:text-sky-400 ml-1">({cacheEnabledCallCount} cached)</span>
            )}
          </span>
        )}
        
        {/* Duration */}
        {event.duration && (
          <span className={textSecondaryColor}>
            <span className="font-semibold">Time:</span>
            <span className="ml-1">{formatDuration(event.duration)}</span>
          </span>
        )}
      </div>
    </div>
  )
}

import React from 'react'
import { Tooltip, TooltipContent, TooltipTrigger } from './tooltip'
import type { TokenUsageEvent } from '../../generated/events'
import { formatDuration } from '../../utils/duration'
import { formatTokens } from '../../utils/formatTokens'

// Type for context-only token usage (from ToolCallEndEvent)
export interface ContextOnlyTokenUsage {
  context_usage_percent: number
  model_context_window?: number
  context_window_usage?: number
  model_id?: string
}

interface CircularProgressProps {
  percentage: number
  size?: number
  strokeWidth?: number
  className?: string
  tokenUsage?: TokenUsageEvent | ContextOnlyTokenUsage
}

export const CircularProgress: React.FC<CircularProgressProps> = ({
  percentage,
  size = 20,
  strokeWidth = 4,
  className = '',
  tokenUsage
}) => {
  const [pinned, setPinned] = React.useState(false)
  const radius = (size - strokeWidth) / 2
  const circumference = 2 * Math.PI * radius
  const offset = circumference - (percentage / 100) * circumference



  // Extract token usage details for tooltip
  const getTooltipContent = () => {
    // Always show percentage in tooltip
    const percentageText = `${percentage.toFixed(1)}%`
    
    if (!tokenUsage) {
      return <p>Context: {percentageText}</p>
    }

    // Check if we only have context info (from ToolCallEndEvent) vs full token breakdown (from TokenUsageEvent)
    const hasFullTokenData = 'prompt_tokens' in tokenUsage || 'generation_info' in tokenUsage
    const hasOnlyContextInfo = 'context_usage_percent' in tokenUsage && !hasFullTokenData

    // If we only have context info, show simplified tooltip
    if (hasOnlyContextInfo) {
      const contextOnlyUsage = tokenUsage as ContextOnlyTokenUsage
      const modelContextWindow = contextOnlyUsage.model_context_window
      const contextWindowUsage = contextOnlyUsage.context_window_usage
      const modelId = contextOnlyUsage.model_id

      return (
        <div className="space-y-1.5 text-xs min-w-[280px]">
          <div className="font-semibold text-sm mb-1.5 border-b pb-1">
            Context Usage: {percentageText}
          </div>

          {/* Model info */}
          {modelId && (
            <div className="flex items-center gap-2">
              <span className="text-gray-500 dark:text-gray-400">Model:</span>
              <span className="font-medium">{modelId}</span>
            </div>
          )}

          {modelContextWindow && modelContextWindow > 0 && (
            <div className="space-y-0.5">
              <div className="flex items-center justify-between gap-4">
                <span className="text-gray-500 dark:text-gray-400">Context:</span>
                <span className="font-medium">
                  {contextWindowUsage ? contextWindowUsage.toLocaleString() : 'N/A'}/{modelContextWindow.toLocaleString()}
                  <span className={`ml-1 ${getPercentageTextColor()}`}>
                    ({percentage.toFixed(1)}%)
                  </span>
                </span>
              </div>
              <div className="text-gray-500 dark:text-gray-400 text-[10px]">
                Max: {formatTokens(modelContextWindow)}
              </div>
            </div>
          )}
        </div>
      )
    }

    const isTotalEvent = tokenUsage.context === 'conversation_total'
    
    // Extract cumulative metrics from generation_info for total events
    const promptTokens = isTotalEvent 
      ? (tokenUsage.generation_info?.cumulative_prompt_tokens as number ?? tokenUsage.prompt_tokens ?? 0)
      : (tokenUsage.generation_info?.PromptTokens as number ?? tokenUsage.prompt_tokens ?? 0)
    
    const completionTokens = isTotalEvent
      ? (tokenUsage.generation_info?.cumulative_completion_tokens as number ?? tokenUsage.completion_tokens ?? 0)
      : (tokenUsage.generation_info?.CompletionTokens as number ?? tokenUsage.completion_tokens ?? 0)
    
    const cacheTokens = isTotalEvent
      ? (tokenUsage.generation_info?.cumulative_cache_tokens as number ?? 0)
      : (tokenUsage.cache_discount ? Math.round((tokenUsage.prompt_tokens ?? 0) * (tokenUsage.cache_discount / 100)) : 0)
    
    const reasoningTokens = isTotalEvent
      ? (tokenUsage.generation_info?.cumulative_reasoning_tokens as number ?? tokenUsage.reasoning_tokens ?? 0)
      : (tokenUsage.reasoning_tokens ?? 0)
    
    // Extract pricing information
    const inputCost = isTotalEvent
      ? (tokenUsage.generation_info?.cumulative_input_cost as number ?? tokenUsage.input_cost_usd ?? 0)
      : (tokenUsage.input_cost_usd ?? 0)
    const outputCost = isTotalEvent
      ? (tokenUsage.generation_info?.cumulative_output_cost as number ?? tokenUsage.output_cost_usd ?? 0)
      : (tokenUsage.output_cost_usd ?? 0)
    const reasoningCost = isTotalEvent
      ? (tokenUsage.generation_info?.cumulative_reasoning_cost as number ?? tokenUsage.reasoning_cost_usd ?? 0)
      : (tokenUsage.reasoning_cost_usd ?? 0)
    const cacheCost = isTotalEvent
      ? (tokenUsage.generation_info?.cumulative_cache_cost as number ?? tokenUsage.cache_cost_usd ?? 0)
      : (tokenUsage.cache_cost_usd ?? 0)
    const totalCost = isTotalEvent
      ? (tokenUsage.generation_info?.cumulative_total_cost as number ?? tokenUsage.total_cost_usd ?? 0)
      : (tokenUsage.total_cost_usd ?? 0)
    
    // Extract context window usage information
    const contextWindowUsage = isTotalEvent
      ? (tokenUsage.generation_info?.current_context_window_usage as number ?? tokenUsage.context_window_usage ?? 0)
      : (tokenUsage.context_window_usage ?? 0)
    const modelContextWindow = isTotalEvent
      ? (tokenUsage.generation_info?.model_context_window as number ?? tokenUsage.model_context_window ?? 0)
      : (tokenUsage.model_context_window ?? 0)
    
    const llmCallCount = isTotalEvent
      ? (tokenUsage.generation_info?.llm_call_count as number ?? 0)
      : 0
    
    const cacheEnabledCallCount = isTotalEvent
      ? (tokenUsage.generation_info?.cache_enabled_call_count as number ?? 0)
      : 0

    return (
      <div className="space-y-1.5 text-xs min-w-[280px]">
        <div className="font-semibold text-sm mb-1.5 border-b pb-1">
          Usage • Context: {percentageText}
        </div>
        
        {/* Model info */}
        {(tokenUsage.model_id || tokenUsage.provider) && (
          <div className="flex items-center gap-2">
            <span className="text-gray-500 dark:text-gray-400">Model:</span>
            <span className="font-medium">
              {tokenUsage.model_id || 'N/A'}
              {tokenUsage.provider && ` (${tokenUsage.provider})`}
            </span>
          </div>
        )}
        
        {/* Tokens */}
        <div className="space-y-0.5">
          <div className="flex items-center justify-between gap-4">
            <span className="text-gray-500 dark:text-gray-400">Input:</span>
            <span className="font-medium">
              {formatTokens(promptTokens)}
              {inputCost > 0 && (
                <span className="text-green-600 dark:text-green-400 ml-1">(${inputCost.toFixed(4)})</span>
              )}
            </span>
          </div>
          {cacheTokens > 0 && (
            <div className="flex items-center justify-between gap-4">
              <span className="text-gray-500 dark:text-gray-400">Cache:</span>
              <span className="font-medium text-sky-600 dark:text-sky-400">
                {formatTokens(cacheTokens)}
                {cacheCost > 0 && (
                  <span className="text-green-600 dark:text-green-400 ml-1">(${cacheCost.toFixed(4)})</span>
                )}
              </span>
            </div>
          )}
          {reasoningTokens > 0 && (
            <div className="flex items-center justify-between gap-4">
              <span className="text-gray-500 dark:text-gray-400">Reasoning:</span>
              <span className="font-medium text-purple-600 dark:text-purple-400">
                {formatTokens(reasoningTokens)}
                {reasoningCost > 0 && (
                  <span className="text-green-600 dark:text-green-400 ml-1">(${reasoningCost.toFixed(4)})</span>
                )}
              </span>
            </div>
          )}
          <div className="flex items-center justify-between gap-4">
            <span className="text-gray-500 dark:text-gray-400">Output:</span>
            <span className="font-medium">
              {formatTokens(completionTokens)}
              {outputCost > 0 && (
                <span className="text-green-600 dark:text-green-400 ml-1">(${outputCost.toFixed(4)})</span>
              )}
            </span>
          </div>
          {totalCost > 0 && (
            <div className="flex items-center justify-between gap-4 pt-0.5 border-t">
              <span className="text-gray-500 dark:text-gray-400 font-semibold">Total:</span>
              <span className="font-semibold text-green-600 dark:text-green-400">
                ${totalCost.toFixed(4)}
              </span>
            </div>
          )}
        </div>
        
        {/* Context window */}
        {modelContextWindow > 0 && (
          <div className="space-y-0.5 pt-0.5 border-t">
            <div className="flex items-center justify-between gap-4">
              <span className="text-gray-500 dark:text-gray-400">Context:</span>
              <span className="font-medium">
                {contextWindowUsage.toLocaleString()}/{modelContextWindow.toLocaleString()}
                <span className={`ml-1 ${getPercentageTextColor()}`}>
                  ({percentage.toFixed(1)}%)
                </span>
              </span>
            </div>
            {modelContextWindow > 0 && (
              <div className="text-gray-500 dark:text-gray-400 text-[10px]">
                Max: {formatTokens(modelContextWindow)}
              </div>
            )}
          </div>
        )}
        
        {/* Stats for total events */}
        {isTotalEvent && llmCallCount > 0 && (
          <div className="flex items-center justify-between gap-4 pt-0.5 border-t">
            <span className="text-gray-500 dark:text-gray-400">Calls:</span>
            <span className="font-medium">
              {llmCallCount}
              {cacheEnabledCallCount > 0 && (
                <span className="text-sky-600 dark:text-sky-400 ml-1">({cacheEnabledCallCount} cached)</span>
              )}
            </span>
          </div>
        )}
        
        {/* Duration */}
        {tokenUsage.duration && (
          <div className="flex items-center justify-between gap-4 pt-0.5 border-t">
            <span className="text-gray-500 dark:text-gray-400">Time:</span>
            <span className="font-medium">{formatDuration(tokenUsage.duration)}</span>
          </div>
        )}
      </div>
    )
  }

  // Get stroke color class for the progress ring based on percentage
  const getProgressRingColor = () => {
    if (percentage >= 90) return 'stroke-red-600 dark:stroke-red-500' // Critical: 90-100%
    if (percentage >= 80) return 'stroke-red-500 dark:stroke-red-400' // High: 80-90%
    if (percentage >= 70) return 'stroke-orange-500 dark:stroke-orange-400' // Warning: 70-80%
    if (percentage >= 50) return 'stroke-yellow-500 dark:stroke-yellow-400' // Caution: 50-70%
    if (percentage >= 30) return 'stroke-blue-500 dark:stroke-blue-400' // Moderate: 30-50%
    return 'stroke-emerald-500 dark:stroke-emerald-400' // Low: 0-30%
  }

  // Get text color class for percentage display in tooltip
  const getPercentageTextColor = () => {
    if (percentage >= 90) return 'text-red-600 dark:text-red-400' // Critical: 90-100%
    if (percentage >= 80) return 'text-red-600 dark:text-red-400' // High: 80-90%
    if (percentage >= 70) return 'text-orange-600 dark:text-orange-400' // Warning: 70-80%
    if (percentage >= 50) return 'text-yellow-600 dark:text-yellow-400' // Caution: 50-70%
    if (percentage >= 30) return 'text-blue-600 dark:text-blue-400' // Moderate: 30-50%
    return '' // Low: 0-30% (default color)
  }

  // Format percentage for display next to circle
  const displayPercent = percentage >= 10 ? `${Math.round(percentage)}%` : `${percentage.toFixed(1)}%`

  return (
    <Tooltip open={pinned || undefined}>
      <TooltipTrigger asChild>
        <div
          className={`relative inline-flex items-center gap-1 cursor-pointer select-none ${className}`}
          onClick={(e) => { e.preventDefault(); e.stopPropagation(); setPinned(p => !p) }}
        >
          <svg
            width={size}
            height={size}
            className="transform -rotate-90 drop-shadow-sm"
          >
            {/* Background circle - lighter and more subtle */}
            <circle
              cx={size / 2}
              cy={size / 2}
              r={radius}
              stroke="currentColor"
              strokeWidth={strokeWidth}
              fill="none"
              className="text-gray-300/50 dark:text-gray-600/50"
            />
            {/* Progress circle - animated and colored */}
            <circle
              cx={size / 2}
              cy={size / 2}
              r={radius}
              stroke="currentColor"
              strokeWidth={strokeWidth}
              fill="none"
              strokeDasharray={circumference}
              strokeDashoffset={offset}
              strokeLinecap="round"
              className={`transition-all duration-500 ease-out ${getProgressRingColor()}`}
              style={{
                filter: percentage >= 90 ? 'drop-shadow(0 0 3px rgb(220 38 38))' : // Critical: stronger red glow
                        percentage >= 80 ? 'drop-shadow(0 0 2px rgb(239 68 68))' : // High: red glow
                        percentage >= 70 ? 'drop-shadow(0 0 2px rgb(249 115 22))' : // Warning: orange glow
                        percentage >= 50 ? 'drop-shadow(0 0 2px rgb(234 179 8))' : // Caution: yellow glow
                        percentage >= 30 ? 'drop-shadow(0 0 2px rgb(59 130 246))' : // Moderate: blue glow
                        'drop-shadow(0 0 2px rgb(16 185 129))' // Low: emerald glow
              }}
            />
          </svg>
          <span className={`text-[10px] font-medium leading-none ${getPercentageTextColor() || 'text-gray-500 dark:text-gray-400'}`}>
            {displayPercent}
          </span>
        </div>
      </TooltipTrigger>
      <TooltipContent
        className="max-w-md min-w-[320px]"
        side="left"
        align="end"
        sideOffset={8}
        onPointerDownOutside={() => setPinned(false)}
      >
        {getTooltipContent()}
      </TooltipContent>
    </Tooltip>
  )
}

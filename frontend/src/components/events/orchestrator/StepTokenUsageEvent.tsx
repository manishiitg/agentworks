import React from 'react'
import type { StepTokenUsageEvent as GeneratedStepTokenUsageEvent } from '../../../generated/event-types'
import { formatTokens } from '../../../utils/formatTokens'

interface StepTokenUsageEventDisplayProps {
  event: GeneratedStepTokenUsageEvent
}

export const StepTokenUsageEventDisplay: React.FC<StepTokenUsageEventDisplayProps> = ({ event }) => {
  const stepLabel = event.step_title || `Step ${(event.step ?? 0) + 1}`
  
  // Extract context usage from metadata
  const contextUsagePercent = event.context_usage_percent as number | undefined
  const modelContextWindow = event.metadata?.model_context_window as number | undefined

  
  return (
    <div className="bg-orange-50 dark:bg-orange-900/20 border border-orange-200 dark:border-orange-800 rounded-md p-2">
      {/* Compact header */}
      <div className="flex items-center justify-between gap-2 mb-1.5">
        <div className="flex items-center gap-1.5 flex-wrap text-xs">
          <span className="font-semibold text-orange-700 dark:text-orange-300">
            📊 {stepLabel}
          </span>
          <span className="text-orange-600 dark:text-orange-400">
            • {event.phase || 'unknown'} • Step {(event.step ?? 0) + 1}
          </span>
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
        <span className="text-orange-700 dark:text-orange-300">
          <span className="font-semibold">Tokens:</span>
          <span className="ml-1">
            In:{(event.prompt_tokens ?? 0).toLocaleString()}
            {(event.input_cost_usd ?? 0) > 0 && (
              <span className="text-green-600 dark:text-green-400 ml-1">(${(event.input_cost_usd ?? 0).toFixed(4)})</span>
            )}
          </span>
          {(event.cache_tokens ?? 0) > 0 && (
            <span className="text-cyan-600 dark:text-cyan-400 ml-1">
              Cache:{(event.cache_tokens ?? 0).toLocaleString()}
              {(event.cache_cost_usd ?? 0) > 0 && (
                <span className="text-green-600 dark:text-green-400 ml-1">(${(event.cache_cost_usd ?? 0).toFixed(4)})</span>
              )}
            </span>
          )}
          {(event.reasoning_tokens ?? 0) > 0 && (
            <span className="text-purple-600 dark:text-purple-400 ml-1">
              Reason:{(event.reasoning_tokens ?? 0).toLocaleString()}
              {(event.reasoning_cost_usd ?? 0) > 0 && (
                <span className="text-green-600 dark:text-green-400 ml-1">(${(event.reasoning_cost_usd ?? 0).toFixed(4)})</span>
              )}
            </span>
          )}
          <span className="ml-1">
            Out:{(event.completion_tokens ?? 0).toLocaleString()}
            {(event.output_cost_usd ?? 0) > 0 && (
              <span className="text-green-600 dark:text-green-400 ml-1">(${(event.output_cost_usd ?? 0).toFixed(4)})</span>
            )}
          </span>
          {(event.total_cost_usd ?? 0) > 0 && (
            <span className="ml-1 font-semibold text-green-600 dark:text-green-400">
              Total:${(event.total_cost_usd ?? 0).toFixed(4)}
            </span>
          )}
        </span>
        
        {/* Context usage */}
        {(contextUsagePercent ?? 0) > 0 && (
          <>
            {contextUsagePercent !== undefined && contextUsagePercent > 0 && (
              <span className={contextUsagePercent > 80 ? 'text-red-600 dark:text-red-400' : contextUsagePercent > 50 ? 'text-yellow-600 dark:text-yellow-400' : 'text-orange-600 dark:text-orange-400'}>
                <span className="font-semibold">Context:</span>
                <span className="ml-1">{contextUsagePercent.toFixed(1)}%</span>
                {modelContextWindow !== undefined && modelContextWindow > 0 && (
                  <span className="text-gray-600 dark:text-gray-400">
                    {' ('}{formatTokens(modelContextWindow)}{')'}
                  </span>
                )}
              </span>
            )}
          </>
        )}
        
        {/* Stats */}
        {(event.llm_call_count ?? 0) > 0 && (
          <span className="text-orange-600 dark:text-orange-400">
            <span className="font-semibold">Calls:</span>
            <span className="ml-1">{event.llm_call_count}</span>
            {(event.cache_enabled_call_count ?? 0) > 0 && (
              <span className="text-cyan-600 dark:text-cyan-400 ml-1">({event.cache_enabled_call_count} cached)</span>
            )}
          </span>
        )}
      </div>
    </div>
  )
}

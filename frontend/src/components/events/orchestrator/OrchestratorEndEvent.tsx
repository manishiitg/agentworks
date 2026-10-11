import React from 'react'
import type { OrchestratorEndEvent } from '../../../generated/events'
import { formatDuration } from '../../../utils/duration'
import { ConversationMarkdownRenderer } from '../../ui/MarkdownRenderer'

interface OrchestratorEndEventDisplayProps {
  event: OrchestratorEndEvent
}

export const OrchestratorEndEventDisplay: React.FC<OrchestratorEndEventDisplayProps> = ({
  event
}) => {
  const isSuccess = event.status === 'success'
  const isError = event.status === 'error'

  const formatTimestamp = (timestamp?: string) => {
    if (!timestamp) return '';
    return new Date(timestamp).toLocaleTimeString();
  };

  const getStatusIcon = () => {
    if (isSuccess) return '✅';
    if (isError) return '❌';
    return '🏁';
  };

  const getLabel = () => {
    const t = event.orchestrator_type
    if (t === 'planner') return 'Planner Orchestrator'
    if (t === 'workflow') return 'Automation Orchestrator'
    return 'Agent'
  }

  return (
    <div className="p-2 bg-yellow-50 dark:bg-yellow-900/20 border border-yellow-200 dark:border-yellow-800 rounded">
      {/* Header with single-line layout */}
      <div className="flex items-center justify-between gap-3">
        {/* Left side: Icon and main content */}
        <div className="flex items-center gap-3 min-w-0 flex-1">
          <div className="min-w-0 flex-1">
            <div className="text-sm font-medium text-yellow-700 dark:text-yellow-300">
              {getStatusIcon()} {getLabel()} Completed{' '}
              <span className="text-xs font-normal text-yellow-600 dark:text-yellow-400">
                | Status: {event.status} | Duration: {formatDuration(event.duration || 0)}
                {event.execution_mode && ` | Mode: ${event.execution_mode === 'parallel_execution' ? 'Parallel' : 'Sequential'}`}
              </span>
            </div>
          </div>
        </div>
        
        {/* Right side: Time */}
        {event.timestamp && (
          <div className="text-xs text-yellow-600 dark:text-yellow-400 flex-shrink-0">
            {formatTimestamp(event.timestamp)}
          </div>
        )}
      </div>

      {/* Result content - always visible with markdown rendering */}
      {event.result && (
        <div className="mt-3">
          <div className="text-xs font-medium text-yellow-600 dark:text-yellow-400 mb-2">Result:</div>
          <ConversationMarkdownRenderer content={event.result} maxHeight="400px" />
        </div>
      )}
    </div>
  )
}

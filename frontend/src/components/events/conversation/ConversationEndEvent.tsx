import React, { useState } from 'react'
import { ChevronDown, ChevronRight, CheckCircle, AlertCircle, Clock } from 'lucide-react'
import type { ConversationEndEvent } from '../../../generated/events'
import { ConversationMarkdownRenderer } from '../../ui/MarkdownRenderer'
import { formatDuration } from '../../../utils/duration'

interface ConversationEndEventProps {
  event: ConversationEndEvent
  compact?: boolean
}

export const ConversationEndEventDisplay: React.FC<ConversationEndEventProps> = ({ event, compact = false }) => {
  const [isQuestionExpanded, setIsQuestionExpanded] = useState(false)
  
  const isSuccess = event.status === 'success' || event.status === 'completed'
  const isError = event.status === 'error' || event.status === 'failed'

  const bgColor = isSuccess
    ? 'bg-green-50 dark:bg-green-900/20 border-green-200 dark:border-green-800'
    : isError
    ? 'bg-red-50 dark:bg-red-900/20 border-red-200 dark:border-red-800'
    : 'bg-blue-50 dark:bg-blue-900/20 border-blue-200 dark:border-blue-800'

  const textColor = isSuccess
    ? 'text-green-700 dark:text-green-300'
    : isError
    ? 'text-red-700 dark:text-red-300'
    : 'text-blue-700 dark:text-blue-300'

  const iconColor = isSuccess
    ? 'text-green-600'
    : isError
    ? 'text-red-600'
    : 'text-blue-600'

  const Icon = isSuccess ? CheckCircle : isError ? AlertCircle : Clock

  if (compact) {
    return (
      <div className={`p-2 ${bgColor} border rounded-md`}>
        <div className={`text-xs ${textColor} flex items-center gap-2`}>
          <Icon className={`w-3 h-3 ${iconColor}`} />
          <span className="font-medium">Conversation End</span>
          {event.status && <span className={`${iconColor} dark:text-opacity-80`}>• {event.status}</span>}
          {event.duration && <span className={`${iconColor} dark:text-opacity-80`}>• {formatDuration(event.duration)}</span>}
          {event.turns && <span className={`${iconColor} dark:text-opacity-80`}>• {event.turns} turns</span>}
          {event.error && <span className="text-red-600 dark:text-red-400">• Error</span>}
        </div>
      </div>
    )
  }

  return (
    <div className={`${bgColor} border rounded-lg p-3`}>
      <div className={`text-xs ${textColor} space-y-1`}>
        {/* Header */}
        <div className="flex items-center gap-2">
          <Icon className={`w-4 h-4 ${iconColor}`} />
          <span className="font-medium">Conversation End</span>
        </div>
        
        {/* Question */}
        {event.question && (
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <strong>User Message:</strong>
              <button
                onClick={() => setIsQuestionExpanded(!isQuestionExpanded)}
                className="flex items-center gap-1 text-xs text-red-600 hover:text-red-800 dark:text-red-400 dark:hover:text-red-200"
              >
                {isQuestionExpanded ? <ChevronDown className="w-3 h-3" /> : <ChevronRight className="w-3 h-3" />}
                {isQuestionExpanded ? 'Collapse' : 'Expand'}
              </button>
            </div>
            
            {/* Brief question preview */}
            <div className="text-gray-600 dark:text-gray-400">
              {event.question.length > 80 ? `${event.question.substring(0, 80)}...` : event.question}
            </div>
            
            {/* Expanded question with markdown rendering */}
            {isQuestionExpanded && (
              <div className="bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-md p-2">
                <ConversationMarkdownRenderer content={event.question} framed={false} />
              </div>
            )}
          </div>
        )}
        
        {/* Result - Always show full result with markdown rendering */}
        {event.result && (
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <strong>Result:</strong>
            </div>
            
            {/* Full result with markdown rendering - always visible */}
            <div className="bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-md p-3">
              <ConversationMarkdownRenderer content={event.result} framed={false} />
            </div>
          </div>
        )}
        
        {/* Status and metrics */}
        {event.status && (
          <div><strong>Status:</strong> {event.status}</div>
        )}
        
        {event.duration && (
          <div><strong>Duration:</strong> {formatDuration(event.duration)}</div>
        )}
        
        {event.turns && (
          <div><strong>Turns:</strong> {event.turns}</div>
        )}
        
        {/* Error information */}
        {event.error && (
          <div className="text-red-700 dark:text-red-300">
            <strong>Error:</strong> {event.error}
          </div>
        )}
        
        {/* Optional metadata fields */}
        {event.timestamp && <div><strong>Timestamp:</strong> {new Date(event.timestamp).toLocaleString()}</div>}
        {event.trace_id && <div><strong>Trace ID:</strong> <code className="text-xs bg-gray-100 dark:bg-gray-800 px-1 rounded">{event.trace_id}</code></div>}
        {event.correlation_id && <div><strong>Correlation ID:</strong> <code className="text-xs bg-gray-100 dark:bg-gray-800 px-1 rounded">{event.correlation_id}</code></div>}
      </div>
    </div>
  )
}

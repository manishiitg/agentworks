import React from 'react'
import { MessageSquare } from 'lucide-react'
import type { ConversationStartEvent } from '../../../generated/events'
import { ConversationMarkdownRenderer } from '../../ui/MarkdownRenderer'

interface ConversationStartEventProps {
  event: ConversationStartEvent
  compact?: boolean
}

export const ConversationStartEventDisplay: React.FC<ConversationStartEventProps> = ({ event, compact = false }) => {
  if (compact) {
    return (
      <div className="p-2 bg-green-50 dark:bg-green-900/20 border border-green-200 dark:border-green-800 rounded-md">
        <div className="text-xs text-green-700 dark:text-green-300 flex items-center gap-2">
          <MessageSquare className="w-3 h-3 text-green-600" />
          <span className="font-medium">Conversation Start</span>
          {event.question && <span className="text-green-600 dark:text-green-400">• {event.question.substring(0, 40)}...</span>}
          {event.tools_count && <span className="text-green-600 dark:text-green-400">• {event.tools_count} tools</span>}
          {event.servers && <span className="text-green-600 dark:text-green-400">• {event.servers}</span>}
        </div>
      </div>
    )
  }

  return (
    <div className="p-3 bg-green-50 dark:bg-green-900/20 border border-green-200 dark:border-green-700 rounded-lg">
      <div className="text-xs text-green-700 dark:text-green-300 space-y-1">
        {/* Header */}
        <div className="flex items-center gap-2">
          <MessageSquare className="w-4 h-4 text-green-600" />
          <span className="font-medium">Conversation Start</span>
        </div>
        
        {/* User Message - Full display without expand/collapse */}
        {event.question && (
          <div className="space-y-2">
            <div className="flex items-center gap-2">
              <strong>User Message:</strong>
            </div>
            
            {/* Full user message with markdown rendering */}
            <div className="bg-card border border-border rounded-md p-2">
              <ConversationMarkdownRenderer content={event.question} framed={false} />
            </div>
          </div>
        )}
        
        {/* Tools and servers */}
        {event.tools_count && (
          <div><strong>Tools Count:</strong> {event.tools_count}</div>
        )}
        
        {event.servers && (
          <div><strong>Servers:</strong> {event.servers}</div>
        )}
        
        {/* Optional metadata fields */}
        {event.timestamp && <div><strong>Timestamp:</strong> {new Date(event.timestamp).toLocaleString()}</div>}
        {event.trace_id && <div><strong>Trace ID:</strong> <code className="text-xs bg-green-100 dark:bg-green-800 px-1 rounded">{event.trace_id}</code></div>}
        {event.correlation_id && <div><strong>Correlation ID:</strong> <code className="text-xs bg-green-100 dark:bg-green-800 px-1 rounded">{event.correlation_id}</code></div>}
      </div>
    </div>
  )
}

import { useState } from 'react'
import type { AgentEndEvent } from '@/generated/events'

interface AgentEndEventProps {
  event: AgentEndEvent
}

export function AgentEndEventComponent({ event }: AgentEndEventProps) {
  const [isExpanded, setIsExpanded] = useState(false)
  
  const formatTimestamp = (timestamp?: string) => {
    if (!timestamp) return 'Unknown time'
    return new Date(timestamp).toLocaleTimeString()
  }

  const isSuccess = event.success !== false // Default to true if not specified
  const hasExpandableContent = event.error || event.parent_id || event.trace_id


  return (
    <div className={`border border-${isSuccess ? 'green' : 'red'}-200 dark:border-${isSuccess ? 'green' : 'red'}-800 rounded p-2 bg-${isSuccess ? 'green' : 'red'}-50 dark:bg-${isSuccess ? 'green' : 'red'}-900/20`}>
      <div className="flex items-center justify-between gap-3">
        {/* Left side: Icon and main content */}
        <div className="flex items-center gap-3 min-w-0 flex-1">
          <div className="min-w-0 flex-1">
            <div className={`text-sm font-medium ${isSuccess ? 'text-green-700 dark:text-green-300' : 'text-red-700 dark:text-red-300'}`}>
              {isSuccess ? '✅' : '❌'} Agent {isSuccess ? 'Completed' : 'Failed'}{' '}
              <span className={`text-xs font-normal ${isSuccess ? 'text-green-600 dark:text-green-400' : 'text-red-600 dark:text-red-400'}`}>
                | Type: {event.agent_type || 'Unknown'} | Status: {isSuccess ? 'Success' : 'Failed'}
              </span>
            </div>
          </div>
        </div>

        {/* Right side: Time and expand button */}
        <div className="flex items-center gap-2 flex-shrink-0">
          {event.timestamp && (
            <div className={`text-xs ${isSuccess ? 'text-green-600 dark:text-green-400' : 'text-red-600 dark:text-red-400'}`}>
              {formatTimestamp(event.timestamp)}
            </div>
          )}
          
          {hasExpandableContent && (
            <button 
              onClick={() => setIsExpanded(!isExpanded)}
              className={`${isSuccess ? 'text-green-600 dark:text-green-400 hover:text-green-800 dark:hover:text-green-200' : 'text-red-600 dark:text-red-400 hover:text-red-800 dark:hover:text-red-200'}`}
            >
              {isExpanded ? '▼' : '▶'}
            </button>
          )}
        </div>
      </div>

      {/* Expandable content */}
      {isExpanded && hasExpandableContent && (
        <div className="mt-3 space-y-2">
          {/* Error Information */}
          {event.error && (
            <div className="bg-red-50 dark:bg-red-950/20 border border-red-200 dark:border-red-800 rounded-md p-2">
              <div className="text-xs font-medium text-red-800 dark:text-red-200 mb-1">Error Details:</div>
              <div className="text-xs text-red-700 dark:text-red-300 font-mono">
                {event.error}
              </div>
            </div>
          )}

          {/* Hierarchy Information */}
          {(event.parent_id || event.hierarchy_level !== undefined) && (
            <div className="bg-card border border-border rounded-md p-2">
              <div className="text-xs font-medium text-foreground mb-1">Hierarchy:</div>
              <div className="text-xs text-muted-foreground space-y-1">
                {event.hierarchy_level !== undefined && (
                  <div>Level: <code className="bg-muted px-1 rounded">L{event.hierarchy_level}</code></div>
                )}
                {event.parent_id && (
                  <div>Parent ID: <code className="bg-muted px-1 rounded">{event.parent_id}</code></div>
                )}
              </div>
            </div>
          )}

          {/* Trace Information */}
          {(event.trace_id || event.span_id || event.event_id) && (
            <div className="bg-card border border-border rounded-md p-2">
              <div className="text-xs font-medium text-foreground mb-1">Trace Info:</div>
              <div className="text-xs text-muted-foreground space-y-1">
                {event.trace_id && (
                  <div>Trace: <code className="bg-muted px-1 rounded">{event.trace_id}</code></div>
                )}
                {event.span_id && (
                  <div>Span: <code className="bg-muted px-1 rounded">{event.span_id}</code></div>
                )}
                {event.event_id && (
                  <div>Event: <code className="bg-muted px-1 rounded">{event.event_id}</code></div>
                )}
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  )
}

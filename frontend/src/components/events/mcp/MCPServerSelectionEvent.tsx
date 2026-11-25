import React from 'react'
import { Server, Hash } from 'lucide-react'
import type { MCPServerSelectionEvent } from '../../../generated/events'

interface MCPServerSelectionEventProps {
  event: MCPServerSelectionEvent
  compact?: boolean
}

export const MCPServerSelectionEventDisplay: React.FC<MCPServerSelectionEventProps> = ({ event, compact = false }) => {
  if (compact) {
    return (
      <div className="p-2 bg-blue-50 dark:bg-blue-900/20 border border-blue-200 dark:border-blue-800 rounded-md">
        <div className="text-xs text-blue-700 dark:text-blue-300 flex items-center gap-2">
          <Server className="w-3 h-3 text-blue-600" />
          <span className="font-medium">MCP Server Selection</span>
          {event.selected_servers && event.selected_servers.length > 0 && (
            <span className="text-blue-600 dark:text-blue-400">• {event.selected_servers.length} servers</span>
          )}
          {event.turn && <span className="text-blue-600 dark:text-blue-400">• Turn {event.turn}</span>}
        </div>
      </div>
    )
  }

  return (
    <div className="p-3 bg-blue-50 dark:bg-blue-900/20 border border-blue-200 dark:border-blue-800 rounded-lg">
      <div className="text-xs text-blue-700 dark:text-blue-300 space-y-1">
        {/* Header */}
        <div className="flex items-center gap-2">
          <Server className="w-4 h-4 text-blue-600" />
          <span className="font-medium">MCP Server Selection</span>
        </div>
        
        {/* Turn information */}
        {event.turn && (
          <div className="flex items-center gap-2">
            <Hash className="w-3 h-3 text-blue-600" />
            <span>Turn: {event.turn}</span>
          </div>
        )}
        
        {/* Selected servers */}
        {event.selected_servers && event.selected_servers.length > 0 && (
          <div>
            <strong>Selected Servers:</strong>
            <div className="mt-1 space-y-1">
              {event.selected_servers.map((server, index) => (
                <div key={index} className="bg-card border border-border rounded-md p-2">
                  <div className="font-medium">{server}</div>
                </div>
              ))}
            </div>
          </div>
        )}
        
        {/* Total servers */}
        {event.total_servers && (
          <div><strong>Total Servers:</strong> {event.total_servers}</div>
        )}
        
        {/* Source */}
        {event.source && (
          <div><strong>Source:</strong> {event.source}</div>
        )}
        
        {/* Query */}
        {event.query && (
          <div className="bg-card border border-border rounded-md p-2">
            <div className="font-medium">Query:</div>
            <div className="mt-1 text-foreground">{event.query}</div>
          </div>
        )}
        
        {/* Optional metadata fields */}
        {event.timestamp && <div><strong>Timestamp:</strong> {new Date(event.timestamp).toLocaleString()}</div>}
        {event.trace_id && <div><strong>Trace ID:</strong> <code className="text-xs bg-blue-100 dark:bg-blue-800 px-1 rounded">{event.trace_id}</code></div>}
        {event.correlation_id && <div><strong>Correlation ID:</strong> <code className="text-xs bg-blue-100 dark:bg-blue-800 px-1 rounded">{event.correlation_id}</code></div>}
      </div>
    </div>
  )
}

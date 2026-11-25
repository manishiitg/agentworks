import React, { useState } from 'react'
import { MessageSquare, ChevronDown, ChevronRight, Hash, Wrench, MessageCircle } from 'lucide-react'
import type { ConversationTurnEvent } from '../../../generated/events'
import { ConversationMarkdownRenderer } from '../../ui/MarkdownRenderer'

interface ConversationTurnEventProps {
  event: ConversationTurnEvent
  compact?: boolean
}

export const ConversationTurnEventDisplay: React.FC<ConversationTurnEventProps> = ({ event, compact = false }) => {
  const [isExpanded, setIsExpanded] = useState(false)
  const [isToolsExpanded, setIsToolsExpanded] = useState(false)
  const [expandedDescriptions, setExpandedDescriptions] = useState<Set<string>>(new Set())

  const hasExpandableContent = event.messages && event.messages.length > 0
  const hasTools = event.tools && event.tools.length > 0

  const toggleDescription = (toolKey: string) => {
    setExpandedDescriptions(prev => {
      const next = new Set(prev)
      if (next.has(toolKey)) {
        next.delete(toolKey)
      } else {
        next.add(toolKey)
      }
      return next
    })
  }

  if (compact) {
    return (
      <div className="bg-blue-50 dark:bg-blue-900/20 border border-blue-200 dark:border-blue-800 rounded p-2">
        <div className="flex items-center justify-between gap-3">
          {/* Left side: Icon and main content */}
          <div className="flex items-center gap-3 min-w-0 flex-1">
            <div className="min-w-0 flex-1">
              <div className="text-sm font-medium text-blue-700 dark:text-blue-300">
                💬 Conversation Turn{' '}
                <span className="text-xs font-normal text-blue-600 dark:text-blue-400">
                  | Turn: {event.turn || 0} | Messages: {event.messages_count || 0}
                  {event.has_tool_calls && ` | Has tools`}
                  {event.tool_calls_count && ` | ${event.tool_calls_count} tool calls`}
                </span>
              </div>
            </div>
          </div>

          {/* Right side: Time and expand button */}
          <div className="flex items-center gap-2 flex-shrink-0">
            {event.timestamp && (
              <div className="text-xs text-blue-600 dark:text-blue-400">
                {new Date(event.timestamp).toLocaleTimeString()}
              </div>
            )}
            
            {hasExpandableContent && (
              <button
                onClick={() => setIsExpanded(!isExpanded)}
                className="text-xs text-blue-600 dark:text-blue-400 hover:text-blue-800 dark:hover:text-blue-200 underline cursor-pointer flex-shrink-0"
              >
                {isExpanded ? '▼' : '▶'}
              </button>
            )}
          </div>
        </div>
        
        {/* Last Message - Always shown */}
        {event.question && (
          <div className="mt-3 border-t border-blue-200 dark:border-blue-700 pt-3">
            <div className="text-xs font-medium text-blue-700 dark:text-blue-300 mb-1">💬 Last Message:</div>
            <div className="bg-card border border-border rounded-md p-2">
              <ConversationMarkdownRenderer content={event.question} framed={false} />
            </div>
          </div>
        )}
        
        {/* Available Tools - Grouped by MCP Server */}
        {hasTools && (
          <div className="mt-3 border-t border-blue-200 dark:border-blue-700 pt-3">
            <div className="flex items-center justify-between mb-2">
              <div className="text-xs font-medium text-blue-700 dark:text-blue-300">Available Tools ({event.tools?.length || 0}):</div>
              <button
                onClick={() => setIsToolsExpanded(!isToolsExpanded)}
                className="text-xs text-blue-600 dark:text-blue-400 hover:text-blue-800 dark:hover:text-blue-200 underline cursor-pointer flex-shrink-0"
              >
                {isToolsExpanded ? '▼ Collapse' : '▶ Expand'}
              </button>
            </div>
            
            {isToolsExpanded && (
              <div className="bg-card border border-border rounded-md p-2 max-h-40 overflow-y-auto">
                {(() => {
                  // Group tools by server
                  const toolsByServer = (event.tools || []).reduce((acc, tool) => {
                    const server = tool.server || 'Unknown Server';
                    if (!acc[server]) {
                      acc[server] = [];
                    }
                    acc[server].push(tool);
                    return acc;
                  }, {} as Record<string, typeof event.tools>);

                  return (
                    <div className="space-y-3">
                      {Object.entries(toolsByServer).map(([serverName, tools]) => (
                        <div key={serverName} className="border-b border-border pb-2 last:border-b-0">
                          <div className="flex items-center gap-2 mb-2">
                            <span className="text-xs font-semibold text-purple-700 dark:text-purple-300">
                              {serverName}
                            </span>
                            <span className="text-xs text-muted-foreground bg-muted px-1 rounded">
                              {tools?.length || 0} tools
                            </span>
                          </div>
                          <div className="space-y-1 ml-4">
                            {tools?.map((tool, index) => {
                              const toolKey = `${serverName}:${tool.name}`
                              const isDescriptionExpanded = expandedDescriptions.has(toolKey)
                              const description = tool.description || ''
                              const shouldTruncate = description.length > 80
                              const displayDescription = shouldTruncate && !isDescriptionExpanded
                                ? `${description.substring(0, 80)}...`
                                : description

                              return (
                                <div key={index} className="text-xs">
                                  <div className="flex items-start gap-2">
                                    <span className="font-medium text-blue-700 dark:text-blue-300 min-w-0 flex-1">
                                      {tool.name}
                                    </span>
                                  </div>
                                  {description && (
                                    <div className="text-muted-foreground mt-1 text-xs">
                                      {displayDescription}
                                      {shouldTruncate && (
                                        <button
                                          onClick={() => toggleDescription(toolKey)}
                                          className="ml-1 text-blue-600 dark:text-blue-400 hover:text-blue-800 dark:hover:text-blue-200 underline cursor-pointer"
                                        >
                                          {isDescriptionExpanded ? 'Read less' : 'Read more'}
                                        </button>
                                      )}
                                    </div>
                                  )}
                                </div>
                              )
                            })}
                          </div>
                        </div>
                      ))}
                    </div>
                  );
                })()}
              </div>
            )}
          </div>
        )}

        {/* Message Array - Expandable */}
        {event.messages && event.messages.length > 0 && (
          <div className="mt-3 border-t border-blue-200 dark:border-blue-700 pt-3">
            <div className="flex items-center justify-between mb-2">
              <div className="text-xs font-medium text-blue-700 dark:text-blue-300">Message Array ({event.messages.length}):</div>
              <button
                onClick={() => setIsExpanded(!isExpanded)}
                className="text-xs text-blue-600 dark:text-blue-400 hover:text-blue-800 dark:hover:text-blue-200 underline cursor-pointer flex-shrink-0"
              >
                {isExpanded ? '▼ Collapse' : '▶ Expand'}
              </button>
            </div>
            
            {isExpanded && (
              <div className="bg-card border border-border rounded-md p-2 space-y-2">
                {event.messages.map((message, index) => (
                  <div key={index} className="border-b border-border pb-2 last:border-b-0">
                    <div className="flex items-center gap-2 mb-1">
                      <span className="text-xs font-medium px-2 py-1 rounded bg-muted">
                        {message.role}
                      </span>
                      <span className="text-xs text-muted-foreground">Message {index + 1}</span>
                    </div>
                    <div className="text-sm">
                      {message.parts && message.parts.length > 0 ? (
                        message.parts.map((part, partIndex: number) => (
                          <div key={partIndex} className="mb-1">
                            <span className="text-xs text-muted-foreground">Part {partIndex + 1} ({part.type}):</span>
                            <div className="mt-1 p-2 bg-muted/50 rounded text-xs font-mono overflow-x-auto">
                              {typeof part.content === 'string' ? (
                                <div className="whitespace-pre-wrap">{part.content}</div>
                              ) : (
                                <div>{JSON.stringify(part.content, null, 2)}</div>
                              )}
                            </div>
                          </div>
                        ))
                      ) : (
                        <div className="text-muted-foreground italic">No parts</div>
                      )}
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
        )}
      </div>
    )
  }

  return (
    <div className="p-3 bg-blue-50 dark:bg-blue-900/20 border border-blue-200 dark:border-blue-800 rounded-lg">
      <div className="text-xs text-blue-700 dark:text-blue-300 space-y-1">
        {/* Header */}
        <div className="flex items-center gap-2">
          <MessageSquare className="w-4 h-4 text-blue-600" />
          <span className="font-medium">Conversation Turn</span>
        </div>
        
        {/* Turn number */}
        {event.turn && (
          <div className="flex items-center gap-2">
            <Hash className="w-3 h-3 text-blue-600" />
            <span>Turn: {event.turn}</span>
          </div>
        )}
        
        {/* Question - Always shown with markdown rendering */}
        {event.question && (
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <strong>Last Message:</strong>
            </div>
            
            {/* Question with markdown rendering - always shown */}
            <div className="bg-card border border-border rounded-md p-2">
              <ConversationMarkdownRenderer content={event.question} framed={false} />
            </div>
          </div>
        )}
        
        {/* Messages count */}
        {event.messages_count && (
          <div className="flex items-center gap-2">
            <MessageCircle className="w-3 h-3 text-blue-600" />
            <span>Messages: {event.messages_count}</span>
          </div>
        )}
        
        {/* Message array with expandable view */}
        {event.messages && event.messages.length > 0 && (
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <strong>Message Array:</strong>
              <button
                onClick={() => setIsExpanded(!isExpanded)}
                className="flex items-center gap-1 text-xs text-blue-600 hover:text-blue-800 dark:text-blue-400 dark:hover:text-blue-200"
              >
                {isExpanded ? <ChevronDown className="w-3 h-3" /> : <ChevronRight className="w-3 h-3" />}
                {isExpanded ? 'Collapse' : 'Expand'}
              </button>
            </div>
            
            {/* Brief messages preview */}
            <div className="text-muted-foreground">
              {event.messages.length} messages in conversation history
            </div>
            
            {/* Expanded messages with detailed view */}
            {isExpanded && (
              <div className="bg-card border border-border rounded-md p-2 space-y-2">
                {event.messages.map((message, index) => (
                  <div key={index} className="border-b border-border pb-2 last:border-b-0">
                    <div className="flex items-center gap-2 mb-1">
                      <span className="text-xs font-medium px-2 py-1 rounded bg-muted">
                        {message.role}
                      </span>
                      <span className="text-xs text-muted-foreground">Message {index + 1}</span>
                    </div>
                    <div className="text-sm">
                      {message.parts && message.parts.length > 0 ? (
                        message.parts.map((part, partIndex: number) => (
                          <div key={partIndex} className="mb-1">
                            <span className="text-xs text-muted-foreground">Part {partIndex + 1} ({part.type}):</span>
                            <div className="mt-1 p-2 bg-muted/50 rounded text-xs font-mono overflow-x-auto">
                              {typeof part.content === 'string' ? (
                                <div className="whitespace-pre-wrap">{part.content}</div>
                              ) : (
                                <div>{JSON.stringify(part.content, null, 2)}</div>
                              )}
                            </div>
                          </div>
                        ))
                      ) : (
                        <div className="text-muted-foreground italic">No parts</div>
                      )}
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
        )}
        
        {/* Tool calls information */}
        {event.has_tool_calls && (
          <div className="flex items-center gap-2">
            <Wrench className="w-3 h-3 text-blue-600" />
            <span>Has Tool Calls: {event.has_tool_calls ? 'Yes' : 'No'}</span>
          </div>
        )}
        
        {event.tool_calls_count && (
          <div><strong>Tool Calls Count:</strong> {event.tool_calls_count}</div>
        )}

        {/* Available Tools */}
        {hasTools && (
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2">
                <Wrench className="w-3 h-3 text-blue-600" />
                <strong>Available Tools ({event.tools?.length || 0}):</strong>
              </div>
              <button
                onClick={() => setIsToolsExpanded(!isToolsExpanded)}
                className="flex items-center gap-1 text-xs text-blue-600 hover:text-blue-800 dark:text-blue-400 dark:hover:text-blue-200"
              >
                {isToolsExpanded ? <ChevronDown className="w-3 h-3" /> : <ChevronRight className="w-3 h-3" />}
                {isToolsExpanded ? 'Collapse' : 'Expand'}
              </button>
            </div>
            
            {/* Brief tools preview */}
            <div className="text-muted-foreground">
              {event.tools?.length || 0} tools available for this conversation turn
            </div>
            
            {/* Expanded tools with detailed view - Grouped by MCP Server */}
            {isToolsExpanded && (
              <div className="bg-card border border-border rounded-md p-2 max-h-60 overflow-y-auto">
                {(() => {
                  // Group tools by server
                  const toolsByServer = (event.tools || []).reduce((acc, tool) => {
                    const server = tool.server || 'Unknown Server';
                    if (!acc[server]) {
                      acc[server] = [];
                    }
                    acc[server].push(tool);
                    return acc;
                  }, {} as Record<string, typeof event.tools>);

                  return (
                    <div className="space-y-3">
                      {Object.entries(toolsByServer).map(([serverName, tools]) => (
                        <div key={serverName} className="border-b border-border pb-2 last:border-b-0">
                          <div className="flex items-center gap-2 mb-2">
                            <span className="text-xs font-semibold text-purple-700 dark:text-purple-300">
                              {serverName}
                            </span>
                            <span className="text-xs text-muted-foreground bg-muted px-1 rounded">
                              {tools?.length || 0} tools
                            </span>
                          </div>
                          <div className="space-y-1 ml-4">
                            {tools?.map((tool, index) => {
                              const toolKey = `${serverName}:${tool.name}`
                              const isDescriptionExpanded = expandedDescriptions.has(toolKey)
                              const description = tool.description || ''
                              const shouldTruncate = description.length > 100
                              const displayDescription = shouldTruncate && !isDescriptionExpanded
                                ? `${description.substring(0, 100)}...`
                                : description

                              return (
                                <div key={index} className="text-xs">
                                  <div className="flex items-start gap-2">
                                    <span className="font-medium text-blue-700 dark:text-blue-300 min-w-0 flex-1">
                                      {tool.name}
                                    </span>
                                  </div>
                                  {description && (
                                    <div className="text-muted-foreground mt-1 text-xs">
                                      {displayDescription}
                                      {shouldTruncate && (
                                        <button
                                          onClick={() => toggleDescription(toolKey)}
                                          className="ml-1 text-blue-600 dark:text-blue-400 hover:text-blue-800 dark:hover:text-blue-200 underline cursor-pointer"
                                        >
                                          {isDescriptionExpanded ? 'Read less' : 'Read more'}
                                        </button>
                                      )}
                                    </div>
                                  )}
                                </div>
                              )
                            })}
                          </div>
                        </div>
                      ))}
                    </div>
                  );
                })()}
              </div>
            )}
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

import React from 'react'
import type { ToolCallStartEvent } from '../../../../generated/event-types'
import { useExpandable } from '../../useExpandable'
import { Plus, Minus } from 'lucide-react'

interface MCPToolCallDisplayProps {
  event: ToolCallStartEvent
}

// Detect if a string value looks like multiline code/script
const isCodeLike = (value: string): boolean => {
  return value.includes('\n') || value.length > 120
}

// Parse mcp__{server}__{tool} into parts
const parseMCPToolName = (toolName: string): { server: string; tool: string } => {
  const parts = toolName.slice('mcp__'.length).split('__')
  return { server: parts[0] || '', tool: parts.slice(1).join('__') || '' }
}

export const MCPToolCallDisplay: React.FC<MCPToolCallDisplayProps> = ({ event }) => {
  const { isExpanded, toggle } = useExpandable(true)

  const toolName = event.tool_name || ''
  const { server, tool } = parseMCPToolName(toolName)

  const parallelBadge = event.is_parallel ? (
    <span className="ml-1.5 text-[10px] text-muted-foreground font-normal opacity-75">
      (parallel)
    </span>
  ) : null

  let parsedArgs: Record<string, unknown> = {}
  let hasArgs = false
  if (event.tool_params?.arguments) {
    try {
      parsedArgs = JSON.parse(event.tool_params.arguments)
      hasArgs = Object.keys(parsedArgs).length > 0
    } catch { /* ignore */ }
  }

  // Find the "primary" argument to show in collapsed preview
  const primaryKeys = ['command', 'code', 'script', 'query', 'input', 'content', 'text', 'message', 'prompt']
  const primaryKey = primaryKeys.find(k => k in parsedArgs && typeof parsedArgs[k] === 'string') || null
  const primaryValue = primaryKey ? (parsedArgs[primaryKey] as string) : null

  const otherArgs = Object.entries(parsedArgs).filter(([k]) => k !== primaryKey)

  return (
    <div className="bg-blue-50 dark:bg-blue-900/20 border border-blue-200 dark:border-blue-800 rounded p-2 transition-colors duration-200">
      <div className="flex items-center justify-between gap-3">
        {/* Left: server + tool name */}
        <div className="flex items-center gap-2 min-w-0 flex-1">
          <div className="min-w-0 flex-1">
            <div className="text-sm font-medium text-blue-700 dark:text-blue-300 flex items-center gap-1.5 flex-wrap">
              <span className="inline-flex items-center gap-1">
                <span className="text-[10px] font-semibold uppercase tracking-wide bg-blue-200 dark:bg-blue-800 text-blue-700 dark:text-blue-300 px-1.5 py-0.5 rounded">
                  MCP
                </span>
                {server && (
                  <span className="font-mono text-blue-600 dark:text-blue-400 text-xs">{server}</span>
                )}
              </span>
              <span>{tool || toolName}</span>
              {parallelBadge}
              <span className="text-xs font-normal text-blue-600 dark:text-blue-400">
                {event.turn !== undefined && `• Turn: ${event.turn}`}
              </span>
            </div>
          </div>
        </div>

        {/* Right: time + toggle */}
        <div className="flex items-center gap-2 flex-shrink-0">
          {event.timestamp && (
            <div className="text-xs text-blue-600 dark:text-blue-400">
              {new Date(event.timestamp).toLocaleTimeString()}
            </div>
          )}
          {hasArgs && (
            <button
              onClick={toggle}
              className="p-0.5 hover:bg-blue-200 dark:hover:bg-blue-800 rounded text-blue-700 dark:text-blue-300 transition-colors"
              title={isExpanded ? 'Collapse arguments' : 'Expand arguments'}
            >
              {isExpanded ? <Minus className="w-4 h-4" /> : <Plus className="w-4 h-4" />}
            </button>
          )}
        </div>
      </div>

      {/* Collapsed: single-line preview of primary arg */}
      {!isExpanded && primaryValue && (
        <div className="mt-1">
          <div className="bg-muted border border-border rounded-md px-2 py-1">
            <pre className="text-xs text-green-300 font-mono truncate">
              {primaryValue.split('\n')[0]}{primaryValue.split('\n').length > 1 ? ' …' : ''}
            </pre>
          </div>
        </div>
      )}

      {/* Expanded: full args */}
      {isExpanded && hasArgs && (
        <div className="mt-2 space-y-2">
          {primaryKey && primaryValue && (
            <div className="bg-muted border border-border rounded-md p-2">
              <div className="flex items-center justify-between mb-1">
                <div className="text-xs font-medium text-muted-foreground uppercase tracking-wide">{primaryKey}</div>
                {isCodeLike(primaryValue) && (
                  <div className="text-xs text-muted-foreground">
                    {primaryValue.split('\n').length} line{primaryValue.split('\n').length !== 1 ? 's' : ''}
                  </div>
                )}
              </div>
              <pre className="text-xs text-green-300 font-mono whitespace-pre-wrap overflow-x-auto max-h-64 overflow-y-auto">
                {primaryValue}
              </pre>
            </div>
          )}

          {otherArgs.length > 0 && (
            <div className="bg-card border border-border rounded-md p-2 space-y-1.5">
              {otherArgs.map(([key, val]) => {
                const strVal = typeof val === 'string' ? val : JSON.stringify(val, null, 2)
                return (
                  <div key={key}>
                    <div className="text-[10px] font-medium text-blue-600 dark:text-blue-400 uppercase tracking-wide mb-0.5">
                      {key}
                    </div>
                    {isCodeLike(strVal) ? (
                      <pre className="text-xs text-foreground font-mono whitespace-pre-wrap bg-muted/50 px-2 py-1 rounded max-h-32 overflow-y-auto">
                        {strVal}
                      </pre>
                    ) : (
                      <span className="text-xs font-mono text-foreground bg-muted/50 px-1.5 py-0.5 rounded">
                        {strVal}
                      </span>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </div>
      )}
    </div>
  )
}

import React from 'react'
import type { ToolCallStartEvent } from '../../../../generated/event-types'
import { useExpandable } from '../../useExpandable'
import { Plus, Minus } from 'lucide-react'
import { getLogicalToolName, getMCPServerName } from '../../../../utils/event-helpers'

interface CodeExecutionToolCallDisplayProps {
  event: ToolCallStartEvent
}

export const CodeExecutionToolCallDisplay: React.FC<CodeExecutionToolCallDisplayProps> = ({ event }) => {
  const { isExpanded, toggle } = useExpandable(true)
  
  const toolName = event.tool_name || ''
  const logicalToolName = getLogicalToolName(toolName)
  const mcpServerName = getMCPServerName(toolName)

  const parallelBadge = event.is_parallel ? (
    <span className="ml-1.5 text-[10px] text-muted-foreground font-normal opacity-75">
      (parallel)
    </span>
  ) : null

  // Handle get_api_spec tool (also handles mcp__*__get_api_spec)
  if (logicalToolName === 'get_api_spec') {
    let serverName = mcpServerName || ''
    let specificToolName = ''
    try {
      const args = event.tool_params?.arguments ? JSON.parse(event.tool_params.arguments) : {}
      serverName = args.server_name || serverName
      specificToolName = args.tool_name || ''
    } catch { /* ignore */ }

    return (
      <div className="bg-indigo-50 dark:bg-indigo-900/20 border border-indigo-200 dark:border-indigo-800 rounded p-2">
        <div className="flex items-center justify-between gap-3">
          <div className="flex items-center gap-3 min-w-0 flex-1">
            <div className="min-w-0 flex-1">
              <div className="text-sm font-medium text-indigo-700 dark:text-indigo-300 flex items-center">
                <span className="mr-1">📋</span> Get API Spec{parallelBadge}{' '}
                <span className="text-xs font-normal text-indigo-600 dark:text-indigo-400">
                  {event.turn !== undefined && `• Turn: ${event.turn}`}
                  {serverName && ` • Server: ${serverName}`}
                  {specificToolName && ` • Tool: ${specificToolName}`}
                </span>
              </div>
            </div>
          </div>

          {event.timestamp && (
            <div className="text-xs text-indigo-600 dark:text-indigo-400 flex-shrink-0">
              {new Date(event.timestamp).toLocaleTimeString()}
            </div>
          )}
        </div>

        <div className="mt-2">
          <div className="bg-card border border-border rounded-md p-2">
            <div className="text-xs text-muted-foreground">
              Fetching OpenAPI spec{specificToolName ? ` for ${specificToolName}` : serverName ? ` for ${serverName}` : ''}...
            </div>
          </div>
        </div>
      </div>
    )
  }

  // Handle execute_shell_command tool
  if (logicalToolName === 'execute_shell_command') {
    let command = ''
    try {
      const args = event.tool_params?.arguments ? JSON.parse(event.tool_params.arguments) : {}
      command = args.command || ''
    } catch { /* ignore */ }

    return (
      <div className="bg-muted border border-border rounded p-2">
        <div className="flex items-center justify-between gap-3">
          <div className="flex items-center gap-3 min-w-0 flex-1">
            <div className="min-w-0 flex-1">
              <div className="text-sm font-medium text-foreground flex items-center">
                <span className="mr-1">▶</span> Execute Shell Command{parallelBadge}{' '}
                <span className="text-xs font-normal text-muted-foreground">
                  {event.turn !== undefined && `• Turn: ${event.turn}`}
                </span>
              </div>
            </div>
          </div>

          <div className="flex items-center gap-2 flex-shrink-0">
            {event.timestamp && (
              <div className="text-xs text-muted-foreground">
                {new Date(event.timestamp).toLocaleTimeString()}
              </div>
            )}
            {command && (
              <button
                onClick={toggle}
                className="p-0.5 hover:bg-muted rounded text-foreground transition-colors"
                title={isExpanded ? "Collapse (Alt+Click for all)" : "Expand (Alt+Click for all)"}
              >
                {isExpanded ? <Minus className="w-4 h-4" /> : <Plus className="w-4 h-4" />}
              </button>
            )}
          </div>
        </div>

        {command && (
          <div className={isExpanded ? 'mt-2' : 'mt-1'}>
            {isExpanded ? (
              <div className="bg-muted border border-border rounded-md p-2">
                <div className="flex items-center justify-between mb-1">
                  <div className="text-xs font-medium text-muted-foreground">
                    $ Command
                  </div>
                  <div className="text-xs text-muted-foreground">
                    {command.split('\n').length} line{command.split('\n').length !== 1 ? 's' : ''}
                  </div>
                </div>
                <pre className="text-xs text-green-300 font-mono whitespace-pre-wrap overflow-x-auto max-h-48 overflow-y-auto">
                  {command}
                </pre>
              </div>
            ) : (
              <div className="bg-muted border border-border rounded-md px-2 py-1">
                <pre className="text-xs text-green-300 font-mono truncate">
                  $ {command.split('\n')[0]}{command.split('\n').length > 1 ? ' ...' : ''}
                </pre>
              </div>
            )}
          </div>
        )}
      </div>
    )
  }

  // Handle discover_code_structure tool (no parameters)
  if (logicalToolName === 'discover_code_structure') {
    return (
      <div className="bg-blue-50 dark:bg-blue-900/20 border border-blue-200 dark:border-blue-800 rounded p-2">
        <div className="flex items-center justify-between gap-3">
          <div className="flex items-center gap-3 min-w-0 flex-1">
            <div className="min-w-0 flex-1">
              <div className="text-sm font-medium text-blue-700 dark:text-blue-300 flex items-center">
                🔍 Discover Code Structure{parallelBadge}{' '}
                <span className="text-xs font-normal text-blue-600 dark:text-blue-400">
                  {event.turn !== undefined && `• Turn: ${event.turn}`}
                  {event.server_name && ` • Server: ${event.server_name}`}
                </span>
              </div>
            </div>
          </div>

          {event.timestamp && (
            <div className="text-xs text-blue-600 dark:text-blue-400 flex-shrink-0">
              {new Date(event.timestamp).toLocaleTimeString()}
            </div>
          )}
        </div>

        <div className="mt-2">
          <div className="bg-card border border-border rounded-md p-2">
            <div className="text-xs text-muted-foreground">
              Discovering available servers, tools, and code structure...
            </div>
          </div>
        </div>
      </div>
    )
  }

  // For other tools, parse arguments
  if (!event.tool_params?.arguments) {
    return null
  }

  let parsedArgs: Record<string, unknown> = {}
  try {
    parsedArgs = JSON.parse(event.tool_params.arguments)
  } catch {
    return null
  }

  // Handle discover_code_files tool
  if (logicalToolName === 'discover_code_files') {
    const serverName = (parsedArgs.server_name as string) || null
    const toolNames = (parsedArgs.tool_names as string[]) || null

    return (
      <div className="bg-blue-50 dark:bg-blue-900/20 border border-blue-200 dark:border-blue-800 rounded p-2">
        <div className="flex items-center justify-between gap-3">
          <div className="flex items-center gap-3 min-w-0 flex-1">
            <div className="min-w-0 flex-1">
              <div className="text-sm font-medium text-blue-700 dark:text-blue-300 flex items-center">
                🔍 Discover Code Files{parallelBadge}{' '}
                <span className="text-xs font-normal text-blue-600 dark:text-blue-400">
                  {event.turn !== undefined && `• Turn: ${event.turn}`}
                  {serverName && ` • Server: ${serverName}`}
                </span>
              </div>
            </div>
          </div>

          <div className="flex items-center gap-2 flex-shrink-0">
            {event.timestamp && (
              <div className="text-xs text-blue-600 dark:text-blue-400">
                {new Date(event.timestamp).toLocaleTimeString()}
              </div>
            )}
            <button
              onClick={toggle}
              className="p-0.5 hover:bg-blue-200 dark:hover:bg-blue-800 rounded text-blue-700 dark:text-blue-300 transition-colors"
              title={isExpanded ? "Collapse arguments (Alt+Click for all)" : "Expand arguments (Alt+Click for all)"}
            >
              {isExpanded ? <Minus className="w-4 h-4" /> : <Plus className="w-4 h-4" />}
            </button>
          </div>
        </div>

        {isExpanded && (
          <div className="mt-2 space-y-2">
            {serverName && (
              <div className="bg-card border border-border rounded-md p-2">
                <div className="text-xs font-medium text-blue-700 dark:text-blue-300 mb-1">📦 Server:</div>
                <div className="text-sm font-mono text-foreground bg-muted/50 px-2 py-1 rounded">
                  {serverName}
                </div>
              </div>
            )}
            {Array.isArray(toolNames) && toolNames.length > 0 && (
              <div className="bg-card border border-border rounded-md p-1.5">
                <div className="text-[10px] font-medium text-blue-700 dark:text-blue-300 mb-0.5">
                  🔧 Tools ({toolNames.length}):
                </div>
                <div className="flex flex-wrap gap-0.5">
                  {toolNames.map((tool, index) => (
                    <span
                      key={index}
                      className="text-[10px] font-mono text-foreground bg-muted/50 px-1.5 py-0.5 rounded border border-border"
                    >
                      {tool}
                    </span>
                  ))}
                </div>
              </div>
            )}
            {(serverName || (Array.isArray(toolNames) && toolNames.length > 0)) && (
              <div className="text-xs text-muted-foreground px-2">
                Fetching Go source code...
              </div>
            )}
          </div>
        )}
      </div>
    )
  }

  // Handle write_code tool
  if (logicalToolName === 'write_code') {
    const filename = (parsedArgs.filename as string) || null
    const code = (parsedArgs.code as string) || ''
    const lineCount = code.split('\n').length
    
    // Extract CLI arguments if present
    let cliArgs: string[] = []
    if (parsedArgs.args) {
      if (Array.isArray(parsedArgs.args)) {
        cliArgs = parsedArgs.args.map(arg => String(arg))
      } else if (typeof parsedArgs.args === 'string') {
        // Handle case where args might be a single string
        cliArgs = [parsedArgs.args]
      }
    }

    return (
      <div className="bg-purple-100 dark:bg-purple-900/50 border border-purple-200 dark:border-purple-800 rounded p-2">
        <div className="flex items-center justify-between gap-3">
          <div className="flex items-center gap-3 min-w-0 flex-1">
            <div className="min-w-0 flex-1">
              <div className="text-sm font-medium text-purple-700 dark:text-purple-300 flex items-center">
                ✍️ Write Code{parallelBadge}{' '}
                <span className="text-xs font-normal text-purple-600 dark:text-purple-400">
                  {event.turn !== undefined && `• Turn: ${event.turn}`}
                  {event.server_name && ` • Server: ${event.server_name}`}
                </span>
              </div>
            </div>
          </div>

          <div className="flex items-center gap-2 flex-shrink-0">
            {event.timestamp && (
              <div className="text-xs text-purple-600 dark:text-purple-400">
                {new Date(event.timestamp).toLocaleTimeString()}
              </div>
            )}
            <button
              onClick={toggle}
              className="p-0.5 hover:bg-purple-200 dark:hover:bg-purple-800 rounded text-purple-700 dark:text-purple-300 transition-colors"
              title={isExpanded ? "Collapse arguments (Alt+Click for all)" : "Expand arguments (Alt+Click for all)"}
            >
              {isExpanded ? <Minus className="w-4 h-4" /> : <Plus className="w-4 h-4" />}
            </button>
          </div>
        </div>

        {isExpanded && (
          <div className="mt-2 space-y-2">
            {filename && (
              <div className="bg-card border border-border rounded-md p-2">
                <div className="text-xs font-medium text-purple-700 dark:text-purple-300 mb-1">📄 Filename:</div>
                <div className="text-sm font-mono text-foreground bg-muted/50 px-2 py-1 rounded">
                  {filename}
                </div>
              </div>
            )}

            {cliArgs.length > 0 && (
              <div className="bg-card border border-border rounded-md p-2">
                <div className="text-xs font-medium text-purple-700 dark:text-purple-300">
                  🔧 CLI Arguments:{' '}
                  <span className="font-mono text-foreground">
                    {cliArgs.join(', ')}
                  </span>
                </div>
              </div>
            )}

            {code && (
              <div className="bg-card border border-border rounded-md p-2">
                <div className="flex items-center justify-between mb-1">
                  <div className="text-xs font-medium text-purple-700 dark:text-purple-300">
                    💻 Go Code Preview
                  </div>
                  <div className="text-xs text-muted-foreground">
                    {lineCount} line{lineCount !== 1 ? 's' : ''} • {code.length} chars
                  </div>
                </div>
                <pre className="text-xs text-foreground font-mono whitespace-pre-wrap overflow-x-auto bg-muted/50 p-2 rounded max-h-48 overflow-y-auto">
                  {code}
                </pre>
              </div>
            )}
          </div>
        )}
      </div>
    )
  }

  return null
}

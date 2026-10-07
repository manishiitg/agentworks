import React from 'react'
import type { ToolCallStartEvent } from '../../../generated/event-types'
import { WorkspaceToolCallDisplay, CodeExecutionToolCallDisplay, SubAgentToolCallDisplay, MCPToolCallDisplay, WebSearchToolCallDisplay } from './ToolCallSpecialRender'
import { isWebSearchToolCall } from '../../../utils/webSearchToolCall'
import { useExpandable } from '../useExpandable'
import { Plus, Minus } from 'lucide-react'
import { normalizeMCPToolName } from '../../../utils/customToolNames'

interface ToolCallStartEventProps {
  event: ToolCallStartEvent
}

export const ToolCallStartEventDisplay: React.FC<ToolCallStartEventProps> = ({ event }) => {
  const { isExpanded, toggle } = useExpandable(true)

  const normalizedToolName = event.tool_name ? normalizeMCPToolName(event.tool_name) : event.tool_name

  // Check if this is a workspace tool
  const isWorkspaceTool = (name: string): boolean => {
    const workspaceToolNames = [
      'update_workspace_file',
      'read_workspace_file',
      'list_workspace_files',
      'diff_patch_workspace_file',
      'delete_workspace_file',
    ]
    return workspaceToolNames.includes(name)
  }

  // Check if this is a human tool
  const isHumanTool = (name: string): boolean => {
    return name === 'human_feedback'
  }

  // Check if this is a code execution tool
  const isCodeExecutionTool = (name: string): boolean => {
    return name === 'discover_code_structure' || name === 'discover_code_files' || name === 'write_code' || name === 'get_api_spec' || name === 'execute_shell_command'
  }

  // If it's a workspace tool, use the specialized component
  if (normalizedToolName && isWorkspaceTool(normalizedToolName)) {
    return <WorkspaceToolCallDisplay event={event} />
  }

  // Human tools: don't render here — blocking_human_feedback
  // events (emitted inside the tool handlers) render the interactive UI via their
  // dedicated display components, so rendering here would show the question twice.
  if (normalizedToolName && isHumanTool(normalizedToolName)) {
    return null
  }

  // If it's a code execution tool, use the specialized component
  if (normalizedToolName && isCodeExecutionTool(normalizedToolName)) {
    return <CodeExecutionToolCallDisplay event={{ ...event, tool_name: normalizedToolName }} />
  }

  // A coding agent's web search: the compact search card (results arrive on the end event).
  if (event.tool_name && isWebSearchToolCall(event.tool_name, event.tool_params?.arguments)) {
    return <WebSearchToolCallDisplay name={event.tool_name} args={event.tool_params?.arguments} status="running" />
  }

  // If it's a sub-agent tool, use the specialized component
  if (normalizedToolName === 'call_sub_agent') {
    return <SubAgentToolCallDisplay event={event} />
  }

  // Unknown MCP tool — generic MCP fallback
  if (event.tool_name && event.tool_name.startsWith('mcp__')) {
    return <MCPToolCallDisplay event={event} />
  }

  // Simple JSON formatting function for regular tools
  const formatArguments = (args: string): string => {
    try {
      const parsed = JSON.parse(args)
      return JSON.stringify(parsed, null, 2)
    } catch {
      return args
    }
  }

  const hasArguments = event.tool_params?.arguments !== undefined

  // Single-line layout following design guidelines
  return (
    <div className="bg-blue-50 dark:bg-blue-900/20 border border-blue-200 dark:border-blue-800 rounded p-2 transition-colors duration-200">
      <div className="flex items-center justify-between gap-3">
        {/* Left side: Icon and main content */}
        <div className="flex items-center gap-3 min-w-0 flex-1">
          <div className="min-w-0 flex-1">
            <div className="text-sm font-medium text-blue-700 dark:text-blue-300 flex items-center gap-1.5">
              Tool Call Start{' '}
              {event.is_parallel && (
                <span className="ml-1.5 text-[10px] text-gray-500 dark:text-gray-400 font-normal opacity-75">
                  (parallel)
                </span>
              )}
              <span className="text-xs font-normal text-blue-600 dark:text-blue-400">
                {event.turn != null && `• Turn: ${event.turn}`}
                {event.tool_name && ` • Tool: ${event.tool_name}`}
                {event.server_name && ` • Server: ${event.server_name}`}
              </span>
            </div>
          </div>
        </div>

        {/* Right side: Time and Toggle */}
        <div className="flex items-center gap-2 flex-shrink-0">
          {event.timestamp && (
            <div className="text-xs text-blue-600 dark:text-blue-400">
              {new Date(event.timestamp).toLocaleTimeString()}
            </div>
          )}

          {hasArguments && (
            <button
              onClick={toggle}
              className="p-0.5 hover:bg-blue-200 dark:hover:bg-blue-800 rounded text-blue-700 dark:text-blue-300 transition-colors"
              title={isExpanded ? "Collapse arguments (Alt+Click for all)" : "Expand arguments (Alt+Click for all)"}
            >
              {isExpanded ? <Minus className="w-4 h-4" /> : <Plus className="w-4 h-4" />}
            </button>
          )}
        </div>
      </div>

      {/* Tool arguments visibility controlled by isExpanded */}
      {hasArguments && isExpanded && (
        <div className="mt-2">
          <div className="bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-md p-2">
            <div className="text-xs font-medium text-blue-700 dark:text-blue-300 mb-1">Arguments:</div>
            <pre className="text-xs text-gray-800 dark:text-gray-200 font-mono whitespace-pre-wrap overflow-x-auto">
              {event.tool_params?.arguments ? formatArguments(event.tool_params.arguments) : '(no arguments)'}
            </pre>
          </div>
        </div>
      )}
    </div>
  )
}

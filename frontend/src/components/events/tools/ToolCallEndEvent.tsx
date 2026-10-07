import React from 'react'
import type { ToolCallEndEvent } from '../../../generated/events'
import { MarkdownRenderer } from '../../ui/MarkdownRenderer'
import { CsvRenderer } from '../../ui/CsvRenderer'
import { WorkspaceToolCallEndDisplay, CodeExecutionToolCallEndDisplay, WebSearchToolCallDisplay } from './ToolCallSpecialRender'
import { isWebSearchToolCall } from '../../../utils/webSearchToolCall'
import { CircularProgress, type ContextOnlyTokenUsage } from '../../ui/CircularProgress'
import { TooltipProvider } from '../../ui/tooltip'
import { useExpandable } from '../useExpandable'
import { Plus, Minus } from 'lucide-react'
import { normalizeMCPToolName } from '../../../utils/customToolNames'
import { formatToolCallResult } from '../../../utils/toolCallFormatting'

type OutputFormat = 'markdown' | 'json' | 'csv' | null

function detectOutputFormat(text: string): OutputFormat {
  if (!text || text.length < 10) return null
  const trimmed = text.trim()
  if (trimmed.startsWith('{') || trimmed.startsWith('[')) {
    try { JSON.parse(trimmed); return 'json' } catch { /* not json */ }
  }
  const lines = trimmed.split('\n').filter(l => l.trim())
  if (lines.length >= 2) {
    const cols = lines[0].split(',').length
    if (cols >= 2 && lines.slice(0, 5).every(l => l.split(',').length === cols)) return 'csv'
  }
  if (text.length >= 30 && (
    /^#{1,6}\s/m.test(text) || /\*\*.+?\*\*/s.test(text) ||
    /^\s*[-*+]\s\S/m.test(text) || /^\|.+\|/m.test(text) ||
    /^>\s/m.test(text) || /```[\s\S]*```/s.test(text)
  )) return 'markdown'
  return null
}

interface ToolCallEndEventProps {
  event: ToolCallEndEvent
}

// Every execution owner (main chat, background child, and workflow step) uses
// the same event component. Some tools have rich specialized cards and some
// take the generic path, but an error must never depend on that presentation
// choice. This frame makes the failure visible before a user expands output.
const ToolCallFailureFrame: React.FC<React.PropsWithChildren<{ event: ToolCallEndEvent }>> = ({ event, children }) => {
  const failed = React.useMemo(
    () => typeof event.result === 'string' && formatToolCallResult(event.result).isError,
    [event.result],
  )
  if (!failed) return <>{children}</>

  return (
    <div className="rounded border border-red-300 bg-red-50 p-1 dark:border-red-800 dark:bg-red-950/25">
      <div className="px-1.5 py-1 text-xs font-medium text-red-700 dark:text-red-300">
        ✗ Tool call failed{event.tool_name ? ` · ${event.tool_name}` : ''}
      </div>
      {children}
    </div>
  )
}

function isWorkspaceTool(name: string): boolean {
  const workspaceToolNames = [
    'read_workspace_file',
    'update_workspace_file',
    'diff_patch_workspace_file',
    'list_workspace_files',
    'delete_workspace_file',
  ]
  return workspaceToolNames.includes(name)
}

function isCodeExecutionTool(name: string): boolean {
  return name === 'discover_code_structure' || name === 'discover_code_files' || name === 'write_code' || name === 'get_api_spec' || name === 'execute_shell_command'
}

export const ToolCallEndEventDisplay: React.FC<ToolCallEndEventProps> = ({ event }) => {
  const normalizedToolName = event.tool_name ? normalizeMCPToolName(event.tool_name) : event.tool_name

  if (normalizedToolName && isWorkspaceTool(normalizedToolName)) {
    return <ToolCallFailureFrame event={event}><WorkspaceToolCallEndDisplay event={event} /></ToolCallFailureFrame>
  }

  if (normalizedToolName && isCodeExecutionTool(normalizedToolName)) {
    return <ToolCallFailureFrame event={event}><CodeExecutionToolCallEndDisplay event={{ ...event, tool_name: normalizedToolName }} /></ToolCallFailureFrame>
  }

  // The end event carries no arguments; the search card reads the query from
  // the result where the agent repeats it (Claude Code does).
  if (event.tool_name && isWebSearchToolCall(event.tool_name)) {
    return <WebSearchToolCallDisplay name={event.tool_name} result={event.result} status="ok" />
  }

  return <GenericToolCallEndEventDisplay event={event} />
}

const GenericToolCallEndEventDisplay: React.FC<ToolCallEndEventProps> = ({ event }) => {
  const { isExpanded, toggle } = useExpandable(true)
  const [isRawMode, setIsRawMode] = React.useState(false)

  const hasResult = typeof event.result === 'string' && event.result.length > 0
  // Custom/bridge tools arrive here instead of the code-execution renderer.
  // Use the same envelope-aware failure detector as the terminal transcript,
  // otherwise a nested bridge error is visually indistinguishable from success.
  const resultFormatting = React.useMemo(
    () => (hasResult ? formatToolCallResult(event.result) : null),
    [event.result, hasResult],
  )
  const isError = resultFormatting?.isError === true

  // Function to parse and extract content from JSON results
  const parseResultContent = (result: string): {
    isJson: boolean;
    textContent: string;
    formattedJson?: string;
    hasTextField: boolean;
  } => {
    try {
      const parsed = JSON.parse(result)

      // Shell-like result: prefer stdout (may itself be JSON/CSV/markdown)
      if (parsed && typeof parsed === 'object' && typeof parsed.stdout === 'string') {
        return {
          isJson: true,
          textContent: parsed.stdout,
          formattedJson: JSON.stringify(parsed, null, 2),
          hasTextField: true
        }
      }

      // Check if it's a structured response with a primary text field
      if (parsed && typeof parsed === 'object' && typeof parsed.text === 'string') {
        return {
          isJson: true,
          textContent: parsed.text,
          formattedJson: JSON.stringify(parsed, null, 2),
          hasTextField: true
        }
      }

      // Some workspace tools return "response" instead of "text"
      if (parsed && typeof parsed === 'object' && typeof parsed.response === 'string') {
        return {
          isJson: true,
          textContent: parsed.response,
          formattedJson: JSON.stringify(parsed, null, 2),
          hasTextField: true
        }
      }

      // If it's JSON but doesn't have a text field, return formatted JSON
      return {
        isJson: true,
        textContent: result,
        formattedJson: JSON.stringify(parsed, null, 2),
        hasTextField: false
      }
    } catch {
      // Not valid JSON, return as plain text
      return {
        isJson: false,
        textContent: result,
        hasTextField: false
      }
    }
  }

  // Note: event.duration is in nanoseconds from Go time.Duration
  const formatDuration = (durationNs: number) => {
    if (!durationNs || durationNs <= 0) {
      return '0ms'
    }

    // Convert nanoseconds to milliseconds
    const durationMs = durationNs / 1000000

    if (durationMs < 1) {
      // Less than 1ms, show in microseconds
      const durationUs = durationNs / 1000
      return `${Math.round(durationUs)}μs`
    } else if (durationMs < 1000) {
      // Less than 1ms, show in milliseconds
      return `${Math.round(durationMs)}ms`
    } else if (durationMs < 60000) {
      // Less than 1 minute, show in seconds
      return `${(durationMs / 1000).toFixed(1)}s`
    } else {
      // 1 minute or more, show in minutes
      return `${(durationMs / 60000).toFixed(1)}m`
    }
  }

  // Parse output lazily. Large tool payloads make group expansion expensive if every
  // collapsed card eagerly JSON-parses and format-detects its result.
  const resultInfo = React.useMemo(
    () => (isExpanded && hasResult && event.result ? parseResultContent(event.result) : null),
    [event.result, hasResult, isExpanded],
  )
  const detectedFormat = React.useMemo(
    () => (isExpanded && resultInfo ? detectOutputFormat(resultInfo.textContent) : null),
    [isExpanded, resultInfo],
  )
  const isFormatted = !!detectedFormat && !isRawMode
  const outputLineCount = React.useMemo(
    () => (resultInfo ? resultInfo.textContent.split('\n').length : 0),
    [resultInfo],
  )
  const formattedJsonText = React.useMemo(() => {
    if (!isExpanded || !resultInfo || detectedFormat !== 'json') return null
    try {
      return JSON.stringify(JSON.parse(resultInfo.textContent.trim()), null, 2)
    } catch {
      return null
    }
  }, [detectedFormat, isExpanded, resultInfo])

  // Extract context usage information
  const contextUsagePercent = event.context_usage_percent
  const modelContextWindow = event.model_context_window
  const contextWindowUsage = event.context_window_usage
  const modelId = event.model_id

  // Create a minimal token usage object for the tooltip (only context info available)
  const tokenUsageForTooltip: ContextOnlyTokenUsage | undefined =
    contextUsagePercent !== undefined && contextUsagePercent > 0 ? {
      context_usage_percent: contextUsagePercent,
      model_context_window: modelContextWindow,
      context_window_usage: contextWindowUsage,
      model_id: modelId,
    } : undefined

  // Determine theme color based on tool name (retrieval vs action)
  const toolName = event.tool_name?.toLowerCase() || ''
  const isRetrieval = toolName.includes('search') ||
                      toolName.includes('list') ||
                      toolName.includes('read') ||
                      toolName.includes('get') ||
                      toolName.includes('fetch') ||
                      toolName.includes('find')

  const theme = isRetrieval ? 'blue' : 'green'

  // A synthetic settle is the platform closing a chip whose tool call never
  // reported an end (PLAT-141) — not a completion the provider actually sent.
  // It gets neutral grey rather than success green: the tool may well have
  // succeeded, but nothing here observed that, and painting it green asserts
  // an outcome no one measured. Red would be equally wrong — nothing failed.
  const isSyntheticSettle = event.synthetic_settle === true

  const bgColor = isError ? 'bg-red-50 dark:bg-red-950/25' : isSyntheticSettle ? 'bg-gray-50 dark:bg-gray-800/40' : theme === 'blue' ? 'bg-blue-50 dark:bg-blue-900/20' : 'bg-green-50 dark:bg-green-900/20'
  const borderColor = isError ? 'border-red-300 dark:border-red-800' : isSyntheticSettle ? 'border-gray-300 dark:border-gray-700' : theme === 'blue' ? 'border-blue-200 dark:border-blue-800' : 'border-green-200 dark:border-green-800'
  const textColor = isError ? 'text-red-700 dark:text-red-300' : isSyntheticSettle ? 'text-gray-700 dark:text-gray-300' : theme === 'blue' ? 'text-blue-700 dark:text-blue-300' : 'text-green-700 dark:text-green-300'
  const textSecondaryColor = isError ? 'text-red-600 dark:text-red-400' : isSyntheticSettle ? 'text-gray-500 dark:text-gray-400' : theme === 'blue' ? 'text-blue-600 dark:text-blue-400' : 'text-green-600 dark:text-green-400'
  const hoverBgColor = isError ? 'hover:bg-red-100 dark:hover:bg-red-900/40' : isSyntheticSettle ? 'hover:bg-gray-100 dark:hover:bg-gray-700' : theme === 'blue' ? 'hover:bg-blue-200 dark:hover:bg-blue-800' : 'hover:bg-green-200 dark:hover:bg-green-800'

  // Single-line layout following design guidelines
  return (
    <div className={`${bgColor} border ${borderColor} rounded p-2`}>
      <div className="flex items-center justify-between gap-3">
        {/* Left side: Icon and main content */}
        <div className="flex items-center gap-3 min-w-0 flex-1">
          <div className="min-w-0 flex-1">
            <div className={`text-sm font-medium ${textColor} flex items-center gap-2`}>
              {isError ? '✗ Tool Call Failed' : isSyntheticSettle ? 'Tool Call — no result reported' : 'Tool Call End'}{' '}
              <span className={`text-xs font-normal ${textSecondaryColor}`}>
                {/* Turn is meaningless on a synthetic settle: the fabricated
                    event carries no turn number, so it always renders 0. */}
                {!isSyntheticSettle && event.turn != null && `• Turn: ${event.turn}`}
                {event.tool_name && ` • Tool: ${event.tool_name}`}
                {event.server_name && ` • Server: ${event.server_name}`}
                {/* On a synthetic settle this is open-to-settle time, NOT how
                    long the tool ran — labelling it "Duration" told readers the
                    tools were slow when nothing had measured that. */}
                {event.duration != null && (isSyntheticSettle
                  ? ` • Open for: ${formatDuration(event.duration)} (not tool runtime)`
                  : ` • Duration: ${formatDuration(event.duration)}`)}
              </span>
              {/* Context completion indicator */}
              {contextUsagePercent !== undefined && contextUsagePercent > 0 && (
                <TooltipProvider>
                  <CircularProgress
                    percentage={contextUsagePercent}
                    size={18}
                    strokeWidth={3}
                    tokenUsage={tokenUsageForTooltip}
                  />
                </TooltipProvider>
              )}
            </div>
          </div>
        </div>

        {/* Right side: Time and Toggle */}
        <div className="flex items-center gap-2 flex-shrink-0">
          {event.timestamp && (
            <div className={`text-xs ${textSecondaryColor}`}>
              {new Date(event.timestamp).toLocaleTimeString()}
            </div>
          )}
          {hasResult && (
            <button
              onClick={toggle}
              className={`p-0.5 ${hoverBgColor} rounded ${textColor} transition-colors`}
              title={isExpanded ? "Collapse output (Alt+Click for all)" : "Expand output (Alt+Click for all)"}
            >
              {isExpanded ? <Minus className="w-4 h-4" /> : <Plus className="w-4 h-4" />}
            </button>
          )}
        </div>
      </div>

      {/* Output panel */}
      {resultInfo && isExpanded && (
        <div className={`border rounded-md mt-2 p-3 ${isError ? 'border-red-300 bg-red-50 dark:border-red-900 dark:bg-red-950/25' : isFormatted ? 'bg-white dark:bg-gray-900 border-gray-200 dark:border-gray-700' : 'bg-gray-900 dark:bg-gray-950 border-gray-700'}`}>
          <div className="flex items-center justify-between mb-2">
            <div className={`text-xs font-medium ${isError ? 'text-red-700 dark:text-red-300' : isFormatted ? 'text-gray-600 dark:text-gray-400' : 'text-gray-400'}`}>
              {isError ? 'Error output' : 'Output'}
            </div>
            <div className="flex items-center gap-2">
              <div className={`text-xs ${isFormatted ? 'text-gray-400 dark:text-gray-500' : 'text-gray-500'}`}>
                {outputLineCount} line{outputLineCount !== 1 ? 's' : ''} • {resultInfo.textContent.length} chars
              </div>
              {detectedFormat && (
                <button
                  onClick={() => setIsRawMode(r => !r)}
                  className={`text-[10px] font-bold px-1.5 py-0.5 rounded border transition-colors ${
                    isFormatted
                      ? 'bg-blue-600 border-blue-500 text-white hover:bg-blue-700'
                      : 'bg-gray-600 border-gray-500 text-white hover:bg-gray-500'
                  }`}
                  title={isFormatted ? 'Show raw text' : `Render as ${detectedFormat}`}
                >
                  {isFormatted ? detectedFormat.toUpperCase() : 'RAW'}
                </button>
              )}
            </div>
          </div>
          {isFormatted && detectedFormat === 'markdown' && (
            <div className="max-h-96 overflow-y-auto overflow-x-auto">
              <MarkdownRenderer content={resultInfo.textContent} />
            </div>
          )}
          {isFormatted && detectedFormat === 'json' && (
            <div className="max-h-96 overflow-y-auto overflow-x-auto">
              <MarkdownRenderer content={`\`\`\`json\n${formattedJsonText ?? resultInfo.textContent}\n\`\`\``} />
            </div>
          )}
          {isFormatted && detectedFormat === 'csv' && (
            <div className="max-h-96 overflow-y-auto overflow-x-auto">
              <CsvRenderer content={resultInfo.textContent} />
            </div>
          )}
          {!isFormatted && (
            <pre className={`text-xs font-mono whitespace-pre-wrap overflow-x-auto max-h-96 overflow-y-auto ${isError ? 'text-red-700 dark:text-red-200' : 'text-green-300'}`}>
              {resultInfo.textContent}
            </pre>
          )}
        </div>
      )}
    </div>
  )
}

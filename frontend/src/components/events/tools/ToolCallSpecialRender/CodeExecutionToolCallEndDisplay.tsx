import React from 'react'
import type { ToolCallEndEvent } from '../../../../generated/events'
import { CircularProgress, type ContextOnlyTokenUsage } from '../../../ui/CircularProgress'
import { TooltipProvider } from '../../../ui/tooltip'
import { useExpandable } from '../../useExpandable'
import { Plus, Minus } from 'lucide-react'
import { MarkdownRenderer } from '../../../ui/MarkdownRenderer'
import { CsvRenderer } from '../../../ui/CsvRenderer'
import { getLogicalToolName, getMCPServerName } from '../../../../utils/event-helpers'
import { normalizeToolCallResultValue, toolCallValueToText } from '../../../../utils/toolCallFormatting'

type OutputFormat = 'markdown' | 'json' | 'csv' | null

function detectOutputFormat(text: string): OutputFormat {
  if (!text || text.length < 10) return null
  const trimmed = text.trim()

  // JSON: starts with { or [
  if (trimmed.startsWith('{') || trimmed.startsWith('[')) {
    try { JSON.parse(trimmed); return 'json' } catch { /* not json */ }
  }

  // CSV: has consistent comma-separated lines (at least 2 lines, 2+ columns)
  const lines = trimmed.split('\n').filter(l => l.trim())
  if (lines.length >= 2) {
    const cols = lines[0].split(',').length
    if (cols >= 2 && lines.slice(0, 5).every(l => l.split(',').length === cols)) {
      return 'csv'
    }
  }

  // Markdown: headings, bold, lists, tables, blockquotes, code fences
  if (text.length >= 30 && (
    /^#{1,6}\s/m.test(text) ||
    /\*\*.+?\*\*/s.test(text) ||
    /^\s*[-*+]\s\S/m.test(text) ||
    /^\|.+\|/m.test(text) ||
    /^>\s/m.test(text) ||
    /```[\s\S]*```/s.test(text)
  )) return 'markdown'

  return null
}

interface CodeExecutionToolCallEndDisplayProps {
  event: ToolCallEndEvent
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

// Format duration from nanoseconds
const formatDuration = (durationNs: number) => {
  if (!durationNs || durationNs <= 0) return '0ms'
  
  const durationMs = durationNs / 1000000
  
  if (durationMs < 1) {
    const durationUs = durationNs / 1000
    return `${Math.round(durationUs)}μs`
  } else if (durationMs < 1000) {
    return `${Math.round(durationMs)}ms`
  } else if (durationMs < 60000) {
    return `${(durationMs / 1000).toFixed(1)}s`
  } else {
    return `${(durationMs / 60000).toFixed(1)}m`
  }
}

export const CodeExecutionToolCallEndDisplay: React.FC<CodeExecutionToolCallEndDisplayProps> = ({ event }) => {
  const { isExpanded: isOutputExpanded, toggle } = useExpandable(true)
  const [isRawMode, setIsRawMode] = React.useState(false)

  // Extract context usage information for CircularProgress
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

  const renderHeaderRight = () => (
    <div className="flex items-center gap-2 flex-shrink-0">
      {event.timestamp && (
        <div className="text-xs text-muted-foreground">
          {new Date(event.timestamp).toLocaleTimeString()}
        </div>
      )}
      
      {/* Output Toggle */}
      <button
        onClick={toggle}
        className="p-0.5 hover:bg-muted rounded text-foreground transition-colors flex items-center gap-1"
        title={isOutputExpanded ? "Collapse output (Alt+Click for all)" : "Expand output (Alt+Click for all)"}
      >
        <span className="text-[10px] uppercase font-bold">Output</span>
        {isOutputExpanded ? <Minus className="w-3 h-3" /> : <Plus className="w-3 h-3" />}
      </button>
    </div>
  )

  const toolName = event.tool_name || ''
  const logicalToolName = getLogicalToolName(toolName)
  const mcpServerName = getMCPServerName(toolName)

  // Runtime bridge events can carry result as an already-decoded object even
  // though the generated schema currently declares a string. Normalize first
  // so no renderer can leak an object/content-block array into string methods.
  const normalizedResult = normalizeToolCallResultValue(event.result as unknown)
  const parsedResult: Record<string, unknown> = isRecord(normalizedResult) ? normalizedResult : {}
  const eventResultText = toolCallValueToText(event.result as unknown)
  const resultText = typeof parsedResult.text === 'string'
    ? parsedResult.text
    : typeof parsedResult.content === 'string'
      ? parsedResult.content
      : toolCallValueToText(normalizedResult)

  // Handle get_api_spec tool response (also handles mcp__*__get_api_spec)
  if (logicalToolName === 'get_api_spec') {
    // The result is an OpenAPI spec (YAML/JSON)
    let serverName = mcpServerName || ''
    let endpointCount = 0

    // Try to extract server name and count endpoints from the spec
    try {
      const specText = resultText || eventResultText
      // Count endpoint paths (lines like "  /tools/mcp/..." or paths in OpenAPI)
      const pathMatches = specText.match(/^\s+\/tools\//gm)
      endpointCount = pathMatches ? pathMatches.length : 0
      // Try to extract server name from the spec title or info (overrides MCP server name if found)
      const titleMatch = specText.match(/title:\s*(.+?)(?:\s+API|\s*$)/m)
      if (titleMatch) {
        serverName = titleMatch[1].trim()
      }
    } catch { /* ignore */ }

    const specLength = (resultText || eventResultText).length
    const lineCount = (resultText || eventResultText).split('\n').length

    return (
      <div className="bg-indigo-50 dark:bg-indigo-900/20 border border-indigo-200 dark:border-indigo-800 rounded p-2">
        <div className="flex items-center justify-between gap-2 py-0.5">
          <div className="flex items-center gap-2 min-w-0 flex-1">
            <div className="min-w-0 flex-1">
              <div className="text-xs font-medium text-indigo-700 dark:text-indigo-300 flex items-center gap-2">
                <span>📋</span> API Spec Retrieved{' '}
                <span className="text-xs font-normal text-indigo-600 dark:text-indigo-400">
                  {event.turn !== undefined && `• Turn: ${event.turn}`}
                  {(serverName || event.server_name) && ` • Server: ${serverName || event.server_name}`}
                  {endpointCount > 0 && ` • ${endpointCount} endpoint${endpointCount !== 1 ? 's' : ''}`}
                  {event.duration != null && ` • Duration: ${formatDuration(event.duration)}`}
                </span>
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

          {renderHeaderRight()}
        </div>

        {isOutputExpanded && (
          <div className="mt-2">
            <div className="bg-card border border-border rounded-md p-3">
              <div className="flex items-center justify-between mb-2">
                <div className="text-xs font-medium text-indigo-700 dark:text-indigo-300">
                  OpenAPI Spec
                </div>
                <div className="text-xs text-muted-foreground">
                  {lineCount} line{lineCount !== 1 ? 's' : ''} • {specLength} chars
                </div>
              </div>
              <pre className="text-xs text-foreground font-mono whitespace-pre-wrap overflow-x-auto bg-muted/50 p-3 rounded max-h-48 overflow-y-auto">
                {resultText || eventResultText}
              </pre>
            </div>
          </div>
        )}
      </div>
    )
  }

  // Handle execute_shell_command tool response
  if (logicalToolName === 'execute_shell_command') {
    // PLAT-160. A synthetic settle (the tool never reported its own end, so
    // the event store closed the chip itself) is not a normal completion —
    // ToolCallEndEvent.tsx already treats it as its own state elsewhere in
    // this app. This branch didn't, so a settled shell command rendered as an
    // ordinary green "Command Completed" with Turn: 0 (a settled event
    // carries no real turn number) and its open-to-settle time labelled
    // "Duration" as if it had been measured.
    const isSyntheticSettle = event.synthetic_settle === true
    const rawOutput = resultText || eventResultText

    // Extract stdout/stderr from parsed JSON if available
    const stdout = typeof parsedResult.stdout === 'string' && parsedResult.stdout
      ? parsedResult.stdout
      : rawOutput
    const stderr = typeof parsedResult.stderr === 'string' ? parsedResult.stderr : ''

    // Check for error indicators - prefer exit_code from JSON if available
    let isError = false
    if (typeof parsedResult.exit_code === 'number') {
      isError = parsedResult.exit_code !== 0
    } else if (rawOutput.trim().startsWith('{')) {
      try {
        const parsed = JSON.parse(rawOutput)
        isError = parsed.exit_code !== undefined ? parsed.exit_code !== 0 : false
      } catch {
        isError = false
      }
    }
    if (!isError) {
      isError = stdout.includes('Traceback') ||
                stdout.includes('command not found') ||
                stdout.includes('Permission denied')
    }

    // Use stderr for errors when available, otherwise stdout
    const output = isError && stderr ? stderr : stdout
    const detectedFormat = isError ? null : detectOutputFormat(output)
    const isFormatted = !!detectedFormat && !isRawMode

    const bgColor = isError
      ? 'bg-red-50 dark:bg-red-900/20 border-red-200 dark:border-red-800'
      : 'bg-muted border-border'

    const textColor = isError
      ? 'text-red-700 dark:text-red-300'
      : 'text-foreground'

    const secondaryTextColor = isError
      ? 'text-red-600 dark:text-red-400'
      : 'text-muted-foreground'

    const statusIcon = isError ? '❌' : isSyntheticSettle ? '○' : '✅'
    const statusText = isError
      ? 'Command Failed'
      : isSyntheticSettle
        ? (output.trim() ? 'Command — result recovered after the fact' : 'Command — no result reported')
        : 'Command Completed'

    return (
      <div className={`${bgColor} border rounded p-2`}>
        <div className="flex items-center justify-between gap-2 py-0.5">
          <div className="flex items-center gap-2 min-w-0 flex-1">
            <div className="min-w-0 flex-1">
              <div className={`text-xs font-medium ${textColor} flex items-center gap-2`}>
                {statusIcon} {statusText}{' '}
                <span className={`text-xs font-normal ${secondaryTextColor}`}>
                  {/* Turn is meaningless on a synthetic settle: the fabricated
                      event carries no turn number, so it always renders 0. */}
                  {!isSyntheticSettle && event.turn !== undefined && `• Turn: ${event.turn}`}
                  {event.duration != null && (isSyntheticSettle
                    ? output.trim()
                      // The backend recovered this from the provider's own
                      // transcript (PLAT-141), so the duration is real —
                      // unlike the no-output case below, this is not
                      // open-to-settle time.
                      ? ` • Runtime (recovered): ${formatDuration(event.duration)}`
                      : ` • Open for: ${formatDuration(event.duration)} (not tool runtime)`
                    : ` • Duration: ${formatDuration(event.duration)}`)}
                </span>
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

          {renderHeaderRight()}
        </div>

        {output.trim() && isOutputExpanded && (
          <div className="mt-2">
            <div className={`border rounded-md p-3 ${isFormatted ? 'bg-card border-border' : 'bg-muted border-border'}`}>
              <div className="flex items-center justify-between mb-2">
                <div className={`text-xs font-medium ${isError ? 'text-red-400' : isFormatted ? 'text-muted-foreground' : 'text-muted-foreground'}`}>
                  {isError ? 'Error Output' : 'Shell Output'}
                </div>
                <div className="flex items-center gap-2">
                  <div className={`text-xs ${isFormatted ? 'text-muted-foreground' : 'text-muted-foreground'}`}>
                    {output.split('\n').length} line{output.split('\n').length !== 1 ? 's' : ''} • {output.length} chars
                  </div>
                  {detectedFormat && (
                    <button
                      onClick={() => setIsRawMode(r => !r)}
                      className={`text-[10px] font-bold px-1.5 py-0.5 rounded border transition-colors ${
                        isFormatted
                          ? 'bg-blue-600 border-blue-500 text-white hover:bg-blue-700'
                          : 'bg-muted border-border text-foreground hover:bg-muted/80'
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
                  <MarkdownRenderer content={output} />
                </div>
              )}
              {isFormatted && detectedFormat === 'json' && (
                <div className="max-h-96 overflow-y-auto overflow-x-auto">
                  <MarkdownRenderer content={'```json\n' + JSON.stringify(JSON.parse(output.trim()), null, 2) + '\n```'} />
                </div>
              )}
              {isFormatted && detectedFormat === 'csv' && (
                <div className="max-h-96 overflow-y-auto overflow-x-auto">
                  <CsvRenderer content={output} />
                </div>
              )}
              {!isFormatted && (
                <pre className={`text-xs font-mono whitespace-pre-wrap overflow-x-auto max-h-96 overflow-y-auto ${
                  isError ? 'text-red-300' : 'text-green-300'
                }`}>
                  {output}
                </pre>
              )}
            </div>
          </div>
        )}
      </div>
    )
  }

  // Handle discover_code_structure tool response
  if (logicalToolName === 'discover_code_structure') {
    // This tool always returns JSON with server/tool structure
    let serverListData: unknown = null

    try {
      const parsed = normalizedResult
      if (isRecord(parsed) && (parsed.servers || parsed.custom_tools || parsed.virtual_tools)) {
        serverListData = parsed
      } else {
        throw new Error('result is not a code-structure response')
      }
    } catch {
      // If it's not valid JSON, show error
      return (
        <div className="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded p-2">
          <div className="flex items-center justify-between gap-3">
            <div className="text-sm font-medium text-red-700 dark:text-red-300">
              ❌ Discovery Error{' '}
              <span className="text-xs font-normal text-red-600 dark:text-red-400">
                {event.turn !== undefined && `• Turn: ${event.turn}`}
                {event.duration != null && ` • Duration: ${formatDuration(event.duration)}`}
              </span>
            </div>
            {renderHeaderRight()}
          </div>
          
          {isOutputExpanded && (
            <div className="mt-2 text-xs text-red-600 dark:text-red-400">
              Invalid JSON response: {eventResultText}
            </div>
          )}
        </div>
      )
    }

    if (serverListData) {
      const data = serverListData as {
        servers?: Array<{ name: string; package: string; tools: string[] }>
        custom_tools?: { package: string; tools: string[] }
        virtual_tools?: { package: string; tools: string[] }
      }

      return (
        <div className="bg-blue-50 dark:bg-blue-900/20 border border-blue-200 dark:border-blue-800 rounded p-2">
          <div className="flex items-center justify-between gap-3">
            <div className="flex items-center gap-3 min-w-0 flex-1">
              <div className="min-w-0 flex-1">
                <div className="text-sm font-medium text-blue-700 dark:text-blue-300 flex items-center gap-2">
                  ✅ Code Structure Discovered{' '}
                  <span className="text-xs font-normal text-blue-600 dark:text-blue-400">
                    {event.turn !== undefined && `• Turn: ${event.turn}`}
                    {event.duration != null && ` • Duration: ${formatDuration(event.duration)}`}
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

            {renderHeaderRight()}
          </div>

          {isOutputExpanded && (
            <div className="mt-2 space-y-3">
              {/* MCP Servers */}
              {data.servers && data.servers.length > 0 && (
                <div className="bg-card border border-border rounded-md p-3">
                  <div className="text-xs font-medium text-blue-700 dark:text-blue-300 mb-2">
                    📦 MCP Servers ({data.servers.length})
                  </div>
                  <div className="space-y-2 max-h-96 overflow-y-auto">
                    {data.servers.map((server, idx) => (
                      <div key={idx} className="border-l-2 border-blue-300 dark:border-blue-700 pl-2">
                        <div className="font-mono text-sm font-semibold text-foreground">
                          {server.name}
                        </div>
                        <div className="text-xs text-muted-foreground mt-1">
                          Package: <span className="font-mono">{server.package}</span>
                        </div>
                        {server.tools && server.tools.length > 0 && (
                          <div className="mt-1">
                            <div className="text-xs text-muted-foreground mb-1">
                              Tools ({server.tools.length}):
                            </div>
                            <div className="flex flex-wrap gap-1">
                              {server.tools.map((tool, toolIdx) => (
                                <span
                                  key={toolIdx}
                                  className="text-xs bg-blue-100 dark:bg-blue-900/30 text-blue-700 dark:text-blue-300 px-2 py-0.5 rounded"
                                >
                                  {tool}
                                </span>
                              ))}
                            </div>
                          </div>
                        )}
                      </div>
                    ))}
                  </div>
                </div>
              )}

              {/* Custom Tools */}
              {data.custom_tools && (
                <div className="bg-card border border-border rounded-md p-3">
                  <div className="text-xs font-medium text-blue-700 dark:text-blue-300 mb-2">
                    🔧 Custom Tools
                  </div>
                  <div className="text-xs text-muted-foreground">
                    Package: <span className="font-mono">{data.custom_tools.package}</span>
                  </div>
                  {data.custom_tools.tools && data.custom_tools.tools.length > 0 && (
                    <div className="flex flex-wrap gap-1 mt-2">
                      {data.custom_tools.tools.map((tool, idx) => (
                        <span
                          key={idx}
                          className="text-xs bg-purple-100 dark:bg-purple-900/30 text-purple-700 dark:text-purple-300 px-2 py-0.5 rounded"
                        >
                          {tool}
                        </span>
                      ))}
                    </div>
                  )}
                </div>
              )}

              {/* Virtual Tools */}
              {data.virtual_tools && (
                <div className="bg-card border border-border rounded-md p-3">
                  <div className="text-xs font-medium text-blue-700 dark:text-blue-300 mb-2">
                    ⚡ Virtual Tools
                  </div>
                  <div className="text-xs text-muted-foreground">
                    Package: <span className="font-mono">{data.virtual_tools.package}</span>
                  </div>
                  {data.virtual_tools.tools && data.virtual_tools.tools.length > 0 && (
                    <div className="flex flex-wrap gap-1 mt-2">
                      {data.virtual_tools.tools.map((tool, idx) => (
                        <span
                          key={idx}
                          className="text-xs bg-green-100 dark:bg-green-900/30 text-green-700 dark:text-green-300 px-2 py-0.5 rounded"
                        >
                          {tool}
                        </span>
                      ))}
                    </div>
                  )}
                </div>
              )}
            </div>
          )}
        </div>
      )
    }
  }

  // Handle discover_code_files tool response
  if (logicalToolName === 'discover_code_files') {
    // Check if result is JSON (server list) or Go code
    let isGoCode = false
    let isServerList = false
    let serverListData: unknown = null

    try {
      const parsed = normalizedResult
      if (isRecord(parsed) && (parsed.servers || parsed.custom_tools || parsed.virtual_tools)) {
        // It's a server list JSON
        isServerList = true
        serverListData = parsed
      }
    } catch {
      // If it's not valid JSON, assume it's Go code
      isGoCode = true
    }

    if (isServerList && serverListData) {
      const data = serverListData as {
        servers?: Array<{ name: string; package: string; tools: string[] }>
        custom_tools?: { package: string; tools: string[] }
        virtual_tools?: { package: string; tools: string[] }
      }

      return (
        <div className="bg-blue-50 dark:bg-blue-900/20 border border-blue-200 dark:border-blue-800 rounded p-2">
          <div className="flex items-center justify-between gap-3">
            <div className="flex items-center gap-3 min-w-0 flex-1">
              <div className="min-w-0 flex-1">
                <div className="text-sm font-medium text-blue-700 dark:text-blue-300 flex items-center gap-2">
                  ✅ Discovery Complete{' '}
                  <span className="text-xs font-normal text-blue-600 dark:text-blue-400">
                    {event.turn !== undefined && `• Turn: ${event.turn}`}
                    {event.tool_name && ` • Tool: ${event.tool_name}`}
                    {event.duration != null && ` • Duration: ${formatDuration(event.duration)}`}
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

            {renderHeaderRight()}
          </div>

          {isOutputExpanded && (
            <div className="mt-2 space-y-3">
              {/* MCP Servers */}
              {data.servers && data.servers.length > 0 && (
                <div className="bg-card border border-border rounded-md p-3">
                  <div className="text-xs font-medium text-blue-700 dark:text-blue-300 mb-2">
                    📦 MCP Servers ({data.servers.length})
                  </div>
                  <div className="space-y-2 max-h-96 overflow-y-auto">
                    {data.servers.map((server, idx) => (
                      <div key={idx} className="border-l-2 border-blue-300 dark:border-blue-700 pl-2">
                        <div className="font-mono text-sm font-semibold text-foreground">
                          {server.name}
                        </div>
                        <div className="text-xs text-muted-foreground mt-1">
                          Package: <span className="font-mono">{server.package}</span>
                        </div>
                        {server.tools && server.tools.length > 0 && (
                          <div className="mt-1">
                            <div className="text-xs text-muted-foreground mb-1">
                              Tools ({server.tools.length}):
                            </div>
                            <div className="flex flex-wrap gap-1">
                              {server.tools.map((tool, toolIdx) => (
                                <span
                                  key={toolIdx}
                                  className="text-xs bg-blue-100 dark:bg-blue-900/30 text-blue-700 dark:text-blue-300 px-2 py-0.5 rounded"
                                >
                                  {tool}
                                </span>
                              ))}
                            </div>
                          </div>
                        )}
                      </div>
                    ))}
                  </div>
                </div>
              )}

              {/* Custom Tools */}
              {data.custom_tools && (
                <div className="bg-card border border-border rounded-md p-3">
                  <div className="text-xs font-medium text-blue-700 dark:text-blue-300 mb-2">
                    🔧 Custom Tools
                  </div>
                  <div className="text-xs text-muted-foreground">
                    Package: <span className="font-mono">{data.custom_tools.package}</span>
                  </div>
                  {data.custom_tools.tools && data.custom_tools.tools.length > 0 && (
                    <div className="flex flex-wrap gap-1 mt-2">
                      {data.custom_tools.tools.map((tool, idx) => (
                        <span
                          key={idx}
                          className="text-xs bg-purple-100 dark:bg-purple-900/30 text-purple-700 dark:text-purple-300 px-2 py-0.5 rounded"
                        >
                          {tool}
                        </span>
                      ))}
                    </div>
                  )}
                </div>
              )}

              {/* Virtual Tools */}
              {data.virtual_tools && (
                <div className="bg-card border border-border rounded-md p-3">
                  <div className="text-xs font-medium text-blue-700 dark:text-blue-300 mb-2">
                    ⚡ Virtual Tools
                  </div>
                  <div className="text-xs text-muted-foreground">
                    Package: <span className="font-mono">{data.virtual_tools.package}</span>
                  </div>
                  {data.virtual_tools.tools && data.virtual_tools.tools.length > 0 && (
                    <div className="flex flex-wrap gap-1 mt-2">
                      {data.virtual_tools.tools.map((tool, idx) => (
                        <span
                          key={idx}
                          className="text-xs bg-green-100 dark:bg-green-900/30 text-green-700 dark:text-green-300 px-2 py-0.5 rounded"
                        >
                          {tool}
                        </span>
                      ))}
                    </div>
                  )}
                </div>
              )}
            </div>
          )}
        </div>
      )
    }

    // It's Go code - display with syntax highlighting
    if (isGoCode || resultText.includes('package ') || resultText.includes('func ')) {
      const codeLines = resultText.split('\n')
      const lineCount = codeLines.length

      return (
        <div className="bg-blue-50 dark:bg-blue-900/20 border border-blue-200 dark:border-blue-800 rounded p-2">
          <div className="flex items-center justify-between gap-2 py-0.5">
            <div className="flex items-center gap-2 min-w-0 flex-1">
              <div className="min-w-0 flex-1">
                <div className="text-xs font-medium text-blue-700 dark:text-blue-300 flex items-center gap-2">
                  ✅ Go Code Retrieved{' '}
                  <span className="text-xs font-normal text-blue-600 dark:text-blue-400">
                    {event.turn !== undefined && `• Turn: ${event.turn}`}
                    {event.duration != null && ` • Duration: ${formatDuration(event.duration)}`}
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

            {renderHeaderRight()}
          </div>

          {isOutputExpanded && (
            <div className="mt-2">
              <div className="bg-card border border-border rounded-md p-3">
                <div className="flex items-center justify-between mb-2">
                  <div className="text-xs font-medium text-blue-700 dark:text-blue-300">
                    💻 Go Source Code
                  </div>
                  <div className="text-xs text-muted-foreground">
                    {lineCount} line{lineCount !== 1 ? 's' : ''} • {resultText.length} chars
                  </div>
                </div>
                <pre className="text-xs text-foreground font-mono whitespace-pre-wrap overflow-x-auto bg-muted/50 p-3 rounded max-h-48 overflow-y-auto">
                  {resultText}
                </pre>
              </div>
            </div>
          )}
        </div>
      )
    }
  }

  // Handle write_code tool response
  if (logicalToolName === 'write_code') {
    // Check if result contains an error - use more precise pattern matching
    // Only check for error patterns at the start or in structured error messages
    const errorPatterns = [
      /^.*\*\*❌ EXECUTION ERROR\*\*$/m,  // Execution error header
      /^.*BUILD ERROR.*$/m,               // Build error
      /^.*PLUGIN LOAD ERROR.*$/m,         // Plugin load error
      /^.*FUNCTION SIGNATURE ERROR.*$/m,  // Function signature error
      /^go run failed:/m,                 // Go run failure at start
      /^Error:.*go run failed/m,         // Error: go run failed
    ]
    
    // Check if result starts with error indicators or contains structured error messages
    const isError = errorPatterns.some(pattern => pattern.test(resultText)) ||
                    (resultText.trim().startsWith('Error:') && resultText.includes('go run')) ||
                    (resultText.includes('**❌ EXECUTION ERROR**'))

    const bgColor = isError 
      ? 'bg-red-50 dark:bg-red-900/20 border-red-200 dark:border-red-800'
      : 'bg-purple-100 dark:bg-purple-900/50 border-purple-200 dark:border-purple-800'
    
    const textColor = isError
      ? 'text-red-700 dark:text-red-300'
      : 'text-purple-700 dark:text-purple-300'
    
    const secondaryTextColor = isError
      ? 'text-red-600 dark:text-red-400'
      : 'text-purple-600 dark:text-purple-400'

    const statusIcon = isError ? '❌' : '✅'
    const statusText = isError ? 'Code Execution Failed' : 'Code Executed Successfully'

    return (
      <div className={`${bgColor} border rounded p-2`}>
        <div className="flex items-center justify-between gap-2 py-0.5">
          <div className="flex items-center gap-2 min-w-0 flex-1">
            <div className="min-w-0 flex-1">
              <div className={`text-xs font-medium ${textColor} flex items-center gap-2`}>
                {statusIcon} {statusText}{' '}
                <span className={`text-xs font-normal ${secondaryTextColor}`}>
                  {event.turn !== undefined && `• Turn: ${event.turn}`}
                  {event.duration != null && ` • Duration: ${formatDuration(event.duration)}`}
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

          {renderHeaderRight()}
        </div>

        {/* Display output or error */}
        {resultText && resultText.trim() && isOutputExpanded && (
          <div className="mt-2">
            <div className={`bg-card border border-border rounded-md p-3`}>
              <div className="flex items-center justify-between mb-2">
                <div className={`text-xs font-medium ${isError ? 'text-red-700 dark:text-red-300' : 'text-purple-700 dark:text-purple-300'}`}>
                  {isError ? '🔨 Error Details' : '📝 Execution Output'}
                </div>
                <div className="text-xs text-muted-foreground">
                  {resultText.length} chars
                </div>
              </div>
              <pre className={`text-xs font-mono whitespace-pre-wrap overflow-x-auto p-3 rounded max-h-96 overflow-y-auto ${ 
                isError 
                  ? 'text-red-800 dark:text-red-200 bg-red-50 dark:bg-red-900/30' 
                  : 'text-foreground bg-muted/50'
              }`}> 
                {resultText}
              </pre>
            </div>
          </div>
        )}
      </div>
    )
  }

  return null
}

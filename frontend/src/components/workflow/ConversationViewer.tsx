import React, { useState, useMemo, useEffect } from 'react'
import {
  Bot,
  User,
  Settings,
  Wrench,
  ChevronDown,
  ChevronRight,
  Code,
  FileText,
  Clock,
  Hash,
  Timer
} from 'lucide-react'
import { cn } from '@/lib/utils'
import { MarkdownRenderer } from '../ui/MarkdownRenderer'
import { formatToolCallResult } from '../../utils/toolCallFormatting'
import { isWebSearchToolCall } from '../../utils/webSearchToolCall'
import { WebSearchToolCallDisplay } from '../events/tools/ToolCallSpecialRender/WebSearchToolCallDisplay'

// Type definitions for conversation structure
export interface ConversationPart {
  Text?: string
  ID?: string
  FunctionCall?: { Name: string; Arguments: string }
  ToolCallID?: string
  Name?: string
  Content?: string
}

export interface ConversationMessage {
  Role: 'system' | 'human' | 'ai' | 'tool'
  Parts: ConversationPart[]
}

interface ConversationData {
  conversation_history: ConversationMessage[]
  llm_calls?: LLMCallTiming[]
  tool_calls?: ToolCallTiming[]
  timing?: TimingData
  llm_call_count?: number
  tool_call_count?: number
}

interface ConversationViewerProps {
  content: string // Raw JSON string
  searchQuery?: string
}

interface ToolCallTiming {
  tool_call_id?: string
  tool_name?: string
  status?: string
  args?: string
  result?: string
  error?: string
  duration_ms?: number
  duration_ns?: number
  timestamp?: string
  started_at?: string
  completed_at?: string
  offset_from_agent_start_ms?: number
}

interface LLMCallTiming {
  turn?: number
  model_id?: string
  status?: string
  error?: string
  started_at?: string
  completed_at?: string
  duration_ms?: number
  duration_ns?: number
  time_to_first_response_ms?: number
  time_to_first_content_ms?: number
  time_to_first_tool_call_ms?: number
  prompt_tokens?: number
  completion_tokens?: number
  total_tokens?: number
  cache_tokens?: number
  reasoning_tokens?: number
  tool_calls?: number
  context_usage_percent?: number
  offset_from_agent_start_ms?: number
}

function failedToolTiming(timing?: ToolCallTiming): boolean {
  const status = timing?.status?.trim().toLowerCase()
  return status === 'error' || status === 'failed' || Boolean(timing?.error)
}

interface TimingData {
  agent?: {
    name?: string
    model?: string
    duration_ms?: number
    llm_call_count?: number
    llm_duration_ms?: number
  }
  llm?: {
    count?: number
    total_duration_ms?: number
    calls?: LLMCallTiming[]
  }
  tools?: {
    count?: number
    total_duration_ms?: number
    calls?: ToolCallTiming[]
  }
}

const asNumber = (value: unknown): number => {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string' && value.trim() !== '') {
    const parsed = Number(value)
    if (Number.isFinite(parsed)) return parsed
  }
  return 0
}

const formatDuration = (durationMs?: number) => {
  const ms = asNumber(durationMs)
  if (ms <= 0) return '0ms'
  if (ms < 1000) return `${Math.round(ms)}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(ms < 10000 ? 1 : 0)}s`
  const minutes = Math.floor(ms / 60000)
  const seconds = Math.round((ms % 60000) / 1000)
  return seconds > 0 ? `${minutes}m ${seconds}s` : `${minutes}m`
}

const formatTokens = (tokens?: number) => {
  const value = asNumber(tokens)
  if (value >= 1000000) return `${(value / 1000000).toFixed(2)}M`
  if (value >= 1000) return `${Math.round(value / 1000)}k`
  return value.toLocaleString()
}

const timingTokenTotal = (call: LLMCallTiming) => (
  asNumber(call.total_tokens) ||
  asNumber(call.prompt_tokens) + asNumber(call.completion_tokens)
)

const TimingChip: React.FC<{ children: React.ReactNode; title?: string }> = ({ children, title }) => (
  <span
    title={title}
    className="inline-flex items-center gap-1 rounded border border-border bg-background/80 px-1.5 py-0.5 text-[9px] font-medium text-muted-foreground"
  >
    {children}
  </span>
)

const ConversationTimingSummary: React.FC<{
  timing?: TimingData
  llmCalls: LLMCallTiming[]
  toolCalls: ToolCallTiming[]
  toolArgsById: Map<string, { name: string; arguments: string }>
  toolResultById: Map<string, string>
  llmMessageByIndex: Map<number, ConversationMessage>
  searchQuery?: string
}> = ({ timing, llmCalls, toolCalls, toolArgsById, toolResultById, llmMessageByIndex, searchQuery }) => {
  const [expandedKeys, setExpandedKeys] = useState<Set<string>>(new Set())
  const [detailsOpen, setDetailsOpen] = useState(false)
  const agentDurationMs = asNumber(timing?.agent?.duration_ms)
  const llmDurationMs = asNumber(timing?.llm?.total_duration_ms) || llmCalls.reduce((sum, call) => sum + asNumber(call.duration_ms), 0)
  const toolDurationMs = asNumber(timing?.tools?.total_duration_ms) || toolCalls.reduce((sum, call) => sum + asNumber(call.duration_ms), 0)
  const inputTokens = llmCalls.reduce((sum, call) => sum + asNumber(call.prompt_tokens), 0)
  const outputTokens = llmCalls.reduce((sum, call) => sum + asNumber(call.completion_tokens), 0)
  const totalTokens = llmCalls.reduce((sum, call) => sum + timingTokenTotal(call), 0)
  const unaccountedMs = Math.max(0, agentDurationMs - llmDurationMs - toolDurationMs)

  const timeline = [
    ...llmCalls.map((call, index) => ({
      key: `llm-${index}`,
      type: 'LLM' as const,
      name: call.model_id || timing?.agent?.model || `LLM call ${index + 1}`,
      status: call.status,
      durationMs: asNumber(call.duration_ms),
      offsetMs: asNumber(call.offset_from_agent_start_ms),
      inputTokens: asNumber(call.prompt_tokens),
      outputTokens: asNumber(call.completion_tokens),
      totalTokens: timingTokenTotal(call),
      toolCalls: asNumber(call.tool_calls),
      toolCallId: undefined as string | undefined,
      llmIndex: index as number | undefined
    })),
    ...toolCalls.map((call, index) => ({
      key: `tool-${call.tool_call_id || index}`,
      type: 'Tool' as const,
      name: call.tool_name || `tool-${index + 1}`,
      status: call.status,
      durationMs: asNumber(call.duration_ms),
      offsetMs: asNumber(call.offset_from_agent_start_ms),
      inputTokens: 0,
      outputTokens: 0,
      totalTokens: 0,
      toolCalls: 0,
      toolCallId: call.tool_call_id as string | undefined,
      llmIndex: undefined as number | undefined
    }))
  ].sort((a, b) => {
    if (a.offsetMs !== b.offsetMs) return a.offsetMs - b.offsetMs
    return b.durationMs - a.durationMs
  })

  const slowest = [...timeline].sort((a, b) => b.durationMs - a.durationMs).slice(0, 5)

  const lowerQuery = searchQuery?.trim().toLowerCase() || ''
  const itemMatchesSearch = (item: (typeof timeline)[number]): boolean => {
    if (!lowerQuery) return false
    if (item.name.toLowerCase().includes(lowerQuery)) return true
    if (item.type === 'Tool' && item.toolCallId) {
      const args = toolArgsById.get(item.toolCallId)
      if (args && (args.name.toLowerCase().includes(lowerQuery) || args.arguments.toLowerCase().includes(lowerQuery))) return true
      const result = toolResultById.get(item.toolCallId)
      if (result && result.toLowerCase().includes(lowerQuery)) return true
    }
    if (item.type === 'LLM' && item.llmIndex !== undefined) {
      const message = llmMessageByIndex.get(item.llmIndex)
      if (message) {
        for (const part of message.Parts) {
          if (part.Text?.toLowerCase().includes(lowerQuery)) return true
          if (part.FunctionCall && (
            part.FunctionCall.Name.toLowerCase().includes(lowerQuery) ||
            part.FunctionCall.Arguments.toLowerCase().includes(lowerQuery)
          )) return true
        }
      }
    }
    return false
  }
  const matchingKeys = lowerQuery ? timeline.filter(itemMatchesSearch).map(item => item.key) : []
  const matchingKeysSignature = matchingKeys.join('|')

  // A search hit can be inside a call's own args/result/text, several clicks
  // deep behind this "Full call timeline" details element and each row's own
  // expand toggle. Open both automatically for every matching row instead of
  // leaving the reader to click through each one to find where the hit is.
  useEffect(() => {
    if (matchingKeys.length === 0) return
    setDetailsOpen(true)
    setExpandedKeys(prev => {
      let changed = false
      const next = new Set(prev)
      for (const key of matchingKeys) {
        if (!next.has(key)) {
          next.add(key)
          changed = true
        }
      }
      return changed ? next : prev
    })
    // matchingKeysSignature is the stable, content-based proxy for matchingKeys
    // (a fresh array every render otherwise).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [matchingKeysSignature])

  if (!timing && llmCalls.length === 0 && toolCalls.length === 0) return null

  return (
    <div className="mb-3 rounded border border-border bg-muted/20 p-2.5">
      <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-1.5 text-[11px] font-semibold text-foreground">
          <Timer className="h-3.5 w-3.5 text-primary" />
          Timing breakdown
        </div>
        {timing?.agent?.name && (
          <span className="text-[10px] text-muted-foreground">{timing.agent.name}</span>
        )}
      </div>

      <div className="grid gap-2 text-[10px] sm:grid-cols-2 lg:grid-cols-4">
        <div className="rounded border border-border bg-background p-2">
          <div className="text-muted-foreground">Wall time</div>
          <div className="mt-0.5 text-sm font-semibold text-foreground">{formatDuration(agentDurationMs)}</div>
        </div>
        <div className="rounded border border-border bg-background p-2">
          <div className="text-muted-foreground">LLM time</div>
          <div className="mt-0.5 text-sm font-semibold text-foreground">{formatDuration(llmDurationMs)}</div>
          <div className="text-muted-foreground">{llmCalls.length} call{llmCalls.length === 1 ? '' : 's'}</div>
        </div>
        <div className="rounded border border-border bg-background p-2">
          <div className="text-muted-foreground">Tool time</div>
          <div className="mt-0.5 text-sm font-semibold text-foreground">{formatDuration(toolDurationMs)}</div>
          <div className="text-muted-foreground">{toolCalls.length} call{toolCalls.length === 1 ? '' : 's'}</div>
        </div>
        <div className="rounded border border-border bg-background p-2">
          <div className="text-muted-foreground">Tokens</div>
          <div className="mt-0.5 text-sm font-semibold text-foreground">{formatTokens(totalTokens)}</div>
          <div className="text-muted-foreground">In {formatTokens(inputTokens)} / Out {formatTokens(outputTokens)}</div>
        </div>
      </div>

      {agentDurationMs > 0 && (
        <div className="mt-2 flex h-2 overflow-hidden rounded bg-background">
          <div className="bg-blue-500/70" style={{ width: `${Math.min(100, (llmDurationMs / agentDurationMs) * 100)}%` }} title={`LLM ${formatDuration(llmDurationMs)}`} />
          <div className="bg-amber-500/70" style={{ width: `${Math.min(100, (toolDurationMs / agentDurationMs) * 100)}%` }} title={`Tools ${formatDuration(toolDurationMs)}`} />
          <div className="bg-muted-foreground/30" style={{ width: `${Math.min(100, (unaccountedMs / agentDurationMs) * 100)}%` }} title={`Other ${formatDuration(unaccountedMs)}`} />
        </div>
      )}

      {slowest.length > 0 && (
        <div className="mt-3">
          <div className="mb-1.5 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">Slowest calls</div>
          <div className="space-y-1">
            {slowest.map(item => (
              <div key={item.key} className="flex flex-wrap items-center gap-1.5 rounded border border-border bg-background px-2 py-1.5 text-[10px]">
                <span className={cn('rounded px-1.5 py-0.5 font-semibold', item.type === 'LLM' ? 'bg-blue-500/10 text-blue-600 dark:text-blue-300' : 'bg-amber-500/10 text-amber-700 dark:text-amber-300')}>
                  {item.type}
                </span>
                <span className="min-w-0 flex-1 truncate font-mono text-foreground">{item.name}</span>
                <span className="text-muted-foreground">@ {formatDuration(item.offsetMs)}</span>
                <span className="font-semibold text-foreground">{formatDuration(item.durationMs)}</span>
                {item.type === 'LLM' && (
                  <span className="text-muted-foreground">In {formatTokens(item.inputTokens)} / Out {formatTokens(item.outputTokens)}</span>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      {timeline.length > 0 && (
        <details
          className="mt-3 rounded border border-border bg-background"
          open={detailsOpen}
          onToggle={(e) => setDetailsOpen(e.currentTarget.open)}
        >
          <summary className="cursor-pointer px-2 py-1.5 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground hover:text-foreground">
            Full call timeline ({timeline.length})
          </summary>
          <div className="max-h-72 overflow-y-auto border-t border-border">
            {timeline.map((item, index) => {
              const rowMatches = lowerQuery ? itemMatchesSearch(item) : false
              const isExpanded = expandedKeys.has(item.key)
              const toolArgs = item.toolCallId ? toolArgsById.get(item.toolCallId) : undefined
              const toolResult = item.toolCallId ? toolResultById.get(item.toolCallId) : undefined
              const llmMessage = item.llmIndex !== undefined ? llmMessageByIndex.get(item.llmIndex) : undefined
              const canExpand = item.type === 'Tool' ? Boolean(toolArgs || toolResult !== undefined) : Boolean(llmMessage)
              return (
                <div key={item.key} className={cn('border-b border-border/70 last:border-b-0', rowMatches && 'bg-amber-500/[0.06]')}>
                  <button
                    type="button"
                    onClick={() => canExpand && setExpandedKeys(prev => {
                      const next = new Set(prev)
                      if (next.has(item.key)) next.delete(item.key)
                      else next.add(item.key)
                      return next
                    })}
                    className={cn(
                      'grid w-full grid-cols-[1.25rem_2rem_minmax(0,1fr)_auto] items-center gap-2 px-2 py-1.5 text-left text-[10px]',
                      canExpand && 'hover:bg-accent/40'
                    )}
                  >
                    {canExpand ? (
                      isExpanded ? <ChevronDown className="h-2.5 w-2.5 text-muted-foreground" /> : <ChevronRight className="h-2.5 w-2.5 text-muted-foreground" />
                    ) : <span />}
                    <span className="font-mono text-muted-foreground">#{index + 1}</span>
                    <div className="min-w-0">
                      <div className="flex min-w-0 items-center gap-1.5">
                        <span className={cn('rounded px-1 py-0.5 font-semibold', item.type === 'LLM' ? 'bg-blue-500/10 text-blue-600 dark:text-blue-300' : 'bg-amber-500/10 text-amber-700 dark:text-amber-300')}>
                          {item.type}
                        </span>
                        <span className="truncate font-mono text-foreground">{item.name}</span>
                        {item.status && <span className="text-muted-foreground">{item.status}</span>}
                        {rowMatches && <span className="rounded bg-amber-500/20 px-1 py-0.5 text-[9px] font-medium text-amber-700 dark:text-amber-300">match</span>}
                      </div>
                      <div className="mt-0.5 flex flex-wrap gap-2 text-muted-foreground">
                        <span>start {formatDuration(item.offsetMs)}</span>
                        {item.type === 'LLM' && (
                          <>
                            <span>In {formatTokens(item.inputTokens)}</span>
                            <span>Out {formatTokens(item.outputTokens)}</span>
                            {item.toolCalls > 0 && <span>{item.toolCalls} tool call{item.toolCalls === 1 ? '' : 's'}</span>}
                          </>
                        )}
                      </div>
                    </div>
                    <span className="font-semibold text-foreground">{formatDuration(item.durationMs)}</span>
                  </button>
                  {isExpanded && item.type === 'Tool' && (
                    <div className="px-2 pb-2 pl-8">
                      {toolArgs && (
                        <ConversationToolCallDisplay name={toolArgs.name} arguments={toolArgs.arguments} callId={item.toolCallId} forceExpanded={rowMatches} />
                      )}
                      {toolResult !== undefined && (
                        <ConversationToolResponseDisplay toolName={item.name} toolCallId={item.toolCallId} toolArgs={toolArgs?.arguments} content={toolResult} forceExpanded={rowMatches} />
                      )}
                    </div>
                  )}
                  {isExpanded && item.type === 'LLM' && llmMessage && (
                    <div className="px-2 pb-2 pl-8">
                      {llmMessage.Parts.map((part, partIndex) => {
                        if (part.Text) {
                          return <CollapsibleContent key={partIndex} content={part.Text} defaultExpanded={rowMatches} maxPreviewLength={1000} className="text-[11px] text-foreground" />
                        }
                        if (part.FunctionCall) {
                          return <ConversationToolCallDisplay key={partIndex} name={part.FunctionCall.Name} arguments={part.FunctionCall.Arguments} callId={part.ID} forceExpanded={rowMatches} />
                        }
                        return null
                      })}
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        </details>
      )}
    </div>
  )
}

// Role-based styling configuration
const roleConfig = {
  system: {
    icon: Settings,
    label: 'System',
    bgClass: 'bg-purple-50 dark:bg-purple-950/30',
    borderClass: 'border-purple-200 dark:border-purple-800',
    iconClass: 'text-purple-500',
    labelClass: 'text-purple-700 dark:text-purple-300'
  },
  human: {
    icon: User,
    label: 'User',
    bgClass: 'bg-blue-50 dark:bg-blue-950/30',
    borderClass: 'border-blue-200 dark:border-blue-800',
    iconClass: 'text-blue-500',
    labelClass: 'text-blue-700 dark:text-blue-300'
  },
  ai: {
    icon: Bot,
    label: 'Assistant',
    bgClass: 'bg-green-50 dark:bg-green-950/30',
    borderClass: 'border-green-200 dark:border-green-800',
    iconClass: 'text-green-500',
    labelClass: 'text-green-700 dark:text-green-300'
  },
  tool: {
    icon: Wrench,
    label: 'Tool',
    bgClass: 'bg-orange-50 dark:bg-orange-950/30',
    borderClass: 'border-orange-200 dark:border-orange-800',
    iconClass: 'text-orange-500',
    labelClass: 'text-orange-700 dark:text-orange-300'
  }
}

// Collapsible content component
const CollapsibleContent: React.FC<{
  content: string
  defaultExpanded?: boolean
  maxPreviewLength?: number
  className?: string
  isJson?: boolean
}> = ({ content, defaultExpanded = false, maxPreviewLength = 200, className, isJson = false }) => {
  const [isExpanded, setIsExpanded] = useState(defaultExpanded)
  // defaultExpanded flips true once a match is found in an already-mounted
  // instance (search text refined while this block was still collapsed) --
  // useState's initial value alone would miss that, so sync on the way up.
  useEffect(() => {
    if (defaultExpanded) setIsExpanded(true)
  }, [defaultExpanded])
  const shouldCollapse = content.length > maxPreviewLength

  if (!shouldCollapse) {
    return isJson ? (
      <pre className={cn('whitespace-pre-wrap text-[10px] font-mono', className)}>{content}</pre>
    ) : (
      <div className={className}>
        <MarkdownRenderer content={content} className="!text-[11px] [&_p]:!text-[11px] [&_li]:!text-[11px] [&_h1]:!text-base [&_h2]:!text-sm [&_h3]:!text-xs [&_code]:!text-[10px]" />
      </div>
    )
  }

  return (
    <div>
      <div className={cn('overflow-hidden', !isExpanded && 'max-h-20')}>
        {isJson ? (
          <pre className={cn('whitespace-pre-wrap text-[10px] font-mono', className)}>{content}</pre>
        ) : (
          <div className={className}>
            <MarkdownRenderer content={content} className="!text-[11px] [&_p]:!text-[11px] [&_li]:!text-[11px] [&_h1]:!text-base [&_h2]:!text-sm [&_h3]:!text-xs [&_code]:!text-[10px]" />
          </div>
        )}
      </div>
      <button
        onClick={() => setIsExpanded(!isExpanded)}
        className="flex items-center gap-1 mt-1.5 text-[10px] font-medium text-muted-foreground hover:text-foreground transition-colors"
      >
        {isExpanded ? (
          <>
            <ChevronDown className="w-2.5 h-2.5" /> Show less
          </>
        ) : (
          <>
            <ChevronRight className="w-2.5 h-2.5" /> Show more ({content.length} chars)
          </>
        )}
      </button>
    </div>
  )
}

// Tool call display component
export const ConversationToolCallDisplay: React.FC<{
  name: string
  arguments: string
  callId?: string
  timing?: ToolCallTiming
  /** Auto-opens Args once (e.g. it matched an active search) without taking
   * away the user's own ability to collapse it again afterward. */
  forceExpanded?: boolean
}> = ({ name, arguments: args, callId, timing, forceExpanded }) => {
  const [showArgs, setShowArgs] = useState(Boolean(forceExpanded))
  useEffect(() => {
    if (forceExpanded) setShowArgs(true)
  }, [forceExpanded])
  const isError = failedToolTiming(timing)

  let formattedArgs = args
  try {
    const parsed = JSON.parse(args)
    formattedArgs = JSON.stringify(parsed, null, 2)
  } catch {
    // Keep original if not valid JSON
  }

  return (
    <div className={cn('rounded border p-1.5 my-1', isError ? 'border-red-300 bg-red-50 dark:border-red-900 dark:bg-red-950/25' : 'bg-muted/50 border-border')}>
      <div className="flex items-center gap-1.5">
        <Code className={cn('w-3 h-3 flex-shrink-0', isError ? 'text-red-500' : 'text-orange-500')} />
        <span className="font-mono text-[11px] font-semibold text-foreground">{name}</span>
        {isError && <span className="rounded bg-red-100 px-1 py-0.5 text-[9px] font-medium text-red-700 dark:bg-red-950 dark:text-red-300">failed</span>}
        {callId && (
          <span className="text-[9px] font-mono text-muted-foreground bg-muted px-1 py-0.5 rounded">
            {callId}
          </span>
        )}
        {timing && (
          <>
            <TimingChip title="Tool duration">
              <Clock className="h-2.5 w-2.5" />
              {formatDuration(timing.duration_ms)}
            </TimingChip>
            {timing.status && (
              <TimingChip title="Tool status">{timing.status}</TimingChip>
            )}
          </>
        )}
        <button
          onClick={() => setShowArgs(!showArgs)}
          className="ml-auto flex items-center gap-1 text-[10px] text-muted-foreground hover:text-foreground transition-colors"
        >
          {showArgs ? <ChevronDown className="w-2.5 h-2.5" /> : <ChevronRight className="w-2.5 h-2.5" />}
          Args
        </button>
      </div>
      {showArgs && (
        <pre className="mt-1.5 p-1.5 bg-background rounded text-[10px] font-mono overflow-x-auto max-h-48 overflow-y-auto text-muted-foreground">
          {formattedArgs}
        </pre>
      )}
    </div>
  )
}

// Tool response display component
export const ConversationToolResponseDisplay: React.FC<{
  toolName?: string
  toolCallId?: string
  /** The call's arguments, when known: a web search shows its query from them. */
  toolArgs?: string
  content: string
  timing?: ToolCallTiming
  /** Auto-opens the result once (e.g. it matched an active search) without
   * taking away the user's own ability to collapse it again afterward. */
  forceExpanded?: boolean
}> = ({ toolName, toolCallId, toolArgs, content, timing, forceExpanded }) => {
  const [showContent, setShowContent] = useState(Boolean(forceExpanded))
  useEffect(() => {
    if (forceExpanded) setShowContent(true)
  }, [forceExpanded])
  const resultFormatting = useMemo(() => formatToolCallResult(content), [content])
  const isError = resultFormatting.isError || failedToolTiming(timing)

  if (toolName && isWebSearchToolCall(toolName, toolArgs)) {
    return (
      <div className="my-1">
        <WebSearchToolCallDisplay name={toolName} args={toolArgs} result={content} status={isError ? 'error' : 'ok'} open={showContent} onToggle={() => setShowContent(value => !value)} />
      </div>
    )
  }

  const displayContent = resultFormatting.text

  const isLongContent = displayContent.length > 300

  return (
    <div className={cn('rounded border p-1.5 my-1', isError ? 'border-red-300 bg-red-50 dark:border-red-900 dark:bg-red-950/25' : 'bg-muted/30 border-border')}>
      <div className="flex items-center gap-1.5">
        <FileText className={cn('w-3 h-3 flex-shrink-0', isError ? 'text-red-500' : 'text-orange-500')} />
        {toolName && (
          <span className="font-mono text-[11px] font-medium text-foreground">{toolName}</span>
        )}
        {toolCallId && (
          <span className="text-[9px] font-mono text-muted-foreground bg-muted px-1 py-0.5 rounded">
            {toolCallId}
          </span>
        )}
        {isError && <span className="rounded bg-red-100 px-1 py-0.5 text-[9px] font-medium text-red-700 dark:bg-red-950 dark:text-red-300">failed</span>}
        {timing && (
          <TimingChip title="Tool duration">
            <Clock className="h-2.5 w-2.5" />
            {formatDuration(timing.duration_ms)}
          </TimingChip>
        )}
        {isLongContent && (
          <button
            onClick={() => setShowContent(!showContent)}
            className="ml-auto flex items-center gap-1 text-[10px] text-muted-foreground hover:text-foreground transition-colors"
          >
            {showContent ? <ChevronDown className="w-2.5 h-2.5" /> : <ChevronRight className="w-2.5 h-2.5" />}
            {showContent ? 'Hide' : 'Show'}
          </button>
        )}
      </div>
      {(isLongContent ? showContent : true) && (
        <pre className={cn('mt-1.5 p-1.5 rounded text-[10px] font-mono overflow-x-auto max-h-48 overflow-y-auto whitespace-pre-wrap', isError ? 'border border-red-200 bg-red-100/60 text-red-800 dark:border-red-900 dark:bg-red-950/50 dark:text-red-200' : 'bg-background text-muted-foreground')}>
          {displayContent}
        </pre>
      )}
    </div>
  )
}

// Message component
export const ConversationMessageDisplay: React.FC<{
  message: ConversationMessage
  index: number
  showIndex?: boolean
  llmCall?: LLMCallTiming
  toolTimingById: Map<string, ToolCallTiming>
  toolTimingByName: Map<string, ToolCallTiming[]>
  /** Call arguments by tool call id, so a tool response can show what was asked (web search query). */
  toolArgsById?: Map<string, { name: string; arguments: string }>
  searchQuery?: string
}> = ({ message, index, showIndex = true, llmCall, toolTimingById, toolTimingByName, toolArgsById, searchQuery }) => {
  const config = roleConfig[message.Role] || roleConfig.system
  const Icon = config.icon
  const lowerQuery = searchQuery?.trim().toLowerCase() || ''

  // Render parts based on their content
  const renderParts = () => {
    return message.Parts.map((part, partIndex) => {
      // Text content
      if (part.Text) {
        const isSystem = message.Role === 'system'
        const matches = lowerQuery ? part.Text.toLowerCase().includes(lowerQuery) : false
        return (
          <CollapsibleContent
            key={partIndex}
            content={part.Text}
            defaultExpanded={!isSystem || matches}
            maxPreviewLength={isSystem ? 500 : 1000}
            className="text-[11px] text-foreground"
          />
        )
      }

      // Tool call (function call from AI)
      if (part.FunctionCall) {
        const timing = part.ID
          ? toolTimingById.get(part.ID)
          : toolTimingByName.get(part.FunctionCall.Name)?.[0]
        const matches = lowerQuery ? (
          part.FunctionCall.Name.toLowerCase().includes(lowerQuery) ||
          part.FunctionCall.Arguments.toLowerCase().includes(lowerQuery)
        ) : false
        return (
          <ConversationToolCallDisplay
            key={partIndex}
            name={part.FunctionCall.Name}
            arguments={part.FunctionCall.Arguments}
            callId={part.ID}
            timing={timing}
            forceExpanded={matches}
          />
        )
      }

      // Tool response
      if (part.ToolCallID || part.Content !== undefined) {
        const timing = part.ToolCallID
          ? toolTimingById.get(part.ToolCallID)
          : part.Name
            ? toolTimingByName.get(part.Name)?.[0]
            : undefined
        const matches = lowerQuery ? (part.Content || '').toLowerCase().includes(lowerQuery) : false
        return (
          <ConversationToolResponseDisplay
            key={partIndex}
            toolName={part.Name}
            toolCallId={part.ToolCallID}
            toolArgs={part.ToolCallID ? toolArgsById?.get(part.ToolCallID)?.arguments : undefined}
            content={part.Content || ''}
            timing={timing}
            forceExpanded={matches}
          />
        )
      }

      return null
    })
  }

  return (
    <div
      className={cn(
        'rounded border p-2 mb-2',
        config.bgClass,
        config.borderClass
      )}
    >
      <div className="flex items-center gap-1.5 mb-1.5">
        <Icon className={cn('w-3 h-3', config.iconClass)} />
        <span className={cn('text-[10px] font-semibold uppercase tracking-wider', config.labelClass)}>
          {config.label}
        </span>
        {showIndex && <span className="text-[9px] text-muted-foreground">#{index + 1}</span>}
        {llmCall && (
          <div className="ml-auto flex flex-wrap items-center justify-end gap-1">
            <TimingChip title="LLM duration">
              <Clock className="h-2.5 w-2.5" />
              {formatDuration(llmCall.duration_ms)}
            </TimingChip>
            <TimingChip title="Input and output tokens">
              <Hash className="h-2.5 w-2.5" />
              In {formatTokens(llmCall.prompt_tokens)} / Out {formatTokens(llmCall.completion_tokens)}
            </TimingChip>
            {asNumber(llmCall.tool_calls) > 0 && (
              <TimingChip title="Tool calls requested by this LLM call">
                {llmCall.tool_calls} tools
              </TimingChip>
            )}
          </div>
        )}
      </div>
      <div className="space-y-1.5">{renderParts()}</div>
    </div>
  )
}

// Main ConversationViewer component
export const ConversationViewer: React.FC<ConversationViewerProps> = ({ content, searchQuery }) => {
  const [showRawJson, setShowRawJson] = useState(false)

  // Parse the conversation JSON
  const { messages, parseError, timing, llmCalls, toolCalls } = useMemo(() => {
    try {
      const data: ConversationData = JSON.parse(content)
      if (data.conversation_history && Array.isArray(data.conversation_history)) {
        const parsedTiming = data.timing
        return {
          messages: data.conversation_history,
          parseError: null,
          timing: parsedTiming,
          llmCalls: parsedTiming?.llm?.calls || data.llm_calls || [],
          toolCalls: parsedTiming?.tools?.calls || data.tool_calls || []
        }
      }
      return { messages: null, parseError: 'Invalid conversation format: missing conversation_history', timing: undefined, llmCalls: [], toolCalls: [] }
    } catch (e) {
      return { messages: null, parseError: `Failed to parse conversation: ${e}`, timing: undefined, llmCalls: [], toolCalls: [] }
    }
  }, [content])

  const messageLLMCallByIndex = useMemo(() => {
    const byIndex = new Map<number, LLMCallTiming>()
    if (!messages) return byIndex
    let aiIndex = 0
    messages.forEach((message, index) => {
      if (message.Role !== 'ai') return
      const call = llmCalls[aiIndex]
      if (call) byIndex.set(index, call)
      aiIndex += 1
    })
    return byIndex
  }, [messages, llmCalls])

  const { toolTimingById, toolTimingByName } = useMemo(() => {
    const byId = new Map<string, ToolCallTiming>()
    const byName = new Map<string, ToolCallTiming[]>()
    toolCalls.forEach(call => {
      if (call.tool_call_id) byId.set(call.tool_call_id, call)
      if (call.tool_name) {
        const items = byName.get(call.tool_name) || []
        items.push(call)
        byName.set(call.tool_name, items)
      }
    })
    return { toolTimingById: byId, toolTimingByName: byName }
  }, [toolCalls])

  // For the compact "Full call timeline" summary: match each timeline row
  // back to its actual call/response content in conversation_history, so a
  // row can be expanded in place instead of only showing timing numbers.
  const { toolArgsById, toolResultById, llmMessageByIndex } = useMemo(() => {
    const argsById = new Map<string, { name: string; arguments: string }>()
    const resultById = new Map<string, string>()
    const byLLMIndex = new Map<number, ConversationMessage>()
    if (!messages) return { toolArgsById: argsById, toolResultById: resultById, llmMessageByIndex: byLLMIndex }
    let aiIndex = 0
    messages.forEach(message => {
      if (message.Role === 'ai') {
        byLLMIndex.set(aiIndex, message)
        aiIndex += 1
      }
      message.Parts.forEach(part => {
        if (part.FunctionCall && part.ID) {
          argsById.set(part.ID, { name: part.FunctionCall.Name, arguments: part.FunctionCall.Arguments })
        }
        if (part.ToolCallID && part.Content !== undefined) {
          resultById.set(part.ToolCallID, part.Content)
        }
      })
    })
    return { toolArgsById: argsById, toolResultById: resultById, llmMessageByIndex: byLLMIndex }
  }, [messages])

  // Filter messages based on search query
  const filteredMessages = useMemo(() => {
    if (!messages || !searchQuery) return messages
    const lowerQuery = searchQuery.toLowerCase()
    
    return messages.filter(msg => {
      // Check if any part matches
      return msg.Parts.some(part => {
        if (part.Text?.toLowerCase().includes(lowerQuery)) return true
        if (part.FunctionCall) {
          if (part.FunctionCall.Name.toLowerCase().includes(lowerQuery)) return true
          if (part.FunctionCall.Arguments.toLowerCase().includes(lowerQuery)) return true
        }
        if (part.Content?.toLowerCase().includes(lowerQuery)) return true
        if (part.Name?.toLowerCase().includes(lowerQuery)) return true
        return false
      })
    })
  }, [messages, searchQuery])

  // If parsing failed, show raw JSON with error message
  if (parseError || !messages) {
    return (
      <div>
        <div className="text-[10px] text-amber-600 dark:text-amber-400 mb-1.5 flex items-center gap-1">
          <Settings className="w-2.5 h-2.5" />
          {parseError || 'Could not parse conversation format'}
        </div>
        <pre className="whitespace-pre-wrap overflow-x-auto text-muted-foreground max-h-80 overflow-y-auto text-[10px] font-mono">
          {content}
        </pre>
      </div>
    )
  }

  return (
    <div>
      {/* Toggle button */}
      <div className="flex justify-end mb-2">
        <button
          onClick={() => setShowRawJson(!showRawJson)}
          className="flex items-center gap-1 px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground hover:text-foreground bg-muted hover:bg-muted/80 rounded transition-colors"
        >
          <Code className="w-2.5 h-2.5" />
          {showRawJson ? 'Formatted' : 'Raw JSON'}
        </button>
      </div>

      {showRawJson ? (
        <pre className="whitespace-pre-wrap overflow-x-auto text-muted-foreground max-h-80 overflow-y-auto text-[10px] font-mono bg-muted/30 p-2 rounded border border-border">
          {content}
        </pre>
      ) : (
        <div className="max-h-[60vh] overflow-y-auto pr-1">
          <ConversationTimingSummary
            timing={timing}
            llmCalls={llmCalls}
            toolCalls={toolCalls}
            toolArgsById={toolArgsById}
            toolResultById={toolResultById}
            llmMessageByIndex={llmMessageByIndex}
            searchQuery={searchQuery}
          />
          {filteredMessages?.map((message) => {
            const originalIndex = messages.indexOf(message)
            return (
              <ConversationMessageDisplay
                key={originalIndex}
                message={message}
                index={originalIndex}
                llmCall={messageLLMCallByIndex.get(originalIndex)}
                toolTimingById={toolTimingById}
                toolTimingByName={toolTimingByName}
                toolArgsById={toolArgsById}
                searchQuery={searchQuery}
              />
            )
          })}
          {filteredMessages?.length === 0 && searchQuery && (
             <div className="text-center py-8 text-muted-foreground text-xs italic">
               No messages match "{searchQuery}"
             </div>
          )}
        </div>
      )}
    </div>
  )
}

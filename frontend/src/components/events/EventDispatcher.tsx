import React from 'react'
import type { PollingEvent } from '../../services/api-types'
import { EventWithOrchestratorContext } from './common/EventWithOrchestratorContext'

// Import the type-safe helpers from the new event-types module
import {
  isEventType,
  getEventData,
  type TodoTaskRouteSelectedEvent,
  type TodoTaskStepCompletedEvent,
} from '../../generated/event-types'

// Import from the new organized component structure
import {
  AgentErrorEventDisplay,
  LLMGenerationWithRetryEventDisplay,
  AgentStartEventComponent,
  AgentEndEventComponent
} from './agents'

import {
  ConversationStartEventDisplay,
  ConversationEndEventDisplay,
  ConversationErrorEventDisplay,
  ConversationTurnEventDisplay,
  ConversationThinkingEventDisplay,
} from './conversation'

import {
  LLMGenerationStartEventDisplay,
  LLMGenerationEndEventDisplay,
  LLMGenerationErrorEventDisplay,
} from './llm'

import {
  ToolCallStartEventDisplay,
  ToolCallEndEventDisplay,
  ToolCallErrorEventDisplay
} from './tools'

import {
  ContextCompactionEventDisplay,
  StatusLineEventDisplay,
  UserMessageEventDisplay
} from './system'
import type { UserMessageEvent } from '../../generated/events'

import {
  OrchestratorEndEventDisplay,
  OrchestratorAgentEndEventDisplay,
  OrchestratorAgentErrorEventDisplay,
  RoutingEvaluatedEventDisplay,
  PreValidationCompletedEventDisplay,
  TodoTaskRouteSelectedEventDisplay,
  TodoTaskStepCompletedEventDisplay,
} from './orchestrator'
import { StepTokenUsageEventDisplay } from './orchestrator/StepTokenUsageEvent'
import { VariablesExtractedEventDisplay } from './orchestrator/VariablesExtractedEvent'

import {
  BatchExecutionCanceledEventDisplay
} from './workflow'

import {
  TokenUsageEventDisplay,
  RetryAttemptEventDisplay,
  BrokenPipeEventDisplay,
  LargeToolOutputDetectedEventDisplay,
  LargeToolOutputFileWrittenEventDisplay,
  MaxTurnsReachedEventDisplay,
  ContextCancelledEventDisplay
} from './debug'
import { UnifiedCompletionEventDisplay } from './debug/UnifiedCompletionEvent'
import { HumanVerificationDisplay } from './HumanVerificationDisplay'
import { BlockingHumanFeedbackDisplay } from './BlockingHumanFeedbackDisplay'
import { PlanApprovalDisplay } from './PlanApprovalDisplay'
import { useChatStore } from '../../stores/useChatStore'
import { MarkdownRenderer } from '../ui/MarkdownRenderer'
import { backgroundAgentCompletionSummary } from '../../utils/backgroundAgentSummary'
import { summarizeBackgroundTaskMessage } from '../../utils/cleanConversation'
// getTerminalOwnerPayload / getOwnedTerminalOwnerKeys moved to
// utils/eventOwnership.ts (pure, no React/store imports) so anything that only
// needs event-ownership logic — the terminal transcript's event scoping, its
// unit tests — can import it without pulling in this component's runtime
// dependencies. Re-exported below for existing importers of this module.

// Sub-agent live streaming text display (subscribes to delegation streaming store independently)
const DelegationStreamingCard: React.FC<{ delegationId: string }> = ({ delegationId }) => {
  const text = useChatStore(state => state.delegationStreamingText[delegationId] || '')
  if (!text) return null
  return (
    <div className="mt-2 border border-blue-200 dark:border-blue-800 bg-blue-50 dark:bg-blue-900/20 rounded p-2">
      <div className="flex items-center gap-1.5 mb-1">
        <div className="w-1.5 h-1.5 bg-blue-500 rounded-full animate-pulse" />
        <span className="text-[10px] text-blue-600 dark:text-blue-400 font-medium">
          Working...
        </span>
      </div>
      <div className="text-xs max-h-60 overflow-y-auto custom-scrollbar overscroll-y-contain">
        <MarkdownRenderer content={text} className="text-xs" />
        <span className="inline-block w-1.5 h-3 bg-blue-500 animate-pulse ml-0.5" />
      </div>
    </div>
  )
}

// Live elapsed timer for running delegation events
const ElapsedTimer: React.FC<{ startTimestamp: string; className?: string }> = ({ startTimestamp, className }) => {
  const [elapsed, setElapsed] = React.useState('')

  React.useEffect(() => {
    const startTime = new Date(startTimestamp).getTime()
    if (isNaN(startTime)) return

    const update = () => {
      const seconds = Math.floor((Date.now() - startTime) / 1000)
      if (seconds < 60) {
        setElapsed(`${seconds}s`)
      } else {
        const m = Math.floor(seconds / 60)
        const s = seconds % 60
        setElapsed(`${m}m${s.toString().padStart(2, '0')}s`)
      }
    }
    update()
    const interval = setInterval(update, 1000)
    return () => clearInterval(interval)
  }, [startTimestamp])

  if (!elapsed) return null
  return <span className={className}>{elapsed}</span>
}

interface EventDispatcherProps {
  event: PollingEvent
  mode?: 'compact' | 'detailed'
  onApproveWorkflow?: (requestId: string) => void
  onSubmitFeedback?: (requestId: string, feedback: string) => void
  onFeedbackSubmitted?: () => void
  onSendMessage?: (msg: string) => void
  isApproving?: boolean
  compact?: boolean
  // The orchestrator-phase/agent-name/step badge (OrchestratorContext) exists
  // to orient a reader who is looking at ONE event pulled out of a deep tree,
  // far from its owning node's header. A flat, single-terminal transcript has
  // no such ambiguity — every event in it already shares the same owner,
  // shown once in the terminal's own header — so the badge would just repeat
  // identically on every card. Callers that render a whole terminal's worth
  // of events in one flat list (TerminalEventTranscript) set this to true.
  hideOrchestratorContext?: boolean
}

// Stable compact styling wrapper — defined outside EventDispatcher to prevent
// component identity changes on re-render (which would unmount children and lose state).
const CompactWrapper: React.FC<{ compact?: boolean; children: React.ReactNode }> = ({ compact, children }) => {
  if (!compact) return <>{children}</>
  return <div className="text-xs [&>*]:text-xs [&_h1]:!text-sm [&_h2]:!text-xs [&_h3]:!text-[11px] [&_p]:!text-xs [&_code]:!text-[10px] [&_span]:!text-xs [&_div]:!text-xs">{children}</div>
}

function getDelegationDisplayTitle(instruction: string): string {
  const firstLine = instruction
    .split(/\r?\n/)
    .map(line => line.trim())
    .find(Boolean)

  if (!firstLine) return 'Delegated task'

  let title = firstLine
    .replace(/^#+\s*/, '')
    .replace(/^\*\*(.*)\*\*$/, '$1')
    .replace(/^(your\s+task|task|objective)\s*:\s*/i, '')
    .replace(/\s*\([^)]*\)\s*$/, '')
    .trim()

  if (!title) title = 'Delegated task'
  return title.length > 64 ? `${title.slice(0, 63)}...` : title
}

function titleCaseIdentifier(value: string): string {
  return value
    .replace(/[-_]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
    .replace(/\b\w+/g, word => {
      const lower = word.toLowerCase()
      if (lower === 'kb') return 'KB'
      if (lower === 'id') return 'ID'
      if (lower === 'api') return 'API'
      return `${lower.charAt(0).toUpperCase()}${lower.slice(1)}`
    })
}

function getBackgroundExecutionDisplayName(rawName: string): string {
  const stripped = rawName.replace(/^Planner:\s*/i, '').trim()
  if (!stripped) return 'Task'

  if (stripped.toLowerCase().startsWith('full-workflow')) {
    return stripped.replace(/^full-workflow/i, 'Full automation')
  }

  const workflowPrefix = 'Workflow step ->'
  if (!stripped.startsWith(workflowPrefix)) return titleCaseIdentifier(stripped)

  let stepName = stripped.slice(workflowPrefix.length).trim()
  if (/^workflow-full-[a-z0-9]+-step-\d+-[a-z0-9]+$/i.test(stepName) || /^workflow-step-\d+-[a-z0-9]+$/i.test(stepName)) {
    return 'Automation step'
  }
  const executionMarker = stepName.lastIndexOf('execution-')
  if (executionMarker >= 0) {
    stepName = stepName.slice(executionMarker + 'execution-'.length)
  } else {
    stepName = stepName.replace(/^step-\d+-(sub-)?/, '')
  }
  stepName = stepName.replace(/-[a-z0-9]{10,}$/gi, '')

  return titleCaseIdentifier(stepName) || 'Automation step'
}

function getBackgroundExecutionKindLabel(kind?: string): string | undefined {
  if (!kind) return undefined
  const normalized = kind.replace(/[-_\s]+/g, ' ').trim().toLowerCase()
  if (!normalized) return undefined
  if (normalized === 'workshop background') return 'Background task'
  if (normalized === 'workflow step') return 'Automation step'
  if (normalized === 'workflow sub agent') return 'Sub-agent'
  if (normalized === 'background agent') return 'Background task'
  return titleCaseIdentifier(normalized)
}

function getExecutionTransportLabel(fields?: Record<string, unknown>): string | undefined {
  const rawTransport = typeof fields?.step_transport === 'string'
    ? fields.step_transport
    : typeof fields?.transport === 'string'
      ? fields.transport
      : ''
  const rawProvider = typeof fields?.provider === 'string'
    ? fields.provider
    : typeof fields?.model_provider === 'string'
      ? fields.model_provider
      : ''

  let transport = rawTransport.trim().toLowerCase()
  if (transport === 'structured_cli' || transport === 'structured') transport = 'structured'
  if (transport === 'non_tmux') transport = 'api'
  if (!transport && rawProvider && !rawProvider.toLowerCase().includes('cli')) transport = 'api'
  if (!transport) return undefined

  const transportLabel = transport === 'tmux'
    ? 'tmux'
    : transport === 'structured'
      ? 'structured CLI'
      : transport === 'api'
        ? 'API'
        : titleCaseIdentifier(transport)

  const provider = rawProvider.trim()
  return provider ? `${provider} · ${transportLabel}` : transportLabel
}

function splitExecutionDisplayPath(displayName: string): { parentPath?: string; title: string } {
  const parts = displayName.split(/\s+>\s+/).map(part => part.trim()).filter(Boolean)
  if (parts.length <= 1) return { title: displayName }
  return {
    parentPath: parts.slice(0, -1).join(' > '),
    title: parts[parts.length - 1],
  }
}

// Helper function to wrap event component with orchestrator context
function WithContext<T extends { metadata?: Record<string, unknown> }>({
  Component,
  data,
  compact,
  hideContext
}: {
  Component: React.ComponentType<{ event: T; compact?: boolean; hideContext?: boolean }>
  data: T
  compact?: boolean
  hideContext?: boolean
}) {
  // hideContext is forwarded, not just consumed: a card rendered inside a
  // terminal that already names the step in its header should not print that
  // name a second time. Only the card knows which part of itself is the
  // duplicate.
  if (hideContext) {
    return <Component event={data} compact={compact} hideContext />
  }
  return (
    <EventWithOrchestratorContext metadata={data.metadata}>
      <Component event={data} compact={compact} />
    </EventWithOrchestratorContext>
  )
}

export const EventDispatcher: React.FC<EventDispatcherProps> = React.memo(({
  event,
  mode,
  onApproveWorkflow,
  onSubmitFeedback,
  onFeedbackSubmitted,
  onSendMessage,
  isApproving,
  compact = false,
  hideOrchestratorContext = false
}) => {

  // Invalid event check
  if (!event.type || !event.data) {
    return (
      <div className={`bg-yellow-50 dark:bg-yellow-900/20 border border-yellow-200 dark:border-yellow-800 rounded-md ${compact ? 'p-2' : 'p-3'}`}>
        <div className={`${compact ? 'text-xs' : 'text-sm'} text-yellow-700 dark:text-yellow-300`}>
          Invalid event: missing type or data
        </div>
      </div>
    )
  }

  // Type-safe event rendering using discriminated unions
  // Each case uses isEventType for type narrowing, then getEventData for typed access

  // Agent Events
  if (isEventType(event, 'agent_error')) {
    return <CompactWrapper compact={compact}><AgentErrorEventDisplay event={getEventData(event)} /></CompactWrapper>
  }
  if (isEventType(event, 'llm_generation_with_retry')) {
    return <CompactWrapper compact={compact}><LLMGenerationWithRetryEventDisplay event={getEventData(event)} /></CompactWrapper>
  }
  if (isEventType(event, 'agent_start')) {
    const data = getEventData(event) as Record<string, unknown>
    return (
      <CompactWrapper compact={compact}>
        <WithContext Component={AgentStartEventComponent} data={data} compact={compact} hideContext={hideOrchestratorContext} />
      </CompactWrapper>
    )
  }
  if (isEventType(event, 'agent_end')) {
    return <CompactWrapper compact={compact}><WithContext Component={AgentEndEventComponent} data={getEventData(event)} compact={compact} hideContext={hideOrchestratorContext} /></CompactWrapper>
  }

  // mcp_server_selection is an internal per-turn routing decision (which MCP
  // servers were offered to the model), not something a reader following a
  // transcript is looking for — never rendered.
  if (isEventType(event, 'mcp_server_selection')) {
    return null
  }
  // Durable answer/expiry marker for a blocking prompt; consumed by the
  // pending-prompt logic, never a card of its own.
  if (event.type === 'human_feedback_resolved') {
    return null
  }

  // Conversation Events
  if (isEventType(event, 'conversation_start')) {
    return <CompactWrapper compact={compact}><WithContext Component={ConversationStartEventDisplay} data={getEventData(event)} compact={compact} hideContext={hideOrchestratorContext} /></CompactWrapper>
  }
  if (isEventType(event, 'conversation_end')) {
    return <CompactWrapper compact={compact}><WithContext Component={ConversationEndEventDisplay} data={getEventData(event)} compact={compact} hideContext={hideOrchestratorContext} /></CompactWrapper>
  }
  if (isEventType(event, 'conversation_error')) {
    return <CompactWrapper compact={compact}><WithContext Component={ConversationErrorEventDisplay} data={getEventData(event)} compact={compact} hideContext={hideOrchestratorContext} /></CompactWrapper>
  }
  if (isEventType(event, 'conversation_turn')) {
    const data = getEventData(event)
    return (
      <CompactWrapper compact={compact}>
        <EventWithOrchestratorContext metadata={data.metadata}>
          <ConversationTurnEventDisplay event={data} compact={compact} />
        </EventWithOrchestratorContext>
      </CompactWrapper>
    )
  }
  if (isEventType(event, 'conversation_thinking')) {
    return <CompactWrapper compact={compact}><WithContext Component={ConversationThinkingEventDisplay} data={getEventData(event)} compact={compact} hideContext={hideOrchestratorContext} /></CompactWrapper>
  }

  // LLM Events
  if (isEventType(event, 'llm_generation_start')) {
    const data = getEventData(event)
    return (
      <CompactWrapper compact={compact}>
        <EventWithOrchestratorContext metadata={data.metadata}>
          <LLMGenerationStartEventDisplay event={data} mode={compact ? 'compact' : mode} />
        </EventWithOrchestratorContext>
      </CompactWrapper>
    )
  }
  if (isEventType(event, 'llm_generation_end')) {
    return <CompactWrapper compact={compact}><WithContext Component={LLMGenerationEndEventDisplay} data={getEventData(event)} compact={compact} hideContext={hideOrchestratorContext} /></CompactWrapper>
  }
  if (isEventType(event, 'llm_generation_error')) {
    const data = getEventData(event)
    return (
      <CompactWrapper compact={compact}>
        <EventWithOrchestratorContext metadata={data.metadata}>
          <LLMGenerationErrorEventDisplay event={data} mode={compact ? 'compact' : mode} />
        </EventWithOrchestratorContext>
      </CompactWrapper>
    )
  }

  // Tool Events
  // Note: delegate tool events are filtered out at EventHierarchy level
  if (isEventType(event, 'tool_call_start')) {
    return <CompactWrapper compact={compact}><WithContext Component={ToolCallStartEventDisplay} data={getEventData(event)} compact={compact} hideContext={hideOrchestratorContext} /></CompactWrapper>
  }
  if (isEventType(event, 'tool_call_end')) {
    return <CompactWrapper compact={compact}><WithContext Component={ToolCallEndEventDisplay} data={getEventData(event)} compact={compact} hideContext={hideOrchestratorContext} /></CompactWrapper>
  }
  if (isEventType(event, 'tool_call_error')) {
    return <CompactWrapper compact={compact}><WithContext Component={ToolCallErrorEventDisplay} data={getEventData(event)} compact={compact} hideContext={hideOrchestratorContext} /></CompactWrapper>
  }

  // System Events
  if (event.type === 'coding_agent_background_task') {
    const envelope = event.data as { data?: Record<string, unknown> } | undefined
    const data = (envelope?.data || event.data || {}) as Record<string, unknown>
    const kind = typeof data.kind === 'string' ? data.kind : 'update'
    const message = typeof data.message === 'string' ? data.message : ''
    const taskID = typeof data.task_id === 'string' ? data.task_id.slice(0, 8) : ''
    // The message is often the raw tool payload (a JSON object with the whole command): one readable line, the rest behind a click.
    const summary = summarizeBackgroundTaskMessage(message)
    const hasMore = message.trim() !== '' && message.trim() !== summary
    return (
      <CompactWrapper compact={compact}>
        <div data-testid="background-task-event" className={`rounded-md border border-cyan-200 bg-cyan-50/70 text-cyan-800 dark:border-cyan-900 dark:bg-cyan-950/20 dark:text-cyan-300 ${compact ? 'p-2 text-xs' : 'p-3 text-sm'}`}>
          <span className="font-medium">Background task {taskID} · {kind === 'task_backgrounded' ? 'started' : kind}</span>
          {summary && <p className="mt-1 break-words opacity-80">{summary}</p>}
          {hasMore && (
            <details className="mt-1">
              <summary className="cursor-pointer select-none text-xs opacity-70">Show details</summary>
              <pre className="mt-1 max-h-64 overflow-auto whitespace-pre-wrap break-words text-xs opacity-80">{message.length > 6000 ? `${message.slice(0, 6000)}\n... (${message.length - 6000} more characters)` : message}</pre>
            </details>
          )}
        </div>
      </CompactWrapper>
    )
  }
  // A coding agent's multiple-choice question (Muse/Claude AskUserQuestion). The clean chat shows it as an answerable
  // card (CodingAgentQuestionCard); this detailed view shows what was asked and, once settled, what was chosen, instead
  // of an "Unknown Event Type" JSON dump.
  if (event.type === 'coding_agent_question') {
    const envelope = event.data as { data?: Record<string, unknown> } | undefined
    const data = (envelope?.data || event.data || {}) as Record<string, unknown>
    const kind = typeof data.kind === 'string' ? data.kind : ''
    const record = (value: unknown) => (value && typeof value === 'object' ? value as Record<string, unknown> : {})
    const questions = (Array.isArray(data.questions) ? data.questions : []).map(record)
    const answers = (Array.isArray(data.answers) ? data.answers : []).map(record)
    const settled = kind === 'settled' || answers.length > 0 || typeof data.outcome === 'string'
    return (
      <CompactWrapper compact={compact}>
        <div data-testid="coding-agent-question-event" className={`rounded-md border border-border bg-card text-card-foreground ${compact ? 'p-2 text-xs' : 'p-3 text-sm'}`}>
          <span className="font-medium">{settled ? (data.outcome === 'answered' ? 'Clarification answered' : 'Clarification closed') : 'Clarification needed'}</span>
          {!settled && questions.length > 0 && (
            <ul className="mt-1 space-y-1">
              {questions.map((question, index) => {
                const options = (Array.isArray(question.options) ? question.options : []).map(record).map(option => String(option.label || '')).filter(Boolean)
                return (
                  <li key={String(question.id || index)}>
                    <span>{String(question.question || question.header || '')}</span>
                    {options.length > 0 && <span className="block opacity-70">{options.join(' · ')}</span>}
                  </li>
                )
              })}
            </ul>
          )}
          {settled && answers.length > 0 && (
            <ul className="mt-1 space-y-0.5 opacity-80">
              {answers.map((answer, index) => (
                <li key={String(answer.id || index)}>{String(answer.id || '')}: {(Array.isArray(answer.selected_labels) ? answer.selected_labels : []).map(String).join(', ') || '—'}</li>
              ))}
            </ul>
          )}
          {settled && answers.length === 0 && typeof data.outcome === 'string' && <p className="mt-1 opacity-80">{data.outcome}</p>}
        </div>
      </CompactWrapper>
    )
  }
  if (event.type === 'context_compaction') {
    return <ContextCompactionEventDisplay event={event} />
  }
  if (event.type === 'status_line') {
    const agentEvent = event.data as { data?: Record<string, unknown> } | undefined
    const data = (agentEvent?.data || event.data || {}) as Record<string, unknown>
    return <CompactWrapper compact={compact}><StatusLineEventDisplay event={data} compact={compact} /></CompactWrapper>
  }
  if (event.type === 'conversation_resumed') {
    const agentEvent = event.data as { data?: { previous_event_count?: number; has_more_history?: boolean } } | undefined
    const count = agentEvent?.data?.previous_event_count ?? 0
    // A restored session is not necessarily truncated. Showing this divider
    // when every saved conversational page is already present falsely suggests
    // that older messages are available. The transcript-level pager is shown
    // only when the durable cursor says there is another page to fetch.
    if (agentEvent?.data?.has_more_history !== true) return null
    return (
      <div className={`flex items-center gap-2 ${compact ? 'py-1' : 'py-2'} ${compact ? 'text-[10px]' : 'text-xs'} text-gray-400 dark:text-gray-500`}>
        <div className="flex-1 border-t border-gray-200 dark:border-gray-700" />
        <span className="shrink-0 px-2">Previous conversation{count > 0 ? ` (${count} events)` : ''}</span>
        <div className="flex-1 border-t border-gray-200 dark:border-gray-700" />
      </div>
    )
  }
  if (isEventType(event, 'user_message')) {
    const data = getEventData(event)
    // Always render - UserMessageEventDisplay handles missing content gracefully
    // Log warning if content is missing for debugging
    if (!data.content) {
      console.warn('USERMSG_DEBUG - EventDispatcher - user_message event has no content, but rendering anyway', data)
    }
    return <CompactWrapper compact={compact}><WithContext Component={UserMessageEventDisplay} data={data} compact={compact} hideContext={hideOrchestratorContext} /></CompactWrapper>
  }
  
  // Fallback: Try to handle user_message events even if type check fails
  // This handles cases where event structure might be slightly different
  if (event.type === 'user_message' && event.data) {
    try {
      // Try to extract data from nested structure
      const agentEvent = event.data as { data?: unknown; type?: string }
      const eventData = (agentEvent?.data || event.data) as UserMessageEvent
      if (eventData) {
        console.log('USERMSG_DEBUG - EventDispatcher - Using fallback for user_message event', eventData)
        return <CompactWrapper compact={compact}><WithContext Component={UserMessageEventDisplay} data={eventData} compact={compact} hideContext={hideOrchestratorContext} /></CompactWrapper>
      }
    } catch (error) {
      console.error('USERMSG_DEBUG - EventDispatcher - Error in fallback handler', error, event)
    }
  }

  // Orchestrator Events (only orchestrator_end is emitted; start/error never were)
  if (isEventType(event, 'orchestrator_end')) {
    return <CompactWrapper compact={compact}><OrchestratorEndEventDisplay event={getEventData(event)} /></CompactWrapper>
  }
  // Sub-agent start rows are intentionally never painted: the main-conversation
  // filter already excludes child-execution starts from product chats, and the
  // diagnostics rail does not need a lifecycle banner per spawn (the end card
  // carries the result). Returning null (not falling through) so this never
  // renders as an "Unknown Event Type" JSON card. The event itself stays live
  // for stores, retention, bot narration, and backend consumers.
  if (isEventType(event, 'orchestrator_agent_start')) {
    return null
  }
  if (isEventType(event, 'orchestrator_agent_end')) {
    return (
      <CompactWrapper compact={compact}>
        <OrchestratorAgentEndEventDisplay
          event={getEventData(event)}
          compact={compact || hideOrchestratorContext}
        />
      </CompactWrapper>
    )
  }
  if (isEventType(event, 'orchestrator_agent_error')) {
    return <CompactWrapper compact={compact}><OrchestratorAgentErrorEventDisplay event={getEventData(event)} /></CompactWrapper>
  }

  // Human Verification Events
  if (isEventType(event, 'request_human_feedback')) {
    const data = getEventData(event)
    return (
      <HumanVerificationDisplay 
        event={{
          type: event.type,
          data: {
            ...data,
            objective: data.objective || '',
            todo_list_markdown: data.todo_list_markdown || '',
            request_id: data.request_id || `request_${Date.now()}`,
          },
          timestamp: event.timestamp || new Date().toISOString()
        }} 
        onApprove={onApproveWorkflow || (() => {})}
        onFeedbackSubmitted={onFeedbackSubmitted}
        isApproving={isApproving}
      />
    )
  }
  if (isEventType(event, 'blocking_human_feedback')) {
    const data = getEventData(event)
    return (
      <BlockingHumanFeedbackDisplay 
        event={{
          type: event.type,
          data: {
            ...data,
            question: data.question || 'Do you want to continue?',
            allow_feedback: data.allow_feedback || false,
            context: data.context || '',
            session_id: data.session_id || '',
            workflow_id: data.workflow_id || '',
            request_id: data.request_id || `request_${Date.now()}`
          },
          timestamp: event.timestamp || new Date().toISOString()
        }} 
        onApprove={onApproveWorkflow || (() => {})}
        onSubmitFeedback={onSubmitFeedback}
        onFeedbackSubmitted={onFeedbackSubmitted}
        isApproving={isApproving}
        surfaceNotifications={false}
      />
    )
  }

  // Plan Approval Event (non-blocking — sends response as chat message)
  if (event.type === 'plan_approval') {
    const data = event.data as { data?: Record<string, unknown> } | undefined
    const payload = (data?.data || event.data) as Record<string, unknown>
    return (
      <PlanApprovalDisplay
        event={{
          type: event.type,
          data: {
            question: (payload?.question as string) || 'Plan is ready for review.',
            context: (payload?.context as string) || '',
            yes_label: (payload?.yes_label as string) || 'Approve & Execute',
          },
          timestamp: event.timestamp || new Date().toISOString()
        }}
        onSendMessage={onSendMessage || (() => {})}
      />
    )
  }

  // Batch execution events (only cancellation is emitted)
  if (isEventType(event, 'batch_execution_canceled')) {
    return <CompactWrapper compact={compact}><BatchExecutionCanceledEventDisplay event={getEventData(event)} compact={compact} /></CompactWrapper>
  }

  // Agent Events
  if (isEventType(event, 'todo_task_route_selected')) {
    const data = getEventData(event) as TodoTaskRouteSelectedEvent
    const actionColors: Record<string, string> = {
      delegate: 'text-blue-600 dark:text-blue-400',
      complete: 'text-green-600 dark:text-green-400',
      continue: 'text-yellow-600 dark:text-yellow-400',
    }
    const tierClass =
      data.preferred_tier === 1 ? 'bg-purple-100 dark:bg-purple-900/50 text-purple-700 dark:text-purple-300'
      : data.preferred_tier === 2 ? 'bg-blue-100 dark:bg-blue-900/50 text-blue-700 dark:text-blue-300'
      : 'bg-green-100 dark:bg-green-900/50 text-green-700 dark:text-green-300'
    const routeName = data.selected_route_name || (data.use_generic_agent ? 'Generic Agent' : '')
    // An orchestrator picks a route per todo, so these arrive in long runs --
    // eleven in a row on a real Pulse orchestrator. As a full card each one
    // repeated "Agent: Route Selected", the iteration chip and "Action:"
    // identically, and the only part that differed (which agent, which tier)
    // was buried three lines down. One dense row per route puts the varying
    // part first and turns a screenful into a readable list.
    return (
      <CompactWrapper compact={compact}>
        {/* Semantic tokens rather than a raw purple wash: the accent lives in
            the left rule, so the route name keeps full foreground contrast in
            both themes instead of sitting on a tinted band. */}
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 rounded border-l-2 border-purple-500/70 bg-muted/20 px-2 py-1 text-xs hover:bg-muted/40">
          <span aria-hidden>📋</span>
          {routeName && <span className="font-medium text-foreground">{routeName}</span>}
          {data.preferred_tier_label && (
            <span className={`rounded-full px-1.5 py-0.5 text-[10px] font-medium ${tierClass}`}>
              {data.preferred_tier_label}
            </span>
          )}
          <span className={`font-medium ${actionColors[data.next_action || ''] || 'text-gray-700 dark:text-gray-300'}`}>
            {data.next_action || 'unknown'}
          </span>
          {/* Iteration is deliberately not shown: it is identical for every
              route in a run, so repeating it per row is the same noise this
              layout exists to remove. It stays available in the run header. */}
          {data.todo_title && (
            <span className="min-w-0 flex-1 truncate text-gray-600 dark:text-gray-400">{data.todo_title}</span>
          )}
          {data.progress_summary && (
            <span className="w-full truncate text-[11px] text-gray-500 dark:text-gray-500">{data.progress_summary}</span>
          )}
        </div>
      </CompactWrapper>
    )
  }




  if (isEventType(event, 'todo_task_step_completed')) {
    const data = getEventData(event) as TodoTaskStepCompletedEvent
    return (
      <CompactWrapper compact={compact}>
        <div className={`bg-purple-50 dark:bg-purple-900/20 border border-purple-200 dark:border-purple-800 rounded-lg ${compact ? 'p-2' : 'p-3'}`}>
          <div className="flex items-center gap-2 mb-2">
            <span className="text-lg">🎉</span>
            <span className={`font-medium ${compact ? 'text-xs' : 'text-sm'} text-purple-700 dark:text-purple-300`}>
              Agent Step Completed: {data.step_title}
            </span>
          </div>
          <div className={`space-y-1 ${compact ? 'text-xs' : 'text-sm'}`}>
            <div className="flex items-center gap-2">
              <span className="text-gray-500 dark:text-gray-400">Todos:</span>
              <span className="text-purple-600 dark:text-purple-400">
                {data.completed_count}/{data.total_todos_count} completed
              </span>
            </div>
            <div className="flex items-center gap-2">
              <span className="text-gray-500 dark:text-gray-400">Iterations:</span>
              <span className="text-purple-600 dark:text-purple-400">{data.total_iterations}</span>
            </div>
            {data.completion_reason && (
              <div className={`${compact ? 'text-[10px]' : 'text-xs'} text-gray-600 dark:text-gray-400 italic mt-1`}>
                {data.completion_reason}
              </div>
            )}
          </div>
        </div>
      </CompactWrapper>
    )
  }

  // Debug Events
  if (isEventType(event, 'token_usage')) {
    return <CompactWrapper compact={compact}><TokenUsageEventDisplay event={getEventData(event)} /></CompactWrapper>
  }
  if (isEventType(event, 'retry_attempt')) {
    return <CompactWrapper compact={compact}><RetryAttemptEventDisplay event={getEventData(event)} /></CompactWrapper>
  }
  if (isEventType(event, 'broken_pipe')) {
    return <CompactWrapper compact={compact}><BrokenPipeEventDisplay event={getEventData(event)} /></CompactWrapper>
  }
  if (isEventType(event, 'large_tool_output_detected')) {
    return <CompactWrapper compact={compact}><LargeToolOutputDetectedEventDisplay event={getEventData(event)} /></CompactWrapper>
  }
  if (isEventType(event, 'large_tool_output_file_written')) {
    return <CompactWrapper compact={compact}><LargeToolOutputFileWrittenEventDisplay event={getEventData(event)} /></CompactWrapper>
  }
  if (isEventType(event, 'max_turns_reached')) {
    return <CompactWrapper compact={compact}><MaxTurnsReachedEventDisplay event={getEventData(event)} /></CompactWrapper>
  }
  if (isEventType(event, 'context_cancelled')) {
    return <CompactWrapper compact={compact}><ContextCancelledEventDisplay event={getEventData(event)} /></CompactWrapper>
  }

  // Unified Completion Events
  if (isEventType(event, 'unified_completion')) {
    return <CompactWrapper compact={compact}><UnifiedCompletionEventDisplay event={getEventData(event)} /></CompactWrapper>
  }


  // Planning Events
  if (isEventType(event, 'variables_extracted')) {
    return <CompactWrapper compact={compact}><VariablesExtractedEventDisplay event={getEventData(event)} /></CompactWrapper>
  }

  // Step Token Usage Events
  if (isEventType(event, 'step_token_usage')) {
    return <CompactWrapper compact={compact}><StepTokenUsageEventDisplay event={getEventData(event)} /></CompactWrapper>
  }

  // Routing Evaluated Event
  if (isEventType(event, 'routing_evaluated')) {
    return (
      <CompactWrapper compact={compact}>
        <RoutingEvaluatedEventDisplay
          event={getEventData(event) as Record<string, unknown>}
          compact={compact}
        />
      </CompactWrapper>
    )
  }

  // Agent Events
  if (isEventType(event, 'todo_task_route_selected')) {
    return (
      <CompactWrapper compact={compact}>
        <TodoTaskRouteSelectedEventDisplay
          event={getEventData(event)}
          compact={compact}
        />
      </CompactWrapper>
    )
  }
  if (isEventType(event, 'todo_task_step_completed')) {
    return (
      <CompactWrapper compact={compact}>
        <TodoTaskStepCompletedEventDisplay 
          event={getEventData(event)} 
          compact={compact}
        />
      </CompactWrapper>
    )
  }

  // Pre-Validation Completed Event
  if (isEventType(event, 'pre_validation_completed')) {
    return (
      <CompactWrapper compact={compact}>
        <PreValidationCompletedEventDisplay 
          event={getEventData(event)} 
          compact={compact}
        />
      </CompactWrapper>
    )
  }

  // Workflow Error Event
  if (event.type === 'workflow_error') {
    const data = event.data as {
      data?: {
        error?: string
        error_chain?: string
        query_id?: string
        [key: string]: unknown
      }
      error?: string
      timestamp?: string
      trace_id?: string
      correlation_id?: string
      [key: string]: unknown
    }
    
    // Extract error from nested structure - handle both nested and flat structures
    const nestedData = data?.data
    const rootCauseError = 
      (typeof nestedData === 'object' && nestedData !== null && 'error' in nestedData && typeof nestedData.error === 'string' && nestedData.error) ||
      (typeof data?.error === 'string' && data.error) ||
      'Unknown automation error'
    
    const fullErrorChain = 
      (typeof nestedData === 'object' && nestedData !== null && 'error_chain' in nestedData && typeof nestedData.error_chain === 'string' && nestedData.error_chain) ||
      undefined
    
    const queryId = 
      (typeof nestedData === 'object' && nestedData !== null && 'query_id' in nestedData && typeof nestedData.query_id === 'string' && nestedData.query_id) ||
      undefined
    
    const hasFullChain = fullErrorChain && fullErrorChain !== rootCauseError
    
    return (
      <CompactWrapper compact={compact}>
        <div className={`bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded-lg ${compact ? 'p-2' : 'p-3'}`}>
          <div className="space-y-2">
            {/* Header */}
            <div className="flex items-center justify-between gap-3">
              <div className="flex items-center gap-2">
                <span className="text-lg">❌</span>
                <div className={`${compact ? 'text-xs' : 'text-sm'} font-medium text-red-700 dark:text-red-300`}>
                  Automation Error
                </div>
              </div>
              {event.timestamp && (
                <div className={`${compact ? 'text-[10px]' : 'text-xs'} text-red-600 dark:text-red-400 flex-shrink-0`}>
                  {new Date(event.timestamp).toLocaleTimeString()}
                </div>
              )}
            </div>
            
            {/* Query ID */}
            {queryId && (
              <div className={`${compact ? 'text-[10px]' : 'text-xs'} text-red-600 dark:text-red-400`}>
                <span className="font-medium">Query ID:</span>{' '}
                <code className="bg-red-100 dark:bg-red-800 px-1 rounded">{queryId}</code>
              </div>
            )}
            
            {/* Root Cause Error - highlighted prominently */}
            <div className="bg-red-200 dark:bg-red-900 border-2 border-red-300 dark:border-red-700 rounded-md p-2">
              <div className={`${compact ? 'text-[10px]' : 'text-xs'} font-bold text-red-900 dark:text-red-100 mb-1 flex items-center gap-1`}>
                <span>🔍</span>
                <span>Root Cause:</span>
              </div>
              <div className={`${compact ? 'text-xs' : 'text-sm'} text-red-950 dark:text-red-50 whitespace-pre-wrap break-words font-mono font-semibold`}>
                {rootCauseError}
              </div>
            </div>
            
            {/* Full Error Chain - shown if different from root cause */}
            {hasFullChain && (
              <details className="bg-red-100 dark:bg-red-800 border border-red-200 dark:border-red-700 rounded-md p-2">
                <summary className={`${compact ? 'text-[10px]' : 'text-xs'} font-medium text-red-800 dark:text-red-200 cursor-pointer`}>
                  Full Error Chain (click to expand)
                </summary>
                <div className={`${compact ? 'text-xs' : 'text-sm'} text-red-900 dark:text-red-100 whitespace-pre-wrap break-words font-mono mt-2`}>
                  {fullErrorChain}
                </div>
              </details>
            )}
            
            {/* Additional metadata */}
            {(data?.trace_id || data?.correlation_id) && (
              <div className={`${compact ? 'text-[10px]' : 'text-xs'} text-red-600 dark:text-red-400 space-y-1`}>
                {data.trace_id && (
                  <div>
                    <span className="font-medium">Trace ID:</span>{' '}
                    <code className="bg-red-100 dark:bg-red-800 px-1 rounded">{data.trace_id}</code>
                  </div>
                )}
                {data.correlation_id && (
                  <div>
                    <span className="font-medium">Correlation ID:</span>{' '}
                    <code className="bg-red-100 dark:bg-red-800 px-1 rounded">{data.correlation_id}</code>
                  </div>
                )}
              </div>
            )}
            
            {/* Show full data structure if available (for debugging) */}
            {compact && Object.keys(data?.data || {}).length > 2 && (
              <details className={`${compact ? 'text-[10px]' : 'text-xs'} text-red-600 dark:text-red-400`}>
                <summary className="cursor-pointer font-medium">Show full error data</summary>
                <pre className="mt-1 bg-red-100 dark:bg-red-800 border border-red-200 dark:border-red-700 rounded p-2 overflow-x-auto text-[10px]">
                  {JSON.stringify(data, null, 2)}
                </pre>
              </details>
            )}
          </div>
        </div>
      </CompactWrapper>
    )
  }

  // Delegation Start Event
  if (event.type === 'delegation_start') {
    const data = event.data as {
      data?: {
        delegation_id?: string
        depth?: number
        instruction?: string
        reasoning_level?: string
        model_id?: string
        servers?: string[]
        agent_template?: string
      }
      delegation_id?: string
      depth?: number
      instruction?: string
      reasoning_level?: string
      model_id?: string
      servers?: string[]
      agent_template?: string
      timestamp?: string
    }

    const delegationData = data?.data || data
    const instruction = delegationData?.instruction || 'No instruction provided'
    const displayTitle = getDelegationDisplayTitle(instruction)
    const delegationId = delegationData?.delegation_id
    const reasoningLevel = delegationData?.reasoning_level
    const modelId = delegationData?.model_id
    const servers = delegationData?.servers
    const agentTemplate = delegationData?.agent_template

    const reasoningColors: Record<string, string> = {
      high: 'bg-red-100 dark:bg-red-900/40 text-red-700 dark:text-red-300',
      medium: 'bg-yellow-100 dark:bg-yellow-900/40 text-yellow-700 dark:text-yellow-300',
      low: 'bg-green-100 dark:bg-green-900/40 text-green-700 dark:text-green-300',
    }

    return (
      <CompactWrapper compact={compact}>
        <details className="bg-purple-50 dark:bg-purple-900/20 border border-purple-200 dark:border-purple-800 rounded px-2 py-1.5 group">
          <summary className="flex items-center gap-2 cursor-pointer list-none [&::-webkit-details-marker]:hidden">
            <span className="text-sm">🔀</span>
            <span className="text-[10px] text-purple-400 group-open:hidden">+</span>
            <span className="text-[10px] text-purple-400 hidden group-open:inline">−</span>
            <div className="text-xs font-medium text-purple-700 dark:text-purple-300 flex-1 truncate" title={instruction}>
              Delegated task: {displayTitle}
            </div>
            <div className="flex items-center gap-1.5 flex-shrink-0">
              {agentTemplate && (
                <span className="text-[10px] px-1.5 py-0.5 rounded font-medium bg-indigo-100 dark:bg-indigo-900/40 text-indigo-700 dark:text-indigo-300" title={`Agent template: ${agentTemplate}`}>
                  {agentTemplate}
                </span>
              )}
              {reasoningLevel && (
                <span className={`text-[10px] px-1.5 py-0.5 rounded font-medium ${reasoningColors[reasoningLevel] || 'bg-gray-100 dark:bg-gray-800 text-gray-600 dark:text-gray-400'}`}>
                  {reasoningLevel}
                </span>
              )}
              {event.timestamp && (
                <span className="text-[10px] text-purple-500 dark:text-purple-400">
                  {new Date(event.timestamp).toLocaleTimeString()}
                </span>
              )}
            </div>
          </summary>
          <div className="mt-2 pt-2 border-t border-purple-200 dark:border-purple-700 space-y-1.5">
            <div className="text-xs text-purple-700 dark:text-purple-300 whitespace-pre-wrap break-words">
              {instruction}
            </div>
            <div className="flex items-center gap-3 text-[10px] text-purple-500 dark:text-purple-400 flex-wrap">
              {agentTemplate && <span>Template: {agentTemplate}</span>}
              {reasoningLevel && <span>Reasoning: {reasoningLevel}</span>}
              {modelId && <span>Model: {modelId}</span>}
              {servers && servers.length > 0 && <span>Servers: {servers.join(', ')}</span>}
            </div>

            {delegationId && (
              <DelegationStreamingCard delegationId={delegationId} />
            )}
          </div>
        </details>

      </CompactWrapper>
    )
  }

  // Delegation End Event
  if (event.type === 'delegation_end') {
    const data = event.data as {
      data?: {
        delegation_id?: string
        depth?: number
        result?: string
        error?: string
        duration?: string
        input_tokens?: number
        output_tokens?: number
        tool_calls?: number
      }
      delegation_id?: string
      depth?: number
      result?: string
      error?: string
      duration?: string
      input_tokens?: number
      output_tokens?: number
      tool_calls?: number
      timestamp?: string
    }

    const delegationData = data?.data || data
    const resultText = delegationData?.result
    const error = delegationData?.error
    const rawDuration = delegationData?.duration || ''
    const isSuccess = !error
    const inputTokens = delegationData?.input_tokens
    const outputTokens = delegationData?.output_tokens
    const toolCalls = delegationData?.tool_calls
    const hasStats = inputTokens || outputTokens || toolCalls
    // Format Go duration (e.g. "45.123456789s", "2m34.567s") to concise form
    const formatDuration = (d: string): string => {
      if (!d) return ''
      // Match Go duration formats: "Xm", "Xs", "XmYs", "XmY.Zs"
      const match = d.match(/^(?:(\d+)m)?(\d+(?:\.\d+)?)s$/)
      if (match) {
        const mins = match[1] ? parseInt(match[1]) : 0
        const secs = parseFloat(match[2])
        if (mins > 0) return `${mins}m${Math.round(secs).toString().padStart(2, '0')}s`
        return `${secs.toFixed(1)}s`
      }
      return d
    }
    const duration = formatDuration(rawDuration)

    const colorClasses = isSuccess
      ? { bg: 'bg-green-50 dark:bg-green-900/20', border: 'border-green-200 dark:border-green-800', text: 'text-green-700 dark:text-green-300', muted: 'text-green-500 dark:text-green-400', divider: 'border-green-200 dark:border-green-700' }
      : { bg: 'bg-red-50 dark:bg-red-900/20', border: 'border-red-200 dark:border-red-800', text: 'text-red-700 dark:text-red-300', muted: 'text-red-500 dark:text-red-400', divider: 'border-red-200 dark:border-red-700' }

    return (
      <CompactWrapper compact={compact}>
        <details className={`${colorClasses.bg} border ${colorClasses.border} rounded px-2 py-1.5 group`}>
          <summary className="flex items-center gap-2 cursor-pointer list-none [&::-webkit-details-marker]:hidden">
            <span className="text-sm">{isSuccess ? '✅' : '❌'}</span>
            <span className={`text-[10px] ${colorClasses.muted} group-open:hidden`}>+</span>
            <span className={`text-[10px] ${colorClasses.muted} hidden group-open:inline`}>−</span>
            <div className={`text-xs font-medium flex-1 ${colorClasses.text}`}>
              {isSuccess ? 'Task completed' : 'Task failed'}
              {error && <span className="font-normal ml-1">- {error.length > 50 ? error.substring(0, 50) + '...' : error}</span>}
            </div>
            <div className="flex items-center gap-1.5 text-[10px] flex-shrink-0">
              {hasStats && (
                <span className={colorClasses.muted}>
                  {inputTokens ? `${((inputTokens + (outputTokens || 0)) / 1000).toFixed(1)}k tok` : ''}
                  {toolCalls ? ` · ${toolCalls} tools` : ''}
                </span>
              )}
              {duration && (
                <span className={colorClasses.muted}>{duration}</span>
              )}
              {event.timestamp && (
                <span className={colorClasses.muted}>
                  {new Date(event.timestamp).toLocaleTimeString()}
                </span>
              )}
            </div>
          </summary>
          <div className={`mt-2 pt-2 border-t ${colorClasses.divider} space-y-1.5`}>
            {error && (
              <div className="text-xs text-red-700 dark:text-red-300 whitespace-pre-wrap break-words">
                <span className="font-medium">Error: </span>{error}
              </div>
            )}
            {resultText && (
              <div className={`text-xs ${colorClasses.text} whitespace-pre-wrap break-words max-h-40 overflow-y-auto overscroll-y-contain`}>
                {resultText}
              </div>
            )}
            {hasStats && (
              <div className={`flex items-center gap-3 text-[10px] ${colorClasses.muted}`}>
                {inputTokens !== undefined && <span>In: {inputTokens.toLocaleString()} tokens</span>}
                {outputTokens !== undefined && <span>Out: {outputTokens.toLocaleString()} tokens</span>}
                {toolCalls !== undefined && <span>Tool calls: {toolCalls}</span>}
              </div>
            )}
            {duration && (
              <div className={`text-[10px] ${colorClasses.muted}`}>Duration: {duration}</div>
            )}
          </div>
        </details>
      </CompactWrapper>
    )
  }

  // Background Agent Started Event
  if (isEventType(event, 'background_agent_started')) {
    const fields = getEventData(event)
    const rawName = fields.name ?? ''
    const displayName = getBackgroundExecutionDisplayName(rawName)
    const displayPath = splitExecutionDisplayPath(displayName)
    // kind/status/transport are typed here as never actually set by the
    // backend for this event type — see BackgroundAgentStartedEvent.
    const status = 'running'
    const kindLabel = getBackgroundExecutionKindLabel(undefined)
    const transportLabel = getExecutionTransportLabel({})
    const isRunning = status === 'running' || status === 'active' || status === 'in_progress'
    // The composer's background-work pill scrolls here from its "Open" link (PLAT-705).
    const backgroundAgentId = fields.agent_id || undefined

    return (
      <CompactWrapper compact={compact}>
        <div data-background-agent-id={backgroundAgentId} className={`bg-blue-50 dark:bg-blue-900/20 border border-blue-200 dark:border-blue-800 rounded-md ${compact ? 'p-2' : 'p-3'}`}>
          <div className="flex min-w-0 items-center gap-2">
            <span className={`inline-block w-2 h-2 rounded-full bg-blue-500 ${isRunning ? 'animate-pulse' : 'opacity-60'}`} />
            <div className="min-w-0 flex-1">
              <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5">
                <span className={`${compact ? 'text-xs' : 'text-sm'} min-w-0 truncate font-medium text-blue-700 dark:text-blue-300`}>
                  {displayPath.title}
                </span>
                {kindLabel && (
                  <span className={`${compact ? 'text-[9px]' : 'text-[10px]'} shrink-0 rounded border border-blue-700/40 px-1.5 py-0.5 leading-none text-blue-500/80 dark:text-blue-400/80`}>
                    {kindLabel}
                  </span>
                )}
                {displayPath.parentPath && (
                  <span className={`${compact ? 'text-[9px]' : 'text-[10px]'} min-w-0 truncate text-blue-500/65 dark:text-blue-400/65`}>
                    inside {displayPath.parentPath}
                  </span>
                )}
                {transportLabel && (
                  <span className={`${compact ? 'text-[9px]' : 'text-[10px]'} shrink-0 rounded border border-cyan-700/35 px-1.5 py-0.5 leading-none text-cyan-500/80 dark:text-cyan-300/80`}>
                    {transportLabel}
                  </span>
                )}
              </div>
            </div>
            <span className={`${compact ? 'text-[10px]' : 'text-xs'} shrink-0 text-blue-500 dark:text-blue-400`}>
              {isRunning ? 'Running' : titleCaseIdentifier(status)}
            </span>
            {isRunning && event.timestamp && (
              <ElapsedTimer startTimestamp={event.timestamp} className="text-[10px] text-blue-500 dark:text-blue-400 animate-pulse font-mono" />
            )}
          </div>
        </div>
      </CompactWrapper>
    )
  }

  // Background Agent Completed Event
  if (isEventType(event, 'background_agent_completed')) {
    const fields = getEventData(event)
    const rawName = fields.name || ''
    const displayPath = splitExecutionDisplayPath(getBackgroundExecutionDisplayName(rawName))
    const displayName = displayPath.title
    const status = fields.status || 'completed'
    const duration = fields.duration || ''
    const result = backgroundAgentCompletionSummary(fields.result)
    const error = fields.error || ''
    const isSuccess = status === 'completed'
    const isFailed = status === 'failed'

    if (compact && (result || error)) {
      return (
        <CompactWrapper compact={compact}>
          {/* Failure keeps its red rail — that is a state worth seeing. A
              successful result is the normal case and needs no green stripe;
              the transcript dropped its own green rail for the same reason. */}
          <div className={`${isFailed ? 'border-l-2 border-red-400 pl-3' : 'pl-3'} py-1`}>
            <div className="mb-1 flex min-w-0 items-center gap-2">
              <span className={`text-[10px] font-medium uppercase tracking-wide ${isFailed ? 'text-red-600 dark:text-red-300' : 'text-muted-foreground'}`}>
                {isFailed ? 'Error' : 'Result'}
              </span>
              <span className="truncate text-[10px] text-muted-foreground">
                {displayName}
              </span>
            </div>
            {error && (
              <div className="whitespace-pre-wrap break-words text-xs text-red-600 dark:text-red-300">
                {error}
              </div>
            )}
            {result && (
              <div className="max-h-56 overflow-y-auto overscroll-y-contain text-xs text-foreground/85">
                <MarkdownRenderer content={result} className="text-xs" />
              </div>
            )}
          </div>
        </CompactWrapper>
      )
    }

    const bgColor = isSuccess ? 'bg-green-50 dark:bg-green-900/20 border-green-200 dark:border-green-800' :
                    isFailed ? 'bg-red-50 dark:bg-red-900/20 border-red-200 dark:border-red-800' :
                    'bg-gray-50 dark:bg-gray-900/20 border-gray-200 dark:border-gray-800'
    const textColor = isSuccess ? 'text-green-700 dark:text-green-300' :
                      isFailed ? 'text-red-700 dark:text-red-300' :
                      'text-gray-700 dark:text-gray-300'
    const dotColor = isSuccess ? 'bg-green-500' : isFailed ? 'bg-red-500' : 'bg-gray-400'
    const statusLabel = isSuccess ? 'completed' : isFailed ? 'failed' : status

    return (
      <CompactWrapper compact={compact}>
        <details className={`${bgColor} border rounded-md ${compact ? 'p-2' : 'p-3'}`}>
          <summary className="cursor-pointer flex items-center gap-2">
            <span className={`inline-block w-2 h-2 rounded-full ${dotColor}`} />
            <span className={`${compact ? 'text-xs' : 'text-sm'} font-medium ${textColor}`}>
              {displayName}
            </span>
            {displayPath.parentPath && (
              <span className={`${compact ? 'text-[10px]' : 'text-xs'} min-w-0 truncate ${textColor} opacity-65`}>
                inside {displayPath.parentPath}
              </span>
            )}
            <span className={`${compact ? 'text-[10px]' : 'text-xs'} ${textColor} opacity-75`}>
              {statusLabel}{duration ? ` (${duration})` : ''}
            </span>
          </summary>
          <div className={`mt-2 ${compact ? 'text-[10px]' : 'text-xs'}`}>
            {error && (
              <div className="text-red-600 dark:text-red-400 whitespace-pre-wrap">{error}</div>
            )}
            {result && (
              <div className="text-gray-600 dark:text-gray-400 max-h-40 overflow-y-auto overscroll-y-contain">
                <MarkdownRenderer content={result} />
              </div>
            )}
          </div>
        </details>
      </CompactWrapper>
    )
  }

  // Background Agent Terminated Event
  if (isEventType(event, 'background_agent_terminated')) {
    const fields = getEventData(event)
    const rawName = fields.name || ''
    const displayName = rawName.replace(/^Planner:\s*/i, '').trim() || 'Task'

    return (
      <CompactWrapper compact={compact}>
        <div className={`bg-gray-50 dark:bg-gray-900/20 border border-gray-200 dark:border-gray-800 rounded-md ${compact ? 'p-2' : 'p-3'}`}>
          <div className="flex items-center gap-2">
            <span className="inline-block w-2 h-2 rounded-full bg-gray-400" />
            <span className={`${compact ? 'text-xs' : 'text-sm'} font-medium text-gray-500 dark:text-gray-400`}>
              {displayName}
            </span>
            <span className={`${compact ? 'text-[10px]' : 'text-xs'} text-gray-400 dark:text-gray-500`}>
              cancelled
            </span>
          </div>
        </div>
      </CompactWrapper>
    )
  }

  // Synthetic Turn Ready Event (shown when a background task has completed and results are being processed)
  if (isEventType(event, 'synthetic_turn_ready')) {
    const fields = getEventData(event)
    if (fields.agent_id?.startsWith('vault-oauth:') || fields.agent_id?.startsWith('private-oauth:')) {
      const vault = fields.agent_id.startsWith('vault-oauth:')
      return <CompactWrapper compact={compact}>
        <div role="status" className={`rounded-md border border-border px-3 py-2 ${compact ? 'text-xs' : 'text-sm'} ${fields.status === 'completed' ? 'text-foreground' : 'text-destructive'}`}>
          {fields.name || 'MCP server'} {fields.status === 'completed' ? (vault ? 'connected to Vault. Assign access in Groups.' : 'sign-in completed. Your private connection is available from the next agent turn.') : (vault ? 'could not connect to Vault. Try signing in again.' : 'sign-in failed or expired. Try signing in again.')}
        </div>
      </CompactWrapper>
    }
    return null
  }

  // Auto Notification Steered Event — a background agent's completion was
  // delivered directly into the main agent's live turn instead of queued for
  // the next turn. Keep the copy user-facing: "steered" and provider routing
  // described the implementation but made a successful notification look
  // like an unexplained diagnostic.
  if (isEventType(event, 'auto_notification_steered')) {
    const fields = getEventData(event)
    const rawName = fields.name || ''
    const displayName = rawName.replace(/^Planner:\s*/i, '').trim() || 'Task'
    return (
      <CompactWrapper compact={compact}>
        <div className={`flex items-center gap-2 ${compact ? 'text-[10px]' : 'text-xs'} text-sky-600 dark:text-sky-400`}>
          <span className="inline-block w-1.5 h-1.5 rounded-full bg-sky-400" />
          <span>
            <span className="font-medium">{displayName}</span> completed · main agent notified
          </span>
        </div>
      </CompactWrapper>
    )
  }

  // Learn Code Script Execution Event
  if (event.type === 'learn_code_script_execution') {
    const wrapper = event.data as { data?: unknown } | undefined
    const d = (wrapper?.data || event.data) as {
      step_id: string; step_title: string; step_path: string
      script_path: string; script_content: string; success: boolean; exit_code: number
      output: string; error: string; fix_iteration: number; is_saved_script: boolean
    }
    const isSaved = d?.is_saved_script
    const success = d?.success
    const fixIter = d?.fix_iteration ?? 0
    let label: string
    if (isSaved) {
      label = '🐍 Script (saved)'
    } else if (fixIter === 0) {
      label = '🐍 Script (new)'
    } else {
      label = `🐍 Script (fix #${fixIter})`
    }
    const exitCode = d?.exit_code
    const exitLabel = exitCode == null || exitCode < 0 ? 'failed' : `exit ${exitCode}`
    const statusColor = success
      ? 'bg-green-50 dark:bg-green-900/20 border-green-200 dark:border-green-800'
      : 'bg-red-50 dark:bg-red-900/20 border-red-200 dark:border-red-800'
    const textColor = success ? 'text-green-700 dark:text-green-300' : 'text-red-700 dark:text-red-300'
    const failDetail = !success ? (d?.error || d?.output) : null
    const successOutput = success ? d?.output : null
    return (
      <CompactWrapper compact={compact}>
        <div className={`border rounded-md ${statusColor} ${compact ? 'p-2' : 'p-3'}`}>
          <div className="flex items-center gap-2 flex-wrap">
            <span className={`${compact ? 'text-xs' : 'text-sm'} font-medium ${textColor}`}>{label}</span>
            <span className={`${compact ? 'text-[10px]' : 'text-xs'} text-gray-500 dark:text-gray-400`}>{d?.step_title || d?.step_path}</span>
            {success
              ? <span className={`${compact ? 'text-[10px]' : 'text-xs'} text-green-600 dark:text-green-400`}>✓ passed</span>
              : <span className={`${compact ? 'text-[10px]' : 'text-xs'} text-red-600 dark:text-red-400`}>✗ {exitLabel}</span>
            }
          </div>
          {failDetail && (() => {
            const preview = failDetail.slice(0, 600)
            const isTruncated = failDetail.length > 600
            return (
              <div className="mt-1">
                <div className={`font-mono ${compact ? 'text-[10px]' : 'text-xs'} text-red-600 dark:text-red-400 whitespace-pre-wrap break-all`}>
                  {preview}{isTruncated ? '…' : ''}
                </div>
                {isTruncated && (
                  <details className="mt-2">
                    <summary className={`cursor-pointer ${compact ? 'text-[10px]' : 'text-xs'} text-gray-500 dark:text-gray-400 select-none`}>
                      View full error
                    </summary>
                    <pre className={`mt-1 font-mono ${compact ? 'text-[10px]' : 'text-xs'} text-red-600 dark:text-red-400 whitespace-pre-wrap break-all bg-red-50 dark:bg-red-950/20 rounded p-2 max-h-64 overflow-y-auto`}>
                      {failDetail}
                    </pre>
                  </details>
                )}
              </div>
            )
          })()}
          {successOutput && (
            <details className="mt-2">
              <summary className={`cursor-pointer ${compact ? 'text-[10px]' : 'text-xs'} text-gray-500 dark:text-gray-400 select-none`}>
                Output
              </summary>
              <pre className={`mt-1 font-mono ${compact ? 'text-[10px]' : 'text-xs'} text-gray-700 dark:text-gray-300 whitespace-pre-wrap break-all bg-gray-50 dark:bg-gray-800 rounded p-2 max-h-64 overflow-y-auto`}>
                {successOutput}
              </pre>
            </details>
          )}
          {d?.script_content && (
            <details className="mt-1">
              <summary className={`cursor-pointer ${compact ? 'text-[10px]' : 'text-xs'} text-gray-500 dark:text-gray-400 select-none`}>
                View main.py
              </summary>
              <pre className={`mt-1 font-mono ${compact ? 'text-[10px]' : 'text-xs'} text-gray-700 dark:text-gray-300 whitespace-pre-wrap break-all bg-gray-50 dark:bg-gray-800 rounded p-2 max-h-64 overflow-y-auto`}>
                {d.script_content}
              </pre>
            </details>
          )}
        </div>
      </CompactWrapper>
    )
  }

  // Default case for unknown event types
  return (
    <div className={`bg-gray-50 dark:bg-gray-900/20 border border-gray-200 dark:border-gray-800 rounded-md ${compact ? 'p-2' : 'p-3'}`}>
      <div className={`${compact ? 'text-xs' : 'text-sm'} text-gray-700 dark:text-gray-300`}>
        <div className="font-medium">Unknown Event Type: {event.type}</div>
        <div className={`${compact ? 'text-[10px]' : 'text-xs'} text-gray-500 dark:text-gray-400 mt-1`}>
          Event data: {JSON.stringify(event.data, null, 2)}
        </div>
      </div>
    </div>
  )
}, (prevProps, nextProps) => {
  // An event card is a pure function of its event plus the few display flags
  // below. The comparator used to also diff the tree view's node/stat props;
  // those are gone, and with them the reason this needed to be subtle.
  return prevProps.event === nextProps.event &&
    prevProps.event.id === nextProps.event.id &&
    prevProps.mode === nextProps.mode &&
    prevProps.compact === nextProps.compact &&
    prevProps.isApproving === nextProps.isApproving &&
    prevProps.hideOrchestratorContext === nextProps.hideOrchestratorContext
})

// Event list component for displaying multiple events
// NOTE: Event filtering is now done on the backend
// Frontend no longer filters events - backend returns pre-filtered events

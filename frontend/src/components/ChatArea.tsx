import { sessionStreamingState, type SessionActivitySnapshot } from '../utils/sessionStreamingState'
import { isForegroundSessionEvent } from '../../shared/session/foreground'
import { useEffect, useRef, useCallback, forwardRef, useImperativeHandle, useMemo, useState, lazy, Suspense, type ComponentType, type ForwardedRef, type ReactNode } from 'react'
import { normalizeEventViewMode } from '../stores/useChatStore'
import { intermediateUpdateFromTranscriptChunk, normalizeTranscriptChunkEvents } from '../utils/transcriptChunkUpdates'
import { applyLiveInputConfirmations, readLiveInputConfirmation, resolveLiveInputConfirmations, stampLiveInputIdentity, withLiveInputReceipt } from '../utils/liveInputReceipt'
import { useRenderLogger, useMemoLogger } from '../utils/renderLogger'
import { acquireBuilderSubmission, isConfirmedUndeliveredSubmission, type ChatSubmissionOptions } from '../utils/chatSubmissionTarget'
import { isTurnRunningConflict } from '../services/turnRunningRetry'
import { configureChatQueueController } from '../utils/chatQueueController'
import { captureChatIdentity, isChatIdentityCurrent } from '../utils/chatIdentity'
import {
  liveInputSubmissionCoordinator,
  shouldRefreshSessionEventStream,
} from '../utils/liveInputSubmission'
import { eventBelongsToSession, sessionOwnsGlobalChatIndicators } from '../utils/sessionEventWorkingSet'
import { useShallow } from 'zustand/react/shallow'
import { agentApi, resetSessionId, getSessionId } from '../services/api'
import type { PollingEvent, ExtendedLLMConfiguration, SSEEventMessage, SSEStatusMessage, ExecutionOptions } from '../services/api-types'
import type { AgentMode } from '../stores/types'
import { ChatInput } from './ChatInput'
import { SessionStopButton } from './SessionStopButton'
import { TerminalEventTranscript } from './TerminalEventTranscript'
import { followTranscriptLatest } from './useTranscriptScroll'
import { MainAgentTerminal } from './MainAgentTerminal'
import { WorkflowModeHandler, type WorkflowModeHandlerRef } from './workflow'
import { useWorkflowStore } from '../stores/useWorkflowStore'
import { useAppStore, useLLMStore, useMCPStore, useChatStore, useGlobalPresetStore } from '../stores'
import { useCapabilitiesStore } from '../stores/useCapabilitiesStore'
import { useModeStore, type ModeCategory } from '../stores/useModeStore'
import { PreviousChatHistoryPanel } from './PreviousChatHistoryPanel'
import { resolveChatSurface, resolveWorkflowChatSurface } from './resolveChatSurface'
import { PresetSelectionOverlay } from './PresetSelectionOverlay'
import { ModeSwitchDialog } from './ui/ModeSwitchDialog'
import type { ChatTab } from '../stores/useChatStore'

import { appendTimelineAndApplyConfirmations, hydrateTabEvents, restoreSession } from '../utils/sessionRestore'
import { hydrateExecutionConversation } from '../utils/executionConversationRestore'
import { isExecutionConversationTab } from '../utils/sessionEventSource'
import { nextForwardCursor } from '../utils/forwardCursor'
import { clientMessageEventId, readClientMessageId } from '../utils/clientMessageIdentity'
import { conversationToRestoredEvents } from '../../shared/session/restore'
import { logger } from '../utils/logger'

import { useResumePreviousChat } from '../hooks/useResumePreviousChat'
import {
  determineModeFlag,
  buildLLMConfigWithApiKeys,
  buildQueryRequestPayload,
  applyAgentProfileBinding,
  buildAgentProfileChatRequest,
  resolveOrCreateTab,
  createUserMessageEvent,
  withoutOptimisticUserMessage,
  validateExecutionGroups,
  isChatCompatiblePhase,
  remainingWorkflowContextAfterSubmission,
} from '../utils/chatSubmitHelpers'
import {
  shouldKeepChatSessionSubscribed,
} from '../utils/workflowSessionSubscription'
import { activateTab } from '../utils/activateTab'
import { selectWorkflowPreset } from '../utils/workflowNavigation'
import { ProductChatSurface } from '../platform/chat/ProductChatSurface'
import { submissionFailure } from '../platform/chat/submissionFailure'
import { getDisplaySafeUserMessageContent } from '../utils/chatMessageContent'
import { recordChatDeliveryTelemetry, recordChatSubmissionTelemetry } from '../utils/chatDeliveryTelemetry'

// Stable empty array to avoid infinite re-render loops in Zustand selectors
// (a new [] on every selector call breaks referential equality checks)
const EMPTY_EVENTS: PollingEvent[] = []
const AUTO_NOTIFICATION_PREFIX = '[AUTO-NOTIFICATION]'
const ENABLE_LEGACY_FRONTEND_AUTO_NOTIFICATIONS = false
const STALE_STREAMING_RECOVERY_GRACE_MS = 10000
// Grace window after a resume marker appears. The normal product surface is an
// event transcript, so we wait only for durable conversation events/SSE rather
// than probing the internal tmux terminal inventory.
const RESUME_SETTLE_MS = 10000
const STREAMING_EVENT_TYPES = new Set(['streaming_start', 'streaming_chunk', 'streaming_end'])
// Terminal inspection (the child/step terminal rail) is developer diagnostics,
// not product navigation: it stays behind the server's explicit
// AGENTWORKS_RUNTIME_DEBUG opt-in (run_server_with_logging.sh
// --enable-chat-terminal-debugs), reported back through GET /api/capabilities
// as runtime_debug. Restored 2026-09-11 -- this panel existed before
// 07ec24c62 ("Keep main tmux available without child terminal rail") dropped
// its wiring from ChatArea while leaving the component, the server capability
// flag, and the server-side diagnostic endpoints themselves fully intact.
const RuntimeDiagnosticsPanel = lazy(() => import('./TerminalCenter').then(module => ({ default: module.TerminalCenter })))

type RuntimeEventScope = {
  kind: 'session' | 'delegation' | 'workshop'
  id?: string
}

function isStreamingEventType(type: unknown): type is string {
  return typeof type === 'string' && STREAMING_EVENT_TYPES.has(type)
}

const AUTO_NOTIFICATION_MAX_AGE_MS = 5 * 60 * 1000

function getEventTimestampMs(event: PollingEvent): number | null {
  if (!event.timestamp) return null
  const parsed = Date.parse(event.timestamp)
  return Number.isFinite(parsed) ? parsed : null
}

function createNextOptimisticUserMessage(sessionId: string, content: string, existing: PollingEvent[], clientMessageId: string): PollingEvent {
  const indexed = existing.filter(event => typeof event.event_index === 'number')
  const eventIndex = indexed.length > 0
    ? indexed.reduce((maxIndex, event) => Math.max(maxIndex, event.event_index as number), -1) + 1
    : undefined
  const latestTimestamp = existing.reduce((latest, event) => {
    const timestamp = getEventTimestampMs(event)
    return timestamp === null ? latest : Math.max(latest, timestamp)
  }, 0)
  const event = createUserMessageEvent(
    content,
    eventIndex,
    new Date(Math.max(Date.now(), latestTimestamp + 1)).toISOString(),
    sessionId,
  )
  // Shares the id of the server's durable row for this submission, which
  // replaces it (and repositions it) when it arrives.
  const outer = event.data as unknown as Record<string, unknown>
  const inner = (outer.data ?? {}) as Record<string, unknown>
  return {
    ...event,
    id: clientMessageEventId(clientMessageId),
    data: { ...outer, data: { ...inner, metadata: { client_message_id: clientMessageId, provisional: true } } } as PollingEvent['data'],
  }
}

function formatAutoNotificationTime(event: PollingEvent): string {
  const ts = getEventTimestampMs(event)
  return new Date(ts ?? Date.now()).toLocaleTimeString([], {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  })
}

function getOwnedTerminalStreamKeys(
  sessionId: string,
  event: PollingEvent,
  innerData: Record<string, unknown> | undefined,
  agentEvent: Record<string, unknown> | undefined,
  correlationId: unknown,
  metadata?: Record<string, unknown>,
): string[] {
  const eventRecord = event as unknown as Record<string, unknown>
  const normalizeOwnerId = (value: unknown): string | null => {
    if (typeof value !== 'string') return null
    const trimmed = value.trim()
    if (!trimmed || trimmed.startsWith('main:')) return null
    for (const prefix of ['delegation:', 'workflow:', 'background:', 'agent:', 'batch:']) {
      if (trimmed.startsWith(prefix)) {
        const unprefixed = trimmed.slice(prefix.length).trim()
        return unprefixed && !unprefixed.startsWith('main:') ? unprefixed : null
      }
    }
    return trimmed
  }

  const candidates: unknown[] = [
    eventRecord.execution_id,
    metadata?.execution_id,
    innerData?.execution_id,
    agentEvent?.execution_id,
    metadata?.owner_execution_id,
    metadata?.execution_owner_id,
    innerData?.delegation_id,
    agentEvent?.delegation_id,
    metadata?.delegation_id,
    innerData?.background_agent_id,
    agentEvent?.background_agent_id,
    metadata?.background_agent_id,
    innerData?.agent_id,
    agentEvent?.agent_id,
    metadata?.agent_id,
    innerData?.agent_name,
    agentEvent?.agent_name,
    metadata?.orchestrator_agent_name,
    typeof correlationId === 'string' && (correlationId.startsWith('delegation-') || correlationId.startsWith('workshop-'))
      ? correlationId
      : undefined,
    metadata?.workshop_step_id,
    metadata?.current_step_id,
    metadata?.orchestrator_step_id,
    metadata?.workflow_step_id,
    metadata?.step_id,
  ]
  for (const candidate of candidates) {
    const ownerId = normalizeOwnerId(candidate)
    if (ownerId) return [`${sessionId}:${ownerId}`]
  }
  return []
}


function isStaleAutoNotificationEvent(event: PollingEvent): boolean {
  const ts = getEventTimestampMs(event)
  return ts !== null && Date.now() - ts > AUTO_NOTIFICATION_MAX_AGE_MS
}

function getUserMessageContent(event: PollingEvent): string {
  const agentEvent = event.data as Record<string, unknown> | undefined
  const innerData = agentEvent?.data as Record<string, unknown> | undefined
  const content = innerData?.content ?? agentEvent?.content
  return typeof content === 'string' ? content : ''
}

function createSubmissionErrorEvent(sessionId: string, error: unknown): PollingEvent {
  const failure = submissionFailure(error)
  return {
    id: `conversation-error-${globalThis.crypto.randomUUID()}`,
    type: 'conversation_error',
    timestamp: new Date().toISOString(),
    session_id: sessionId,
    data: {
      type: 'conversation_error',
      data: {
        error: failure.message,
        code: failure.code,
        provider: failure.provider,
        technical_details: failure.technicalDetails,
      },
    } as PollingEvent['data'],
  }
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  return value && typeof value === 'object' ? value as Record<string, unknown> : undefined
}

function firstString(...values: unknown[]): string | undefined {
  for (const value of values) {
    if (typeof value === 'string' && value.trim()) return value.trim()
  }
  return undefined
}

function isRootLikeExecutionId(value?: string): boolean {
  return !value || value.startsWith('main:') || value.startsWith('session:')
}

function getEventPayloadParts(event: PollingEvent) {
  const eventRecord = event as unknown as Record<string, unknown>
  const agentEvent = asRecord(event.data)
  const innerData = asRecord(agentEvent?.data)
  const metadata = asRecord(innerData?.metadata) || asRecord(agentEvent?.metadata)
  return { eventRecord, agentEvent, innerData, metadata }
}

function getRuntimeEventScope(event: PollingEvent): RuntimeEventScope {
  const { eventRecord, agentEvent, innerData, metadata } = getEventPayloadParts(event)
  const component = firstString(eventRecord.component, innerData?.component, agentEvent?.component)
  const correlationId = firstString(
    eventRecord.correlation_id,
    innerData?.correlation_id,
    agentEvent?.correlation_id,
    metadata?.correlation_id
  )
  const delegationId = firstString(innerData?.delegation_id, agentEvent?.delegation_id, metadata?.delegation_id)
  const workshopStepId = firstString(metadata?.workshop_step_id, innerData?.workshop_step_id, agentEvent?.workshop_step_id)
  const executionId = firstString(eventRecord.execution_id)
  const parentExecutionId = firstString(
    eventRecord.parent_execution_id,
    metadata?.parent_execution_id,
    innerData?.parent_execution_id,
    agentEvent?.parent_execution_id
  )
  const backgroundAgentId = firstString(
    innerData?.background_agent_id,
    agentEvent?.background_agent_id,
    innerData?.agent_id,
    agentEvent?.agent_id
  )
  const executionKind = firstString(eventRecord.execution_kind)

  if (component?.startsWith('delegation-')) return { kind: 'delegation', id: component }
  if (delegationId?.startsWith('delegation-')) return { kind: 'delegation', id: delegationId }
  if (correlationId?.startsWith('delegation-')) return { kind: 'delegation', id: correlationId }
  if ((executionKind === 'workflow_step' || executionId?.startsWith('workflow-step:')) && !isRootLikeExecutionId(executionId)) {
    return { kind: 'workshop', id: executionId }
  }
  if (!isRootLikeExecutionId(parentExecutionId)) return { kind: 'workshop', id: parentExecutionId }
  if (!isRootLikeExecutionId(backgroundAgentId)) return { kind: 'workshop', id: backgroundAgentId }
  if (correlationId?.startsWith('workshop-')) return { kind: 'workshop', id: correlationId }
  if (workshopStepId?.startsWith('workshop-')) return { kind: 'workshop', id: workshopStepId }

  return { kind: 'session' }
}

function handleLiveStreamingEvent(
  event: PollingEvent,
  actualSessionId: string,
  chatStore: ReturnType<typeof useChatStore.getState>
) {
  // PLAT-106: streaming text is session-owned state. The response envelope
  // alone is not authoritative — an event that names a different owning
  // session must never drive another session's visible stream.
  if (!eventBelongsToSession(actualSessionId, event)) return

  const { agentEvent, innerData, metadata } = getEventPayloadParts(event)
  const scope = getRuntimeEventScope(event)
  const correlationId = innerData?.correlation_id ?? agentEvent?.correlation_id
  const isTerminalStreaming = metadata?.kind === 'terminal'
  const ownedTerminalKeys = isTerminalStreaming
    ? getOwnedTerminalStreamKeys(actualSessionId, event, innerData, agentEvent, correlationId, metadata)
    : []

  if (event.type === 'streaming_start') {
    if (ownedTerminalKeys.length > 0) {
      // Child terminal snapshots are already retained by the backend terminal
      // store. Keeping another full-screen copy per unopened owner in Zustand
      // made Electron memory grow with every agent and had no UI consumer.
      return
    } else if (scope.kind === 'delegation' && scope.id) {
      chatStore.clearDelegationStreamingText(scope.id)
    } else if (scope.kind === 'session') {
      chatStore.clearStreamingText(actualSessionId)
    } else if (scope.kind === 'workshop' && scope.id) {
      chatStore.clearExecutionStreamingText(scope.id)
    }
    return
  }

  if (event.type === 'streaming_chunk') {
    const rawContent = innerData?.content ?? agentEvent?.content
    const content = typeof rawContent === 'string' ? rawContent : ''
    if (!content) return

    const rawIndex = innerData?.chunk_index ?? agentEvent?.chunk_index
    const chunkIndex = typeof rawIndex === 'number' ? rawIndex : -1
    // is_delta decides how this chunk joins the text so far (verbatim for
    // fragment streams like pi, newline-separated for block streams like
    // claude-code); source is the backend's authoritative terminal/clean
    // classification. Both are carried on StreamingChunkEvent — dropping them
    // here is what made block-provider output render as one run-on blob.
    const rawIsDelta = innerData?.is_delta ?? agentEvent?.is_delta
    // Modern streaming packets put this on the typed chunk payload; a few
    // retained/bridge envelopes preserve it in metadata instead. Either way,
    // source is authoritative and must not be inferred from the screen text.
    const rawSource = innerData?.source ?? agentEvent?.source ?? metadata?.source
    const chunkMeta = {
      isDelta: typeof rawIsDelta === 'boolean' ? rawIsDelta : undefined,
      source: typeof rawSource === 'string' ? rawSource : undefined,
    }
    if (ownedTerminalKeys.length > 0) {
      return
    } else if (chunkMeta.source?.trim().toLowerCase() === 'terminal' && scope.kind === 'session') {
      // A terminal chunk is a complete tmux/pane frame, not assistant prose.
      // Keeping it in streamingText made the chat render the previous Claude
      // screen again after a follow-up message. Store it only in the dedicated
      // snapshot channel; the product transcript deliberately never renders
      // that channel as a chat response.
      chatStore.setStreamingTerminalSnapshot(actualSessionId, chunkIndex, content)
      return
    } else if (scope.kind === 'delegation' && scope.id) {
      if (chunkIndex === 0 || chunkIndex === 1) chatStore.clearDelegationStreamingText(scope.id)
      chatStore.appendDelegationStreamingChunk(scope.id, chunkIndex, content)
    } else if (scope.kind === 'workshop' && scope.id) {
      if (chunkIndex === 0 || chunkIndex === 1) chatStore.clearExecutionStreamingText(scope.id)
      chatStore.appendExecutionStreamingChunk(actualSessionId, scope.id, chunkIndex, content)
    } else if (scope.kind === 'session') {
      if (chunkIndex === 0 || chunkIndex === 1) chatStore.clearStreamingText(actualSessionId)
      // A whole-message narration chunk becomes its own durable row (both
      // callers push intermediateUpdateFromTranscriptChunk's result), so it
      // must not also go into the live buffer -- block chunks accumulate
      // there newline-joined for the whole turn, and the tail then re-showed
      // every narration of the turn concatenated under the rows that already
      // carried them.
      if (intermediateUpdateFromTranscriptChunk(event)) return
      chatStore.appendStreamingChunk(actualSessionId, chunkIndex, content, chunkMeta)
    }
    return
  }

  if (event.type === 'streaming_end' && ownedTerminalKeys.length > 0) {
    return
  } else if (event.type === 'streaming_end' && scope.kind === 'session') {
    chatStore.clearStreamingStatus(actualSessionId)
    const sidForClear = actualSessionId
    const textSnapshot = useChatStore.getState().streamingText[sidForClear]
    setTimeout(() => {
      const currentText = useChatStore.getState().streamingText[sidForClear]
      const match = currentText === textSnapshot
      if (currentText && match) {
        useChatStore.getState().clearStreamingText(sidForClear)
      }
    }, 500)
  } else if (event.type === 'streaming_end' && scope.kind === 'workshop' && scope.id) {
    chatStore.clearExecutionStreamingStatus(scope.id)
    const executionIdForClear = scope.id
    const textSnapshot = useChatStore.getState().executionStreaming[executionIdForClear]?.text
    setTimeout(() => {
      const current = useChatStore.getState().executionStreaming[executionIdForClear]
      if (current && current.text === textSnapshot) {
        useChatStore.getState().clearExecutionStreamingText(executionIdForClear)
      }
    }, 500)
  }
}

function withDisplaySafeUserMessage(event: PollingEvent): PollingEvent {
  if (event.type !== 'user_message') return event

  const content = getUserMessageContent(event)
  const safeContent = getDisplaySafeUserMessageContent(content)
  if (!content || safeContent === content) return event

  const agentEvent = event.data as Record<string, unknown> | undefined
  const innerData = agentEvent?.data as Record<string, unknown> | undefined
  if (innerData) {
    return {
      ...event,
      data: {
        ...agentEvent,
        data: {
          ...innerData,
          content: safeContent,
        },
      } as PollingEvent['data'],
    }
  }

  return {
    ...event,
    data: {
      ...agentEvent,
      content: safeContent,
    } as PollingEvent['data'],
  }
}

function getQueuedAutoNotificationTimestampMs(message: string): number | null {
  const match = message.match(/\[(\d{2}):(\d{2}):(\d{2})\]/)
  if (!match) return null

  const now = new Date()
  const parsed = new Date(now)
  parsed.setHours(Number(match[1]), Number(match[2]), Number(match[3]), 0)

  // Handle notifications carried across midnight.
  if (parsed.getTime() - now.getTime() > 60 * 1000) {
    parsed.setDate(parsed.getDate() - 1)
  }

  return parsed.getTime()
}

function isStaleQueuedAutoNotification(message: string): boolean {
  const ts = getQueuedAutoNotificationTimestampMs(message)
  return ts !== null && Date.now() - ts > AUTO_NOTIFICATION_MAX_AGE_MS
}

export interface ChatContentRendererProps {
  events: PollingEvent[]
  isStreaming: boolean
  isRestoring: boolean
  streamingText: string
  streamingStatus?: string
  hasOlder?: boolean
  loadingOlder?: boolean
  historyError?: string
  onLoadOlder?: () => void
  landingContent?: ReactNode
  onRetryLastMessage?: () => void | Promise<void>
  /** Submit a message on the user's behalf (a suggestion pill, a button in the transcript). */
  onSubmitQuery?: (query: string) => void
}

function prepareQueuedChatMessages(queuedMessages: string[]) {
    // Separate human messages from auto-notifications
    const humanMessages = queuedMessages.filter(m => !m.startsWith(AUTO_NOTIFICATION_PREFIX))
    const autoMessages = queuedMessages.filter(m => m.startsWith(AUTO_NOTIFICATION_PREFIX))
    const freshAutoMessages = autoMessages.filter(m => !isStaleQueuedAutoNotification(m))

    // Human messages: combine all as-is
    // Auto-notifications: if multiple, condense to first line of each to avoid overwhelming the agent
    const parts: string[] = []
    if (humanMessages.length > 0) {
      parts.push(humanMessages.map(m => m.trim()).join('\n\n'))
    }
    if (freshAutoMessages.length > 0) {
      if (freshAutoMessages.length === 1) {
        parts.push(freshAutoMessages[0].trim())
      } else {
        // Multiple auto-notifications: take first line of each and combine into a compact summary
        const summaryLines = freshAutoMessages.map(m => {
          const firstLine = m.trim().split('\n')[0]
          return firstLine
        })
        parts.push(`${AUTO_NOTIFICATION_PREFIX} Multiple step completions:\n${summaryLines.map(l => l.replace(AUTO_NOTIFICATION_PREFIX, '').trim()).map(l => `- ${l}`).join('\n')}`)
      }
    }
    const combinedMessage = parts.join('\n\n')

    return { message: combinedMessage, isAutoNotification: humanMessages.length === 0 && freshAutoMessages.length > 0 }
}

interface ChatAreaProps {
  // New chat handler
  onNewChat?: () => void
  // Hide header when used inside another layout (like WorkflowLayout)
  hideHeader?: boolean
  // Hide input area when used inside workflow mode
  hideInput?: boolean
  // Compact mode for smaller font sizes (used in workflow layout)
  compact?: boolean
  // Tab ID - if provided, use this tab's session ID (works for both chat and workflow modes).
  // Pass null explicitly to disable all active behavior (SSE, polling, queue) — used when
  // this ChatArea instance is hidden behind another instance for the same tab.
  tabId?: string | null
  // Multi-agent landing previous-chats panel: icon-only (compact) tabs when the
  // chat sits in the narrow rail; labeled tabs when it fills the pane (org panel
  // minimized). The panel always renders in fill mode so it scrolls in both.
  previousChatsCompact?: boolean
  // Scope the shared previous-chat landing panel to one product/workflow
  // workspace. Without this, the panel intentionally shows the user's global
  // AgentWorks history, which is not appropriate inside a project surface.
  previousChatsWorkspacePath?: string
  // Product surfaces can reuse AgentWorks history without automation tabs.
  previousChatsRecentOnly?: boolean
  // Product shells with an explicit history/workshop tab can show the scoped
  // previous-chat browser independently of the active tab's session state.
  // This avoids hiding history when a permanent launch tab retained a stale
  // session id after an upgrade or browser restore.
  forcePreviousChats?: boolean
  // Content shown above the composer when a workflow's persistent Chat has no
  // saved conversation yet.
  workflowLandingContent?: React.ReactNode
  // Product surfaces can replace the default previous-chat landing
  // without forking the shared stream, terminal, event, and composer stack.
  landingContent?: React.ReactNode
  // Product surfaces can replace the developer-facing terminal presentation
  // while keeping the exact same session, streaming, steering, cancellation,
  // persistence, and submission transport underneath.
  contentRenderer?: ComponentType<ChatContentRendererProps>
  // Product input removes provider/runtime controls while preserving uploads,
  // send, cancel, and live steering behavior.
  inputVariant?: 'default' | 'product'
  // Run each submission through the normal server-owned query lifecycle. The
  // coding CLI may still use tmux internally, but the UI receives structured
  // progress and a definitive completion event instead of relying on an idle
  // retained-pane live-input shortcut.
  fullTurnStreaming?: boolean
  // Product surfaces can render the structured per-turn token usage beneath a
  // final response. The shared AgentWorks surface keeps this internal by
  // default to avoid changing its transcript density.
  showConversationUsage?: boolean
  // Product deployments with one fixed runtime can omit a redundant badge.
  hideRuntimeStatus?: boolean
  // Show AgentWorks' compact running spinner beside the microphone. When this
  // is enabled the model badge stays a stable status dot, so the composer never
  // renders two activity spinners for the same turn.
  showCompactRuntimeLoading?: boolean
  // Work reuses AgentWorks' queued-message UI and exposes an explicit Steer
  // action even though the underlying provider is a coding CLI.
  showProductSteerAction?: boolean
  // Product surfaces normally hide the retained terminal; Work exposes it.
  showProductTerminalControl?: boolean
  // Product chats otherwise have no generic header in which to start fresh.
  showNewChatAction?: boolean
  /** Product-specific composer placeholder (the product variant otherwise says "Describe what you want to create…"). */
  composerPlaceholder?: string
}

// Ref interface for ChatArea component
export interface ChatAreaRef {
  handleNewChat: (targetTabId?: string) => void
  resetChatState: () => void
  refreshWorkflowPresets: () => Promise<void>
  submitQuery: (query: string, executionOptions?: ExecutionOptions) => Promise<void>
  getEvents: () => PollingEvent[]
  isStreaming: boolean
  currentWorkflowPhase: string
}


// Global flag to ensure auto-restore only happens once per page load
let globalHasRestored = false

// Inner component for chat area
const ChatAreaInner = forwardRef((props: ChatAreaProps, ref: ForwardedRef<ChatAreaRef>) => {
  const { onNewChat, hideInput = false, compact = false, tabId, previousChatsCompact = false, previousChatsWorkspacePath, previousChatsRecentOnly = false, forcePreviousChats = false, workflowLandingContent, landingContent, contentRenderer: ContentRenderer, inputVariant = 'default', fullTurnStreaming = false, showConversationUsage = false, hideRuntimeStatus = false, showCompactRuntimeLoading = false, showProductSteerAction = false, showProductTerminalControl = false, showNewChatAction = false , composerPlaceholder} = props
  // Product mode is a complete shared surface, not just a simplified composer.
  // Products may still supply a renderer for domain-specific presentation, but
  // every new product gets the durable transcript and normalized error UI by
  // default simply by selecting inputVariant="product".
  const EffectiveContentRenderer = ContentRenderer ?? (inputVariant === 'product' ? ProductChatSurface : undefined)
  // null means "inactive — don't subscribe to any tab or run any effects"
  const isInactive = tabId === null

  // Store subscriptions
  const {
    agentMode,
    setCurrentQuery,
  } = useAppStore(useShallow(state => ({
    agentMode: state.agentMode,
    setCurrentQuery: state.setCurrentQuery,
  })))

  const { selectedModeCategory, getAgentModeFromCategory } = useModeStore(useShallow(state => ({
    selectedModeCategory: state.selectedModeCategory,
    getAgentModeFromCategory: state.getAgentModeFromCategory
  })))
  const { getActivePreset, applyPreset, clearActivePreset, currentPresetServers } = useGlobalPresetStore(useShallow(state => ({
    getActivePreset: state.getActivePreset,
    applyPreset: state.applyPreset,
    clearActivePreset: state.clearActivePreset,
    currentPresetServers: state.currentPresetServers
  })))

  // Derive correct agent mode from selectedModeCategory (source of truth)
  const correctAgentMode = useMemo(() => {
    if (selectedModeCategory) {
      return getAgentModeFromCategory(selectedModeCategory) as AgentMode
    }
    return agentMode // Fallback to agentMode if selectedModeCategory is null
  }, [selectedModeCategory, agentMode, getAgentModeFromCategory])

  // LLM provider configs are read via useLLMStore.getState() in helpers

  const {
    toolList: allTools,
    chatSelectedServers,
    workflowSelectedServers,
  } = useMCPStore(useShallow(state => ({
    toolList: state.toolList,
    chatSelectedServers: state.chatSelectedServers,
    workflowSelectedServers: state.workflowSelectedServers,
  })))
  const selectedServers = selectedModeCategory === 'workflow'
    ? workflowSelectedServers
    : chatSelectedServers

  // All servers that are currently connected (status=ok)
  const connectedServers = useMemo<Set<string>>(
    () => new Set(allTools
      .filter(t => t.status === 'ok')
      .map(t => t.server)
      .filter((server): server is string => typeof server === 'string' && server.length > 0)),
    [allTools]
  )

  // Get active tab reactively (works for both chat and workflow modes)
  // Use selector to ensure reactivity when tab config changes
  const activeTabIdFromStore = useChatStore(state => state.activeTabId)
  // null = explicitly inactive (no tab); undefined = use store's active tab
  const targetTabId = isInactive ? null : (tabId || activeTabIdFromStore)
  const activeTab = useChatStore(state =>
    targetTabId ? state.chatTabs[targetTabId] : undefined
  )
  // PERF FIX: Stable tab-session key to avoid phantom re-renders.
  //
  // PROBLEM: Previously `const chatTabs = useChatStore(state => state.chatTabs)` subscribed
  // to the full chatTabs object. Every `setTabStreaming`, `setTabCompleted`, `setTabConfig`
  // call creates a new `chatTabs` reference (Zustand immutable update), causing ChatArea
  // to re-render even when no tab/session was added or removed. This caused 10-20 phantom
  // renders between actual data changes (visible as "no dep change" in render logs).
  //
  // FIX: Derive a stable string key from tab IDs + session IDs + modes. This key only
  // changes when tabs are created/deleted or sessions are assigned — NOT when tab properties
  // tabsWithSessions, tabsWithActiveSessions) recompute only when this key changes.
  const tabSessionKey = useChatStore(state => {
    const tabs = state.chatTabs
    const parts: string[] = []
    for (const id of Object.keys(tabs)) {
      const t = tabs[id]
      parts.push(`${id}:${t.sessionId || ''}:${t.metadata?.mode || ''}`)
    }
    return parts.sort().join(',')
  })

  // Determine which servers to use based on mode category
  // CRITICAL: Workflow preset servers should ONLY be used in workflow mode, never leak into multi-agent mode
  const effectiveServers = useMemo<string[]>(() => {
    // For workflow mode, use preset servers
    if (selectedModeCategory === 'workflow') {
      const workflowServers = currentPresetServers.length > 0 ? currentPresetServers : selectedServers
      return workflowServers.filter((server): server is string => typeof server === 'string')
    }
    // For multi-agent mode, ALWAYS use tab's selected servers from config (if available), otherwise fall back to global
    // NEVER use currentPresetServers in multi-agent mode - workflow preset state is isolated to workflow mode only
    const isChatLike = selectedModeCategory === 'multi-agent'
    const tabSelectedServers: string[] = ((isChatLike && activeTab?.config)
      ? activeTab.config.selectedServers
      : selectedServers).filter((server): server is string => typeof server === 'string')

    // Product profiles are explicit opt-in: an empty project selection means
    // no MCP servers. It must never inherit every account-connected server.
    if (tabSelectedServers.length === 0) {
      if (activeTab?.metadata?.agentProfileId) return ["NO_SERVERS"]
      const all = Array.from(connectedServers)
      return all.length > 0 ? all : ["NO_SERVERS"]
    }
    // Filter out servers that aren't currently connected (status=ok).
    // Stale servers from localStorage could block queries if sent to backend.
    const filtered = tabSelectedServers.filter((s): s is string => s === "NO_SERVERS" || connectedServers.has(s))
    return filtered
  }, [
    selectedModeCategory,
    currentPresetServers,
    selectedServers,
    connectedServers,
    activeTab?.config,
    activeTab?.metadata?.agentProfileId
  ])

  // Filter tools to only include those from effective servers
  // If "NO_SERVERS" is selected, return empty tools (pure LLM mode)
  const enabledTools = useMemo(() => {
    if (effectiveServers.includes("NO_SERVERS")) {
      return []
    }

    return allTools.filter(tool =>
      tool.server && effectiveServers.includes(tool.server)
    )
  }, [allTools, effectiveServers])

  // PERF FIX: Derive tab lists from stable tabSessionKey instead of raw chatTabs reference.
  // Uses getState() for the actual tab objects (avoids subscription), and tabSessionKey
  // as the recomputation trigger (only changes on tab add/remove/session change).
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const allTabs = useMemo(() => Object.values(useChatStore.getState().chatTabs), [tabSessionKey])
  const tabsWithSessions = useMemo(() => allTabs.filter(tab => tab.sessionId), [allTabs])

  // No observer ID syncing needed - sessions are used directly

  const {
    // Chat state
    isStreaming,
    setIsStreaming,
    lastEventIndex,
    setLastEventIndex,
    pollingInterval,
    // Deprecated: totalEvents, setTotalEvents, setLastEventCount, events, setEvents removed
    getTabEvents,
    addTabEvents,
    getTabLastEventIndex,
    setTabLastEventIndex,
    setHasActiveChat,
    autoScroll,
    setAutoScroll,
    finalResponse,
    setIsCompleted,
    isLoadingHistory,
    sessionState,
    isCheckingActiveSessions,
    currentWorkflowPhase,
    setCurrentWorkflowPhase,
    setCurrentWorkflowQueryId,
    addToast,
    resetChatState,
    isAtBottom,
    switchTab,
    setTabSyntheticTurn,
    setTabCanSteer,
  } = useChatStore(useShallow(state => ({
    isStreaming: state.isStreaming,
    setIsStreaming: state.setIsStreaming,
    lastEventIndex: state.lastEventIndex,
    setLastEventIndex: state.setLastEventIndex,
    pollingInterval: state.pollingInterval,
    getTabEvents: state.getTabEvents,
    addTabEvents: state.addTabEvents,
    getTabLastEventIndex: state.getTabLastEventIndex,
    setTabLastEventIndex: state.setTabLastEventIndex,
    setHasActiveChat: state.setHasActiveChat,
    autoScroll: state.autoScroll,
    setAutoScroll: state.setAutoScroll,
    finalResponse: state.finalResponse,
    setIsCompleted: state.setIsCompleted,
    isLoadingHistory: state.isLoadingHistory,
    sessionState: state.sessionState,
    isCheckingActiveSessions: state.isCheckingActiveSessions,
    currentWorkflowPhase: state.currentWorkflowPhase,
    setCurrentWorkflowPhase: state.setCurrentWorkflowPhase,
    setCurrentWorkflowQueryId: state.setCurrentWorkflowQueryId,
    addToast: state.addToast,
    resetChatState: state.resetChatState,
    isAtBottom: state.isAtBottom,
    switchTab: state.switchTab,
    setTabSyntheticTurn: state.setTabSyntheticTurn,
    setTabCanSteer: state.setTabCanSteer,
  })))

  // Session-specific selector: only re-renders when the ACTIVE session's events change
  // (not when any other session gets events)
  const activeSessionId = activeTab?.sessionId
  const activeEventViewMode = normalizeEventViewMode(activeTab?.viewMode)
  const serverRuntimeDiagnosticsEnabled = useCapabilitiesStore(state => state.capabilities?.runtime_debug === true)
  const showMainTerminal = activeEventViewMode === 'terminal'
  const showRuntimeDiagnostics = serverRuntimeDiagnosticsEnabled && showMainTerminal

  // processEventsResponse runs for every polled session and does not carry
  // activeSessionId in its dependency array, so reading it through a ref keeps
  // the foreground-ownership check current instead of closing over a stale one.
  // Also read inside polling callbacks, which must not be rebuilt (and their
  // timers restarted) every time the user switches tabs.
  const activeSessionIdRef = useRef<string | undefined>(activeSessionId)
  useEffect(() => {
    activeSessionIdRef.current = activeSessionId
  }, [activeSessionId])
  const tabEvents = useChatStore((state) =>
    activeSessionId ? state.tabEvents[activeSessionId] || EMPTY_EVENTS : EMPTY_EVENTS
  )
  const formattedTranscriptHydratedRef = useRef<string | null>(null)
  useEffect(() => {
    if (activeEventViewMode !== 'formatted') return
    const isVideoStudio = activeTab?.metadata?.agentProfileId === 'video-studio'
    const isWorkflowChat = activeTab?.metadata?.mode === 'workflow'
    if ((!isVideoStudio && !isWorkflowChat) || isExecutionConversationTab(activeTab)) return

    const workspacePath = activeTab?.metadata?.agentProfileWorkspace ||
      (isWorkflowChat ? getActivePreset('workflow')?.selectedFolder?.filepath : undefined)
    if (
      !activeSessionId ||
      (isVideoStudio && !workspacePath) ||
      formattedTranscriptHydratedRef.current === activeSessionId
    ) return
    formattedTranscriptHydratedRef.current = activeSessionId
    void hydrateTabEvents(activeSessionId, {
      workspacePath,
      fallbackToChatHistory: true,
      preferChatHistory: true,
      includeUiEvents: true,
    }).catch((error) => {
      formattedTranscriptHydratedRef.current = null
      console.error('[SessionRestore] Formatted transcript hydration failed:', error)
    })
  }, [
    activeEventViewMode,
    activeSessionId,
    activeTab?.metadata?.agentProfileId,
    activeTab?.metadata?.agentProfileWorkspace,
    activeTab?.metadata?.mode,
    activeTab?.metadata?.isExecutionRun,
    activeTab?.metadata?.isScheduledRun,
    activeTab?.metadata?.isBotRun,
    activeTab?.metadata?.isViewOnly,
    getActivePreset,
  ])
  const activeStreamingText = useChatStore((state) =>
    activeSessionId ? state.streamingText[activeSessionId] || '' : ''
  )
  const streamingStatus = useChatStore((state) =>
    activeSessionId ? state.streamingStatus[activeSessionId] || '' : ''
  )
  const historyPagination = useChatStore((state) =>
    activeSessionId ? state.tabHistoryPagination[activeSessionId] : undefined
  )
  const [olderHistory, setOlderHistory] = useState<{
    sessionId?: string
    events: PollingEvent[]
    loading: boolean
    error?: string
  }>({ events: [], loading: false })

  // Get active preset for workflow mode
  const activeWorkflowPreset = getActivePreset('workflow')
  const selectedWorkflowPreset = activeWorkflowPreset?.id || null

  // Always use tab events - never fall back to global events to prevent cross-tab mixing
  // If there are no tabs, return empty array (tabs should always exist in multi-tab mode)
  // Return a ref-stable array when every filtered event is unchanged. Durable
  // reconciliation can replace middle rows, so boundary IDs are insufficient.
  // Keeping the reference only for an identical window avoids unnecessary
  // downstream transcript recomputation without hiding replacements.
  // Holds the last returned displayEvents array. Used to avoid creating a new array
  // reference when the filtered output is identical — which would otherwise cascade
  // through EventHierarchy props → eventTree memo → flattenedItems memo → Virtuoso diff,
  // all for zero actual change.
  const displayEventsRef = useRef<PollingEvent[]>([])
  // PLAT-106 repair 3: the ref-stability cache above is session-scoped. Carried
  // across a session change it can return the PREVIOUS session's array — the
  // length + first/last-ID check is a same-session heuristic and says nothing
  // about ownership. Stamping it with the session it belongs to makes the reset
  // synchronous with the switch instead of one render late.
  const displayEventsSessionRef = useRef<string | undefined>(activeSessionId)

  const displayEvents = useMemo(() => {
    if (displayEventsSessionRef.current !== activeSessionId) {
      displayEventsSessionRef.current = activeSessionId
      displayEventsRef.current = []
    }

    const filtered = normalizeTranscriptChunkEvents(tabEvents).filter(event => {
      // See the Formatted View Visibility Contract in
      // utils/terminalEventTranscript.ts. Streaming packets drive the transient
      // live buffer; they are not durable conversation records.
      if (isStreamingEventType(event.type)) return false

      // Resume markers are lifecycle metadata, not chat rows. The transcript
      // already exposes older durable pages through its top pagination control;
      // rendering a marker at its event timestamp puts "Previous conversation"
      // below the newest restored answer after a refresh.
      if (event.type === 'conversation_resumed') return false

      // Auto-notifications deliberately pass through and are compacted by the
      // transcript according to the shared visibility contract.

      // Usage visibility is also defined by the shared contract.
      if (event.type === 'token_usage' && !showConversationUsage) {
        const agentEvent = event.data as { data?: Record<string, unknown> } | undefined
        const payload = agentEvent?.data || event.data as Record<string, unknown> | undefined

        if (payload?.context === 'conversation_total') {
          return false
        }
      }

      if (event.type === 'large_tool_output_detected' || event.type === 'large_tool_output_file_written') {
        return false
      }

      return true
    })

    // REF-STABILITY CHECK
    // .filter() always returns a new array, even when every element passes through unchanged.
    // That new reference triggers downstream useMemo/React.memo to recompute (they compare by ===).
    //
    // Hydration replaces the event window and may change a row in the middle
    // without changing its length or boundary IDs. Reuse the prior array only
    // when every event object is unchanged; otherwise transcript memos must
    // observe the replacement.
    //
    // When the check passes we return the *previous* array ref — downstream memos see the same
    // object and bail out entirely: eventTree skip → flattenedItems skip → Virtuoso no-op.
    const prev = displayEventsRef.current
    if (
      filtered.length === prev.length &&
      filtered.length > 0 &&
      filtered.every((event, index) => event === prev[index])
    ) {
      return prev  // same ref → no downstream recomputation
    }

    // Output actually changed — cache the new array for next comparison
    displayEventsRef.current = filtered
    return filtered
  }, [tabEvents, showConversationUsage, activeSessionId])

  // Durable chat history is deliberately fetched in bounded tail-first pages.
  // Keeping older pages locally avoids polluting the live event working set,
  // while the stable server cursor prevents loading the same page twice.
  const transcriptEvents = useMemo(() => (
    olderHistory.sessionId === activeSessionId && olderHistory.events.length > 0
      ? normalizeTranscriptChunkEvents([...olderHistory.events, ...displayEvents])
      : displayEvents
  ), [activeSessionId, displayEvents, olderHistory.events, olderHistory.sessionId])

  // Primitive deps only: the tab object changes on every composer keystroke,
  // and this callback is a prop of the memoized transcript.
  const activeTabIsExecution = isExecutionConversationTab(activeTab)
  const activeTabProfileWorkspace = activeTab?.metadata?.agentProfileWorkspace
  const loadOlderConversationPage = useCallback(async () => {
    if (!activeSessionId || !historyPagination?.hasMore || olderHistory.loading) return

    const sessionId = activeSessionId
    setOlderHistory((current) => ({
      sessionId,
      events: current.sessionId === sessionId ? current.events : [],
      loading: true,
      error: undefined,
    }))
    try {
      const execution = activeTabIsExecution
      let olderEvents: PollingEvent[]
      let hasMore: boolean
      let nextOffset: number | undefined
      if (execution) {
        const conversation = await agentApi.getChatHistoryResumeConversation(
          sessionId,
          activeTabProfileWorkspace || activeWorkflowPreset?.selectedFolder?.filepath,
          100,
          historyPagination.nextOffset,
          true,
        )
        olderEvents = conversationToRestoredEvents(conversation).filter(event => event.type !== 'conversation_resumed')
        hasMore = conversation.history_pagination?.has_more ?? false
        nextOffset = conversation.history_pagination?.next_offset
      } else {
        const response = await agentApi.getSessionEvents(sessionId, undefined, {
          limit: 100,
          beforeSequence: historyPagination.nextOffset,
          durableChat: true,
        })
        olderEvents = response.events || []
        hasMore = response.has_more
        nextOffset = response.oldest_sequence
      }
      olderEvents = resolveLiveInputConfirmations(olderEvents)
      const chatStore = useChatStore.getState()
      const currentIDs = new Set([
        ...chatStore.getTabEvents(sessionId),
        ...(olderHistory.sessionId === sessionId ? olderHistory.events : []),
      ].map(event => event.id).filter(Boolean))
      olderEvents = olderEvents.filter(event => !event.id || !currentIDs.has(event.id))
      chatStore.setTabHistoryPagination(
        sessionId,
        nextOffset
          ? { hasMore, nextOffset }
          : null,
      )
      chatStore.setTabHasMoreOlderEvents(sessionId, hasMore)
      setOlderHistory((current) => ({
        sessionId,
        events: current.sessionId === sessionId
          ? [...olderEvents, ...current.events]
          : olderEvents,
        loading: false,
      }))
    } catch (error) {
      setOlderHistory((current) => ({
        sessionId,
        events: current.sessionId === sessionId ? current.events : [],
        loading: false,
        error: error instanceof Error ? error.message : 'Could not load earlier messages',
      }))
    }
  }, [activeSessionId, activeTabIsExecution, activeTabProfileWorkspace, activeWorkflowPreset, historyPagination?.hasMore, historyPagination?.nextOffset, olderHistory.loading])

  const hasConversationContent = useMemo(() => {
    return displayEvents.some(event =>
      event.type === 'user_message' ||
      event.type === 'conversation_end' ||
      event.type === 'unified_completion'
    )
  }, [displayEvents])

  // --- Render tracking (filter by [Render] in console) ---
  useRenderLogger('ChatArea', {
    displayEvents: displayEvents.length,
    tabEvents: tabEvents.length,
    isStreaming,
    autoScroll,
    activeTabId: activeTab?.tabId,
    activeSessionId,
    finalResponse: !!finalResponse,
    tabSessionKey,
  })
  useMemoLogger('ChatArea.displayEvents', displayEvents, displayEvents.length)

  // Computed values
  const isRequiredFolderSelected = useMemo(() => {
    if (selectedModeCategory !== 'workflow') return true; // No validation needed for other modes
    if (activeTab?.metadata?.isOrganizationAssistant) return true

    // Workflow mode requires Workflow/ folder from preset
    if (selectedModeCategory === 'workflow') {
      const workflowFolder = activeWorkflowPreset?.selectedFolder?.filepath
      return workflowFolder ? workflowFolder.startsWith('Workflow/') : false
    }

    return true;
  }, [selectedModeCategory, activeWorkflowPreset, activeTab?.metadata?.isOrganizationAssistant])

  // Use currentPresetServers from props (passed from App.tsx when preset is selected)

  // State for preset selection overlay
  const [showPresetSelection, setShowPresetSelection] = useState(false)
  const [pendingModeCategory, setPendingModeCategory] = useState<Exclude<ModeCategory, null> | null>(null)

  // State for session restoration loading
  const [isRestoringChatSessions, setIsRestoringChatSessions] = useState(false)
  // Only the active session can put this pane into a restoring state. Other
  // workflows may hydrate concurrently without blanking the chat the user is
  // currently reading (PLAT-143).
  const isRestoringWorkflowSession = useChatStore(state => {
    const sessionId = activeTab?.sessionId
    return !!sessionId && (state.restoringWorkflowSessions[sessionId] ?? 0) > 0
  })
  // A resumed chat sets restoredConversationPath on the tab (cleared by New Chat's
  // resetTabChat). We no longer hard-hide the list on this marker alone — an empty
  // or stale resume (terminal restore that yielded nothing) would otherwise leave a
  // dead pane with no way back to the chats list. Instead we hide the list only
  // while the resume is genuinely loading (see the two guards below), so an empty
  // resumed tab falls through to the previous-chats list.
  const activeTabHasRestoredConversation = !!activeTab?.config?.restoredConversationPath
  // Subscribe to the session cache for lifecycle only. The transcript itself is
  // sourced from the session event stream; it does not inspect terminal state.
  const activeSessionsCache = useChatStore((state) => state.activeSessionsCache)
  const activeSessionIds = useMemo(() => {
    return new Set(activeSessionsCache.map(s => s.session_id))
  }, [activeSessionsCache])
  const activeRuntimeSessionIds = useMemo(() => {
    return new Set(
      activeSessionsCache
        .filter(session => sessionStreamingState(session).isActive)
        .map(session => session.session_id),
    )
  }, [activeSessionsCache])
  // A tab observing a specific scheduled/bot run is a read-only view of THAT
  // run, never a fresh chat. The chat-surface resolver keeps such tabs in
  // restoring (while events load) or active (once present) and never lets them
  // bounce to the previous-chats landing panel (the "schedule-bounce" fix).
  const isReadOnlyRunView =
    !!activeTab?.metadata?.isScheduledRun || !!activeTab?.metadata?.isBotRun
  // A resumed conversation becomes active when its durable events arrive. Never
  // use a tmux snapshot as a proxy for product-visible chat content.
  const hasRestoredLiveContent = activeTabHasRestoredConversation && hasConversationContent
  // Use the ACTIVE TAB's streaming flag, not the global state.isStreaming, which
  // lingers true after New Chat from a running conversation (a cross-tab signal,
  // not session-scoped) and would wrongly force 'active'.
  const activeTabStreaming = !!activeTab?.isStreaming
  // isStreaming is deliberately false while only background agents run, so the
  // composer stays usable (see isForegroundStreaming in utils/sessionRestore).
  // It therefore follows can_steer, which on the server falls through to a tmux
  // busy-content heuristic (polling.go: SessionHasBusyMainCodingTmux) — that
  // flips as the pane's output starts and stalls, so anything MOUNTED on this
  // flag unmounts and remounts a couple of times a second during a run.
  // Measured: 13 mount/unmount pairs in 6s for the Working indicator and its
  // Cancel button. Display state needs a signal that stays true for the whole
  // turn; the composer keeps the volatile one.
  const activeTabBusy = activeTabStreaming || !!activeTab?.hasRunningBgAgents
  // Resume give-up TIMER only. A resume that never produces a terminal/content may
  // eventually fall to 'landing'; resumeGaveUp flips true after RESUME_SETTLE_MS so
  // a genuinely-dead resume isn't stuck on a spinner forever. This is purely a
  // timeout — the "resume is pending" decision is derived SYNCHRONOUSLY below, not
  // from this state, so the first render after a resume can never be 'landing'.
  const [resumeGaveUp, setResumeGaveUp] = useState(false)
  useEffect(() => {
    if (!activeTabHasRestoredConversation || hasConversationContent || activeTabStreaming) {
      setResumeGaveUp(false)
      return
    }
    setResumeGaveUp(false)
    const timer = window.setTimeout(() => setResumeGaveUp(true), RESUME_SETTLE_MS)
    return () => window.clearTimeout(timer)
  }, [activeTabHasRestoredConversation, hasConversationContent, activeTabStreaming, activeSessionId])
  // Read-only run view give-up TIMER. isReadOnlyRunView (a scheduled/bot run
  // tab) forces resolveChatSurface to 'restoring' unconditionally while empty,
  // by design (a read-only run must never bounce to the previous-chats
  // landing panel — the "schedule-bounce" fix). But that means a tab whose
  // session the backend no longer knows about (its in-memory event store was
  // wiped by a server restart, or openScheduledRunInChat's own fetch failed
  // and was silently swallowed) spins forever with no escape. This timer only
  // flips a display flag consumed below to swap the spinner for an explicit
  // "couldn't load" message with a retry action; it never changes
  // resolveChatSurface's returned surface or its landing-avoidance guarantee.
  const [readOnlyRunViewGaveUp, setReadOnlyRunViewGaveUp] = useState(false)
  useEffect(() => {
    if (!isReadOnlyRunView || displayEvents.length > 0 || activeTabStreaming) {
      setReadOnlyRunViewGaveUp(false)
      return
    }
    setReadOnlyRunViewGaveUp(false)
    const timer = window.setTimeout(() => setReadOnlyRunViewGaveUp(true), RESUME_SETTLE_MS)
    return () => window.clearTimeout(timer)
  }, [isReadOnlyRunView, displayEvents.length, activeTabStreaming, activeSessionId])
  // Manual retry for the give-up message above. This used to read only the
  // backend's volatile in-memory event store, which is exactly the thing a
  // server restart empties -- so for a finished scheduled run it could never
  // succeed. Use the same durable-history-first hydration a page load uses;
  // it needs the workflow's workspace path, which is what scopes the
  // conversation file lookup (without it the history endpoint is a 404). If
  // the run really is gone, this comes back empty and the give-up timer
  // above simply re-arms and fires again — an honest "still not there".
  const retryReadOnlyRunView = useCallback(async () => {
    const sessionId = activeSessionId
    const tabId = activeTab?.tabId
    if (!sessionId || !tabId) return
    setReadOnlyRunViewGaveUp(false)
    try {
      const workspacePath = activeWorkflowPreset?.selectedFolder?.filepath || activeTab?.metadata?.agentProfileWorkspace
      const runtime = isExecutionConversationTab(activeTab)
        ? await hydrateExecutionConversation(sessionId, workspacePath)
        : await hydrateTabEvents(sessionId, { workspacePath })
      const chatStore = useChatStore.getState()
      const isDone = runtime.status === 'completed' || runtime.status === 'stopped'
      const isError = runtime.status === 'error'
      chatStore.setTabCompleted(tabId, isDone)
      chatStore.setTabStreaming(tabId, !isDone && !isError && runtime.status === 'running')
    } catch {
      // Leave it to the give-up timer to re-fire; nothing else to do here.
    }
  }, [activeSessionId, activeTab, activeWorkflowPreset])
  // resumePending — SYNCHRONOUS (derived in render, NOT an effect-set state). This
  // is the regression fix: on a Resume click restoredConversationPath is set
  // synchronously (setTabConfig), so this is already true on the FIRST render →
  // resolveChatSurface returns 'restoring' on that render, never the transient
  // 'landing' that the old effect-lagged resumeSettling produced for one render.
  // That transient 'landing' was what let the clear-on-landing effect fire and
  // cancel the resume (the 2-3-click flakiness). resumePending stays true until
  // content/stream arrives (→ 'active') or the give-up timer elapses (→ 'landing').
  const resumePending =
    activeTabHasRestoredConversation &&
    !hasConversationContent &&
    !activeTabStreaming &&
    !resumeGaveUp
  // Resume a previous chat from the landing "Previous chats" panel. The same
  // resume path is used anywhere else that needs to restore a multi-agent chat.
  const handleResumePreviousChat = useResumePreviousChat()

  // State for mode switch dialog
  const [showModeSwitchDialog, setShowModeSwitchDialog] = useState(false)
  const [pendingModeSwitch, setPendingModeSwitch] = useState<Exclude<ModeCategory, null> | null>(null)


  // Handle mode selection from dropdown
  // Handle mode switching with preset selection for Workflow
  const handleModeSwitchWithPreset = (category: Exclude<ModeCategory, null>) => {
    if (category === 'multi-agent') {
      // Multi-agent mode doesn't need preset selection
      // Clear any active presets when switching to multi-agent mode
      clearActivePreset('workflow')
      switchMode(category)
    } else {
      // Workflow mode - always show preset selection when switching between modes
      // Clear the current mode's preset first
      if (selectedModeCategory === 'workflow') {
        clearActivePreset('workflow')
      }

      // Check if target mode already has a preset
      const activePreset = getActivePreset(category)

      if (activePreset) {
        // Preset already selected, switch mode directly
        switchMode(category)
      } else {
        // No preset selected, show preset selection overlay
        setPendingModeCategory(category)
        setShowPresetSelection(true)
      }
    }
  }

  // Switch mode function
  const switchMode = (category: Exclude<ModeCategory, null>) => {
    const { setModeCategory, getAgentModeFromCategory } = useModeStore.getState()
    const { setAgentMode } = useAppStore.getState()

    setModeCategory(category)

    // Set the corresponding agent mode using centralized mapping
    const agentModeToSet = getAgentModeFromCategory(category) as AgentMode
    setAgentMode(agentModeToSet)
  }

  // Handle preset selection from overlay
  const handlePresetSelected = (presetId: string) => {
    if (pendingModeCategory) {
      // Now switch to the mode
      switchMode(pendingModeCategory)

      // Apply the preset after mode switch (this will also set the active preset ID)
      setTimeout(() => {
        const result = applyPreset(presetId, pendingModeCategory)
        if (!result.success) {
          logger.error('ChatArea', 'Failed to apply preset:', result.error)
        }
      }, 100)

      // Close overlay
      setShowPresetSelection(false)
      setPendingModeCategory(null)
    }
  }

  // Handle preset selection overlay close
  const handlePresetSelectionClose = () => {
    setShowPresetSelection(false)
    setPendingModeCategory(null)
  }


  // Handle mode switch dialog confirmation
  const handleModeSwitchConfirm = () => {
    if (pendingModeSwitch) {
      handleModeSwitchWithPreset(pendingModeSwitch)
      // Clear backend session and reset UI after mode switch
      handleNewChat()
    }
    setShowModeSwitchDialog(false)
    setPendingModeSwitch(null)
  }

  // Handle mode switch dialog cancellation
  const handleModeSwitchCancel = () => {
    setShowModeSwitchDialog(false)
    setPendingModeSwitch(null)
  }

  // Add ref for auto-scrolling
  const chatContentRef = useRef<HTMLDivElement>(null)

  // Add ref for workflow mode handler
  const workflowModeHandlerRef = useRef<WorkflowModeHandlerRef>(null)


  // Track processed completion events to avoid stopping on old ones
  const processedCompletionEventsRef = useRef<Set<string>>(new Set())


  // Selected preset folder state
  const lastEventIndexRef = useRef<number>(-1)
  // Deprecated: totalEventsRef removed
  const previousEventCountRef = useRef<number>(0)

  // Track whether workspace-modifying events occurred during the current run

  // Ref to track if we're currently performing programmatic scrolling
  const isProgrammaticScrollRef = useRef<boolean>(false)
  const programmaticScrollTimeoutRef = useRef<NodeJS.Timeout | null>(null)
  const manualScrollVersionRef = useRef<number>(0)
  // Local ref for scroll position — avoids Zustand re-renders on every scroll event
  const lastScrollTopRef = useRef<number>(0)

  // Ref to track currentWorkflowPhase without causing callback re-renders
  const currentWorkflowPhaseRef = useRef<string>(currentWorkflowPhase)
  useEffect(() => {
    currentWorkflowPhaseRef.current = currentWorkflowPhase
  }, [currentWorkflowPhase])

  // Observer initialization removed - no longer needed

  // Re-enable auto-scroll when user scrolls back to the bottom.
  // The wheel handler below covers the disable-on-scroll-up path.
  const handleScroll = useCallback(() => {
    if (!chatContentRef.current) return;
    const element = chatContentRef.current;
    if (element.classList.contains('overflow-hidden')) return;
    const currentScrollTop = element.scrollTop;
    if (isProgrammaticScrollRef.current) {
      lastScrollTopRef.current = currentScrollTop;
      return;
    }

    const movedUp = currentScrollTop < lastScrollTopRef.current - 2;
    const atBottom = isAtBottom(element);
    if (movedUp && !atBottom) {
      manualScrollVersionRef.current += 1;
      if (autoScroll) setAutoScroll(false);
    } else if (atBottom && !autoScroll) {
      setAutoScroll(true);
    }
    lastScrollTopRef.current = currentScrollTop;
  }, [autoScroll, isAtBottom, setAutoScroll]);

  // Set up scroll + wheel event listeners
  useEffect(() => {
    const element = chatContentRef.current;
    if (!element) return;

    lastScrollTopRef.current = element.scrollTop;

    const onWheel = (e: WheelEvent) => {
      if (e.deltaY < 0 && element.scrollTop > 0) {
        // Only disable if user is scrolling up AND there's room to scroll up
        // (i.e., not already at the very top or at the bottom with no overflow)
        const atBottom = element.scrollTop + element.clientHeight >= element.scrollHeight - 150;
        if (!atBottom) {
          manualScrollVersionRef.current += 1;
          setAutoScroll(false);
        }
      }
    };

    element.addEventListener('scroll', handleScroll);
    element.addEventListener('wheel', onWheel, { passive: true });
    return () => {
      element.removeEventListener('scroll', handleScroll);
      element.removeEventListener('wheel', onWheel);
      if (programmaticScrollTimeoutRef.current) {
        clearTimeout(programmaticScrollTimeoutRef.current);
        programmaticScrollTimeoutRef.current = null;
      }
    };
  }, [handleScroll, setAutoScroll]);

  // Reset auto-scroll when starting new conversation (events go from 0 to > 0)
  // Use displayEvents (tabEvents) instead of events to track the actual displayed events
  useEffect(() => {
    const currentEventCount = displayEvents.length
    const previousEventCount = previousEventCountRef.current

    // Only reset auto-scroll when starting a new conversation (0 -> > 0)
    // Don't reset if user has manually disabled it or if events are just updating
    const isRestoredMultiAgentHydration = selectedModeCategory === 'multi-agent' && (
      isRestoringChatSessions ||
      activeTabHasRestoredConversation ||
      activeTab?.metadata?.isRestored === true
    )
    if (previousEventCount === 0 && currentEventCount > 0 && !isStreaming && !isRestoredMultiAgentHydration) {
      setAutoScroll(true);
    }

    previousEventCountRef.current = currentEventCount
  }, [activeTab?.metadata?.isRestored, activeTabHasRestoredConversation, displayEvents.length, isRestoringChatSessions, isStreaming, selectedModeCategory, setAutoScroll]);

  // Improved auto-scroll for new events
  const scrollToBottom = useCallback((behavior: ScrollBehavior = 'smooth') => {
    if (!chatContentRef.current) return;

    // Mark that we're performing programmatic scrolling
    isProgrammaticScrollRef.current = true

    // Clear any existing timeout
    if (programmaticScrollTimeoutRef.current) {
      clearTimeout(programmaticScrollTimeoutRef.current)
    }

    // Use requestAnimationFrame for smoother scrolling
    requestAnimationFrame(() => {
      const element = chatContentRef.current
      if (!element || element.classList.contains('overflow-hidden')) {
        isProgrammaticScrollRef.current = false
        return
      }

      const targetScrollTop = element.scrollHeight - element.clientHeight
      element.scrollTo({
        top: targetScrollTop,
        behavior
      });

      // Clear the programmatic scroll flag after scroll completes
      // For smooth scroll, wait longer; for instant, clear immediately
      const timeoutDuration = behavior === 'smooth' ? 600 : 100
      programmaticScrollTimeoutRef.current = setTimeout(() => {
        isProgrammaticScrollRef.current = false
        programmaticScrollTimeoutRef.current = null
      }, timeoutDuration)
    });
  }, [])

  // Auto-scroll to bottom when new events arrive (only if autoScroll is enabled)
  // Use displayEvents (tabEvents) instead of events to track the actual displayed events
  useEffect(() => {
    if (autoScroll && chatContentRef.current && displayEvents.length > 0) {
      // During streaming, use instant scroll — smooth scroll called repeatedly every event
      // causes each call to interrupt the previous animation, producing visible jank.
      scrollToBottom(isStreaming ? 'instant' : 'smooth');
    }
  }, [displayEvents.length, autoScroll, scrollToBottom, isStreaming])

  // Auto-scroll to bottom when final response is updated (only if autoScroll is enabled)
  useEffect(() => {
    if (autoScroll && chatContentRef.current && finalResponse) {
      scrollToBottom('smooth');
    }
  }, [finalResponse, autoScroll, scrollToBottom])

  // Scroll to bottom when switching tabs (including workflow switch via Ctrl+K)
  useEffect(() => {
    if (!targetTabId) return
    // Re-enable auto-scroll so subsequent events keep the view pinned to the bottom
    setAutoScroll(true)
    const scrollVersion = manualScrollVersionRef.current
    const scrollIfUserHasNotMoved = (behavior: ScrollBehavior) => {
      if (manualScrollVersionRef.current === scrollVersion) {
        scrollToBottom(behavior)
      }
    }
    // Small delay to let the new tab's content render before scrolling.
    // Use two attempts: 50ms for fast renders, 300ms as fallback when events are still loading.
    const timer1 = setTimeout(() => scrollIfUserHasNotMoved('instant'), 50)
    const timer2 = setTimeout(() => scrollIfUserHasNotMoved('instant'), 300)
    return () => { clearTimeout(timer1); clearTimeout(timer2) }
  }, [targetTabId, scrollToBottom, setAutoScroll])

  // Cross-mode switchers can change mode/preset before the target ChatArea has
  // fully rendered. Listen for an explicit request and retry shortly after.
  useEffect(() => {
    const handleScrollRequest = () => {
      setAutoScroll(true)
      scrollToBottom('instant')
      const timer1 = setTimeout(() => scrollToBottom('instant'), 80)
      const timer2 = setTimeout(() => scrollToBottom('instant'), 350)
      return () => {
        clearTimeout(timer1)
        clearTimeout(timer2)
      }
    }

    let cleanupTimers: (() => void) | null = null
    const listener = () => {
      cleanupTimers?.()
      cleanupTimers = handleScrollRequest()
    }
    window.addEventListener('chat-scroll-to-bottom', listener)
    return () => {
      cleanupTimers?.()
      window.removeEventListener('chat-scroll-to-bottom', listener)
    }
  }, [scrollToBottom, setAutoScroll])

  // Update refs when values change (for global observer)
  useEffect(() => {
    if (!activeTab) {
      lastEventIndexRef.current = lastEventIndex
    }
  }, [lastEventIndex, activeTab])

  // Update displayEvents when active tab changes
  // Tab events are automatically loaded via tabEvents useMemo

  // Deprecated: totalEventsRef useEffect removed

  // Workflow preset handlers
  const handleWorkflowPresetSelected = useCallback(async (presetId: string, presetContent: string) => {
    // Apply the preset using the global preset store
    // File context is now preset-specific (from preset.selectedFolder), no need to clear
    selectWorkflowPreset(presetId)
    setCurrentWorkflowQueryId(presetId) // Store the preset query ID for workflow approval

    try {
      // Ensure phases are loaded and get them from store
      const workflowStore = useWorkflowStore.getState()
      if (!workflowStore.phasesInitialized) {
        await workflowStore.loadPhases()
      }
      const phases = workflowStore.phases
      const phaseIds = phases.map(p => p.id)
      const defaultPhase = workflowStore.getDefaultPhase()

      // Check if workflow already exists for this preset
      const workflowStatus = await agentApi.getWorkflowStatus(presetId)

      if (workflowStatus.success && workflowStatus.workflow) {
        const workflow = workflowStatus.workflow
        const status = workflow.workflow_status

        // Set the workflow phase based on the database status
        // Use the status if it's a valid phase ID, otherwise use default (first phase)
        if (status && phaseIds.includes(status)) {
          setCurrentWorkflowPhase(status)
        } else {
          // Default to first phase if status is invalid or not found
          setCurrentWorkflowPhase(defaultPhase)
        }

        // Use presetContent directly (this is the objective from preset query)
        setCurrentQuery(presetContent)
      } else {
        // No workflow exists, proceed with default phase
        setCurrentWorkflowPhase(defaultPhase)
        setCurrentQuery(presetContent)
      }
    } catch (error) {
      logger.error('ChatArea', 'Error checking workflow status:', error)
      // Fallback to default phase on error
      const defaultPhase = useWorkflowStore.getState().getDefaultPhase()
      setCurrentWorkflowPhase(defaultPhase)
      setCurrentQuery(presetContent)
    }
  }, [setCurrentQuery, applyPreset, setCurrentWorkflowPhase, setCurrentWorkflowQueryId])

  // Clear workflow state when starting a new chat
  const clearWorkflowState = useCallback(() => {
    clearActivePreset('workflow')
    setCurrentWorkflowQueryId(null)
    const defaultPhase = useWorkflowStore.getState().getDefaultPhase()
    setCurrentWorkflowPhase(defaultPhase)
  }, [clearActivePreset, setCurrentWorkflowQueryId, setCurrentWorkflowPhase])

  // Handle human verification actions
  // TODO: Re-enable when RequestHumanFeedbackEvent is available
  /*
  const handleApproveWorkflow = useCallback(async (_requestId: string, eventData?: { next_phase?: string }) => {

    setIsApprovingWorkflow(true)  // Set loading state

    // Use the stored preset query ID instead of the request ID
    const presetQueryId = currentWorkflowQueryId
    if (!presetQueryId) {
      logger.error('ChatArea', 'No preset query ID available for workflow approval')
      setIsApprovingWorkflow(false)
      return
    }

    try {
      // Determine next phase based on event data
      // If next_phase is provided, use it; otherwise get the second phase (planning) as default
      let nextPhase = eventData?.next_phase
      if (!nextPhase) {
        const phases = useWorkflowStore.getState().phases
        // Use second phase (planning) if available, otherwise first phase
        nextPhase = phases.length > 1 ? phases[1].id : (phases.length > 0 ? phases[0].id : 'execution')
      }

      // Update workflow status to the determined next phase
      await agentApi.updateWorkflow(presetQueryId, nextPhase)

      // Stop any ongoing SSE / polling to prevent events from coming back
      if (currentTab?.sessionId) {
        disconnectSSE(currentTab.sessionId)
      }
      if (pollingInterval) {
        stopPolling()
      }

      // Clear all events to show clean slate for execution phase
      // Note: Using tabEvents now, not global events
      if (currentTab?.sessionId) {
        chatStore.clearTabEvents(currentTab.sessionId)
      }
      // Deprecated: setLastEventCount removed
      setLastEventIndex(-1)
      setFinalResponse('')
      setIsCompleted(false)
      setCurrentUserMessage('')
      setShowUserMessage(false)

      // Update phase to the determined next phase
      setCurrentWorkflowPhase(nextPhase as WorkflowPhase)

    } catch (error) {
      logger.error('ChatArea', 'Failed to approve workflow:', error)
      // TODO: Show error message to user
    } finally {
      setIsApprovingWorkflow(false)  // Clear loading state
    }
  }, [currentWorkflowQueryId, pollingInterval, setIsApprovingWorkflow, setLastEventIndex, setFinalResponse, setIsCompleted, setCurrentUserMessage, setShowUserMessage, setCurrentWorkflowPhase, setPollingInterval])
  */

  // Observer initialization removed - no longer needed

  // (Batching removed — events are now processed immediately as they arrive)

  // Removed extractUserMessageContent - no longer needed since we removed duplicate detection


  // Get polling management actions from store (before pollEvents callback)
  const { startPolling, stopPolling, getActiveSessions, connectSSE, disconnectSSE } = useChatStore(useShallow(state => ({
    startPolling: state.startPolling,
    stopPolling: state.stopPolling,
    getActiveSessions: state.getActiveSessions,
    connectSSE: state.connectSSE,
    disconnectSSE: state.disconnectSSE,
  })))
  const buildExecutionOptions = useWorkflowStore(state => state.buildExecutionOptions)

  // Get active sessions from cache (shared across all components)
  const startActiveSessionsPolling = useChatStore(state => state.startActiveSessionsPolling)

  // Track recently notified workshop agent names to prevent duplicate notifications
  // (retries emit multiple orchestrator_agent_end events with the same agent name)
  const notifiedWorkshopAgentsRef = useRef<Set<string>>(new Set())
  // A restored tab is created before its durable transcript and EventStore
  // cursor finish loading. Track that startup work so the SSE effect cannot
  // connect at its default cursor (0) and replay megabytes of raw history.
  const sseRestoreInFlightRef = useRef<Set<string>>(new Set())
  // Suppress auto-notifications during initial SSE backfill (first 3s after mount).
  // Without this, page reload would replay all old completion events as new notifications.
  // After the backfill window, all notifications are allowed. The dedup set
  // (notifiedWorkshopAgentsRef) still prevents duplicates within a session.
  const hasUserSentMessageRef = useRef(false)
  useEffect(() => {
    const timer = setTimeout(() => {
      hasUserSentMessageRef.current = true
    }, 3000)
    return () => clearTimeout(timer)
  }, [])

  // Reusable event processing logic — shared by both SSE and polling paths.
  // Takes an events response (same shape from SSE or REST) and a tab, then processes
  // session status, streaming chunks, event filtering, and stores events.
  const processEventsResponse = useCallback((
    response: SessionActivitySnapshot & { events: PollingEvent[]; session_status?: string; last_processed_index?: number; has_more?: boolean; has_running_background_agents?: boolean; is_synthetic_turn?: boolean; can_steer?: boolean; session_id?: string },
    sessionId: string,
    tab: ChatTab | null
  ) => {
    const chatStore = useChatStore.getState()
    const actualSessionId = response.session_id || sessionId

    // --- Session status handling ---
    const activity = sessionStreamingState(response)
    const sessionStatus = activity.status
    if (tab && sessionStatus) {
      const hasBgAgents = activity.hasRunningBgAgents
      const isSyntheticTurn = activity.isSyntheticTurn
      const canSteer = activity.canSteer
      const isForegroundStreaming = activity.isStreaming
      if (sessionStatus === 'completed' || sessionStatus === 'error') {
        if (hasBgAgents) {
          chatStore.setTabCompleted(tab.tabId, false)
          chatStore.setTabStreaming(tab.tabId, false)
        } else {
          chatStore.setTabCompleted(tab.tabId, true)
          chatStore.setTabStreaming(tab.tabId, false)
        }
        chatStore.clearStreamingText(actualSessionId)
      } else if (sessionStatus === 'running') {
        chatStore.setTabCompleted(tab.tabId, false)
        chatStore.setTabStreaming(tab.tabId, isForegroundStreaming)
      } else if (sessionStatus === 'stopped' || sessionStatus === 'inactive') {
        chatStore.setTabCompleted(tab.tabId, false)
        chatStore.setTabStreaming(tab.tabId, false)
        chatStore.clearStreamingText(actualSessionId)
      }
      chatStore.setTabHasRunningBgAgents(tab.tabId, hasBgAgents)
      setTabSyntheticTurn(tab.tabId, isSyntheticTurn)
      setTabCanSteer(tab.tabId, canSteer)
    } else if (
      !tab &&
      sessionStatus &&
      sessionOwnsGlobalChatIndicators(actualSessionId, activeSessionIdRef.current)
    ) {
      // Only the session the user is actually viewing may drive the app-wide
      // indicators below. They are globals, and responses arrive here for every
      // polled session — see sessionOwnsGlobalChatIndicators.
      const isForegroundStreaming = activity.isStreaming
      if (sessionStatus === 'completed' || sessionStatus === 'error') {
        setIsStreaming(false)
        setIsCompleted(true)
        setHasActiveChat(false)
        chatStore.clearStreamingText(actualSessionId)
      } else if (sessionStatus === 'running') {
        setIsStreaming(isForegroundStreaming)
        setIsCompleted(false)
      } else if (sessionStatus === 'stopped' || sessionStatus === 'inactive') {
        setIsStreaming(false)
        setIsCompleted(false)
        chatStore.clearStreamingText(actualSessionId)
      }
    }

    // --- Update last event index ---
    // CRITICAL: Must happen BEFORE the empty-events early return below.
    // SSE backfill may contain only streaming events (handled immediately in handleSSEMessage),
    // leaving the batched events array empty. Without updating the index here, tabEventIndices
    // stays at 0 and every SSE reconnection re-fetches all events from the beginning.
    // Every caller here is a forward read, where has_more means "newer rows",
    // so it must never drive the older-history pager.
    if (response.last_processed_index !== undefined && response.last_processed_index >= 0) {
      const newLastEventIndex = response.last_processed_index
      if (tab) {
        setTabLastEventIndex(actualSessionId, nextForwardCursor(
          getTabLastEventIndex(actualSessionId),
          newLastEventIndex,
          response.events.length,
          !isExecutionConversationTab(tab),
        ))
      } else {
        setLastEventIndex(newLastEventIndex)
      }
    } else if (response.last_processed_index === -1) {
      // -1 is the backend's explicit "this session is not in my in-memory
      // event store" signal (polling.go: !exists -> LastProcessedIndex: -1;
      // sse.go sends the same sentinel on a reconnect that hits an empty,
      // post-restart store). The store is in-memory only, so a server
      // restart wipes every live session's event log; a tab left open
      // across that restart keeps polling/streaming with its pre-restart
      // since= cursor forever, which the fresh process's own (much shorter)
      // post-restart event log can never reach -- events silently stop
      // arriving, with no visible error, indefinitely. Only reset when we
      // previously tracked real progress (index > 0): a genuinely brand-new,
      // never-polled session legitimately gets -1 on its first poll too, and
      // resetting an already-0 index would be a no-op anyway.
      const priorIndex = tab
        ? getTabLastEventIndex(actualSessionId)
        : lastEventIndexRef.current
      if (priorIndex > 0) {
        logger.warn('ChatArea', `Backend has no record of session ${actualSessionId} (likely a server restart) after prior progress at index ${priorIndex}; resetting cursor and reloading from the durable transcript`)
        if (tab) {
          setTabLastEventIndex(actualSessionId, 0)
        } else {
          setLastEventIndex(0)
        }
        // Zeroing the cursor only stops future events from being silently
        // dropped going forward -- it does nothing to recover whatever was
        // generated while this tab's connection was stale (the in-memory
        // store is wiped by a restart, but the durable transcript on disk
        // is not). Re-run the same durable-history restore a fresh page
        // load already uses (sessionRestore.ts) so an already-open tab
        // self-heals instead of requiring a manual refresh. Confirmed live
        // on Dominion 2026-08-31: a fully-generated, persisted chat answer
        // never appeared in an open tab until it was manually reloaded.
        void restoreSession(actualSessionId, { source: 'sse-reset-resync', skipConfigRestore: true })
      }
    }

    if (response.events.length === 0) return

    // --- Event filtering & processing ---
    let eventsBeforeFilter = response.events as PollingEvent[]
    // Durability receipts upgrade rows in place and never enter the
    // timeline: a live_input_confirmed event carries no chat content,
    // only the verdict for the user_message row it names. Receipts are
    // filtered here but applied after the append below: a window can
    // carry a user_message and its own receipt together (reconnect
    // replay), and the row must exist before the verdict can match it.
    const confirmations = eventsBeforeFilter.map(readLiveInputConfirmation).filter((u): u is NonNullable<typeof u> => u !== null)
    if (confirmations.length > 0) {
      eventsBeforeFilter = eventsBeforeFilter.filter(e => e.type !== 'live_input_confirmed')
    }
    // Consumes this window's receipts against the stored rows. Used only
    // when the append below is skipped (receipt-only window, vanished
    // tab); appended windows go through the shared helper instead.
    const applyConfirmationsToStore = () => {
      if (confirmations.length === 0) return
      // patchTabEvents, not get+set: setTabEvents clears the micro-batch
      // buffer, dropping tool events that arrived in this same window.
      chatStore.patchTabEvents(actualSessionId, events => applyLiveInputConfirmations(events, confirmations))
    }
    const newEvents: PollingEvent[] = []
    let hasCompletionEvent = false
    // Check if we already have frontend-created user messages for this session.
    // We only want to suppress the backend echo for the exact same submitted text.
    // Other backend user_message events, like steer pickup notifications injected
    // later by the server, must still be allowed through.
    const existingEvents = chatStore.getTabEvents(actualSessionId)
    const frontendUserMessageContents = new Set(
      existingEvents
        .filter(e => e.type === 'user_message' && e.id?.startsWith('user-message-'))
        .map(e => getDisplaySafeUserMessageContent(getUserMessageContent(e)))
        .filter(Boolean)
    )
    const hasFrontendUserMessage = frontendUserMessageContents.size > 0
    // A suppressed backend echo still carries the delivery identity the
    // optimistic row needs: without the transfer, a query the backend
    // steered live would keep a bubble no durability receipt can match.
    // Stamping reuses the store snapshot read above; the loop below only
    // appends, so a mid-pass setTabEvents cannot lose events.
    const transferSuppressedEchoIdentity = (echo: PollingEvent, echoContent: string) => {
      const echoOuter = echo.data as unknown as Record<string, unknown> | undefined
      const echoInner = (echoOuter?.data ?? {}) as Record<string, unknown>
      const echoMetadata = (echoInner.metadata ?? {}) as Record<string, unknown>
      const messageId = echoMetadata.message_id
      if (typeof messageId !== 'string' || !messageId) return
      const deliveryStatus = typeof echoMetadata.delivery_status === 'string' ? echoMetadata.delivery_status : 'sent_to_cli'
      const provider = typeof echoMetadata.provider === 'string' ? echoMetadata.provider : undefined
      const current = chatStore.getTabEvents(actualSessionId)
      let changed = false
      const stamped = current.map(row => {
        if (row.type !== 'user_message' || !row.id?.startsWith('user-message-')) return row
        if (getDisplaySafeUserMessageContent(getUserMessageContent(row)) !== echoContent) return row
        const next = stampLiveInputIdentity(row, messageId, deliveryStatus, provider)
        if (next !== row) changed = true
        return next
      })
      if (changed) chatStore.setTabEvents(actualSessionId, stamped)
    }

    for (const event of eventsBeforeFilter) {
      const agentEvent = event.data as Record<string, unknown> | undefined
      const innerData = agentEvent?.data as Record<string, unknown> | undefined
      const rawComponent = (event as unknown as Record<string, unknown>).component ?? innerData?.component ?? agentEvent?.component
      const rawCorrelationId = (event as unknown as Record<string, unknown>).correlation_id ?? innerData?.correlation_id ?? agentEvent?.correlation_id
      const isForegroundSession = isForegroundSessionEvent(event, rawComponent, rawCorrelationId)
      const runtimeScope = getRuntimeEventScope(event)
      const isSubAgentEvent = !isForegroundSession || runtimeScope.kind !== 'session'

      // Skip backend user_message events when we already have a frontend-created one
      // (avoids duplicate user message bubbles in the chat). Internal
      // [AUTO-NOTIFICATION] messages still enter the event store, but the
      // displayEvents filter keeps them out of the human timeline.
      // Keyed echoes reconcile by id in the store (the durable row replaces its
      // provisional bubble). Text matching remains only for legacy rows.
      const isKeyedUserEcho = event.type === 'user_message' && Boolean(readClientMessageId(event))
      if (!isKeyedUserEcho && event.type === 'user_message' && hasFrontendUserMessage && !event.id?.startsWith('user-message-')) {
        const msgContent = getDisplaySafeUserMessageContent(getUserMessageContent(event))
        if (
          !msgContent.startsWith(AUTO_NOTIFICATION_PREFIX) &&
          frontendUserMessageContents.has(msgContent)
        ) {
          transferSuppressedEchoIdentity(event, msgContent)
          continue
        }
      }

      if (isStreamingEventType(event.type)) {
        handleLiveStreamingEvent(event, actualSessionId, chatStore)
        // A coding CLI's whole-message narration ("I'll separate posts from
        // replies…") must outlive the live buffer, or it vanishes from the chat
        // while tmux still shows it. Keep it as the same intermediate reply row
        // the durable restore synthesizes.
        if (!isSubAgentEvent) {
          const update = intermediateUpdateFromTranscriptChunk(event)
          if (update) newEvents.push(update)
        }
        continue
      }
      // Allow distinct backend user_message events through when there's no
      // matching frontend-created message. Internal auto-notifications are
      // retained here for orchestration and filtered only at display time.
      if (!isKeyedUserEcho && event.type === 'user_message' && hasFrontendUserMessage) {
        const msgContent = getDisplaySafeUserMessageContent(getUserMessageContent(event))
        if (
          !msgContent.startsWith(AUTO_NOTIFICATION_PREFIX) &&
          !frontendUserMessageContents.has(msgContent)
        ) {
          // This is a distinct backend user_message (for example a steer message
          // picked up mid-run), so keep it visible in the timeline.
        } else if (!msgContent.startsWith(AUTO_NOTIFICATION_PREFIX)) {
          transferSuppressedEchoIdentity(event, msgContent)
          continue
        }
      }

      // An intermediate narration row (intermediateUpdateFromTranscriptChunk)
      // reuses the llm_generation_end shape but is not the turn's completion.
      const isIntermediateUpdate = innerData?.restored_intermediate_update === true
      if (!isSubAgentEvent && !isIntermediateUpdate && (event.type === 'llm_generation_end' || event.type === 'unified_completion' || event.type === 'agent_end' || event.type === 'conversation_end' || event.type === 'conversation_error' || event.type === 'context_cancelled')) {
        hasCompletionEvent = true
      }

      if (event.type === 'delegation_end') {
        const correlationId = innerData?.correlation_id ?? innerData?.delegation_id ?? agentEvent?.correlation_id ?? agentEvent?.delegation_id
        if (correlationId && typeof correlationId === 'string') {
          chatStore.clearDelegationStreamingText(correlationId)
        }
      }

      // Dedup keys now include correlation_id (unique per execution), so clearing is not needed

      // Auto-notifications for workshop step completions are now handled entirely by the backend
      // via processBackgroundAgentCompletion → executeSyntheticTurn. The backend injects a
      // [AUTO-NOTIFICATION] user_message event which the frontend retains for
      // orchestration but does not show as human chat. No frontend queuing needed.
      //
      // Legacy: orchestrator_agent_end events were previously queued as auto-notifications here.
      // That code has been removed. The backend bgAgentRegistry handles all workshop execution
      // completion notifications.
      if (ENABLE_LEGACY_FRONTEND_AUTO_NOTIFICATIONS && event.type === 'orchestrator_agent_end' && tab) {
        const agentType = (innerData?.agent_type ?? agentEvent?.agent_type ?? '') as string
        const isWorkshopWrapper = agentType === 'workshop-step-execution' || agentType === 'workshop-step-debug' || agentType === 'workshop-step-learning' || agentType === 'workshop-background-task' || agentType === 'workshop-report-execution'
        // Sub-agents within workshop steps have workshop_step_id in metadata (set by ContextAwareEventBridge)
        const metadata = (innerData?.metadata ?? agentEvent?.metadata) as Record<string, unknown> | undefined
        const workshopStepId = metadata?.workshop_step_id as string | undefined
        // Any agent with workshop_step_id metadata is a sub-agent of a workshop step
        // (includes execution, learning, eval, and generic agents)
        const isWorkshopSubAgent = !isWorkshopWrapper && !!workshopStepId
        if ((isWorkshopWrapper || isWorkshopSubAgent) && hasUserSentMessageRef.current) {
          if (isStaleAutoNotificationEvent(event)) {
            console.log('[WORKSHOP] Skipping stale auto-notification event', {
              eventType: event.type,
              agentType,
              timestamp: event.timestamp,
            })
            continue
          }

          const agentName = (innerData?.agent_name ?? agentEvent?.agent_name ?? 'unknown') as string
          const success = (innerData?.success ?? agentEvent?.success) as boolean
          const result = (innerData?.result ?? agentEvent?.result ?? '') as string

          const inputData = (innerData?.input_data ?? agentEvent?.input_data) as Record<string, string> | undefined
          const stepType = inputData?.step_type ?? ''

          // Skip notification for human_input steps — they complete instantly and don't need notifications
          // Skip notification for cancelled steps — only real failures should be reported
          const isCancelled = result.startsWith('Cancelled:')
          if (stepType === 'human_input' || isCancelled) {
            console.log('[WORKSHOP] Skipping notification for step', { agentName, stepType, isCancelled })
          } else {
            const truncated = result.length > 5000 ? result.substring(0, 5000) + '...' : result
            const fullFailureText = result
            const timestamp = formatAutoNotificationTime(event)
            const runFolder = inputData?.run_folder ?? ''
            const runInfo = runFolder ? ` [run: ${runFolder}]` : ''

            // Prefix all notifications so the LLM knows these are automated, not user messages
            const AUTO_PREFIX = `${AUTO_NOTIFICATION_PREFIX} `
            let notification: string
            if (agentType === 'workshop-step-learning') {
              notification = success
                ? `${AUTO_PREFIX}[LEARNING COMPLETE] [${timestamp}] ${agentName} — ${truncated}`
                : `${AUTO_PREFIX}[LEARNING FAILED] [${timestamp}] ${agentName} failed.\nError: ${fullFailureText}`
            } else if (agentType === 'workshop-step-debug') {
              notification = success
                ? `${AUTO_PREFIX}[OPTIMIZATION COMPLETE] [${timestamp}] ${agentName} — ${truncated}`
                : `${AUTO_PREFIX}[OPTIMIZATION FAILED] [${timestamp}] ${agentName} failed.\nError: ${fullFailureText}`
            } else if (agentType === 'workshop-background-task') {
              notification = success
                ? `${AUTO_PREFIX}[BACKGROUND TASK COMPLETE] [${timestamp}] ${agentName} finished.\nResult: ${truncated}`
                : `${AUTO_PREFIX}[BACKGROUND TASK FAILED] [${timestamp}] ${agentName} failed.\nError: ${fullFailureText}`
            } else {
              // Check if the result content indicates failure even when success=true (no execution error)
              // A step can complete without throwing an error but still report STATUS: FAILED in the result
              const resultIndicatesFailure = success && result && /STATUS:\s*FAILED|FAILED:|FAILURE:/i.test(result)
              // Determine if this is a sub-agent within a todo task (vs a top-level step)
              const isSubAgent = isWorkshopSubAgent
              const eventLabel = isSubAgent ? 'SUB-AGENT' : 'STEP'

              // Action hints removed — system prompt already has detailed instructions
              const actionHint = ''

              if (resultIndicatesFailure) {
                notification = `${AUTO_PREFIX}[${eventLabel} FAILED] [${timestamp}]${runInfo} ${agentName} completed but result indicates failure.\nResult: ${fullFailureText}${actionHint}`
              } else if (success) {
                notification = `${AUTO_PREFIX}[${eventLabel} COMPLETED] [${timestamp}]${runInfo} ${agentName} finished successfully.\nResult: ${truncated}${actionHint}`
              } else {
                notification = `${AUTO_PREFIX}[${eventLabel} FAILED] [${timestamp}]${runInfo} ${agentName} failed.\nError: ${fullFailureText}${actionHint}`
              }
            }

	            const corrId = (innerData?.correlation_id ?? agentEvent?.correlation_id ?? '') as string
	            const dedupeKey = `${agentName}::${agentType}::${corrId}`
	            if (notifiedWorkshopAgentsRef.current.has(dedupeKey)) {
	              console.log('[WORKSHOP] Skipping duplicate notification for', dedupeKey)
		            } else {
		              const tabId = tab?.tabId
		              if (typeof tabId !== 'string') {
		                continue
		              }
		              const safeTabId = tabId as string
		              notifiedWorkshopAgentsRef.current.add(dedupeKey)
		              const currentQueue = chatStore.getTabConfig(safeTabId)?.queuedMessages || []
		              chatStore.setTabConfig(safeTabId, { queuedMessages: [...currentQueue, notification] })
		              console.log('[WORKSHOP] Queued step completion notification', { agentName, agentType, success })
		            }
          }
        }
      }

      if (event.type === 'learn_code_script_execution') {
        const scriptedData = (innerData ?? agentEvent ?? {}) as Record<string, unknown>
        console.log('[FIX_LEARN_CODE_UI] chat_area_event_received', {
          sessionId: actualSessionId,
          tabId: tab?.tabId ?? null,
          eventId: event.id,
          correlationId: (event as unknown as Record<string, unknown>).correlation_id ?? agentEvent?.correlation_id ?? scriptedData?.correlation_id ?? null,
          stepId: scriptedData.step_id ?? null,
          stepTitle: scriptedData.step_title ?? null,
          fixIteration: scriptedData.fix_iteration ?? null,
          isSavedScript: scriptedData.is_saved_script ?? null,
          success: scriptedData.success ?? null,
        })
      }

      newEvents.push(withDisplaySafeUserMessage(event))
    }
    // Left and right state are separate: chat events never refresh, mark
    // stale, or otherwise touch the right pane (workspace files, decisions,
    // Pulse, secrets). The right pane gets its state from the server via its
    // own fetches and the live feed; the agent may only change it through an
    // explicit ui-control command.
    // A foreground terminal event is the per-turn completion contract. Settle
    // the chat immediately instead of waiting for a later activity-cache poll;
    // the retained tmux/session may stay alive for the next user message.
    if (hasCompletionEvent && tab) {
      const hasBgAgents = response.has_running_background_agents ?? tab.hasRunningBgAgents
      chatStore.setTabStreaming(tab.tabId, false)
      chatStore.setTabCompleted(tab.tabId, !hasBgAgents)
      chatStore.clearStreamingText(actualSessionId)
      if (sessionOwnsGlobalChatIndicators(actualSessionId, activeSessionIdRef.current)) {
        setIsStreaming(false)
        setIsCompleted(!hasBgAgents)
        setHasActiveChat(hasBgAgents)
      }
      void chatStore.getActiveSessions(true).catch(error => {
        logger.warn('ChatArea', 'Failed to refresh activity after foreground completion', error)
      })
    }

    // Process workflow events — only for the ACTIVE preset's tabs
    // Background workflow tabs (different preset) still receive and store events via SSE,
    // but we skip side effects (canvas updates, step progress, workspace refresh) to avoid
    // polluting the currently visible workflow's UI state.
    //
    // PERF: Removed step_progress_updated / batch_group_start/end processing from chat events.
    // These were calling setStepStatus/handleBatchGroupStart which update workflowStore →
    // trigger usePlanToFlow → full Dagre layout recomputation for ALL canvas nodes on every event.
    // Step status coloring on the canvas is not needed during chat — it only matters in execution mode.
    // Auto-notifications for step completions are now handled by the backend via
    // processBackgroundAgentCompletion → executeSyntheticTurn. Disabled frontend queuing.
    if (ENABLE_LEGACY_FRONTEND_AUTO_NOTIFICATIONS && selectedModeCategory === 'workflow') {
      for (const event of response.events as PollingEvent[]) {
        if (event.type === 'todo_task_step_completed' && hasUserSentMessageRef.current) {
          if (isStaleAutoNotificationEvent(event)) {
            console.log('[WORKFLOW] Skipping stale todo completion auto-notification', {
              timestamp: event.timestamp,
            })
            continue
          }

          const eventData = event.data as Record<string, unknown> | undefined
          const todoStepData = (eventData?.data as Record<string, unknown>) || eventData
          const stepTitle = todoStepData?.step_title as string | undefined
	          const tabId = tab?.tabId
	          const phaseId = tab?.metadata?.phaseId
		          if (typeof tabId === 'string' && stepTitle && isChatCompatiblePhase(phaseId)) {
		            const safeTabId = tabId as string
		            const dedupeKey = `${stepTitle}::todo-step`
		            if (!notifiedWorkshopAgentsRef.current.has(dedupeKey)) {
		              notifiedWorkshopAgentsRef.current.add(dedupeKey)
		              const notification = `${AUTO_NOTIFICATION_PREFIX} [STEP COMPLETED] [${formatAutoNotificationTime(event)}] ${stepTitle} finished successfully.`
		              const currentQueue = chatStore.getTabConfig(safeTabId)?.queuedMessages || []
		              chatStore.setTabConfig(safeTabId, { queuedMessages: [...currentQueue, notification] })
		            }
		          }
        }
      }
    }

    // Store events for ALL tabs with active SSE connections, including background presets.
    // Why: Background workflows keep SSE alive while running (see tabsWithActiveSessions).
    // Their events must be stored so they're visible when the user switches back — otherwise
    // tool calls, step completions, and agent outputs that arrived while viewing another
    // workflow would be permanently lost. Chat events have no right-pane side effects.
    if (tab && newEvents.length > 0) {
      const finalTab = chatStore.getTab(tab.tabId)
      if (!finalTab) {
        applyConfirmationsToStore()
        return
      }
      // Receipt windows take the synchronous append+apply helper so a
      // verdict matches rows it arrived with; the hot path stays
      // micro-batched exactly as before.
      if (confirmations.length > 0) appendTimelineAndApplyConfirmations(actualSessionId, newEvents, confirmations)
      else addTabEvents(actualSessionId, newEvents)
      recordChatDeliveryTelemetry('processed', actualSessionId, newEvents, 'event_processor', finalTab.tabId)
    } else {
      applyConfirmationsToStore()
    }

  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [getTabEvents, getTabLastEventIndex, setTabLastEventIndex, setLastEventIndex, addTabEvents, setIsStreaming, setIsCompleted, setHasActiveChat, selectedModeCategory])

  // Handle an incoming SSE event message: ignore global streaming display events,
  // then process non-streaming events inline.
  const handleSSEMessage = useCallback((msg: SSEEventMessage, sid: string) => {
    const chatStore = useChatStore.getState()
    const actualSessionId = (msg as unknown as Record<string, unknown>).session_id as string || sid

    const incomingEvents = msg.events
    const matchingTabForTelemetry = Object.values(chatStore.chatTabs).find(tab => tab.sessionId === actualSessionId)
    recordChatDeliveryTelemetry('sse_received', actualSessionId, incomingEvents, 'sse', matchingTabForTelemetry?.tabId)

    // Streaming events are transport/progress noise for the main timeline. Keep
    // delegation and owned terminal state, but do not populate the global
    // generation card state.
    const nonStreamingEvents: PollingEvent[] = []
    for (const event of incomingEvents) {
      if (isStreamingEventType(event.type)) {
        // Process streaming events immediately for real-time text display.
        handleLiveStreamingEvent(event, actualSessionId, chatStore)
        // Whole-message narration from a coding CLI is also a durable
        // intermediate reply; see intermediateUpdateFromTranscriptChunk.
        const update = intermediateUpdateFromTranscriptChunk(event)
        if (update) nonStreamingEvents.push(update)
      } else {
        nonStreamingEvents.push(event)
      }
    }

    // Process non-streaming events immediately (no batching delay)
    if (nonStreamingEvents.length > 0 || msg.session_status) {
      const msgAny = msg as unknown as Record<string, unknown>
      const store = useChatStore.getState()
      const matchingTab = Object.values(store.chatTabs).find(t => t.sessionId === actualSessionId) || null
      processEventsResponse(
        {
          events: nonStreamingEvents,
          session_status: msg.session_status,
          runtime_state: msg.runtime_state,
          display_status: msg.display_status,
          last_processed_index: msg.last_processed_index,
          has_more: msgAny.has_more as boolean | undefined,
          has_running_background_agents: msg.has_running_background_agents,
          is_synthetic_turn: (msg as unknown as Record<string, unknown>).is_synthetic_turn as boolean | undefined,
          can_steer: (msg as unknown as Record<string, unknown>).can_steer as boolean | undefined,
          session_id: actualSessionId !== sid ? actualSessionId : undefined,
        },
        sid,
        matchingTab
      )
    }
  }, [processEventsResponse])

  // Handle SSE status-only messages (no events, just session status updates)
  const handleSSEStatus = useCallback((msg: SSEStatusMessage, sid: string) => {
    // last_processed_index is required on SSEEventMessage but a status-only
    // tick carries no cursor information at all -- it must NOT be -1, which
    // processEventsResponse treats as "the backend has no record of this
    // session, resync" (see the reset branch below). This is a real,
    // distinct signal now sent by the backend's SSE handler on an actual
    // detected restart; the backend also sends status ticks every 2s for
    // every open tab regardless, so reusing -1 here made that reset branch
    // fire on a ~2s cadence for every healthy session, not just a genuine
    // restart. -2 is a plain "no cursor info" placeholder: it doesn't match
    // either the `>= 0` (real progress) or `=== -1` (resync) branch below.
    handleSSEMessage(
      { events: [], ...msg, last_processed_index: -2 } as SSEEventMessage,
      sid
    )
  }, [handleSSEMessage])

  // Polling function to get events for ALL active sessions (fallback when SSE unavailable)
  const pollEvents = useCallback(async () => {
    const identity = captureChatIdentity()
    const chatStore = useChatStore.getState()

    // Read mode from store directly to avoid stale closure from setInterval capture
    // Transport ownership follows sessions, including background tabs in
    // another mode. Switching the visible view cannot pause catch-up.
    const allTabs = Object.values(chatStore.chatTabs)

    // CRITICAL: Only poll tabs that are:
    // 1. Actively streaming (query in progress)
    // 2. Have session ID in backend's active sessions list (backend determines activity based on events)
    // 3. The visible chat (so a completed transcript stays immediately usable)
    // We don't poll completed sessions - they're done and won't have new events
    // We also don't poll uninitialized sessions (no query submitted yet)
    //
    // Read activeSessionIds fresh from the store to avoid stale closure from setInterval capture
    const freshActiveIds = new Set(
      chatStore.activeSessionsCache
        .filter(session => sessionStreamingState(session).isActive)
        .map(session => session.session_id),
    )
    const tabsToPoll = allTabs.filter(tab => {
      const currentTab = chatStore.getTab(tab.tabId)
      if (!currentTab?.sessionId) {
        return false
      }

      // Hidden completed chats must not replay their durable history forever.
      // Real background work remains connected through the activity signals.
      if (currentTab.metadata?.mode === 'multi-agent') {
        return shouldKeepChatSessionSubscribed({
          isVisible: chatStore.activeTabId === currentTab.tabId,
          isStreaming: currentTab.isStreaming,
          hasRunningBackgroundAgents: currentTab.hasRunningBgAgents,
          isBackendActive: freshActiveIds.has(currentTab.sessionId),
        })
      }

      // Check if session is in backend's active sessions list (source of truth)
      // Backend determines activity based on event activity (10 min timeout)
      // CRITICAL: Also allow polling if tab is streaming (user just submitted a query)
      const isStreaming = currentTab.isStreaming
      const isInActiveSessions = freshActiveIds.has(currentTab.sessionId)

      // Allow polling if:
      // 1. Session is in backend's active sessions list, OR
      // 2. Tab is currently streaming (query just submitted)
      if (!isInActiveSessions && !isStreaming) {
        return false
      }

      // A locally completed turn may still receive child completions while the
      // backend keeps the session active. Only stop polling when both sources
      // agree that the session is done.
      if (
        currentTab.isCompleted &&
        !currentTab.hasRunningBgAgents &&
        !freshActiveIds.has(currentTab.sessionId)
      ) {
        return false
      }

      return true
    })

    // CRITICAL: Poll by sessionId, not observerId
    // Multiple observers can view the same session, but events are stored per session
    const sessionsToPoll: Array<{ sessionId: string; tab: ChatTab | null }> = []

    // Add all tab sessions (deduplicate by sessionId)
    const seenSessionIds = new Set<string>()
    tabsToPoll.forEach(tab => {
      const currentTab = chatStore.getTab(tab.tabId)
      const sessionId = currentTab?.sessionId || tab.sessionId
      if (sessionId && !seenSessionIds.has(sessionId)) {
        seenSessionIds.add(sessionId)
        sessionsToPoll.push({ sessionId, tab: currentTab || tab })
      }
    })

    if (sessionsToPoll.length === 0) {
      return
    }

    // Poll each session
    for (const { sessionId, tab } of sessionsToPoll) {
      if (!isChatIdentityCurrent(identity)) return
      let currentTab = tab

      if (tab) {
        // Re-fetch the tab from store to ensure we have the latest session ID
        const fetchedTab = chatStore.getTab(tab.tabId)
        if (!fetchedTab) {
          continue
        }
        currentTab = fetchedTab

        // Verify session ID matches
        if (currentTab.sessionId !== sessionId) {
          // Use the new session ID
          if (!currentTab.sessionId) {
            continue
          }
        }

        // Double-check: verify this tab should still be polled
        // Only check isCompleted and sessionId - isStreaming is UI-only, not used for polling decisions
        if (currentTab.isCompleted && !currentTab.sessionId) {
          continue
        }
      }

      // Get fresh tab from store to ensure we have latest session ID
      const freshTab = currentTab ? chatStore.getTab(currentTab.tabId) : null
      const effectiveSessionId = freshTab?.sessionId || currentTab?.sessionId || sessionId

      let rawLastEventIndex = currentTab
        ? getTabLastEventIndex(effectiveSessionId)
        : lastEventIndexRef.current

      // Event cursors belong to the backend's raw event store, not the rendered
      // transcript. A restored conversation includes synthesized history rows,
      // so its length (or an old sentinel) can be ahead of a newly attached
      // CLI stream and make every fresh tool/result event look already seen.
      // Restart from the backend's safe forward cursor instead of guessing.
      if (rawLastEventIndex >= 9999) {
        rawLastEventIndex = 0
        if (currentTab) {
          setTabLastEventIndex(effectiveSessionId, 0)
        } else {
          setLastEventIndex(0)
        }
      }

      // Ensure lastEventIndex is >= 0 (API requirement)
      // -1 means "no events yet", which should be treated as 0
      const currentLastEventIndex = Math.max(0, rawLastEventIndex === -1 ? 0 : rawLastEventIndex)

      // Track which session is currently being polled (for derived isStreaming)

      try {
        const response = await agentApi.getSessionEvents(effectiveSessionId, currentLastEventIndex, {
          durableChat: !isExecutionConversationTab(currentTab),
        })
        if (!isChatIdentityCurrent(identity)) return
        if (response.session_id && response.session_id !== effectiveSessionId) continue
        if (currentTab && chatStore.getTab(currentTab.tabId)?.sessionId !== effectiveSessionId) continue

        recordChatDeliveryTelemetry('poll_received', effectiveSessionId, response.events, 'poll', currentTab?.tabId)
        processEventsResponse(response, effectiveSessionId, currentTab)
      } catch {
        // Continue polling other observers even if one fails
      }
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps -- selectedModeCategory read from store directly inside callback to avoid stale setInterval closure
  }, [getTabLastEventIndex, setTabLastEventIndex, setLastEventIndex, addTabEvents, getTabEvents, setIsStreaming, setIsCompleted, setHasActiveChat, activeSessionIds, processEventsResponse])

  const handleSSEFallback = useCallback((sessionId: string) => {
    logger.warn('ChatArea', `SSE failed for session ${sessionId}; starting polling fallback`)
    startPolling(pollEvents)
  }, [startPolling, pollEvents])

  // No foreground chat can rely on EventSource alone. Browsers cap concurrent
  // HTTP/1.1 connections per origin, and restored AgentWorks tabs may already
  // occupy those slots with long-lived SSE streams. A queued EventSource does
  // not reliably raise onerror, so the nominal SSE fallback never starts and a
  // healthy tmux turn can look completely blank. Keep one small session-scoped
  // catch-up loop for every submitted foreground turn. It reads the same event
  // store and event IDs make concurrent SSE delivery harmlessly idempotent.
  const foregroundCatchUpTimersRef = useRef<Record<string, number>>({})
  const foregroundCatchUpGenerationRef = useRef<Record<string, number>>({})
  const foregroundCatchUpMountedRef = useRef(true)
  useEffect(() => {
    foregroundCatchUpMountedRef.current = true
    return () => {
      foregroundCatchUpMountedRef.current = false
      Object.values(foregroundCatchUpTimersRef.current).forEach(timer => window.clearTimeout(timer))
      foregroundCatchUpTimersRef.current = {}
      foregroundCatchUpGenerationRef.current = {}
    }
  }, [])

  const startForegroundEventCatchUp = useCallback((sessionId: string) => {
    // One loop per durable session is enough. The active-tab healing effect and
    // the submission path may both request it in the same render window.
    if (foregroundCatchUpTimersRef.current[sessionId] !== undefined) return

    const generation = (foregroundCatchUpGenerationRef.current[sessionId] || 0) + 1
    foregroundCatchUpGenerationRef.current[sessionId] = generation
    // Reserve the slot before the first async request so a concurrent caller
    // cannot start a second immediate tick.
    foregroundCatchUpTimersRef.current[sessionId] = -1

    const tick = async () => {
      if (
        !foregroundCatchUpMountedRef.current ||
        foregroundCatchUpGenerationRef.current[sessionId] !== generation
      ) return

      const store = useChatStore.getState()
      const tab = Object.values(store.chatTabs).find(candidate => candidate.sessionId === sessionId) || null
      // Returning without clearing the timer entry leaked a dead generation into
      // the map; the loop is over for this session once its tab is gone.
      if (!tab) {
        delete foregroundCatchUpTimersRef.current[sessionId]
        return
      }

      let shouldContinue = true
      try {
        const since = Math.max(0, store.getTabLastEventIndex(sessionId))
        if (isExecutionConversationTab(tab)) {
          delete foregroundCatchUpTimersRef.current[sessionId]
          return
        }
        const response = await agentApi.getSessionEvents(sessionId, since, { durableChat: true })
        const freshStore = useChatStore.getState()
        const freshTab = Object.values(freshStore.chatTabs).find(candidate => candidate.sessionId === sessionId) || null
        recordChatDeliveryTelemetry('catchup_received', sessionId, response.events, 'foreground_catchup', freshTab?.tabId)
        processEventsResponse(response, sessionId, freshTab)
        shouldContinue = sessionStreamingState(response).isActive
      } catch (error) {
        logger.debug('ChatArea', `Foreground event catch-up failed for ${sessionId}; retrying`, error)
      }

      if (
        shouldContinue &&
        foregroundCatchUpMountedRef.current &&
        foregroundCatchUpGenerationRef.current[sessionId] === generation
      ) {
        foregroundCatchUpTimersRef.current[sessionId] = window.setTimeout(tick, 750)
      } else {
        delete foregroundCatchUpTimersRef.current[sessionId]
      }
    }

    void tick()
  }, [processEventsResponse])



  // Start centralized active sessions polling when component mounts
  useEffect(() => {
    startActiveSessionsPolling()
    return () => {
      // Note: We don't stop polling here because other components might be using it
      // The polling will be managed globally and cleaned up when app unmounts
    }
  }, [startActiveSessionsPolling])

  // Unified page-load restore: handles both active sessions AND persisted tabs with no events.
  // Runs once per page load to avoid duplicate restores from separate effects racing each other.
  useEffect(() => {
    if (globalHasRestored) return
    // Only restore in multi-agent mode (workflow handles its own restore)
    if (selectedModeCategory !== 'multi-agent') return

    const restoreAll = async () => {
      globalHasRestored = true

      try {
        // --- Phase 1: restore active / recently-completed sessions from backend ---
        // getActiveSessions(true) fetches (or joins the in-flight poll), so no
        // settle delay is needed in front of it.
        const activeSessions = await getActiveSessions(true)
        const restoredSessionIds = new Set<string>()

        if (activeSessions.length > 0) {
          const runningSessions = activeSessions.filter(s => {
            if (s.agent_mode?.toLowerCase() === 'workflow' || s.agent_mode?.toLowerCase() === 'workflow_phase') return false
            if (s.status === 'running') return true
            if (s.status === 'completed' && s.last_activity) {
              if (new Date(s.last_activity).getTime() > Date.now() - 30 * 60 * 1000) return true
            }
            return false
          })

          // Only restore sessions that have a persisted tab or are actively running
          const chatStore = useChatStore.getState()
          const persistedSessionIds = new Set(
            Object.values(chatStore.chatTabs)
              // Product surfaces own their restoration contract. In
              // particular, Video Studio restores the durable transcript;
              // letting the generic live-event hydrator race it leaves a
              // user-only cache after refresh.
              .filter(tab => tab.sessionId && tab.metadata?.agentProfileId !== 'video-studio')
              .map(tab => tab.sessionId!)
          )
          const sessionsToRestore = runningSessions.filter(s =>
            persistedSessionIds.has(s.session_id) || (
              s.status === 'running' && !Object.values(chatStore.chatTabs).some(tab =>
                tab.sessionId === s.session_id && tab.metadata?.agentProfileId === 'video-studio'
              )
            )
          )

          if (sessionsToRestore.length > 0) {
            setIsRestoringChatSessions(true)
          }

          // Each restore is independent; run them together so the pane
          // waits for the slowest one, not the sum of all of them.
          await Promise.all(sessionsToRestore.map(async (activeSession, index) => {
            try {
              const tabId = await restoreSession(activeSession.session_id, {
                title: activeSession.query || 'Active Chat',
                source: 'auto-restore',
              })
              restoredSessionIds.add(activeSession.session_id)
              if (index === 0) {
                switchTab(tabId)
              }
            } catch (err) {
              console.error(`[SessionRestore] auto-restore failed for ${activeSession.session_id}:`, err)
            }
          }))
        }

        // --- Phase 2: hydrate persisted tabs that Phase 1 didn't cover ---
        // (completed sessions from history that are in localStorage but have no events)
        const chatStore = useChatStore.getState()
        const tabs = Object.values(chatStore.chatTabs)
        const tabsToHydrate = tabs.filter(tab => {
          if (!tab.sessionId || tab.metadata?.mode === 'workflow') return false
          if (tab.metadata?.agentProfileId === 'video-studio') return false
          if (restoredSessionIds.has(tab.sessionId)) return false
          return chatStore.getTabEvents(tab.sessionId).length === 0
        })
        if (tabsToHydrate.length > 0) {
          setIsRestoringChatSessions(true)
        }
        // Loading persisted chats one after another kept an empty tab on
        // "Loading conversation..." until every chat before it had loaded.
        // Load the visible tab first and the rest a few at a time.
        const visibleTabId = useChatStore.getState().activeTabId
        const hydrationQueue = [...tabsToHydrate].sort((a, b) =>
          Number(b.tabId === visibleTabId) - Number(a.tabId === visibleTabId))
        let nextHydration = 0
        const hydrateWorker = async () => {
          while (nextHydration < hydrationQueue.length) {
            const tab = hydrationQueue[nextHydration++]
            try {
              await restoreSession(tab.sessionId!, {
                source: 'page-refresh',
                skipConfigRestore: true,
                workspacePath: tab.metadata?.agentProfileWorkspace,
              })
            } catch (err) {
              console.error(`[SessionRestore] page-refresh hydrate failed for tab ${tab.tabId}:`, err)
            }
          }
        }
        await Promise.all([hydrateWorker(), hydrateWorker(), hydrateWorker()])
      } catch (error) {
        console.error('[SessionRestore] page-load restore failed:', error)
      } finally {
        setIsRestoringChatSessions(false)
      }
    }

    restoreAll()
  }, [getActiveSessions, switchTab, selectedModeCategory])

  // Only poll tabs that have their session ID in the backend's active sessions list
  // Backend determines activity based on event activity (10 min timeout)
  // CRITICAL: Also include tabs that are streaming (user just submitted a query)
  // This ensures restored sessions start polling immediately when replying
  const tabsWithActiveSessions = useMemo(() => {
    const activeIds = activeRuntimeSessionIds // Completed rows remain listed for restore; only runtime activity owns a transport.
    const chatStore = useChatStore.getState() // Get fresh store state to check streaming status

    const filtered = tabsWithSessions.filter(tab => {
      // Must have session ID
      if (!tab.sessionId) {
        return false
      }

      // Workflow tabs stay lightweight when genuinely idle. The backend active
      // session signal must remain authoritative after the foreground turn
      // settles: background children can still steer auto-notifications into
      // the main transcript after isStreaming/hasRunningBgAgents have already
      // gone false. Dropping SSE in that gap made those updates appear only
      // after the user's next message reconnected the session.
      if (tab.metadata?.mode === 'workflow') {
        const bgTab = chatStore.getTab(tab.tabId)
        const bgStreaming = bgTab?.isStreaming ?? tab.isStreaming
        const bgRunning = bgTab?.hasRunningBgAgents ?? false
        // The visible Builder chat keeps its stream like any chat: input typed
        // into an idle-but-alive retained CLI (no streaming flag, not listed
        // as backend-active) must still stream its reply without a reload.
        return shouldKeepChatSessionSubscribed({
          isVisible: activeTabIdFromStore === tab.tabId,
          isStreaming: bgStreaming,
          hasRunningBackgroundAgents: bgRunning,
          isBackendActive: activeIds.has(tab.sessionId),
        })
      }

      // Skip completed sessions (definitely done) — unless bg agents are still running.
      const freshTab = chatStore.getTab(tab.tabId)
      if (tab.isCompleted && !(freshTab?.hasRunningBgAgents) && tab.metadata?.mode !== 'multi-agent') {
        return false
      }

      // CRITICAL: Check streaming status directly from store (not from tab object)
      // This ensures we get the latest streaming status even if tabsWithSessions is stale
      const currentTab = chatStore.getTab(tab.tabId)
      const isStreaming = currentTab?.isStreaming ?? tab.isStreaming

      // CRITICAL: Include tabs that are streaming (user just submitted a query)
      // This handles the case where a restored session is being replied to
      // The backend might not have added it to active sessions yet, but we should poll it
      if (isStreaming) {
        return true
      }

      // Include tabs with running background agents (even if session is "completed")
      if (currentTab?.hasRunningBgAgents) {
        return true
      }

      // Keep the visible multi-agent chat available, but do not keep every
      // hidden completed chat subscribed indefinitely.
      if (tab.metadata?.mode === 'multi-agent') {
        return shouldKeepChatSessionSubscribed({
          isVisible: activeTabIdFromStore === tab.tabId,
          isStreaming,
          hasRunningBackgroundAgents: currentTab?.hasRunningBgAgents ?? false,
          isBackendActive: activeIds.has(tab.sessionId),
        })
      }

      // Must be in backend's active sessions list
      // If backend says it's active, poll it even if local isStreaming is false
      // This ensures we catch events that come after stop is pressed
      if (!activeIds.has(tab.sessionId)) {
        return false
      }

      return true
    })

    return filtered
    // PERF FIX: Removed `chatTabs` from dependencies. Previously this memo recomputed on
    // every setTabStreaming/setTabCompleted/setTabConfig because `chatTabs` changed reference.
    // The function already uses getState() for fresh tab data (lines above), so the memo
    // only needs to recompute when tabsWithSessions or activeSessionIds actually change.
  }, [tabsWithSessions, activeRuntimeSessionIds, activeTabIdFromStore])

  // Also heal turns that were already running when this component mounted or
  // hot-reloaded. Submission starts the loop immediately, but restored tabs
  // and a UI updated while tmux is mid-turn must not wait for another message
  // before gaining the same transport fallback.
  useEffect(() => {
    const store = useChatStore.getState()
    for (const tab of tabsWithActiveSessions) {
      if (!tab.sessionId) continue
      const freshTab = store.getTab(tab.tabId) || tab
      if (freshTab.isStreaming || freshTab.hasRunningBgAgents) {
        startForegroundEventCatchUp(tab.sessionId)
      }
    }
  }, [tabsWithActiveSessions, startForegroundEventCatchUp])

  // SSE connection management — connect/disconnect based on active sessions
  // Falls back to polling if SSE connection fails (handled inside connectSSE's onError callback)
  // NOTE: sseConnections is intentionally NOT in the dependency array to avoid infinite loops
  // (connectSSE updates the store → sseConnections changes → effect re-fires → connectSSE again)
  useEffect(() => {
    // Read SSE state fresh from store (not from React state to avoid dep cycle)
    const currentSSE = useChatStore.getState().sseConnections
    // Determine which session IDs need SSE connections.
    // Terminal view mode used to skip SSE — the assumption was that
    // TerminalCenter's /api/terminals poll covered everything. That was
    // true while every coding-agent provider was tmux-backed (live pane
    // state came from polling). For structured CLI providers, the synthetic terminal is
    // built from streaming_chunk events, so skipping SSE means the pane
    // never updates and user messages appear lost. Connect SSE for every
    // active tab regardless of view mode.
    const neededSessionIds = new Set<string>()
    for (const tab of tabsWithActiveSessions) {
      if (!tab.sessionId) continue
      neededSessionIds.add(tab.sessionId)
    }
    const connectSessionStream = (sessionId: string) => {
      const store = useChatStore.getState()
      const stillOwnsTab = Object.values(store.chatTabs).some(tab => tab.sessionId === sessionId)
      if (!stillOwnsTab || store.sseConnections[sessionId]) return
      connectSSE(
        sessionId,
        (msg: SSEEventMessage) => handleSSEMessage(msg, sessionId),
        (msg: SSEStatusMessage) => handleSSEStatus(msg, sessionId),
        () => handleSSEFallback(sessionId),
        !isExecutionConversationTab(Object.values(store.chatTabs).find(tab => tab.sessionId === sessionId)),
      )
    }
    // Connect SSE for sessions that don't have a connection yet (any
    // view mode — see neededSessionIds comment for why terminal mode
    // can no longer skip this).
    for (const tab of tabsWithActiveSessions) {
      if (!tab.sessionId) continue
      const sid = tab.sessionId
      if (currentSSE[sid]) {
        continue
      }

      // On a page refresh tabEventIndices is intentionally empty: the backend
      // EventStore cursor is volatile and cannot be trusted across restarts.
      // Hydrate the compact durable transcript and fetch a fresh cursor before
      // opening SSE. Otherwise connectSSE defaults to since=0 and the server
      // replays the raw retained event window before the current chat appears.
      if (useChatStore.getState().tabEventIndices[sid] === undefined) {
        if (sseRestoreInFlightRef.current.has(sid)) continue
        sseRestoreInFlightRef.current.add(sid)
        const restore = isExecutionConversationTab(tab)
          ? hydrateExecutionConversation(sid, tab.metadata?.agentProfileWorkspace)
          : hydrateTabEvents(sid, { workspacePath: tab.metadata?.agentProfileWorkspace })
        void restore.catch(error => {
          logger.error('ChatArea', `Failed to establish restore cursor before SSE for ${sid}`, error)
        }).finally(() => {
          sseRestoreInFlightRef.current.delete(sid)
          connectSessionStream(sid)
        })
        continue
      }

      connectSessionStream(sid)
    }

    // Disconnect SSE for sessions that are no longer active.
    // Safety guard: never disconnect a session whose tab still has isStreaming=true —
    // tabsWithActiveSessions may have computed before the latest setTabStreaming(true) call,
    // and disconnecting mid-execution would make the stop button disappear.
    const freshChatTabs = useChatStore.getState().chatTabs
    for (const sid of Object.keys(currentSSE)) {
      if (!neededSessionIds.has(sid)) {
        const stillStreaming = Object.values(freshChatTabs).some(
          t => t.sessionId === sid && t.isStreaming === true && !t.isCompleted
        )
        if (!stillStreaming) {
          disconnectSSE(sid)
        }
      }
    }

    // Stop polling when no active sessions
    if (neededSessionIds.size === 0 && pollingInterval) {
      stopPolling()
    }

  }, [tabsWithActiveSessions, connectSSE, disconnectSSE, handleSSEMessage, handleSSEStatus, handleSSEFallback, pollingInterval, startPolling, stopPolling, pollEvents])

  // The account-scoped store owns session transports. Closing a tab or changing
  // accounts tears them down there; unmounting a view must not disconnect the
  // other open conversations or cancel their fallback poller.


  const stopStreamingInFlightRef = useRef(false)

  const stopStreaming = useCallback(async () => {
    const identity = captureChatIdentity()
    if (stopStreamingInFlightRef.current) return
    stopStreamingInFlightRef.current = true

    const chatStore = useChatStore.getState()

    // DO NOT stop polling - let backend determine activity based on events
    // Backend will mark session as inactive after 10 minutes of no events
    // This ensures we catch any pending events after stop is pressed

    // Cancel only the foreground LLM turn for this tab. Background/workflow
    // agents are intentionally left running; explicit session stop handles those.
    // CRITICAL: Only use the active tab's session ID - never fall back to global sessionId.
    const sessionIdToStop = activeTab?.sessionId
    if (!sessionIdToStop) {
      logger.warn('ChatArea', 'No session ID available for active tab')
      stopStreamingInFlightRef.current = false
      return
    }

    let cancelled = false
    try {
      await agentApi.cancelCurrentTurn(sessionIdToStop)
      cancelled = true
    } catch (error) {
      logger.error('ChatArea', 'Failed to cancel current turn:', error)
    } finally {
      // Only mark idle after the backend acknowledges cancellation. If we flip
      // this earlier, queued/new messages can auto-send while the old foreground
      // turn is still accepting cancellation.
      if (cancelled && isChatIdentityCurrent(identity) && activeTab &&
        useChatStore.getState().getTab(activeTab.tabId)?.sessionId === sessionIdToStop) {
        if (useChatStore.getState().activeTabId === activeTab.tabId) setIsStreaming(false)
        chatStore.setTabStreaming(activeTab.tabId, false)
        chatStore.setTabCompleted(activeTab.tabId, true)
      }
      stopStreamingInFlightRef.current = false
    }

    // Deprecated: setLastEventCount removed
  }, [setIsStreaming, activeTab])

  // Store execution options for use in the request
  const executionOptionsRef = useRef<ExecutionOptions | undefined>(undefined)
  // Helper: reset streaming state (replaces 4 duplicated blocks)
  const resetStreamingState = useCallback((tabId?: string) => {
    const store = useChatStore.getState()
    store.setIsStreaming(false)
    store.setHasActiveChat(false)
    if (tabId) store.setTabStreaming(tabId, false)
  }, [])

  // Wrapper function to submit query with the current local query
  const submitQueryImmediately = useCallback(async (
    query: string,
    executionOptions?: ExecutionOptions,
    options?: ChatSubmissionOptions,
  ): Promise<boolean> => {
    const identity = options?.identity ?? captureChatIdentity()
    if (!isChatIdentityCurrent(identity)) return false
    // Mark that user has interacted — enables auto-notifications
    // (prevents stale notifications from SSE backfill on page load)
    hasUserSentMessageRef.current = true

    const trimmedQuery = query?.trim() || ''
    const chatStore = useChatStore.getState()
    const sourceTab = options?.sourceTabId ? chatStore.chatTabs[options.sourceTabId] : undefined
    if (options?.sourceTabId && !sourceTab) {
      logger.warn('ChatArea', `Submission source tab ${options.sourceTabId} no longer exists`)
      return false
    }
    // Delayed delivery owns an exact conversation, even if the tab was reused
    // or the selected chat changed while waiting in the submission lane.
    if (options?.sourceSessionId !== undefined && sourceTab?.sessionId !== options.sourceSessionId) {
      return false
    }
    const handoffTabId = options?.builderHandoff?.tabId
    const submissionTab = handoffTabId ? chatStore.chatTabs[handoffTabId] : (sourceTab ?? activeTab)
    if (handoffTabId && (!submissionTab || submissionTab.sessionId !== options?.builderHandoff?.sessionId)) return false
    const activeTabModeCategory =
      submissionTab?.metadata?.mode === 'workflow' || submissionTab?.metadata?.mode === 'multi-agent'
        ? submissionTab.metadata.mode
        : null
    const submitModeCategory = activeTabModeCategory ?? selectedModeCategory
    const submitAgentMode = submitModeCategory
      ? getAgentModeFromCategory(submitModeCategory) as AgentMode
      : correctAgentMode
    console.log('[ChatArea] submitQueryWithQuery called', { query: trimmedQuery.substring(0, 80), stack: new Error().stack?.split('\n').slice(1, 4).join(' <- ') })

    // Get fresh tab state from store to avoid stale closure issues
    let freshActiveTab = submissionTab?.tabId ? chatStore.chatTabs[submissionTab.tabId] : submissionTab
    const oneShotWorkflowContextSourceTabId = freshActiveTab?.tabId

    // Early validation
    if (!trimmedQuery) {
      logger.warn('ChatArea', 'Empty query, returning early')
      return false
    }

    // AgentWorks has one persistent interactive Chat. Sending the first turn
    // uses the existing tab/session; it must never fork a second Chat from an
    // empty Builder tab. Workshop is now a separate read-only history panel.

    if (options?.sourceSessionId === undefined && freshActiveTab?.metadata?.agentProfileBuilder) {
      const normalized = trimmedQuery.replace(/\s+/g, ' ').trim()
      const chatName = normalized.length > 110 ? `${normalized.slice(0, 110)}...` : normalized
      const builderConfig = freshActiveTab.config
      const projectId = freshActiveTab.metadata.agentProfileProjectId
      if (!projectId) return false
      const chatTabId = await chatStore.createChatTab(chatName, {
        ...freshActiveTab.metadata,
        agentProfileBuilder: false,
        agentProfileConversationKey: `${projectId}:${globalThis.crypto.randomUUID()}`,
        agentProfileConversationId: undefined,
      })
      if (!isChatIdentityCurrent(identity)) return false
      if (builderConfig) chatStore.setTabConfig(chatTabId, { ...builderConfig })
      activateTab(chatTabId)
      freshActiveTab = useChatStore.getState().chatTabs[chatTabId]
      if (options?.builderHandoff) Object.assign(options.builderHandoff, { tabId: chatTabId, sessionId: freshActiveTab?.sessionId })
    }


    // Resolve or create tab
    const resolved = await resolveOrCreateTab({ freshActiveTab, selectedModeCategory: submitModeCategory })
    if (!isChatIdentityCurrent(identity)) return false
    if (!resolved) return false
    const { tab: currentTab, sessionId: tabSessionId } = resolved
    const stillOwnsSubmission = () => isChatIdentityCurrent(identity) &&
      useChatStore.getState().getTab(currentTab.tabId)?.sessionId === tabSessionId
    if (options?.sourceSessionId !== undefined && (
      currentTab.tabId !== options.sourceTabId ||
      tabSessionId !== options.sourceSessionId ||
      useChatStore.getState().getTab(currentTab.tabId)?.sessionId !== options.sourceSessionId
    )) return false
    const receipt = { id: options?.submissionId ?? crypto.randomUUID(), message: trimmedQuery,
      sessionId: tabSessionId, targetTabId: currentTab.tabId }
    const submittedAtPerformanceMS = options?.submittedAtPerformanceMS ?? performance.now()
    const submittedAtClientTime = options?.submittedAtClientTime ?? new Date().toISOString()
    recordChatSubmissionTelemetry('submitted', tabSessionId, receipt.id, {
      tabId: currentTab.tabId,
      observedPerformanceMS: submittedAtPerformanceMS,
    })
    for (const id of new Set([sourceTab?.tabId, currentTab.tabId])) {
      if (id) chatStore.setTabConfig(id, { pendingSubmission: receipt })
    }
    const acceptReceipt = () => {
      if (stillOwnsSubmission()) chatStore.setTabConfig(currentTab.tabId, { conversationAcknowledged: true })
      for (const id of new Set([sourceTab?.tabId, currentTab.tabId])) {
        if (id && useChatStore.getState().getTabConfig(id)?.pendingSubmission?.id === receipt.id) {
          chatStore.setTabConfig(id, { pendingSubmission: undefined })
        }
      }
    }

    // Capture # references before constructing the request. They are consumed
    // only after the backend accepts this message; failed submissions retain
    // them for retry. A blank Builder may have copied them into a new chat, so
    // clear the accepted references from both tabs without removing anything
    // the user attached while this request was starting.
    const submittedWorkflowContext = [...(currentTab.config?.workflowContext ?? [])]
    const consumeSubmittedWorkflowContext = () => {
      if (submittedWorkflowContext.length === 0) return
      const ownerTabIds = new Set([currentTab.tabId, oneShotWorkflowContextSourceTabId].filter((id): id is string => Boolean(id)))
      for (const ownerTabId of ownerTabIds) {
        const latest = useChatStore.getState().getTabConfig(ownerTabId)?.workflowContext ?? []
        const remaining = remainingWorkflowContextAfterSubmission(latest, submittedWorkflowContext)
        if (remaining.length !== latest.length) {
          useChatStore.getState().setTabConfig(ownerTabId, { workflowContext: remaining })
        }
      }
    }

    // Single-entry routing: every chat message goes through /api/query and the
    // backend decides whether to steer it into the running turn, queue it for
    // the next boundary, or start a new turn. The frontend never chooses a
    // live-input endpoint.
    const pendingRestoredConversationPath = currentTab.config?.restoredConversationPath?.trim() || ''
    const hasLocalSessionEvents = currentTab.config.conversationAcknowledged === true || chatStore.getTabEvents(tabSessionId).some(event =>
      ['conversation_resumed', 'unified_completion', 'agent_end', 'streaming_chunk'].includes(event.type ?? ''))
    // An empty transport window is not a new-conversation request. Keep the
    // tab's allocated identity through startup, retries and deployments.

    const ownerPresetId = currentTab.metadata?.presetQueryId
    const presetState = useGlobalPresetStore.getState()
    const freshWorkflowPreset = submitModeCategory === 'workflow'
      ? presetState.getPresetsForMode('workflow').find(preset => preset.id === ownerPresetId) : undefined
    if (submitModeCategory === 'workflow' && !freshWorkflowPreset) {
      chatStore.addToast('The owning automation is unavailable. Reopen it before sending.', 'error')
      return false
    }
    const ownsSelectedWorkflow = ownerPresetId === presetState.activePresetIds.workflow
    const ownedExecutionOptions: ExecutionOptions = ownsSelectedWorkflow ? buildExecutionOptions() : {
      execution_strategy: 'start_from_beginning_no_human',
      workshop_mode: currentTab.metadata?.workshopMode ?? 'workshop',
      enable_knowledgebase: freshWorkflowPreset?.llmConfig?.use_knowledgebase,
    }
    const baseExecutionOptions = executionOptions ?? (
      submitModeCategory === 'workflow' && currentTab?.metadata?.phaseId ? ownedExecutionOptions : undefined
    )
    // Builder and Run chats can be open concurrently. Their prompt/tool policy
    // belongs to the tab that owns the conversation, not the workflow-wide mode
    // selector (which can change while another tab is still alive).
    const effectiveExecutionOptions =
      submitModeCategory === 'workflow' && currentTab?.metadata?.workshopMode
        ? {
            ...(baseExecutionOptions ?? ownedExecutionOptions),
            workshop_mode: currentTab.metadata.workshopMode,
          }
        : baseExecutionOptions
    executionOptionsRef.current = effectiveExecutionOptions

    if (
      submitModeCategory === 'workflow' &&
      !options?.isAutoNotification &&
      currentTab?.metadata?.phaseId &&
      isChatCompatiblePhase(currentTab.metadata.phaseId)
    ) {
      window.dispatchEvent(new CustomEvent('workflow-chat-user-started', {
        detail: {
          tabId: currentTab.tabId,
          presetQueryId: currentTab.metadata?.presetQueryId,
          phaseId: currentTab.metadata?.phaseId,
        },
      }))
    }

    // Build file context — read preset fresh from store to avoid stale closure
    // when switching between workflows (the closure's activeWorkflowPreset may lag behind)
    // Only include visible/removable file context from tab config. Workflow execution
    // folders still travel through workflow_context_paths; restored conversation files
    // use restoredConversationPath so coding agents can native-resume without a visible
    // file chip.
    let effectiveFileContext: Array<{ name: string; path: string; type: 'file' | 'folder' }> = []
    if ((submitModeCategory === 'multi-agent' || submitModeCategory === 'workflow') && currentTab?.config) {
      effectiveFileContext = currentTab.config.fileContext
    }

    const shouldResumeRestoredConversation =
      submitModeCategory === 'multi-agent' ||
      (
        submitModeCategory === 'workflow' &&
        !!currentTab?.metadata?.phaseId &&
        isChatCompatiblePhase(currentTab.metadata.phaseId)
      )
    const storedRestoredConversationPath = shouldResumeRestoredConversation
      ? currentTab?.config?.restoredConversationPath?.trim()
      : ''
    const restoredConversationPath = storedRestoredConversationPath || ''
    const restoredConversationUsesNative = restoredConversationPath
      ? currentTab?.config?.restoredConversationNativeResume === true
      : false
    const restoredConversationSummary = currentTab?.config?.restoredConversationSummary?.trim()
    const restoredConversationHasVisibleFallback = restoredConversationPath
      ? effectiveFileContext.some((file) => file.path === restoredConversationPath)
      : false
    const restoredConversationContext = restoredConversationPath && restoredConversationHasVisibleFallback
      ? `\n\nPrevious workflow-builder conversation file: ${restoredConversationPath}\nThis file is JSON with a top-level conversation_history array. User messages have Role "human" or "user" and text in Parts[].Text; assistant replies have Role "ai" or "assistant"; tool calls/results may be interleaved and are usually noisy. To understand the recent context, scan conversation_history from the end for the latest user/assistant Text parts. Do not treat the last JSON entry as the last user request, because it may be a tool result or function call.${restoredConversationSummary ? `\n\n${restoredConversationSummary}` : ''}`
      : ''
    const fileContextForPrompt = restoredConversationPath
      ? effectiveFileContext.filter((file) => file.path !== restoredConversationPath)
      : effectiveFileContext

    const queryBaseWithContext = fileContextForPrompt.length > 0
      ? `${query.trim()}\n\n📁 Files in context: ${fileContextForPrompt.map((file: { path: string }) => file.path).join(', ')}`
      : query.trim()
    // The agent receives file paths as request context; the chat timeline shows
    // only the text the user entered.
    const displayQueryWithContext = query.trim()
    const queryWithContext = `${queryBaseWithContext}${restoredConversationContext}`

    if (restoredConversationUsesNative) {
      chatStore.setTabViewMode(currentTab.tabId, 'formatted')
    }

    // Secret values resolve server-side from the workflow box and globals.
    // Personal browser secrets are gone: nothing decrypts client-side here.
    if (!stillOwnsSubmission()) return false
    if (submitModeCategory === 'workflow') {
      useAppStore.getState().setCurrentQuery(displayQueryWithContext)
    }

    // Restored chats should resume naturally in the same session.
    // Only seed an optimistic event_index when the restored history already has backend
    // indices. Mixing "history without indices" and "optimistic message with index 0"
    // creates inconsistent ordering metadata and can make the first follow-up jump around.
    let optimisticUserEventID = options?.optimisticUserEventId
    if (!optimisticUserEventID) {
      const optimisticUserMessage = createNextOptimisticUserMessage(
        tabSessionId,
        displayQueryWithContext,
        chatStore.getTabEvents(tabSessionId),
        receipt.id,
      )
      optimisticUserEventID = optimisticUserMessage.id
      chatStore.addTabEvents(tabSessionId, [optimisticUserMessage])
    }

    // Consume only this request's context after acceptance. Failed sends keep
    // files/resume bindings; files added during delivery remain attached.
    const consumeSubmittedFiles = () => {
      if (!stillOwnsSubmission()) return
      const config = useChatStore.getState().getTabConfig(currentTab.tabId)
      if (!config) return
      const submittedPaths = new Set(effectiveFileContext.map(file => file.path))
      const remaining = config.fileContext.filter(file => !submittedPaths.has(file.path))
      const consumedRestore = restoredConversationPath && config.restoredConversationPath === restoredConversationPath
      if (remaining.length !== config.fileContext.length || consumedRestore) {
        chatStore.setTabConfig(currentTab.tabId, {
          fileContext: remaining,
          ...(consumedRestore ? {
            restoredConversationPath: undefined, restoredConversationSummary: undefined,
            restoredConversationTitle: undefined, restoredConversationWorkshopModeLabel: undefined,
            restoredConversationRuntimeLabel: undefined, restoredConversationNativeResume: undefined,
          } : {}),
        })
      }
    }

    // Enable auto-scroll and scroll to bottom
    chatStore.setAutoScroll(true)
    setTimeout(() => { scrollToBottom('smooth') }, 50)
    // The conversation scrolls inside the transcript, not the chat container.
    followTranscriptLatest(currentTab.tabId)

    // Clear query text
    useAppStore.getState().setCurrentQuery('')

    // A selected view's cached finalResponse is not evidence for this session.
    // Durable history and session-owned events are the only transcript sources.

    // Reset UI state for new query
    chatStore.setFinalResponse('')
    chatStore.setIsCompleted(false)
    chatStore.setIsStreaming(true)
    chatStore.setHasActiveChat(true)
    chatStore.setTabCompleted(currentTab.tabId, false)
    chatStore.setTabStreaming(currentTab.tabId, true)

    // Reset lastEventIndex so polling starts fresh from the in-memory event store
    // (critical when continuing a restored session — DB events have different indices than in-memory)
    if (!fullTurnStreaming) {
      chatStore.setTabLastEventIndex(tabSessionId, -1)
    }

    // SSE connection is established in connectAfterRefresh below (after getActiveSessions)
    // Polling is only used as a fallback if SSE fails (handled by connectSSE's onError)

    processedCompletionEventsRef.current.clear()

    try {
      // Get active presets for the current mode
      const presetStore = useGlobalPresetStore.getState()
      const chatPreset = submitAgentMode === 'multi-agent' && ownerPresetId
        ? presetStore.getPresetsForMode('multi-agent').find(preset => preset.id === ownerPresetId) : null
      // Read workflow preset fresh from store (not from stale closure)
      // For workflow mode, always try to get the active preset regardless of selectedWorkflowPreset closure value
      const workflowPreset = freshWorkflowPreset
      const activePreset = workflowPreset || chatPreset

      const presetTools = activePreset?.selectedTools || []
      const filteredPresetTools = presetTools.filter(t => !t.endsWith(':*'))

      const chatPresetId = chatPreset?.id || null
      const workflowPresetId = workflowPreset?.id || null

      // Determine mode flags using helper
      const useCodeExecutionMode = determineModeFlag({
        correctAgentMode: submitAgentMode,
        selectedModeCategory: submitModeCategory || '',
        presetValue: activePreset?.useCodeExecutionMode,
        tabConfigValue: currentTab?.config?.useCodeExecutionMode,
      })
      // Build LLM config
      const isMultiAgentMode = submitModeCategory === 'multi-agent'
      const llmStore = useLLMStore.getState()
      // For multi-agent and workflow phase chat: use tab's LLM if set (user may override)
      const isWorkflowPhaseChat = submitModeCategory === 'workflow'
        && currentTab?.metadata?.phaseId
        && isChatCompatiblePhase(currentTab.metadata.phaseId)
      // For phase chat: prefer preset LLM if user hasn't explicitly overridden
      // (tab config always has a default from workflowPrimaryConfig, so we also check the preset)
      const phaseChatPreset = isWorkflowPhaseChat
        ? freshWorkflowPreset
        : null
      const presetBuilderLLM = phaseChatPreset?.llmConfig?.builder_llm
      const presetLLMConfig = presetBuilderLLM?.provider && presetBuilderLLM?.model_id
        ? { provider: presetBuilderLLM.provider, model_id: presetBuilderLLM.model_id, options: presetBuilderLLM.options }
        : null
      const baseLLMConfig = isWorkflowPhaseChat
        ? (currentTab?.config?.llmConfig || presetLLMConfig || llmStore.primaryConfig)
        : (isMultiAgentMode && currentTab?.config?.llmConfig)
          ? currentTab.config.llmConfig
          : llmStore.primaryConfig
      // Chat has one primary model. Product profiles may replace it with their
      // server-owned runtime; delegation tiers no longer participate in normal
      // chat model selection because chat no longer creates sub-agents.
      const effectiveLLMConfig: ExtendedLLMConfiguration = baseLLMConfig

      const llmConfigWithApiKeys = buildLLMConfigWithApiKeys(effectiveLLMConfig)

      // DEBUG: browser config from current tab before payload build
      console.log('[DEBUG browser tab config]', {
        tabId: currentTab?.tabId,
        modeCategory: submitModeCategory,
        browserMode: currentTab?.config?.browserMode,
        enableBrowserAccess: currentTab?.config?.enableBrowserAccess,
        useCdp: currentTab?.config?.useCdp,
        cdpPort: currentTab?.config?.cdpPort,
        selectedServers: currentTab?.config?.selectedServers,
      })

      // Build request payload
      const requestPayload = applyAgentProfileBinding(buildQueryRequestPayload({
        queryWithContext,
        correctAgentMode: submitAgentMode,
        selectedModeCategory: submitModeCategory,
        enabledTools: allTools.filter(tool => currentTab.config.selectedServers.includes(tool.server ?? '')),
        effectiveServers: currentTab.config.selectedServers,
        currentTab,
        effectiveLLMConfig,
        llmConfigWithApiKeys,
        useCodeExecutionMode,
        executionOptions: effectiveExecutionOptions,
        workflowPresetId,
        chatPresetId,
        filteredPresetTools,
        hasActivePreset: !!activePreset,
        selectedGlobalSecrets: activePreset?.selectedGlobalSecretNames ?? undefined,
        restoredConversationPath,
      }), currentTab)

      // Validate execution groups for workflow mode
      const executionPhaseId = currentTab?.metadata?.phaseId
      const requiresGroupValidation = executionPhaseId !== 'evaluation-execution' && executionPhaseId !== 'report-execution'

      if (submitAgentMode === 'workflow' && requestPayload.execution_options && !isWorkflowPhaseChat && requiresGroupValidation) {
        const validationError = validateExecutionGroups(requestPayload.execution_options)
        if (validationError) {
          chatStore.addToast(validationError, 'warning')
          resetStreamingState(currentTab.tabId)
          return false
        }
      }

      // DEBUG: log final request payload preset_query_id
      console.log('[DEBUG request payload]', {
        agent_mode: requestPayload.agent_mode,
        preset_query_id: requestPayload.preset_query_id,
        phase_id: requestPayload.phase_id,
        has_files_in_context: requestPayload.query.includes('📁 Files in context:'),
        restored_conversation_path: restoredConversationPath || undefined,
        enable_browser_access: requestPayload.enable_browser_access,
        browser_mode: requestPayload.browser_mode,
        cdp_port: requestPayload.cdp_port,
        enabled_servers: requestPayload.enabled_servers,
      })

      // Mark auto-notification requests so backend treats them as synthetic turns
      if (options?.isAutoNotification) {
        requestPayload.is_auto_notification = true
      }
      if (fullTurnStreaming) {
        requestPayload.disable_live_input_delivery = true
      }

      // Request preparation can await secrets/configuration. Recheck the
      // captured owner before delivery if the user closed or reused its tab.
      if (options?.sourceSessionId !== undefined &&
        useChatStore.getState().getTab(currentTab.tabId)?.sessionId !== options.sourceSessionId) {
        return false
      }
      // Set session ID and submit
      if (!stillOwnsSubmission()) return false
      chatStore.setSessionId(tabSessionId)
      console.log('[WF_DEBUG] 1. Submitting', { tabId: currentTab.tabId, tabSessionId, eventCount: chatStore.getTabEvents(tabSessionId).length, mode: currentTab.metadata?.mode })
      const response = currentTab.metadata?.agentProfileChatContract === 'profile-v1' && currentTab.metadata.agentProfileId
        ? await agentApi.startAgentProfileQuery(
            currentTab.metadata.agentProfileId,
            buildAgentProfileChatRequest(requestPayload, currentTab.metadata.agentProfileConversationKey, currentTab.metadata.agentProfileEngine, currentTab.metadata.agentProfileModelID, currentTab.metadata.agentProfileReasoningEffort),
            tabSessionId,
            { identity, submissionId: receipt.id, submittedAtClientTime, continuation: hasLocalSessionEvents || Boolean(pendingRestoredConversationPath) || currentTab.metadata?.isRestored === true, queuedDelivery: options?.queuedDelivery },
          )
        : await agentApi.startQuery(requestPayload, tabSessionId, { identity, submissionId: receipt.id, submittedAtClientTime, queuedDelivery: options?.queuedDelivery })
      recordChatSubmissionTelemetry('api_acknowledged', response.session_id || tabSessionId, receipt.id, {
        tabId: currentTab.tabId,
        elapsedMS: performance.now() - submittedAtPerformanceMS,
        deliveryStatus: response.delivery_status,
        provider: response.provider,
        deliverySource: response.delivery_source,
        serverReceivedAt: response.server_received_at,
        cliAcceptedAt: response.cli_accepted_at,
        serverToCLIMS: response.server_to_cli_ms,
      })
      if (!isChatIdentityCurrent(identity)) return false
      if (!stillOwnsSubmission()) return response.status === 'started' || response.status === 'workflow_started' || response.status === 'live_input_delivered'
      console.log('[WF_DEBUG] 2. Response', { status: response.status, responseSessionId: response.session_id || response.query_id, tabSessionId, match: (response.session_id || response.query_id) === tabSessionId })

      if (response.status === 'started' || response.status === 'workflow_started') {
        const responseSessionId = response.session_id || response.query_id
        if (!responseSessionId) {
          console.log('[WF_DEBUG] ERROR: No sessionId in response')
          logger.error('ChatArea', 'No sessionId in response')
          chatStore.addTabEvents(tabSessionId, [createSubmissionErrorEvent(tabSessionId, 'The server started the request without returning a session identifier.')])
          resetStreamingState(currentTab.tabId)
          return false
        }

        consumeSubmittedWorkflowContext()
        consumeSubmittedFiles()
        acceptReceipt()

        console.log('[WF_DEBUG] 3. Before updateTabSessionId', { old: tabSessionId, new: responseSessionId, changed: responseSessionId !== tabSessionId, oldEvents: chatStore.getTabEvents(tabSessionId).length, newEvents: chatStore.getTabEvents(responseSessionId).length })
        chatStore.setSessionId(responseSessionId)
        chatStore.updateTabSessionId(currentTab.tabId, responseSessionId)
        if (options?.builderHandoff) options.builderHandoff.sessionId = responseSessionId
        if (currentTab.metadata?.agentProfileRuntimeDirty) {
          chatStore.setTabMetadata(currentTab.tabId, { agentProfileRuntimeDirty: false })
        }
        console.log('[WF_DEBUG] 4. After updateTabSessionId', { events: chatStore.getTabEvents(responseSessionId).length, activeTabSession: useChatStore.getState().chatTabs[currentTab.tabId]?.sessionId })
        chatStore.setTabStreaming(currentTab.tabId, true)
        chatStore.setTabCompleted(currentTab.tabId, false)

        // Reactivate historical session if needed
        const currentSessionState = useChatStore.getState().sessionState
        if (currentSessionState === 'completed' || currentSessionState === 'error') {
          chatStore.setSessionState('active')
        }

        // Refresh active sessions cache — SSE connection useEffect will pick up the new session
        const connectAfterRefresh = () => {
          if (!isChatIdentityCurrent(identity)) return
          const store = useChatStore.getState()
          const sid = responseSessionId
          console.log('[WF_DEBUG] 5. connectAfterRefresh', { sid, hasSSE: !!store.sseConnections[sid], events: store.tabEvents[sid]?.length ?? 0, sinceIndex: store.tabEventIndices[sid] })
          // Connect SSE for the new session immediately
          if (shouldRefreshSessionEventStream(fullTurnStreaming, Boolean(store.sseConnections[sid]))) {
            connectSSE(
              sid,
              (msg: SSEEventMessage) => handleSSEMessage(msg, sid),
              (msg: SSEStatusMessage) => handleSSEStatus(msg, sid),
              () => handleSSEFallback(sid)
            )
          }
        }

        getActiveSessions(true)
          .then(connectAfterRefresh)
          .catch(error => {
            logger.error('ChatArea', 'Failed to refresh active sessions cache:', error)
            connectAfterRefresh()
          })
        startForegroundEventCatchUp(responseSessionId)
        return true
      } else if (response.status === 'live_input_delivered' || response.status === 'accepted') {
        // Single-entry routing: the backend took this message into the
        // already-running turn instead of starting a new one — steered into a
        // live CLI ('live_input_delivered') or queued for the next boundary
        // ('accepted'). Keep the turn streaming — the optimistic user bubble
        // already shows the message (the backend-recorded echo is deduped by
        // exact content), and the running turn's SSE carries the agent output
        // until its completion event clears the spinner. Do NOT
        // resetStreamingState here (that would stop the spinner mid-turn).
        const sid = response.session_id || tabSessionId
        consumeSubmittedWorkflowContext()
        consumeSubmittedFiles()
        acceptReceipt()
        if (response.delivery_status) {
          const receiptEventID = optimisticUserEventID
          useChatStore.setState(state => ({ tabEvents: { ...state.tabEvents,
            [tabSessionId]: (state.tabEvents[tabSessionId] || []).map(event => event.id === receiptEventID
              ? withLiveInputReceipt(event, response.delivery_status!, response.provider, response.message_id, response.queue_position)
              : event),
          } }))
        }
        chatStore.setTabStreaming(currentTab.tabId, true)
        chatStore.setTabCompleted(currentTab.tabId, false)
        if (sid && shouldRefreshSessionEventStream(
          fullTurnStreaming,
          Boolean(useChatStore.getState().sseConnections[sid]),
        )) {
          connectSSE(
            sid,
            (msg: SSEEventMessage) => handleSSEMessage(msg, sid),
            (msg: SSEStatusMessage) => handleSSEStatus(msg, sid),
            () => handleSSEFallback(sid)
          )
        }
        if (sid) startForegroundEventCatchUp(sid)
        return true
      } else {
        console.log('[WF_DEBUG] ERROR: Backend non-started response', { status: response.status, message: response.message, response })
        logger.error('ChatArea', 'Backend error:', response)
        chatStore.patchTabEvents(tabSessionId, events => withoutOptimisticUserMessage(events, optimisticUserEventID))
        chatStore.addTabEvents(tabSessionId, [createSubmissionErrorEvent(tabSessionId, response.message || `The server returned ${response.status || 'an error'}.`)])
        resetStreamingState(currentTab.tabId)
        return false
      }
    } catch (error) {
      recordChatSubmissionTelemetry('api_failed', tabSessionId, receipt.id, {
        tabId: currentTab.tabId,
        elapsedMS: performance.now() - submittedAtPerformanceMS,
      })
      if (!isChatIdentityCurrent(identity)) return false

      // A reconciled, authoritative no-delivery outcome is safe to release.
      // Keep receipts for uncertain delivery and identity conflicts.
      if (isConfirmedUndeliveredSubmission(error)) acceptReceipt()
      console.log('[WF_DEBUG] ERROR: Submit exception', { error })
      logger.error('ChatArea', 'Failed to submit query:', error)
      chatStore.patchTabEvents(tabSessionId, events => withoutOptimisticUserMessage(events, optimisticUserEventID))
      // turn_running exhaustion (the interceptor retried for minutes): the
      // previous reply never finished, and the message provably never sent.
      const turnStillRunning = isTurnRunningConflict(error)
      chatStore.addTabEvents(tabSessionId, [createSubmissionErrorEvent(tabSessionId, turnStillRunning
        ? 'The previous reply is still running, so this message was not sent. Send it again once the reply finishes.'
        : error)])
      resetStreamingState(currentTab.tabId)
      return false
    }

  }, [correctAgentMode, selectedModeCategory, getAgentModeFromCategory, isRequiredFolderSelected, finalResponse, effectiveServers, enabledTools, processedCompletionEventsRef, activeTab, scrollToBottom, getActiveSessions, resetStreamingState, connectSSE, handleSSEMessage, handleSSEStatus, buildExecutionOptions, handleSSEFallback, fullTurnStreaming, startForegroundEventCatchUp])

  // Submit every message immediately. The backend's durable conversation-turn
  // dispatcher owns per-session ordering and restart recovery. A second
  // browser-side promise lane can deadlock the entire conversation when one
  // request never settles, leaving every later message as an optimistic
  // single-tick row that never reaches the server.
  const submitQueryWithQuery = useCallback((query: string, executionOptions?: ExecutionOptions, options?: ChatSubmissionOptions) => {
    const sourceTab = options?.sourceTabId
      ? useChatStore.getState().chatTabs[options.sourceTabId]
      : activeTab
    if (options?.sourceTabId && !sourceTab) return Promise.resolve(false)
    const laneKey = sourceTab?.sessionId || sourceTab?.tabId || `${selectedModeCategory || 'unknown'}:pending-tab`
    const identity = options?.identity ?? captureChatIdentity()
    if (!isChatIdentityCurrent(identity)) return Promise.resolve(false)
    const isBuilder = sourceTab?.metadata?.agentProfileBuilder === true
    const builder = isBuilder && options?.sourceSessionId === undefined
      ? acquireBuilderSubmission(`${identity}:${sourceTab?.tabId}:${options?.sourceComposerId ?? 'direct'}`) : undefined
    const pending = sourceTab?.config?.pendingSubmission
    const retryTarget = pending?.message === query.trim() && pending.targetTabId
      ? useChatStore.getState().getTab(pending.targetTabId) : undefined
    const retry = retryTarget && retryTarget.sessionId === pending?.sessionId ? pending : undefined
    const submissionId = options?.submissionId ?? retry?.id ?? crypto.randomUUID()
    const captured: ChatSubmissionOptions = { ...options, identity,
      sourceTabId: sourceTab?.tabId,
      sourceSessionId: options?.sourceSessionId ?? (!isBuilder ? sourceTab?.sessionId ?? undefined : undefined),
      submissionId,
      submittedAtPerformanceMS: options?.submittedAtPerformanceMS ?? performance.now(),
      submittedAtClientTime: options?.submittedAtClientTime ?? new Date().toISOString(),
      builderHandoff: retry ? { tabId: retry.targetTabId, sessionId: retry.sessionId } : builder?.target,
    }
    const submit = () => {
      // A retained CLI can take many seconds to durably acknowledge input, and
      // the lane intentionally serializes those requests to preserve terminal
      // order. Stage the visible user row before entering that lane so rapid
      // messages all appear immediately instead of surfacing one-by-one as the
      // preceding HTTP requests finish.
      const currentSource = sourceTab?.tabId
        ? useChatStore.getState().getTab(sourceTab.tabId)
        : undefined
      if (
        !captured.optimisticUserEventId &&
        sourceTab?.sessionId &&
        currentSource?.sessionId === sourceTab.sessionId &&
        isChatIdentityCurrent(identity)
      ) {
        const store = useChatStore.getState()
        const optimistic = createNextOptimisticUserMessage(
          sourceTab.sessionId,
          query.trim(),
          store.getTabEvents(sourceTab.sessionId),
          submissionId,
        )
        captured.optimisticUserEventId = optimistic.id
        store.addTabEvents(sourceTab.sessionId, [optimistic])
      }

      return (isChatIdentityCurrent(identity)
        ? submitQueryImmediately(query, executionOptions, captured)
        : Promise.resolve(false))
        .then(accepted => {
          if (!accepted && captured.optimisticUserEventId && sourceTab?.sessionId) {
            useChatStore.getState().patchTabEvents(sourceTab.sessionId!, events =>
              withoutOptimisticUserMessage(events, captured.optimisticUserEventId!))
          }
          return accepted
        })
    }
    // Every send funnels through one coordinator: a rapid Enter/double-click
    // shares one HTTP mutation per session + message instead of dispatching
    // the same text twice. The backend still owns live-vs-new-turn routing.
    return liveInputSubmissionCoordinator(`${identity}:${laneKey}`, query, submit).finally(() => builder?.release())
  }, [activeTab, selectedModeCategory, submitQueryImmediately])

  const retryLastProductMessage = useCallback(async () => {
    const lastUserMessage = [...displayEvents]
      .reverse()
      .find((event) => event.type === 'user_message' && !getUserMessageContent(event).startsWith(AUTO_NOTIFICATION_PREFIX))
    const content = lastUserMessage
      ? getDisplaySafeUserMessageContent(getUserMessageContent(lastUserMessage))
      : ''
    if (!content) return
    await submitQueryWithQuery(content, undefined, { sourceTabId: activeTab?.tabId })
  }, [activeTab?.tabId, displayEvents, submitQueryWithQuery])

  // "Not delivered · Resend" on a user message whose live input failed.
  const resendProductMessage = useCallback((content: string) => {
    if (!content.trim()) return
    void submitQueryWithQuery(content, undefined, { sourceTabId: activeTab?.tabId })
  }, [activeTab?.tabId, submitQueryWithQuery])

  // If the active tab is stuck in streaming state, ChatInput queues the user's text
  // instead of calling /api/query. Force-refresh active sessions so the store can
  // clear stale streaming state and let the queue flush as the next turn.
  useEffect(() => {
    const queuedCount = activeTab?.config?.queuedMessages?.length ?? 0
    if (!activeTab?.isStreaming || !activeTab.sessionId || queuedCount === 0) return

    const streamingAge = activeTab.lastStreamingStartedAt
      ? Date.now() - activeTab.lastStreamingStartedAt
      : Number.POSITIVE_INFINITY
    const delay = Number.isFinite(streamingAge)
      ? Math.max(750, STALE_STREAMING_RECOVERY_GRACE_MS - streamingAge + 250)
      : 750

    const timeout = window.setTimeout(() => {
      getActiveSessions(true).catch(error => {
        logger.warn('ChatArea', 'Failed to refresh active sessions for queued-message recovery:', error)
      })
    }, delay)

    return () => window.clearTimeout(timeout)
  }, [
    activeTab?.tabId,
    activeTab?.sessionId,
    activeTab?.isStreaming,
    activeTab?.lastStreamingStartedAt,
    activeTab?.config?.queuedMessages?.length,
    getActiveSessions,
  ])

  // Auto-send queued messages when agent is idle (not streaming)
  const submitQueryWithQueryRef = useRef(submitQueryWithQuery)
  useEffect(() => { submitQueryWithQueryRef.current = submitQueryWithQuery }, [submitQueryWithQuery])

  useEffect(() => {
    configureChatQueueController(
      (message, options) => submitQueryWithQueryRef.current(message, undefined, options),
      prepareQueuedChatMessages,
      message => addToast(message, 'error'),
    )
  }, [addToast])

  // Handle new chat for the active tab. Keep this scoped: workflow and
  // multi-agent tabs can coexist, so starting a fresh conversation in one tab
  // must not clear every tab/event/SSE connection in the app.
  const handleNewChat = useCallback(async (targetTabId?: string) => {
    const identity = captureChatIdentity()
    const chatStore = useChatStore.getState()
    const targetTab = targetTabId ? chatStore.getTab(targetTabId) : activeTab
    const stillOwnsTarget = () => isChatIdentityCurrent(identity) && (!targetTab ||
      useChatStore.getState().getTab(targetTab.tabId)?.sessionId === targetTab.sessionId)
    // Stop the previous backend session first (if it exists). This closes any
    // tmux-backed CLI owner before the tab rotates to a fresh AgentWorks
    // session, preventing two pi-cli sessions from sharing the Chats cwd.
    const currentSessionId = getSessionId()
    // An explicitly targeted empty chat must not fall back to the globally
    // selected session ID: that ID may belong to the concurrently viewed
    // scheduled agent session.
    const sessionIdToClear = targetTabId !== undefined
      ? targetTab?.sessionId
      : (targetTab?.sessionId || currentSessionId)
    if (sessionIdToClear) {
      try {
        const activeSessions = await getActiveSessions(true)
        if (!stillOwnsTarget()) return
        const backendKnowsSession = activeSessions.some(session => session.session_id === sessionIdToClear)
        if (backendKnowsSession) {
          await agentApi.stopSession(sessionIdToClear, true)
        }
      } catch (error) {
        logger.error('ChatArea', 'Failed to stop previous session:', error)
        // Continue with frontend reset even if backend stop fails.
      }
    }

    if (!stillOwnsTarget()) return

    // Product profiles own their durable conversation identity on the server.
    // Rotating only this tab's local UUID would be overwritten by the next
    // product resolve and the prior transcript would reappear.
    let rotatedConversation: { conversation_id: string; session_id: string } | undefined
    const profileId = targetTab?.metadata?.agentProfileId
    const conversationKey = targetTab?.metadata?.agentProfileConversationKey
    if (profileId && conversationKey) {
      try {
        rotatedConversation = await agentApi.startNewAgentProfileConversation(profileId, {
          conversation_key: conversationKey,
        })
      } catch (error) {
        logger.error('ChatArea', 'Failed to start a new product conversation:', error)
        addToast('Could not start a new chat. Your existing conversation was kept.', 'error')
        return
      }
    }

    if (!stillOwnsTarget()) return

    // For workflow mode, preserve the selected preset but reset workflow phase
    if (useChatStore.getState().activeTabId === targetTab?.tabId && selectedModeCategory === 'workflow' && selectedWorkflowPreset) {
      // Keep the preset selected, just reset the workflow phase to default
      const defaultPhase = useWorkflowStore.getState().getDefaultPhase()
      setCurrentWorkflowPhase(defaultPhase)
      // Don't clear selectedWorkflowPreset or currentWorkflowQueryId
    } else if (!targetTab) {
      // For other modes, clear workflow state completely
      clearWorkflowState()
    }

    if (targetTab) {
      activateTab(targetTab.tabId)
      chatStore.resetTabChat(targetTab.tabId, rotatedConversation?.session_id)
      if (rotatedConversation) {
        chatStore.setTabMetadata(targetTab.tabId, {
          agentProfileConversationId: rotatedConversation.conversation_id,
        })
      }
      chatStore.setTabConfig(targetTab.tabId, {
        queuedMessages: [],
        isQueueProcessing: false,
      })
    } else {
      // Legacy fallback for the rare case where New Chat is triggered before
      // a tab exists. Normal tabbed chats use resetTabChat above.
      resetChatState()
      resetSessionId()
      onNewChat?.()
    }

    notifiedWorkshopAgentsRef.current.clear()

    // Explicitly reset events and tracking for new chat
    // Note: Using tabEvents now, not global events
    // Events are cleared when tab is removed/cleared
    setLastEventIndex(-1)
    processedCompletionEventsRef.current.clear()


  }, [addToast, clearWorkflowState, resetChatState, onNewChat, activeTab, selectedModeCategory, selectedWorkflowPreset, setCurrentWorkflowPhase, setLastEventIndex, getActiveSessions])

  // Refresh workflow presets function
  const refreshWorkflowPresets = useCallback(async () => {
    if (workflowModeHandlerRef.current) {
      await workflowModeHandlerRef.current.refreshPresets()
    }
  }, [])

  // Expose methods to parent component
  useImperativeHandle(ref, () => ({
    handleNewChat,
    resetChatState,
    refreshWorkflowPresets,
    submitQuery: async (query, executionOptions) => {
      await submitQueryWithQuery(query, executionOptions)
    },
    getEvents: () => displayEvents,
    isStreaming,
    currentWorkflowPhase
  }), [handleNewChat, resetChatState, refreshWorkflowPresets, submitQueryWithQuery, displayEvents, isStreaming, currentWorkflowPhase])

  // Single source of truth for which product surface shows. A chat is active
  // when it has durable messages or a live turn; terminal presence is never a
  // product-visible state.
  const multiAgentSurface = resolveChatSurface({
    isRestoring: isRestoringChatSessions,
    // The resolver's resume-pending → 'restoring' input is now the SYNCHRONOUS
    // resumePending (computed in render), not the old effect-lagged state.
    resumeSettling: resumePending,
    hasContent: hasConversationContent,
    isStreaming: activeTabStreaming,
    hasRestoredLiveContent,
    isReadOnlyRunView,
  })
  const workflowSurface = resolveWorkflowChatSurface({
    isRestoring: isRestoringWorkflowSession,
    resumeSettling: resumePending,
    hasContent: displayEvents.length > 0,
    isStreaming: activeTabStreaming,
    hasRestoredLiveContent,
    isReadOnlyRunView,
  }, false, !selectedWorkflowPreset || (
    activeTab?.metadata?.mode === 'workflow' &&
    activeTab.metadata.presetQueryId === selectedWorkflowPreset
  ))
  const visibleWorkflowSurface = workflowSurface

  // Keep stale restore markers in sync with the surface (both modes).
  // A native/terminal resume that settles onto the previous-chats list
  // (landing) yielded nothing live, so its restore markers are stale: clear
  // them so typing starts a fresh chat instead of silently resuming a chat
  // you're no longer viewing. (File-fallback resumes —
  // NativeResume false — stay: their attached file context still drives the next
  // turn. Read-only run views never reach landing, so they're untouched.)
  const activeChatSurface = selectedModeCategory === 'workflow' ? visibleWorkflowSurface : multiAgentSurface
  useEffect(() => {
    if (activeChatSurface !== 'landing') return
    const tabId = activeTab?.tabId
    const cfg = activeTab?.config
    if (!tabId || cfg?.restoredConversationNativeResume !== true) return
    // HARD GUARD: never clear the restore markers while a resume could still be
    // pending — that is exactly what canceled resumes mid-flight (the 2-3-click
    // bug). A native resume only legitimately reaches 'landing' once the give-up
    // timer has elapsed with no terminal and no content (resumeGaveUp). Until then
    // restoredConversationPath stays in place so the synchronous resumePending keeps
    // the surface on 'restoring'. When in doubt, do NOT clear.
    if (!resumeGaveUp) return
    const restoredPath = cfg.restoredConversationPath?.trim()
    if (!restoredPath) return
    useChatStore.getState().setTabConfig(tabId, {
      restoredConversationPath: undefined,
      restoredConversationSummary: undefined,
      restoredConversationTitle: undefined,
      restoredConversationWorkshopModeLabel: undefined,
      restoredConversationRuntimeLabel: undefined,
      restoredConversationNativeResume: undefined,
      fileContext: (cfg.fileContext || []).filter(item => item.path !== restoredPath),
    })
  }, [activeChatSurface, activeTab?.tabId, activeTab?.config, resumeGaveUp])

  // Multi-agent landing surface = the previous-chats panel (mirrors the old
  // showNormalPreviousChatsPanel).
  const showNormalPreviousChatsPanel = selectedModeCategory === 'multi-agent' && multiAgentSurface === 'landing'
  // A first-time workflow Chat shows its getting-started guide above the
  // composer. Saved histories live in the separate Workshop workspace view.
  const showWorkflowLandingContent =
    selectedModeCategory === 'workflow' &&
    visibleWorkflowSurface === 'landing' &&
    !!workflowLandingContent
  const hasActiveTranscript =
    (selectedModeCategory === 'workflow' && visibleWorkflowSurface === 'active') ||
    (selectedModeCategory === 'multi-agent' && multiAgentSurface === 'active')
  // A product-supplied content renderer owns the whole pane, so it is always
  // full height regardless of which transcript surface is active.
  const shouldUseFullHeightContent = forcePreviousChats || !!EffectiveContentRenderer || hasActiveTranscript || showNormalPreviousChatsPanel || showWorkflowLandingContent

  return (
    <div className="flex flex-col h-full min-w-0" data-testid="chat-area-container">
      {/* Preset Selection Overlay */}
      {showPresetSelection && pendingModeCategory && (
        <PresetSelectionOverlay
          isOpen={showPresetSelection}
          onClose={handlePresetSelectionClose}
          onPresetSelected={handlePresetSelected}
          modeCategory={pendingModeCategory}
          setCurrentQuery={setCurrentQuery}
        />
      )}

      {/* Mode Switch Dialog */}
      {showModeSwitchDialog && pendingModeSwitch && (
        <ModeSwitchDialog
          isOpen={showModeSwitchDialog}
          onCancel={handleModeSwitchCancel}
          onConfirm={handleModeSwitchConfirm}
          currentModeCategory={selectedModeCategory}
          newModeCategory={pendingModeSwitch}
        />
      )}



      {/* Chat Content - Separated to prevent input re-renders.
          In terminal mode the inner pane owns its own scrolling
          (the rail + log scroll independently), so this wrapper
          must NOT scroll — otherwise the whole page scrolls
          around the fixed-height terminal box. */}
      <div ref={chatContentRef} className={`flex-1 ${shouldUseFullHeightContent ? 'overflow-hidden' : 'overflow-y-auto'} overflow-x-hidden min-w-0 relative overscroll-y-none ${compact ? 'text-sm' : ''}`} style={{ scrollBehavior: 'auto' }}>

        <div className={`min-w-0 ${shouldUseFullHeightContent ? 'flex h-full flex-col' : 'min-h-full'} ${EffectiveContentRenderer ? '' : compact ? 'px-2 pb-2' : 'px-3 pb-4'}`}>
          {/* Loading indicator for historical events */}
          {!EffectiveContentRenderer && isLoadingHistory && (
            <div className={`flex items-center justify-center ${compact ? 'py-4' : 'py-8'}`}>
              <div className="flex items-center gap-3 text-gray-600 dark:text-gray-400">
                <div className={`${compact ? 'w-4 h-4' : 'w-5 h-5'} border-2 border-gray-300 dark:border-gray-600 border-t-blue-600 dark:border-t-blue-400 rounded-full animate-spin`}></div>
                <span className={compact ? 'text-xs' : 'text-sm'}>Loading chat history...</span>
              </div>
            </div>
          )}

          {/* Loading indicator for active session checking */}
          {!EffectiveContentRenderer && isCheckingActiveSessions && (
            <div className={`flex items-center justify-center ${compact ? 'py-4' : 'py-8'}`}>
              <div className="flex items-center gap-3 text-gray-600 dark:text-gray-400">
                <div className={`${compact ? 'w-4 h-4' : 'w-5 h-5'} border-2 border-gray-300 dark:border-gray-600 border-t-green-600 dark:border-t-green-400 rounded-full animate-spin`}></div>
                <span className={compact ? 'text-xs' : 'text-sm'}>Checking for active session...</span>
              </div>
            </div>
          )}

          {/* Active session indicator */}
          {!EffectiveContentRenderer && sessionState === 'active' && (
            <div className={`flex items-center justify-center ${compact ? 'py-2' : 'py-4'}`}>
              <div className={`flex items-center gap-2 ${compact ? 'px-2 py-1' : 'px-3 py-2'} bg-green-100 dark:bg-green-900/20 border border-green-200 dark:border-green-800 rounded-lg`}>
                <div className={`${compact ? 'w-1.5 h-1.5' : 'w-2 h-2'} bg-green-500 rounded-full animate-pulse`}></div>
                <span className={`${compact ? 'text-xs' : 'text-sm'} text-green-700 dark:text-green-300 font-medium`}>Live Session - Reconnected</span>
              </div>
            </div>
          )}

          {/* Session error indicator */}
          {!EffectiveContentRenderer && sessionState === 'error' && (
            <div className={`flex items-center justify-center ${compact ? 'py-2' : 'py-4'}`}>
              <div className={`flex items-center gap-2 ${compact ? 'px-2 py-1' : 'px-3 py-2'} bg-red-100 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded-lg`}>
                <svg className={`${compact ? 'w-3 h-3' : 'w-4 h-4'} text-red-600 dark:text-red-400`} fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                </svg>
                <span className={`${compact ? 'text-xs' : 'text-sm'} text-red-700 dark:text-red-300 font-medium`}>Session Error - Unable to reconnect</span>
              </div>
            </div>
          )}

        {forcePreviousChats ? (
          <PreviousChatHistoryPanel
            workspacePath={previousChatsWorkspacePath}
            recentOnly={previousChatsRecentOnly}
            title="Previous chats"
            actionLabel="Resume"
            emptyText="No previous chats yet."
            onSelectSession={handleResumePreviousChat}
            fill
            compact={previousChatsCompact}
          />
        ) : EffectiveContentRenderer && selectedModeCategory !== 'workflow' ? (
          <EffectiveContentRenderer
            events={transcriptEvents}
            isStreaming={activeTabBusy}
            isRestoring={multiAgentSurface === 'restoring'}
            streamingText={activeStreamingText}
            streamingStatus={streamingStatus}
            hasOlder={historyPagination?.hasMore ?? false}
            loadingOlder={olderHistory.sessionId === activeSessionId && olderHistory.loading}
            historyError={olderHistory.sessionId === activeSessionId ? olderHistory.error : undefined}
            onLoadOlder={historyPagination?.hasMore ? loadOlderConversationPage : undefined}
            landingContent={landingContent}
            onRetryLastMessage={retryLastProductMessage}
            onSubmitQuery={(query) => submitQueryWithQuery(query)}
          />
        ) : selectedModeCategory === 'workflow' ? (
          <WorkflowModeHandler
            ref={workflowModeHandlerRef}
            onPresetSelected={handleWorkflowPresetSelected}
            onWorkflowPhaseChange={setCurrentWorkflowPhase}
          >
            {/* restoring — reconnectWorkflowTabs is replaying events. A
                read-only run view (scheduled/bot run) that never receives
                content — its session is gone from the backend's in-memory
                event store, or the fetch itself failed — has no other escape
                from 'restoring' (see readOnlyRunViewGaveUp above), so past
                the give-up timeout show an explicit message instead of
                spinning forever. */}
            {visibleWorkflowSurface === 'restoring' && isReadOnlyRunView && readOnlyRunViewGaveUp && (
              <div className="flex flex-col items-center justify-center py-12 gap-2 text-center px-4">
                <p className="text-sm text-gray-500 dark:text-gray-400">Couldn't load this run's session data — it may no longer be available.</p>
                <button
                  type="button"
                  className="text-sm text-blue-600 dark:text-blue-400 hover:underline"
                  onClick={() => { void retryReadOnlyRunView() }}
                >
                  Try again
                </button>
              </div>
            )}
            {visibleWorkflowSurface === 'restoring' && !(isReadOnlyRunView && readOnlyRunViewGaveUp) && (
              <div className="flex flex-col items-center justify-center py-12 gap-3">
                <div className="w-6 h-6 border-2 border-gray-300 dark:border-gray-600 border-t-blue-600 dark:border-t-blue-400 rounded-full animate-spin"></div>
                <p className="text-sm text-gray-500 dark:text-gray-400">Loading conversation...</p>
              </div>
            )}

            {/* Active workflow runs render the same simple, event-backed
                conversation as a normal chat. The terminal rail exists only
                in explicitly enabled runtime diagnostics. */}
            {visibleWorkflowSurface === 'active' && activeTab?.sessionId && (
              showMainTerminal
                ? (
                    showRuntimeDiagnostics
                      ? (
                          <Suspense fallback={<div className="p-4 text-sm text-neutral-500">Loading runtime diagnostics…</div>}>
                            <RuntimeDiagnosticsPanel currentSessionId={activeTab.sessionId} compact={false} />
                          </Suspense>
                        )
                      : <MainAgentTerminal sessionId={activeTab.sessionId} onUnavailable={() => useChatStore.getState().setTabViewMode(activeTab.tabId, 'formatted')} />
                  )
                : <TerminalEventTranscript
                    scrollKey={activeTab.tabId}
                    events={transcriptEvents}
                    terminal={null}
                    onRetryLastMessage={activeTabBusy ? undefined : retryLastProductMessage}
                    onResendMessage={resendProductMessage}
                    streamingText={activeStreamingText}
                    streamingStatus={streamingStatus}
                    hasOlder={historyPagination?.hasMore ?? false}
                    loadingOlder={olderHistory.sessionId === activeSessionId && olderHistory.loading}
                    error={olderHistory.sessionId === activeSessionId ? olderHistory.error : undefined}
                    onLoadOlder={historyPagination?.hasMore ? loadOlderConversationPage : undefined}
                    onRetry={loadOlderConversationPage}
                  />
            )}

            {/* Fresh workflow Chat. TerminalCenter is intentionally not rendered
                until a message starts a real turn. */}
            {visibleWorkflowSurface === 'landing' && (
              workflowLandingContent ?? null
            )}
          </WorkflowModeHandler>
        ) : (
          <>
            {/* restoring — a previous multi-agent session is loading/replaying. */}
            {multiAgentSurface === 'restoring' && (
              <div className="flex flex-col items-center justify-center py-12 gap-3">
                <div className="w-6 h-6 border-2 border-gray-300 dark:border-gray-600 border-t-blue-600 dark:border-t-blue-400 rounded-full animate-spin"></div>
                <p className="text-sm text-gray-500 dark:text-gray-400">Loading conversation...</p>
              </div>
            )}

            {/* landing — fresh chat / New Chat. The panel renders the list OR its
                own "No previous chats yet." empty, so no separate help page is
                needed here. */}
            {multiAgentSurface === 'landing' && (
              landingContent ?? (
                <PreviousChatHistoryPanel
                  workspacePath={previousChatsWorkspacePath}
                  recentOnly={previousChatsRecentOnly}
                  activeSessionId={hasConversationContent ? activeTab?.sessionId ?? undefined : undefined}
                  title="Previous chats"
                  actionLabel="Resume"
                  emptyText="No previous chats yet."
                  onSelectSession={handleResumePreviousChat}
                  fill
                  compact={previousChatsCompact}
                />
              )
            )}

            {/* The product chat is event-backed. Internal terminal inspection
                is available only through the explicit developer flag. */}
            {multiAgentSurface === 'active' && activeTab?.sessionId && (
              showMainTerminal
                ? (
                    showRuntimeDiagnostics
                      ? (
                          <Suspense fallback={<div className="p-4 text-sm text-neutral-500">Loading runtime diagnostics…</div>}>
                            <RuntimeDiagnosticsPanel currentSessionId={activeTab.sessionId} compact={false} />
                          </Suspense>
                        )
                      : <MainAgentTerminal sessionId={activeTab.sessionId} onUnavailable={() => useChatStore.getState().setTabViewMode(activeTab.tabId, 'formatted')} />
                  )
                : <TerminalEventTranscript
                    scrollKey={activeTab.tabId}
                    events={transcriptEvents}
                    terminal={null}
                    onRetryLastMessage={activeTabBusy ? undefined : retryLastProductMessage}
                    onResendMessage={resendProductMessage}
                    streamingText={activeStreamingText}
                    streamingStatus={streamingStatus}
                    hasOlder={historyPagination?.hasMore ?? false}
                    loadingOlder={olderHistory.sessionId === activeSessionId && olderHistory.loading}
                    error={olderHistory.sessionId === activeSessionId ? olderHistory.error : undefined}
                    onLoadOlder={historyPagination?.hasMore ? loadOlderConversationPage : undefined}
                    onRetry={loadOlderConversationPage}
                  />
            )}
          </>
        )}
        </div>
      </div>

      {/* Input Area - completely isolated from event updates. AgentWorks keeps
          this composer on its one persistent Chat; Workshop history is rendered
          outside ChatArea and therefore never exposes a composer. */}
      {!hideInput && (
        <ChatInput
          // The composer owns tab-local draft, paste, upload and submit state.
          // Remount only this boundary when switching chats. Remounting the
          // whole ChatArea also tears down every shared SSE subscription and
          // foreground catch-up loop, which can drop another tab's completion
          // while a resumed chat is opening.
          key={targetTabId ?? 'inactive-chat-input'}
          onSubmit={(query, options) => submitQueryWithQuery(query, undefined, options)}
          onStopStreaming={stopStreaming}
          onNewChat={() => void handleNewChat(targetTabId ?? undefined)}
          tabId={targetTabId}
          surfaceVariant={inputVariant}
          hideRuntimeStatus={hideRuntimeStatus}
          showCompactRuntimeLoading={showCompactRuntimeLoading}
          showProductSteerAction={showProductSteerAction}
          showProductTerminalControl={showProductTerminalControl}
          showNewChatAction={showNewChatAction}
          placeholderOverride={composerPlaceholder}
        />
      )}

      {/* Scheduled runs show a compact stop action beside the terminal toggle.
          Keep the footer for bot runs and surfaces that hide the input row. */}
      {isReadOnlyRunView && activeTab && (!activeTab.metadata?.isScheduledRun || hideInput) && (
        <SessionStopButton key={`run-stop-${activeTab.tabId}`} tabId={activeTab.tabId} footer />
      )}

      {/* Toasts render from ToastHost at the app root, so they also appear on
          surfaces that do not mount a chat. */}
    </div>
  )
})

ChatAreaInner.displayName = 'ChatAreaInner'

// Main ChatArea component
const ChatArea = ChatAreaInner

ChatArea.displayName = 'ChatArea'

export default ChatArea

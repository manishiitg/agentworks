import { getWorkspaceScopedStorageKey } from './useWorkspaceConnectionStore'
import { captureChatIdentity, isChatIdentityCurrent, assertChatIdentityCurrent, invalidateChatIdentity, getAccountChatStorageKey, readPersistedChatOwner } from '../utils/chatIdentity'
import { sessionStreamingState } from '../utils/sessionStreamingState'
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import type { PollingEvent, ExtendedLLMConfiguration, SessionStatusResponse, ActiveSessionInfo, DelegationTierConfig, SSEEventMessage, SSEStatusMessage, WorkflowScheduleSummary } from '../services/api-types'
import { SSEConnection } from '../services/sse'
import { shouldShowEventByMode } from '../components/events/eventModeUtils'
import type { StoreActions } from './types'
import type { FileContextItem } from './types'
import type { WorkflowPhase } from '../constants/workflow'
import { useAppStore } from './useAppStore'
import { agentApi } from '../services/api'
import { useMCPStore } from './useMCPStore'
import { useLLMStore } from './useLLMStore'
import { MAX_EVENTS_TO_PROCESS, CLEANUP_THRESHOLD } from '../constants/events'
import { logger } from '../utils/logger'
import { compareEventsChronologically, compareEventsReverseChronologically } from '../utils/eventOrdering'
import { appendStreamingText, looksLikeTerminalScreenText, splitStreamingStatusAndText } from '../utils/streamingStatus'
import { coalesceThinkingDeltas } from '../utils/thinkingDeltas'

/**
 * Per-chunk metadata carried by streaming_chunk events. `source` is the
 * backend's authoritative classification (transcript | content | terminal) and
 * is preferred over the looksLikeTerminalScreenText heuristic whenever present —
 * the field exists precisely so a no-terminal UI does not have to guess.
 */
export interface StreamingChunkMeta {
  isDelta?: boolean
  source?: string
}

/** True when the backend positively identifies this chunk as a raw pane frame. */
const isTerminalSourceChunk = (meta?: StreamingChunkMeta): boolean =>
  typeof meta?.source === 'string' && meta.source.trim().toLowerCase() === 'terminal'
import { createHydrationGate, HydrationBackstopError, type HydrationGateSnapshot } from '../utils/hydrationGate'
import { createBufferedPersistStorage } from '../utils/bufferedPersistStorage'
import { retainEventInSessionWorkingSet } from '../utils/sessionEventWorkingSet'
import { autoNotificationDedupKey } from '../utils/internalChatEvents'
import { durableStreamStartCursor } from '../utils/forwardCursor'
import { reconcileDurableUserEchoes } from '../utils/clientMessageIdentity'
import { intermediateUpdateFromTranscriptChunk, isTranscriptChunkUpdate, normalizeTranscriptChunkEvents } from '../utils/transcriptChunkUpdates'

// Active sessions cache TTL (30 seconds - shorter than polling interval to allow force refresh)
const ACTIVE_SESSIONS_CACHE_TTL = 30000
const STALE_STREAMING_GRACE_MS = 10000

// getActiveSessions(true) is called from many independent places (composer
// busy-transition effect, GlobalActivityMonitor's 5s tick, several ChatArea
// turn-lifecycle points) with no coordination between them. Without this
// guard, two calls close together fire two independent HTTP requests that
// can resolve out of order -- and since each resolution unconditionally
// re-runs reconcileSessionTabStreamingState against whatever payload IT got,
// a response that started later but arrives first, followed shortly by one
// that started earlier but arrives second, flips isStreaming back and forth
// on every such pair. Confirmed live: the composer spinner's state genuinely
// oscillating true/false within single-digit milliseconds, not merely
// re-rendering, with paired "cleared stale streaming state" / "marked ...
// streaming to match a backend-active session" log lines from opposite
// reconciliation branches firing back-to-back for the same tab. Sharing one
// in-flight promise across concurrent callers makes at most one request (and
// therefore one reconciliation pass) outstanding at a time.
let pendingActiveSessionsFetch: Promise<ActiveSessionInfo[]> | null = null

// This gate must exist before Zustand creates the persisted store. Browser
// localStorage hydration can finish synchronously during create(), so the
// callback cannot safely touch useChatStore or module bindings declared later.
const CHAT_STORE_HYDRATION_BACKSTOP_MS = 10_000
const chatStoreHydrationGate = createHydrationGate({
  backstopMs: CHAT_STORE_HYDRATION_BACKSTOP_MS,
  backstopMessage: `Chat-store hydration did not finish within ${CHAT_STORE_HYDRATION_BACKSTOP_MS}ms`,
})
let chatStoreHydrationBackstopReported = false

// Streaming inactivity auto-clear timers (per sessionId)
// When no new chunk arrives for 3s, streaming text is auto-cleared
const _streamingInactivityTimers: Record<string, ReturnType<typeof setTimeout>> = {}
const _executionStreamingInactivityTimers: Record<string, ReturnType<typeof setTimeout>> = {}
const STREAMING_INACTIVITY_MS = 60000

// Per-mode event counts type — kept for backwards compat with persisted state
export type PerModeEventCounts = { micro: number }
// `tree` remains only so persisted state and older callers deserialize safely;
// normalizeEventViewMode always migrates it to the formatted product surface.
export type EventViewMode = 'formatted' | 'terminal' | 'tree'

export type ExecutionStreamingActivity = {
  sessionId: string
  executionId: string
  text: string
  status?: string
  lastChunkIndex: number
  updatedAt: number
}

export function normalizeEventViewMode(viewMode?: string | null): EventViewMode {
  // The product default is a readable event-backed conversation. Raw terminal
  // inspection is developer diagnostics and is only rendered when its explicit
  // runtime gate is enabled; preserve an intentional terminal choice for that
  // mode, but migrate the removed legacy tree surface to formatted.
  return viewMode === 'terminal' ? 'terminal' : 'formatted'
}

type ScriptedExecutionData = {
  step_id?: string
  fix_iteration?: number
}

const getScriptedExecutionData = (event: PollingEvent): ScriptedExecutionData | undefined => {
  if (event.type !== 'learn_code_script_execution') return undefined
  const wrapper = event.data as { data?: ScriptedExecutionData } | undefined
  return wrapper?.data || event.data as ScriptedExecutionData | undefined
}

// Helper to compute visible event counts (full recomputation — use sparingly)
const computePerModeCounts = (events: PollingEvent[]): PerModeEventCounts => {
  return {
    micro: events.filter(e => e.type && shouldShowEventByMode(e.type)).length,
  }
}

// Incremental helper: count only new events and add to existing counts
const incrementPerModeCounts = (
  existing: PerModeEventCounts,
  newEvents: PollingEvent[]
): PerModeEventCounts => {
  let microDelta = 0
  for (const e of newEvents) {
    if (e.type && shouldShowEventByMode(e.type)) microDelta++
  }
  return {
    micro: existing.micro + microDelta,
  }
}
// Event memory management constants - use shared constants
const MAX_EVENTS = MAX_EVENTS_TO_PROCESS
const MAX_RETAINED_EVENT_TEXT_CHARS = 200_000
const MAX_COMPLETED_STREAMING_TEXT_CHARS = 60_000

const truncateRetainedText = (value: string, fieldName: string): string => {
  if (value.length <= MAX_RETAINED_EVENT_TEXT_CHARS) return value

  const headChars = Math.floor(MAX_RETAINED_EVENT_TEXT_CHARS * 0.6)
  const tailChars = MAX_RETAINED_EVENT_TEXT_CHARS - headChars
  const omitted = value.length - MAX_RETAINED_EVENT_TEXT_CHARS
  return `${value.slice(0, headChars)}\n\n[Truncated ${omitted.toLocaleString()} chars from ${fieldName} to keep the UI responsive. Load older events or inspect backend logs for the full payload.]\n\n${value.slice(-tailChars)}`
}

const trimLargeRetainedEventFields = (event: PollingEvent): PollingEvent => {
  const agentEvent = event.data as { data?: Record<string, unknown> } | undefined
  const payload = agentEvent?.data
  if (!payload || typeof payload !== 'object') return event

  const updates: Record<string, string> = {}
  if (typeof payload.content === 'string' && payload.content.length > MAX_RETAINED_EVENT_TEXT_CHARS) {
    updates.content = truncateRetainedText(payload.content, 'content')
  }
  if (typeof payload.result === 'string' && payload.result.length > MAX_RETAINED_EVENT_TEXT_CHARS) {
    updates.result = truncateRetainedText(payload.result, 'result')
  }

  if (Object.keys(updates).length === 0) return event

  return {
    ...event,
    data: {
      ...agentEvent,
      data: {
        ...payload,
        ...updates,
        metadata: {
          ...((payload.metadata && typeof payload.metadata === 'object') ? payload.metadata as Record<string, unknown> : {}),
          frontend_retained_payload_truncated: true,
        },
      },
    },
  }
}

const trimLargeRetainedEvents = (events: PollingEvent[]): PollingEvent[] => {
  return events.map(trimLargeRetainedEventFields)
}

const capCompletedStreamingText = (value: string): string => {
  if (value.length <= MAX_COMPLETED_STREAMING_TEXT_CHARS) return value
  const omitted = value.length - MAX_COMPLETED_STREAMING_TEXT_CHARS
  return `[Trimmed ${omitted.toLocaleString()} chars of older streaming text to keep the UI responsive.]\n\n${value.slice(-MAX_COMPLETED_STREAMING_TEXT_CHARS)}`
}

// Persistent event ID index — avoids O(n) Set rebuild on every addTabEvents call.
// Lives outside zustand state so mutating it doesn't trigger re-renders.
const tabEventIdSets = new Map<string, Set<string>>()

// Per-session set of auto-notification identities already in the timeline.
// The live transport can surface one steered [AUTO-NOTIFICATION] completion as
// several distinct-ID user_message events within the session window; the ID
// index above cannot catch those, so they each drew their own duplicate
// "Automation update" row. Keyed by content (autoNotificationDedupKey), which
// is idempotent for a status card and distinct across real re-runs. Shares
// tabEventIdSets' lifecycle: rebuilt on setTabEvents, deleted on clear/close.
const tabAutoNotificationKeys = new Map<string, Set<string>>()

function collectAutoNotificationKeys(events: PollingEvent[]): Set<string> {
  const keys = new Set<string>()
  for (const event of events) {
    const key = autoNotificationDedupKey(event)
    if (key) keys.add(key)
  }
  return keys
}

// --- Micro-batching for addTabEvents ---
// Instead of updating zustand state on every SSE event (10-50/sec),
// buffer events and flush at most every BATCH_INTERVAL_MS.
// This reduces render cascades (ChatArea → EventHierarchy) by ~10-50x.
const BATCH_INTERVAL_MS = 200 // 200ms for the active session — keeps UI responsive
const BACKGROUND_BATCH_INTERVAL_MS = 1000 // 1s for background sessions — only badge count needs updating
const _eventBatchBuffers = new Map<string, PollingEvent[]>()
const _eventBatchTimers = new Map<string, ReturnType<typeof setTimeout>>()

function _flushEventBatch(sessionId: string) {
  _eventBatchTimers.delete(sessionId)
  const buffer = _eventBatchBuffers.get(sessionId)
  if (!buffer || buffer.length === 0) return
  _eventBatchBuffers.delete(sessionId)
  useChatStore.getState()._addTabEventsImmediate(sessionId, buffer)
}

function clearPendingEventBatch(sessionId: string) {
  const timer = _eventBatchTimers.get(sessionId)
  if (timer) clearTimeout(timer)
  _eventBatchTimers.delete(sessionId)
  _eventBatchBuffers.delete(sessionId)
}

function addTabEventsBatched(sessionId: string, events: PollingEvent[]) {
  // Check if any event is "important" (completion, error, human feedback) — flush immediately
  const hasImportant = events.some(e => {
    const t = e.type
    return t === 'unified_completion' || t === 'conversation_end' ||
      t === 'agent_error' || t === 'conversation_error' ||
      t === 'orchestrator_agent_error' || t === 'workflow_error' ||
      t === 'request_human_feedback' || t === 'blocking_human_feedback' ||
      t === 'plan_approval' || t === 'pre_validation_completed' ||
      t === 'batch_execution_canceled' || t === 'context_cancelled' ||
      t === 'orchestrator_end' || t === 'agent_end' ||
      t === 'user_message' || t === 'conversation_resumed'
  })

  const existing = _eventBatchBuffers.get(sessionId)
  if (existing) {
    existing.push(...events)
  } else {
    _eventBatchBuffers.set(sessionId, [...events])
  }

  if (hasImportant) {
    // Flush immediately for important events regardless of active/background
    const timer = _eventBatchTimers.get(sessionId)
    if (timer) clearTimeout(timer)
    _flushEventBatch(sessionId)
  } else if (!_eventBatchTimers.has(sessionId)) {
    // Use slower flush for background sessions — they only need badge count updates
    const state = useChatStore.getState()
    const activeSessionId = state.activeTabId ? state.chatTabs[state.activeTabId]?.sessionId : null
    const interval = sessionId === activeSessionId ? BATCH_INTERVAL_MS : BACKGROUND_BATCH_INTERVAL_MS
    _eventBatchTimers.set(sessionId, setTimeout(() => _flushEventBatch(sessionId), interval))
  }
}

// Helper function to identify important events that should always be retained
// These events provide critical context and should not be removed during cleanup
const shouldRetainEvent = (event: PollingEvent): boolean => {
  if (!event.type) return false

  const importantTypes = [
    // Error events - always keep for debugging
    'agent_error',
    'conversation_error',
    'orchestrator_agent_error',
    'workflow_error',
    // Completion/end events - always keep
    'unified_completion',
    'conversation_end',
    'context_cancelled',
    'orchestrator_end',
    'agent_end',
    // Start events - keep for context
    'conversation_start',
    // Human feedback events - critical for workflow
    'request_human_feedback',
    'blocking_human_feedback',
    'plan_approval',
    // User input - keep for conversation context
    'user_message',
    // Tool events - keep for understanding what happened
    'tool_call',
    'tool_result',
    // LLM output - keep final generation results
    'llm_generation_end',
    // Workflow execution events
    'pre_validation_completed',
    'routing_evaluated',
    'batch_execution_canceled',
    'todo_task_route_selected',
    'todo_task_step_completed',
    'learn_code_script_execution',
    // Delegation structural events - must survive for sub-agent cards
    'delegation_start',
    'delegation_end',
    // Orchestrator agent boundaries - must survive for step collapse/expand
    'orchestrator_agent_start',
    'orchestrator_agent_end',
    // Background owners - required by tree event continuity
    'background_agent_started',
    'background_agent_completed',
    'background_agent_terminated',
    'coding_agent_background_task',
    'coding_agent_question'
  ]
  return importantTypes.includes(event.type)
}

// Tab session status type
export interface TabSessionStatus {
  status: string | null
  agentMode: string | null
  lastActivity: string | null
}

// Pasted text can become an attachment chip rather than inline text.
// Stored per-tab; serialized into the outgoing message as fenced blocks.
export interface PastedAttachment {
  id: string
  marker?: string
  content: string
  chars: number
  lines: number
  createdAt: number
}

// Tab-specific configuration (all settings that should be per-tab)
export interface ChatTabConfig {
  conversationAcknowledged?: boolean
  composerRevision?: number  // Store-owned revision survives composer remounts
  inputText: string  // Chat input text
  useCodeExecutionMode: boolean  // Code execution mode toggle
  selectedServers: string[]  // Selected MCP servers
  selectedSkills: string[]  // Selected skills to include in chat
  selectedSecrets: string[]  // Selected secret IDs to inject into chat
  llmConfig: ExtendedLLMConfiguration  // LLM configuration (provider, model, etc.)
  fileContext: FileContextItem[]  // Files/folders in context
  browserMode?: 'none' | 'auto' | 'headless' | 'cdp'  // Browser access mode (default: 'auto')
  enableBrowserAccess?: boolean  // Enable/disable browser automation tool (auto-enables workspace when true)
  useCdp?: boolean  // Whether CDP mode is enabled (connect to local Chrome)
  cdpPort?: number  // CDP port (default 9222)
  delegationTierConfig?: DelegationTierConfig  // Per-tab delegation tier config (multi-agent mode)
  workflowContext: Array<{
    presetId: string
    label: string
    workspacePath: string
  }>  // Workflow presets selected via # in chat input
  restoredConversationPath?: string  // Durable conversation file for UI-restored tabs
  restoredConversationSummary?: string  // Compact recent context from a restored workflow-builder conversation
  restoredConversationTitle?: string  // Display title for pending resumed conversation
  restoredConversationWorkshopModeLabel?: string  // Builder/Optimizer/Run label for pending resumed workflow conversation
  restoredConversationRuntimeLabel?: string  // Provider/model display for pending resumed conversation
  restoredConversationNativeResume?: boolean  // Whether restore should use the CLI path instead of visible file-context fallback
  queuedMessages: string[]  // Queue of messages to send one by one when chat completes
  pastedAttachments?: PastedAttachment[]  // Long pastes captured as attachment chips, prepended on send
  pendingSubmission?: { id: string; message: string; sessionId: string; targetTabId?: string; status?: 'pending' | 'uncertain' }
  queuedSubmission?: { id: string; messages: string[]; sessionId: string; queueIndex?: number; queuePrefix?: string[]; accepted?: boolean }
  queueError?: string
  isQueueProcessing?: boolean  // Lock to prevent multiple ChatArea instances from double-processing the queue
  autoRun?: boolean  // Automatically run the chat when tab is loaded
}

const stripRestoreOnlyTabConfig = (config: ChatTabConfig): ChatTabConfig => {
  const nextConfig: ChatTabConfig = { ...config }
  delete nextConfig.conversationAcknowledged
  delete nextConfig.pendingSubmission
  delete nextConfig.queuedSubmission
  delete nextConfig.queueError
  nextConfig.queuedMessages = []
  nextConfig.isQueueProcessing = false
  const restoredPath = nextConfig.restoredConversationPath?.trim()
  if (restoredPath) {
    nextConfig.fileContext = (nextConfig.fileContext || []).filter(item => item.path !== restoredPath)
  }
  delete nextConfig.restoredConversationPath
  delete nextConfig.restoredConversationSummary
  delete nextConfig.restoredConversationTitle
  delete nextConfig.restoredConversationWorkshopModeLabel
  delete nextConfig.restoredConversationRuntimeLabel
  delete nextConfig.restoredConversationNativeResume
  return nextConfig
}

const persistDurableTabConfig = (config: ChatTabConfig): ChatTabConfig => ({ ...config, isQueueProcessing: false })

// Generalized ChatTab interface (works for multi-agent and workflow modes)
export interface ChatTab {
  tabId: string  // Unique ID: `chat_${timestamp}` or `phase_${phaseId}_${timestamp}`
  name: string  // Display name (e.g., "Chat 1", "Planning", "Execution")
  sessionId: string | null  // Chat session ID if exists
  isStreaming: boolean  // Whether this tab's execution is currently running
  lastStreamingStartedAt?: number  // Timestamp for the current/last foreground turn
  isCompleted: boolean  // Whether this tab's execution has completed
  hasUnreadCompletion?: boolean  // Finished while another tab was selected; cleared when opened
  hasRunningBgAgents: boolean  // Whether background agents are still running for this session
  isSyntheticTurn: boolean  // Whether current running turn is an auto-notification that should not queue user input
  canSteer: boolean  // Whether the backend currently has a live agent that can accept steer injection
  hideToolCalls: boolean  // Whether to hide tool_call_start/end events in this tab
  viewMode: EventViewMode
  config: ChatTabConfig  // Tab-specific configuration
  createdAt: number  // Timestamp for ordering
  lastAccessedAt?: number  // Timestamp for MRU switchers
  lastViewedEventCount: number  // @deprecated - use lastViewedEventCounts instead
  lastViewedEventCounts: PerModeEventCounts  // Per-mode event counts for accurate badge calculation
  // Mode-specific metadata
  metadata?: {
    phaseId?: string  // For workflow mode: phase ID
    phaseName?: string  // For workflow mode: phase name
    workshopMode?: 'workshop' | 'run' // Prompt/tool policy pinned to this workflow chat
    mode?: 'workflow' | 'multi-agent'  // Which mode this tab belongs to
    presetQueryId?: string  // For workflow mode: preset query ID (workflow identifier)
    isOrganizationAssistant?: boolean // True when tab is reserved for Organization panel
    isRestored?: boolean  // True when restored from history (inline history panel or page refresh)
    isRestoringSession?: boolean  // True while session events are being loaded from backend
    isViewOnly?: boolean // True when tab is in view-only mode (e.g. shared session or bot connector)
    isExecutionRun?: boolean // Execution diagnostics use JSON history, not the interactive chat journal.
    isScheduledRun?: boolean // True when tab is observing a scheduled-run session (read-only live view)
    scheduledJobName?: string // Display name of the scheduled job, surfaced in the view-only banner
    isBotRun?: boolean // True when tab is observing a bot-triggered session (read-only live view)
    botPlatform?: string // Display label for the bot platform, e.g. Slack or WhatsApp
    readOnlyRestoredAt?: number // Timestamp for an explicit user-opened Schedule/Bot restore
    // Generic product-agent binding. These fields are durable so a project can
    // recover its normal AgentWorks session after a refresh without maintaining
    // a second product-specific conversation store.
    agentProfileId?: string
    agentProfileVersion?: number
    agentProfileWorkspace?: string
    agentProfileProjectId?: string
    agentProfileProjectTitle?: string
    agentProfileProjectIcon?: string
    agentProfileIdentityName?: string
    // Permanent product home tab. Sending from it creates a separate chat,
    // matching AgentWorks' Builder -> Chat tab workflow.
    agentProfileBuilder?: boolean
    agentProfileWorkspaceDescription?: string
    // Stable product-domain identity used by the server conversation registry.
    // Singleton products omit it; keyed products use a project/resource id.
    agentProfileConversationKey?: string
    // Server-issued logical conversation identity. This remains stable even
    // if a future runtime migration rotates the underlying agent session.
    agentProfileConversationId?: string
    // Opts a product into the narrow /agent-profiles/{id}/query contract.
    // Kept on the product-owned tab so ChatArea remains product-agnostic.
    agentProfileChatContract?: 'profile-v1'
    // The product user's chosen coding-agent runtime, as one of the profile's
    // declared provider_options ids. Sent as `engine` on every profile query.
    agentProfileConnectionID?: string
    agentProfileEngine?: string
    // A model within that engine's provider (the composer's switcher). Sent as
    // `model_id` on every profile query; empty keeps the option's own model.
    agentProfileModelID?: string
    // A reasoning level from the engine's declared reasoning_efforts (the
    // composer's ReasoningEffortControl). Sent as `reasoning_effort` on every
    // profile query; empty keeps the engine's own default.
    agentProfileReasoningEffort?: string
    // Product-owned MCP selection is isolated from the old global chat
    // default. Existing tabs without this marker are migrated to no servers;
    // right-side Setup marks the project selection explicit.
    agentProfileMCPSelectionInitialized?: boolean
    // A project-level runtime selection changed after this native session was
    // launched. The next message must take the full query path once so the
    // shared runner can relaunch the CLI with the saved provider/model.
    agentProfileRuntimeDirty?: boolean
    userInteractiveContinuation?: boolean // Observed run promoted to an interactive chat without changing session ID
  }
}

// Helper function to get default tab config from current global state
// Uses mode-specific configs for LLM and server selections
const getDefaultTabConfig = (mode: 'workflow' | 'multi-agent' = 'multi-agent'): ChatTabConfig => {
  const mcpStore = useMCPStore?.getState?.()
  const llmStore = useLLMStore?.getState?.()
  const appStore = useAppStore?.getState?.()

  // Get mode-specific server selection (multi-agent uses chat settings)
  const isWorkflowMode = mode === 'workflow'

  const selectedServers = isWorkflowMode
    ? (mcpStore?.workflowSelectedServers || [])
    : (mcpStore?.chatSelectedServers || [])

  // Get mode-specific LLM config (multi-agent uses chat settings)
  const llmConfig = isWorkflowMode
    ? (llmStore?.workflowPrimaryConfig || llmStore?.primaryConfig)
    : (llmStore?.chatPrimaryConfig || llmStore?.primaryConfig)

  return {
    inputText: '',
    // Default to false (simple mode) - user can toggle to true (code exec mode) via ChatInput
    useCodeExecutionMode: false,
    selectedServers,
    selectedSecrets: [],
    workflowContext: [],
    llmConfig: llmConfig || {
      provider: 'codex-cli',
      model_id: 'codex-cli',
    },
    // CRITICAL: Don't copy global chatFileContext - chat tabs should have independent file context
    // Workflow mode uses global chatFileContext, but chat mode uses tab-specific fileContext
    fileContext: [],
    browserMode: appStore?.lastBrowserMode ?? 'auto',
    enableBrowserAccess: ['auto', 'headless', 'cdp'].includes(appStore?.lastBrowserMode ?? 'auto'),
    selectedSkills: appStore?.lastSelectedSkills ?? [],
    delegationTierConfig: undefined,
    queuedMessages: [],
    pastedAttachments: [],
    autoRun: false,
  }
}

// Helper function to cleanup old events while retaining important ones
const cleanupOldEvents = (events: PollingEvent[]): PollingEvent[] => {
  if (events.length <= MAX_EVENTS) return events
  
  // Separate important and regular events
  const important = events.filter(shouldRetainEvent)
  const regular = events.filter(e => !shouldRetainEvent(e))
  
  // Trim important events if they exceed MAX_EVENTS
  let trimmedImportant = important
  if (important.length > MAX_EVENTS) {
    // Keep only the newest MAX_EVENTS important events
    trimmedImportant = important
      .sort(compareEventsReverseChronologically)
      .slice(0, MAX_EVENTS)
  }
  
  // Calculate budget for regular events (clamped to 0)
  const budget = Math.max(0, MAX_EVENTS - trimmedImportant.length)
  
  // Keep latest regular events within budget
  const keepRegular = budget > 0 ? regular.slice(-budget) : []
  
  // Combine and sort by timestamp
  return trimLargeRetainedEvents([...trimmedImportant, ...keepRegular].sort(compareEventsChronologically))
}

export interface ChatState extends StoreActions {
  // Chat streaming state
  isStreaming: boolean
  lastEventIndex: number
  pollingInterval: NodeJS.Timeout | null
  
  // Event tracking removed - using tabEvents only (keyed by sessionId)
  // Per-tab event storage (keyed by session ID)
  tabEvents: Record<string, PollingEvent[]>  // sessionId -> events
  tabEventIndices: Record<string, number>  // sessionId -> lastEventIndex
  tabHasMoreOlderEvents: Record<string, boolean>  // sessionId -> hasMoreOlderEvents (from initial fetch)
  // Cursor for durable conversation-history paging. The live event stream has
  // its own index; this cursor only applies to the formatted transcript's
  // older, persisted conversation pages.
  tabHistoryPagination: Record<string, { hasMore: boolean; nextOffset: number }>
  // Stash for the latest human message around Terminal view mode. This
  // remains as a fallback for optimistic input while Tree catches up from
  // poll-based backfill.
  pinnedHumanByTerminalMode: Record<string, PollingEvent>
  
  // User message state
  currentUserMessage: string
  showUserMessage: boolean
  
  // Session state
  sessionId: string | null
  hasActiveChat: boolean
  
  // Chat UI state
  autoScroll: boolean
  eventViewModePreference: EventViewMode
  terminalCenterOpen: boolean
  
  // Response state
  finalResponse: string
  isCompleted: boolean
  
  // Loading states
  isLoadingHistory: boolean
  isApprovingWorkflow: boolean
  // Restore ownership is session-scoped. A single global boolean made an
  // unrelated workflow switch replace the active chat with a restore screen.
  // Counts make overlapping hydrations for the same session race-safe.
  restoringWorkflowSessions: Record<string, number>
  
  // Session management
  sessionState: 'loading' | 'active' | 'completed' | 'not_found' | 'error'
  isCheckingActiveSessions: boolean
  
  // Workflow execution state (not preset management)
  currentWorkflowPhase: WorkflowPhase
  currentWorkflowQueryId: string | null
  
  // Toast notifications
  toasts: Array<{ id: string; message: string; type: 'success' | 'info' | 'error' | 'warning' }>
  
  // Multi-tab chat state (generalized for both chat and workflow modes)
  chatTabs: Record<string, ChatTab>  // tabId -> tab
  activeTabId: string | null  // Currently selected tab
  
  // Tab session status (fetched from backend)
  tabSessionStatus: Record<string, TabSessionStatus>  // tabId -> status
  
  // Active sessions cache (shared across all components)
  activeSessionsCache: ActiveSessionInfo[]
  activeSessionsCacheTimestamp: number | null
  activeSessionsPollingInterval: NodeJS.Timeout | null
  // Workflow schedule header counts, refreshed as a side effect of
  // getActiveSessions() (both come from the combined /api/header-summary poll)
  workflowScheduleSummary: WorkflowScheduleSummary | null

  // Streaming text accumulation (per session)
  // Only tracks parent agent streaming - sub-agent streaming routed to delegationStreamingText
  streamingText: Record<string, string>  // sessionId → accumulated streaming text (response content only)
  streamingStatus: Record<string, string>  // sessionId → latest status/heartbeat message (⏳/⚠️ messages)
  streamingTerminalText: Record<string, string>  // sessionId → latest live terminal/screen snapshot
  streamingTerminalActive: Record<string, boolean>  // sessionId → true while live terminal snapshots are arriving
  terminalOutputOpen: Record<string, boolean>  // sessionId → user-controlled terminal panel open state
  lastStreamingChunkIndex: Record<string, number>  // sessionId → last processed chunk_index (dedup guard)
  lastStreamingTerminalChunkIndex: Record<string, number>  // sessionId → last terminal snapshot chunk_index
  completedStreamingText: Record<string, string>  // sessionId → preserved streaming text after generation completes

  // Sub-agent streaming text accumulation (per delegation)
  delegationStreamingText: Record<string, string>  // delegationId → accumulated streaming text
  lastDelegationChunkIndex: Record<string, number>  // delegationId → last processed chunk_index (dedup guard)

  // Workflow/background execution streaming text accumulation (per execution)
  executionStreaming: Record<string, ExecutionStreamingActivity> // executionId → live streaming state

  // Actions
  setIsStreaming: (streaming: boolean) => void
  // Computed: Derive isStreaming from polling status
  // Use this selector: useChatStore(state => state.getIsStreaming())
  getIsStreaming: () => boolean
  setLastEventIndex: (index: number) => void
  setPollingInterval: (interval: NodeJS.Timeout | null) => void
  
  // Polling management actions
  startPolling: (onPoll: () => Promise<void>) => void
  stopPolling: () => void
  updatePollingState: () => void  // Auto-start/stop based on active sessions
  
  // Event actions removed - using tabEvents only
      // Per-tab event actions (now keyed by sessionId instead of observerId)
      getTabEvents: (sessionId: string) => PollingEvent[]
      addTabEvent: (sessionId: string, event: PollingEvent) => void
      addTabEvents: (sessionId: string, events: PollingEvent[]) => void
      _addTabEventsImmediate: (sessionId: string, events: PollingEvent[]) => void
      setTabEvents: (sessionId: string, events: PollingEvent[]) => void
      patchTabEvents: (sessionId: string, patch: (events: PollingEvent[]) => PollingEvent[]) => void
      clearTabEvents: (sessionId: string) => void
      cleanupTabEvents: (sessionId: string, keepCount: number) => void
      cleanupOrphanedTabEvents: () => void
      getTabLastEventIndex: (sessionId: string) => number
      setTabLastEventIndex: (sessionId: string, index: number) => void
      getTabHasMoreOlderEvents: (sessionId: string) => boolean
      setTabHasMoreOlderEvents: (sessionId: string, hasMore: boolean) => void
      getTabHistoryPagination: (sessionId: string) => { hasMore: boolean; nextOffset: number } | undefined
      setTabHistoryPagination: (sessionId: string, pagination: { hasMore: boolean; nextOffset: number } | null) => void
  
  // User message actions
  setCurrentUserMessage: (message: string) => void
  setShowUserMessage: (show: boolean) => void
  
  // Session actions
  setSessionId: (id: string | null) => void
  setHasActiveChat: (active: boolean) => void
  
  // UI actions
  setAutoScroll: (autoScroll: boolean) => void
  setEventViewModePreference: (viewMode: EventViewMode) => void
  setTerminalCenterOpen: (open: boolean) => void
  toggleTerminalCenterOpen: () => void
  
  // Response actions
  setFinalResponse: (response: string) => void
  setIsCompleted: (completed: boolean) => void
  
  // Loading actions
  setIsLoadingHistory: (loading: boolean) => void
  setIsApprovingWorkflow: (loading: boolean) => void
  beginWorkflowSessionRestore: (sessionId: string) => void
  endWorkflowSessionRestore: (sessionId: string) => void
  
  // Session management actions
  setSessionState: (state: 'loading' | 'active' | 'completed' | 'not_found' | 'error') => void
  setIsCheckingActiveSessions: (checking: boolean) => void
  
  // Workflow execution actions
  setCurrentWorkflowPhase: (phase: WorkflowPhase) => void
  setCurrentWorkflowQueryId: (id: string | null) => void
  
  // Toast actions
  addToast: (message: string, type: 'success' | 'info' | 'error' | 'warning') => void
  removeToast: (id: string) => void
  clearToasts: () => void
  
  // Tab management actions
  createChatTab: (name: string, metadata?: ChatTab['metadata'], existingObserverId?: string) => Promise<string>  // Returns tabId
  switchTab: (tabId: string) => void
  closeTab: (tabId: string, stopSession?: boolean, keepEvents?: boolean) => Promise<void>
  getTab: (tabId: string) => ChatTab | undefined
  getActiveTab: () => ChatTab | undefined
  getTabsByMode: (mode: 'multi-agent' | 'workflow') => ChatTab[]
  getTabsByPhaseId: (phaseId: string, presetQueryId?: string) => ChatTab[]  // Find workflow tabs by phaseId (optionally scoped to preset)
  setTabStreaming: (tabId: string, isStreaming: boolean) => void
  setTabCompleted: (tabId: string, isCompleted: boolean) => void
  setTabHasRunningBgAgents: (tabId: string, hasRunningBgAgents: boolean) => void
  setTabSyntheticTurn: (tabId: string, isSyntheticTurn: boolean) => void
  setTabCanSteer: (tabId: string, canSteer: boolean) => void
  updateTabSessionId: (tabId: string, sessionId: string) => void
  renameTab: (tabId: string, name: string) => void
  setTabHideToolCalls: (tabId: string, hideToolCalls: boolean) => void
  setTabViewMode: (tabId: string, viewMode: EventViewMode) => void
  getTabConfig: (tabId: string) => ChatTabConfig | undefined
  setTabConfig: (tabId: string, configUpdate: Partial<ChatTabConfig>) => void
  setTabMetadata: (tabId: string, metadataUpdate: Partial<NonNullable<ChatTab['metadata']>>) => void
  getTabStreamingStatus: (tabId: string) => boolean
  checkTabCompletion: (tabId: string, events: Array<{ type: string }>) => boolean
  
  // Tab session status actions
  fetchTabSessionStatus: (tabId: string) => Promise<void>
  fetchAllTabSessionStatuses: (tabIds: string[]) => Promise<void>
  getTabSessionStatus: (tabId: string) => TabSessionStatus | undefined
  
  // Active sessions cache actions
  getActiveSessions: (forceRefresh?: boolean) => Promise<ActiveSessionInfo[]>
  getActiveSessionIds: () => Set<string>
  startActiveSessionsPolling: () => void
  stopActiveSessionsPolling: () => void
  
  // Streaming text actions
  appendStreamingChunk: (sessionId: string, chunkIndex: number, chunk: string, meta?: StreamingChunkMeta) => void
  setStreamingTerminalSnapshot: (sessionId: string, chunkIndex: number, chunk: string) => void
  setStreamingTerminalActive: (sessionId: string, active: boolean) => void
  setTerminalOutputOpen: (sessionId: string, open: boolean) => void
  toggleTerminalOutputOpen: (sessionId: string) => void
  clearStreamingText: (sessionId: string) => void
  clearStreamingTerminal: (sessionId: string) => void
  clearStreamingStatus: (sessionId: string) => void

  // Delegation streaming text actions
  appendDelegationStreamingChunk: (delegationId: string, chunkIndex: number, chunk: string) => void
  clearDelegationStreamingText: (delegationId: string) => void

  // Workflow/background execution streaming text actions
  appendExecutionStreamingChunk: (sessionId: string, executionId: string, chunkIndex: number, chunk: string) => void
  clearExecutionStreamingText: (executionId: string) => void
  clearExecutionStreamingStatus: (executionId: string) => void

  // SSE connection management
  sseConnections: Record<string, SSEConnection>  // sessionId -> SSEConnection
  connectSSE: (sessionId: string, onMessage: (msg: SSEEventMessage) => void, onStatus: (msg: SSEStatusMessage) => void, onError?: () => void, durableChat?: boolean) => void
  disconnectSSE: (sessionId: string) => void
  disconnectAllSSE: () => void

  // Helper methods
  resetTabChat: (tabId: string, nextSessionId?: string) => void
  resetChatState: () => void
  // Clears browser-local state when the authenticated account changes. This
  // deliberately does not stop backend sessions, which may belong to the
  // account that just signed out.
  discardChatStateForAccountChange: () => void
  isAtBottom: (element: HTMLDivElement) => boolean
}

type DurableChatState = Pick<ChatState, 'chatTabs' | 'activeTabId' | 'eventViewModePreference'>

let previousDurableChatTabs: ChatState['chatTabs'] | null = null
let previousDurableActiveTabId: string | null | undefined
let previousDurableViewMode: EventViewMode | undefined
let previousDurableChatState: DurableChatState | null = null

/**
 * Keeps volatile stream/event updates out of persistence. Zustand invokes
 * partialize for every set, so unchanged durable input must return the exact
 * same object for the buffered adapter to skip serialization entirely.
 */
const selectDurableChatState = (state: ChatState): DurableChatState => {
  const normalizedViewMode = normalizeEventViewMode(state.eventViewModePreference)
  if (
    previousDurableChatState &&
    state.chatTabs === previousDurableChatTabs &&
    state.activeTabId === previousDurableActiveTabId &&
    normalizedViewMode === previousDurableViewMode
  ) {
    return previousDurableChatState
  }

  const chatTabs = Object.fromEntries(
    Object.entries(state.chatTabs)
      .filter(([, tab]) => {
        // Explicit product decision: a tab, once opened, is closed only by the
        // user -- including a view-only Schedule lane whose run has finished,
        // and (as of the workflow-builder tab simplification) a builder tab
        // too, no matter how old it is. This snapshot forces isStreaming
        // false, so a restored tab looks dead (not live) on reload -- that's
        // accepted, not a bug; the tab still exists as a closable record
        // until the user closes it. See shouldDisplayWorkflowTab in
        // workflowRuntimeTabProjection.ts for the matching live-display side
        // of this decision.
        return tab.metadata?.mode === 'workflow' || tab.metadata?.mode === 'multi-agent'
      })
      .map(([tabId, tab]) => [
        tabId,
        {
          tabId: tab.tabId,
          name: tab.name,
          sessionId: tab.sessionId,
          isStreaming: false,
          isCompleted: false,
          hasUnreadCompletion: false,
          hasRunningBgAgents: false,
          isSyntheticTurn: false,
          canSteer: false,
          hideToolCalls: tab.hideToolCalls ?? true,
          viewMode: normalizeEventViewMode(tab.viewMode),
          config: persistDurableTabConfig(tab.config),
          createdAt: tab.createdAt,
          lastAccessedAt: tab.lastAccessedAt || tab.createdAt,
          lastViewedEventCount: 0,
          lastViewedEventCounts: { micro: 0 },
          metadata: tab.metadata,
        },
      ]),
  )
  const activeTab = state.activeTabId ? state.chatTabs[state.activeTabId] : null
  const activeTabId = activeTab?.metadata?.mode === 'workflow' || activeTab?.metadata?.mode === 'multi-agent'
    ? state.activeTabId
    : null

  previousDurableChatTabs = state.chatTabs
  previousDurableActiveTabId = state.activeTabId
  previousDurableViewMode = normalizedViewMode
  previousDurableChatState = {
    chatTabs,
    activeTabId,
    eventViewModePreference: normalizedViewMode,
  }
  return previousDurableChatState
}

const legacyPersistedOwner = readPersistedChatOwner()
const chatPersistStorage = (() => {
  try {
    return typeof globalThis.localStorage === 'undefined'
      ? undefined
      : createBufferedPersistStorage<DurableChatState>(globalThis.localStorage)
  } catch (error) {
    console.error('[ChatStore] Browser storage is unavailable', error)
    return undefined
  }
})()

// Exported for direct unit testing — this reconciliation is easier to verify
// as a pure function than through the full getActiveSessions() network path.
export const reconcileSessionTabStreamingState = (
  state: ChatState,
  activeSessions: ActiveSessionInfo[],
  now: number
): Partial<ChatState> => {
  const activeSessionById = new Map(activeSessions.map(session => [session.session_id, session]))
  let nextChatTabs: Record<string, ChatTab> | null = null
  let nextTabSessionStatus: Record<string, TabSessionStatus> | null = null
  let clearedCount = 0
  let healedCount = 0

  for (const [tabId, tab] of Object.entries(state.chatTabs)) {
    if (!tab.sessionId) continue
    const activeSession = activeSessionById.get(tab.sessionId)
    const activity = activeSession ? sessionStreamingState(activeSession) : undefined

    // Recover externally started work or a dead SSE stream using the same
    // foreground/background decision as event polling. Raw status=running
    // alone can describe an idle retained CLI or background-only work; forcing
    // foreground streaming then makes each SSE status tick undo this recovery.
    if (
      activity && (activity.isStreaming || activity.hasRunningBgAgents) &&
      (tab.isStreaming !== activity.isStreaming || tab.hasRunningBgAgents !== activity.hasRunningBgAgents)
    ) {
      if (!nextChatTabs) nextChatTabs = { ...state.chatTabs }
      nextChatTabs[tabId] = {
        ...tab,
        isStreaming: activity.isStreaming,
        isCompleted: false,
        lastStreamingStartedAt: activity.isStreaming ? (tab.lastStreamingStartedAt ?? now) : undefined,
        hasRunningBgAgents: activity.hasRunningBgAgents,
        isSyntheticTurn: activity.isSyntheticTurn,
        canSteer: activity.canSteer,
      }
      healedCount += 1
      continue
    }

    if (activity?.isActive) continue
    if (!tab.isStreaming && !tab.hasRunningBgAgents && !tab.canSteer && !tab.isSyntheticTurn) continue

    const streamingAge = tab.lastStreamingStartedAt ? now - tab.lastStreamingStartedAt : Number.POSITIVE_INFINITY
    if (tab.isStreaming && streamingAge < STALE_STREAMING_GRACE_MS) continue

    if (!nextChatTabs) nextChatTabs = { ...state.chatTabs }
    nextChatTabs[tabId] = {
      ...tab,
      isStreaming: false,
      lastStreamingStartedAt: undefined,
      hasRunningBgAgents: false,
      isSyntheticTurn: false,
      canSteer: false,
    }

    if (state.tabSessionStatus[tabId]) {
      if (!nextTabSessionStatus) nextTabSessionStatus = { ...state.tabSessionStatus }
      nextTabSessionStatus[tabId] = {
        status: null,
        agentMode: null,
        lastActivity: null,
      }
    }

    clearedCount += 1
  }

  if (clearedCount > 0) {
    logger.debug('SessionStore', `Cleared stale streaming state for ${clearedCount} tab(s) with no active backend session`)
  }
  if (healedCount > 0) {
    logger.debug('SessionStore', `Recovered activity for ${healedCount} tab(s) to match a backend-active session (dead SSE / externally-triggered turn recovery)`)
  }

  return {
    ...(nextChatTabs ? { chatTabs: nextChatTabs } : {}),
    ...(nextTabSessionStatus ? { tabSessionStatus: nextTabSessionStatus } : {}),
  }
}

export const useChatStore = create<ChatState>()(
    persist(
      (set, get) => ({
      // Initial state
      isStreaming: false,
      lastEventIndex: -1,
      pinnedHumanByTerminalMode: {},
      pollingInterval: null,
      tabEvents: {},
      tabEventIndices: {},
      tabHasMoreOlderEvents: {},
      tabHistoryPagination: {},
      currentUserMessage: '',
      showUserMessage: true,
      sessionId: null,
      hasActiveChat: false,
      autoScroll: true,
    eventViewModePreference: 'formatted',
      terminalCenterOpen: false,
      finalResponse: '',
      isCompleted: false,
      isLoadingHistory: false,
      isApprovingWorkflow: false,
      restoringWorkflowSessions: {},
      sessionState: 'loading',
      isCheckingActiveSessions: false,
      currentWorkflowPhase: 'planning' as WorkflowPhase,
      currentWorkflowQueryId: null,
      toasts: [],
      chatTabs: {},
      activeTabId: null,
      tabSessionStatus: {},
      
      // Active sessions cache (shared across all components)
      activeSessionsCache: [],
      activeSessionsCacheTimestamp: null,
      activeSessionsPollingInterval: null,
      workflowScheduleSummary: null,
      
      // Streaming text accumulation (per session)
      streamingText: {},
      streamingStatus: {},
      streamingTerminalText: {},
      streamingTerminalActive: {},
      terminalOutputOpen: {},
      lastStreamingChunkIndex: {},
      lastStreamingTerminalChunkIndex: {},
      completedStreamingText: {},

      // Sub-agent streaming text accumulation (per delegation)
      delegationStreamingText: {},
      lastDelegationChunkIndex: {},

      // Workflow/background execution streaming text accumulation (per execution)
      executionStreaming: {},

      // SSE connections (not persisted)
      sseConnections: {},

      // Actions
      setIsStreaming: (streaming) => {
        set({ isStreaming: streaming })
      },
      
      // Computed getter for isStreaming (derived from polling status)
      // This ensures isStreaming reflects actual polling state
      getIsStreaming: () => {
        const state = get()
        // Derive from polling status: if polling is active and not completed, we're streaming
        // Exception: if manually set to false (e.g., human feedback pause), respect that
        return state.pollingInterval !== null && !state.isCompleted && state.isStreaming !== false
      },

      setLastEventIndex: (index) => {
        set({ lastEventIndex: index })
      },

      setPollingInterval: (interval) => {
        set({ pollingInterval: interval })
        // Auto-update isStreaming based on polling status
        const state = get()
        const derivedStreaming = interval !== null && !state.isCompleted
        if (state.isStreaming !== derivedStreaming && state.isStreaming !== false) {
          // Only auto-update if not manually paused (isStreaming === false means paused)
          set({ isStreaming: derivedStreaming })
        }
      },
      
      // Start polling with a callback function
      startPolling: (onPoll) => {
        const state = get()
        // Don't start if already polling
        if (state.pollingInterval) {
          logger.debug('ChatStore', 'Polling already active, skipping start')
          return
        }
        
        logger.debug('ChatStore', 'Starting polling interval')
        const interval = setInterval(() => {
          onPoll().catch(error => {
            logger.error('ChatStore', 'Error in polling callback:', error)
          })
        }, 500)  // 500ms for streaming responsiveness
        
        set({ pollingInterval: interval })
      },
      
      // Stop polling
      stopPolling: () => {
        const state = get()
        if (state.pollingInterval) {
          logger.debug('ChatStore', 'Stopping polling interval')
          clearInterval(state.pollingInterval)
          set({ pollingInterval: null })
        }
      },
      
      // Update polling state based on active (streaming) sessions
      // This should be called when tab streaming status changes
      updatePollingState: () => {
        const state = get()
        const activeTabs = Object.values(state.chatTabs).filter(tab => tab.isStreaming)
        
        // If there are active sessions and no polling, start it
        if (activeTabs.length > 0 && !state.pollingInterval) {
          logger.debug('ChatStore', `Found ${activeTabs.length} active session(s), but polling not started yet. Call startPolling() with your poll callback.`)
          // Note: We can't start polling here because we need the poll callback
          // The component should call startPolling with the callback
        }
        // If there are no active sessions but polling is running, stop it
        else if (activeTabs.length === 0 && state.pollingInterval) {
          logger.debug('ChatStore', 'No active sessions, stopping polling')
          state.stopPolling()
        }
      },
      

      // Event actions
      // Deprecated: setTotalEvents and setLastEventCount removed - use tabEvents instead

      // Deprecated: setEvents, addEvent, clearEvents removed - use tabEvents instead
      
      // Per-tab event actions (now keyed by sessionId)
      getTabEvents: (sessionId: string) => {
        const state = get()
        return state.tabEvents[sessionId] || []
      },
      
      addTabEvent: (sessionId: string, event: PollingEvent) => {
        set((state) => {
          if (!retainEventInSessionWorkingSet(sessionId, event)) return state
          const currentEvents = state.tabEvents[sessionId] || []
          const newEvents = [...currentEvents, trimLargeRetainedEventFields(event)]
          
          // Trigger cleanup if threshold exceeded
          let finalEvents = newEvents
          if (newEvents.length >= CLEANUP_THRESHOLD) {
            logger.debug('Memory', `Cleaning up events for session ${sessionId}: ${newEvents.length} -> ${MAX_EVENTS}`)
            finalEvents = cleanupOldEvents(newEvents)
          }
          
          return {
            tabEvents: {
              ...state.tabEvents,
              [sessionId]: finalEvents
            },
            // Deprecated: totalEvents removed
          }
        })
      },
      
      // Public API: micro-batched — buffers events and flushes every 100ms
      // to reduce render cascades from 10-50/sec to ~10/sec
      addTabEvents: (sessionId: string, events: PollingEvent[]) => {
        addTabEventsBatched(sessionId, events)
      },

      // Internal: immediate store update (called by the batch flush)
      _addTabEventsImmediate: (sessionId: string, incomingEvents: PollingEvent[]) => {
        set((state) => {
          let currentEvents = state.tabEvents[sessionId] || []

          // Use persistent event ID index (O(1) lookup instead of rebuilding Set each call)
          let idSet = tabEventIdSets.get(sessionId)
          if (!idSet) {
            // First call for this session — build from existing events
            idSet = new Set(currentEvents.map(e => e.id).filter(Boolean) as string[])
            tabEventIdSets.set(sessionId, idSet)
          }
          // A durable user_message replaces the provisional bubble with the
          // same client id instead of being dropped as a duplicate, so it lands
          // at its own position (after a reply the CLI was still writing).
          const reconciled = reconcileDurableUserEchoes(currentEvents, incomingEvents)
          if (reconciled.replacedIds.length > 0) {
            currentEvents = reconciled.current
            for (const id of reconciled.replacedIds) idSet.delete(id)
          }
          // Store narration in its rendered form so the live copy
          // (`<chunk>-update`) and the durable raw chunk share one id: the
          // durable row replaces the live one in place instead of landing
          // beside it as a second item with the same key.
          const events: PollingEvent[] = []
          const eventsBeforeNarration = currentEvents
          for (const raw of reconciled.incoming) {
            const event = intermediateUpdateFromTranscriptChunk(raw) || raw
            if (isTranscriptChunkUpdate(event) && idSet.has(event.id!)) {
              const at = currentEvents.findIndex(existing => existing.id === event.id)
              if (at >= 0 && currentEvents[at].sequence === undefined && event.sequence !== undefined) {
                currentEvents = [...currentEvents.slice(0, at), event, ...currentEvents.slice(at + 1)]
              }
              continue
            }
            events.push(event)
          }
          let autoNotifKeys = tabAutoNotificationKeys.get(sessionId)
          if (!autoNotifKeys) {
            autoNotifKeys = collectAutoNotificationKeys(currentEvents)
            tabAutoNotificationKeys.set(sessionId, autoNotifKeys)
          }

          // Filter out events that already exist
          const uniqueNewEvents = events.filter(event => {
            // An auto-notification is an idempotent status card: the same
            // steered completion can arrive as several distinct-ID events, so
            // collapse by content even when the ID index would let it through.
            const autoNotifKey = autoNotificationDedupKey(event)
            if (autoNotifKey) {
              if (autoNotifKeys!.has(autoNotifKey)) return false
              autoNotifKeys!.add(autoNotifKey)
            }
            if (!event.id) {
              logger.warn('EventStore', 'Event without ID detected:', event)
              return true
            }
            if (idSet!.has(event.id)) {
              return false
            }
            idSet!.add(event.id)
            return true
          })
            .filter(event => retainEventInSessionWorkingSet(sessionId, event))
            .map(trimLargeRetainedEventFields)

          // PERF: Skip state update entirely when no new events — avoids creating a new
          // array reference which would cascade re-renders through ChatArea → EventHierarchy.
          if (uniqueNewEvents.length === 0) {
            return currentEvents === eventsBeforeNarration
              ? state
              : { tabEvents: { ...state.tabEvents, [sessionId]: currentEvents } }
          }

          // When a new learn_code_script_execution starts (fix_iteration=0), purge all
          // previous scripted events for that step so stale ✗ failed events don't linger.
          let baseEvents = currentEvents
          for (const ev of uniqueNewEvents) {
            const scriptedData = getScriptedExecutionData(ev)
            if (scriptedData?.fix_iteration === 0) {
              const stepId = scriptedData.step_id
              console.log('[FIX_LEARN_CODE_UI] store_reset_step_history', {
                sessionId,
                eventId: ev.id,
                stepId,
                currentEventCount: currentEvents.length,
              })
              baseEvents = baseEvents.filter(
                e => getScriptedExecutionData(e)?.step_id !== stepId
              )
              // Rebuild idSet after removal so dedup stays consistent
              idSet = new Set(baseEvents.map(e => e.id).filter(Boolean) as string[])
              tabEventIdSets.set(sessionId, idSet)
            }
          }

          // Token-streamed thinking fragments (Cursor, is_delta) join the
          // thinking event before them instead of becoming one card each.
          const newEvents = coalesceThinkingDeltas([...baseEvents, ...uniqueNewEvents])

          // For scripted steps, keep only the final result event per step_id.
          // A step goes through multiple fix iterations; intermediate failures should
          // not linger once a later attempt succeeds. Keep the event with the highest
          // fix_iteration — that is always the latest (and definitive) result.
          const lcLastIterByStep = new Map<string, number>()
          for (const ev of newEvents) {
            const scriptedData = getScriptedExecutionData(ev)
            if (scriptedData) {
              const sid = scriptedData.step_id
              const iter = scriptedData.fix_iteration ?? 0
              if (sid && (lcLastIterByStep.get(sid) ?? -1) < iter) lcLastIterByStep.set(sid, iter)
            }
          }
          const deduped = newEvents.filter(ev => {
            const scriptedData = getScriptedExecutionData(ev)
            if (!scriptedData) return true
            const sid = scriptedData.step_id
            const iter = scriptedData.fix_iteration ?? 0
            if (!sid) return true
            return iter === lcLastIterByStep.get(sid)
          })

          for (const ev of uniqueNewEvents) {
            const scriptedData = getScriptedExecutionData(ev)
            if (!scriptedData) continue
            console.log('[FIX_LEARN_CODE_UI] store_add_event', {
              sessionId,
              eventId: ev.id,
              stepId: scriptedData.step_id ?? null,
              fixIteration: scriptedData.fix_iteration ?? null,
              latestFixIterationForStep: scriptedData.step_id ? (lcLastIterByStep.get(scriptedData.step_id) ?? null) : null,
              keptAfterDedup: deduped.some(e => e.id === ev.id),
              finalEventCount: deduped.length,
            })
          }
          // Trigger cleanup if threshold exceeded
          let finalEvents = deduped
          let didCleanup = false
          if (deduped.length >= CLEANUP_THRESHOLD) {
            logger.debug('Memory', `Cleaning up events for session ${sessionId}: ${deduped.length} -> ${MAX_EVENTS}`)
            finalEvents = cleanupOldEvents(deduped)
            didCleanup = true
            // Rebuild ID index after cleanup discards events
            tabEventIdSets.set(sessionId, new Set(finalEvents.map(e => e.id).filter(Boolean) as string[]))
            tabAutoNotificationKeys.set(sessionId, collectAutoNotificationKeys(finalEvents))
          }
          // Update lastViewedEventCounts for the ACTIVE tab if it owns this session.
          // Events arriving on the active tab are visible to the user in real-time,
          // so they shouldn't show as "new" in the badge.
          const updates: Partial<ChatState> = {
            tabEvents: {
              ...state.tabEvents,
              [sessionId]: finalEvents
            },
          }

          const activeTab = state.activeTabId ? state.chatTabs[state.activeTabId] : null
          if (activeTab && activeTab.sessionId === sessionId) {
            // Incremental count: only scan new events instead of the entire array.
            // Fall back to full recomputation when cleanup discards old events (counts become stale).
            const newCounts = didCleanup
              ? computePerModeCounts(finalEvents)
              : incrementPerModeCounts(activeTab.lastViewedEventCounts, uniqueNewEvents)
            updates.chatTabs = {
              ...state.chatTabs,
              [activeTab.tabId]: {
                ...activeTab,
                lastViewedEventCount: finalEvents.length,
                lastViewedEventCounts: newCounts
              }
            }
          }

          return updates
        })
      },

      setTabEvents: (sessionId: string, events: PollingEvent[]) => {
        clearPendingEventBatch(sessionId)
        const retainedEvents = trimLargeRetainedEvents(
          coalesceThinkingDeltas(normalizeTranscriptChunkEvents(events).filter(event => retainEventInSessionWorkingSet(sessionId, event))),
        )
        // Rebuild the persistent ID index for this session
        tabEventIdSets.set(sessionId, new Set(retainedEvents.map(e => e.id).filter(Boolean) as string[]))
        tabAutoNotificationKeys.set(sessionId, collectAutoNotificationKeys(retainedEvents))

        set((state) => {
          // Trigger cleanup if threshold exceeded
          let finalEvents = retainedEvents
          if (retainedEvents.length >= CLEANUP_THRESHOLD) {
            logger.debug('Memory', `Cleaning up events for session ${sessionId}: ${retainedEvents.length} -> ${MAX_EVENTS}`)
            finalEvents = cleanupOldEvents(retainedEvents)
          }

          // Also update lastViewedEventCounts for the tab owning this session
          // so restored/hydrated events don't show as "new" in the badge
          const updatedTabs = { ...state.chatTabs }
          for (const [tabId, tab] of Object.entries(updatedTabs)) {
            if (tab.sessionId === sessionId) {
              updatedTabs[tabId] = {
                ...tab,
                lastViewedEventCount: finalEvents.length,
                lastViewedEventCounts: computePerModeCounts(finalEvents)
              }
            }
          }

          return {
            tabEvents: {
              ...state.tabEvents,
              [sessionId]: finalEvents
            },
            chatTabs: updatedTabs
          }
        })
      },

      // Metadata-only upgrade of committed rows WITHOUT dropping the
      // micro-batch buffer (unlike setTabEvents, which clears it via
      // clearPendingEventBatch). Durability receipts use this so a tool
      // event buffered in the same window still flushes afterwards.
      // The patch maps 1:1 (same rows, upgraded metadata) across both
      // the committed array and any pending buffer; it never grows the
      // timeline, so no cleanup/badge bookkeeping runs here. Indexes
      // rebuild like setTabEvents since the array ref is replaced.
      patchTabEvents: (sessionId: string, patch: (events: PollingEvent[]) => PollingEvent[]) => {
        const buffered = _eventBatchBuffers.get(sessionId)
        if (buffered) _eventBatchBuffers.set(sessionId, patch(buffered))
        let patched: PollingEvent[] | undefined
        set((state) => {
          const current = state.tabEvents[sessionId]
          if (!current) return state
          patched = patch(current)
          return {
            tabEvents: {
              ...state.tabEvents,
              [sessionId]: patched
            }
          }
        })
        if (patched) {
          tabEventIdSets.set(sessionId, new Set(patched.map(e => e.id).filter(Boolean) as string[]))
          tabAutoNotificationKeys.set(sessionId, collectAutoNotificationKeys(patched))
        }
      },

      clearTabEvents: (sessionId: string) => {
        clearPendingEventBatch(sessionId)
        tabEventIdSets.delete(sessionId)
        tabAutoNotificationKeys.delete(sessionId)
        set((state) => {
          const newTabEvents = { ...state.tabEvents }
          delete newTabEvents[sessionId]
          const newTabEventIndices = { ...state.tabEventIndices }
          delete newTabEventIndices[sessionId]
          const newTabHasMoreOlderEvents = { ...state.tabHasMoreOlderEvents }
          delete newTabHasMoreOlderEvents[sessionId]
          const newTabHistoryPagination = { ...state.tabHistoryPagination }
          delete newTabHistoryPagination[sessionId]
          const newStreamingText = { ...state.streamingText }
          delete newStreamingText[sessionId]
          const newStreamingStatus = { ...state.streamingStatus }
          delete newStreamingStatus[sessionId]
          const newStreamingTerminalText = { ...state.streamingTerminalText }
          delete newStreamingTerminalText[sessionId]
          const newStreamingTerminalActive = { ...state.streamingTerminalActive }
          delete newStreamingTerminalActive[sessionId]
          const newTerminalOutputOpen = { ...state.terminalOutputOpen }
          delete newTerminalOutputOpen[sessionId]
          const newLastStreamingTerminalChunkIndex = { ...state.lastStreamingTerminalChunkIndex }
          delete newLastStreamingTerminalChunkIndex[sessionId]
          const newLastStreamingChunkIndex = { ...state.lastStreamingChunkIndex }
          delete newLastStreamingChunkIndex[sessionId]
          const newCompletedStreamingText = { ...state.completedStreamingText }
          delete newCompletedStreamingText[sessionId]
          const newExecutionStreaming = { ...state.executionStreaming }
          for (const [executionId, activity] of Object.entries(newExecutionStreaming)) {
            if (activity.sessionId === sessionId) {
              if (_executionStreamingInactivityTimers[executionId]) {
                clearTimeout(_executionStreamingInactivityTimers[executionId])
                delete _executionStreamingInactivityTimers[executionId]
              }
              delete newExecutionStreaming[executionId]
            }
          }

          return {
            tabEvents: newTabEvents,
            tabEventIndices: newTabEventIndices,
            tabHasMoreOlderEvents: newTabHasMoreOlderEvents,
            tabHistoryPagination: newTabHistoryPagination,
            streamingText: newStreamingText,
            streamingStatus: newStreamingStatus,
            streamingTerminalText: newStreamingTerminalText,
            streamingTerminalActive: newStreamingTerminalActive,
            terminalOutputOpen: newTerminalOutputOpen,
            lastStreamingChunkIndex: newLastStreamingChunkIndex,
            lastStreamingTerminalChunkIndex: newLastStreamingTerminalChunkIndex,
            completedStreamingText: newCompletedStreamingText,
            executionStreaming: newExecutionStreaming
          }
        })
      },

      cleanupTabEvents: (sessionId: string, keepCount: number) => {
        set((state) => {
          const events = state.tabEvents[sessionId]

          if (!events || events.length <= keepCount) {
            return state // No cleanup needed
          }

          // Keep only recent events and important events using the utility
          const cleaned = cleanupOldEvents(events)

          logger.debug('ChatStore', `Cleaned up events for ${sessionId}: ${events.length} -> ${cleaned.length}`)

          return {
            tabEvents: {
              ...state.tabEvents,
              [sessionId]: cleaned
            }
          }
        })
      },

      // Remove tabEvents/tabEventIndices entries for session IDs that no tab references
      // This prevents memory leaks when tabs are reused with new session IDs
      cleanupOrphanedTabEvents: () => {
        const state = get()
        const activeSessionIds = new Set(
          Object.values(state.chatTabs)
            .map(tab => tab.sessionId)
            .filter(Boolean)
        )

        let orphanCount = 0
        const newTabEvents = { ...state.tabEvents }
        const newTabEventIndices = { ...state.tabEventIndices }
        const newTabHasMore = { ...state.tabHasMoreOlderEvents }
        const newTabHistoryPagination = { ...state.tabHistoryPagination }

        for (const sessionId of Object.keys(state.tabEvents)) {
          if (!activeSessionIds.has(sessionId)) {
            delete newTabEvents[sessionId]
            delete newTabEventIndices[sessionId]
            delete newTabHasMore[sessionId]
            delete newTabHistoryPagination[sessionId]
            tabEventIdSets.delete(sessionId)
            tabAutoNotificationKeys.delete(sessionId)
            orphanCount++
          }
        }

        if (orphanCount > 0) {
          logger.debug('ChatStore', `Cleaned up ${orphanCount} orphaned tabEvent entries`)
          set({
            tabEvents: newTabEvents,
            tabEventIndices: newTabEventIndices,
            tabHasMoreOlderEvents: newTabHasMore,
            tabHistoryPagination: newTabHistoryPagination
          })
        }
      },

      getTabLastEventIndex: (sessionId: string) => {
        const state = get()
        return state.tabEventIndices[sessionId] ?? -1
      },
      
      setTabLastEventIndex: (sessionId: string, index: number) => {
        // PERF: skip state update when index hasn't changed (avoids unnecessary re-renders)
        if (get().tabEventIndices[sessionId] === index) return
        set((state) => ({
          tabEventIndices: {
            ...state.tabEventIndices,
            [sessionId]: index
          }
        }))
      },
      
      getTabHasMoreOlderEvents: (sessionId: string) => {
        const state = get()
        return state.tabHasMoreOlderEvents[sessionId] ?? false
      },
      
      setTabHasMoreOlderEvents: (sessionId: string, hasMore: boolean) => {
        if (get().tabHasMoreOlderEvents[sessionId] === hasMore) return
        set((state) => ({
          tabHasMoreOlderEvents: {
            ...state.tabHasMoreOlderEvents,
            [sessionId]: hasMore
          }
        }))
      },

      getTabHistoryPagination: (sessionId: string) => get().tabHistoryPagination[sessionId],

      setTabHistoryPagination: (sessionId, pagination) => {
        const current = get().tabHistoryPagination[sessionId]
        if (
          (pagination === null && current === undefined) ||
          (pagination !== null && current?.hasMore === pagination.hasMore && current?.nextOffset === pagination.nextOffset)
        ) return
        set((state) => {
          const next = { ...state.tabHistoryPagination }
          if (pagination === null) {
            delete next[sessionId]
          } else {
            next[sessionId] = pagination
          }
          return { tabHistoryPagination: next }
        })
      },

      // User message actions
      setCurrentUserMessage: (message) => {
        set({ currentUserMessage: message })
      },

      setShowUserMessage: (show) => {
        set({ showUserMessage: show })
      },

      // Session actions
      setSessionId: (id) => {
        set({ sessionId: id })
      },

      setHasActiveChat: (active) => {
        set({ hasActiveChat: active })
      },

      // UI actions
      setAutoScroll: (autoScroll) => {
        set({ autoScroll })
      },

      setEventViewModePreference: (viewMode) => {
        set({ eventViewModePreference: normalizeEventViewMode(viewMode) })
      },

      setTerminalCenterOpen: (open) => {
        set({ terminalCenterOpen: open })
      },

      toggleTerminalCenterOpen: () => {
        set((state) => ({ terminalCenterOpen: !state.terminalCenterOpen }))
      },

      // Response actions
      setFinalResponse: (response) => {
        set({ finalResponse: response })
      },

      setIsCompleted: (completed) => {
        set({ isCompleted: completed })
        // Auto-update isStreaming: if completed, stop streaming
        if (completed) {
          const state = get()
          if (state.isStreaming) {
            set({ isStreaming: false })
          }
        }
      },

      // Loading actions
      setIsLoadingHistory: (loading) => {
        set({ isLoadingHistory: loading })
      },

      setIsApprovingWorkflow: (loading) => {
        set({ isApprovingWorkflow: loading })
      },

      beginWorkflowSessionRestore: (sessionId) => {
        if (!sessionId) return
        set((state) => ({
          restoringWorkflowSessions: {
            ...state.restoringWorkflowSessions,
            [sessionId]: (state.restoringWorkflowSessions[sessionId] ?? 0) + 1,
          },
        }))
      },

      endWorkflowSessionRestore: (sessionId) => {
        if (!sessionId) return
        set((state) => {
          const current = state.restoringWorkflowSessions[sessionId] ?? 0
          if (current <= 1) {
            const next = { ...state.restoringWorkflowSessions }
            delete next[sessionId]
            return { restoringWorkflowSessions: next }
          }
          return {
            restoringWorkflowSessions: {
              ...state.restoringWorkflowSessions,
              [sessionId]: current - 1,
            },
          }
        })
      },

      // Session management actions
      setSessionState: (state) => {
        set({ sessionState: state })
      },

      setIsCheckingActiveSessions: (checking) => {
        set({ isCheckingActiveSessions: checking })
      },

      // Workflow execution actions
      setCurrentWorkflowPhase: (phase) => {
        set({ currentWorkflowPhase: phase })
      },

      setCurrentWorkflowQueryId: (id) => {
        set({ currentWorkflowQueryId: id })
      },

      // Toast actions
      addToast: (message, type) => {
        const id = Date.now().toString()
        set((state) => ({
          toasts: [...state.toasts, { id, message, type }]
        }))
      },

      removeToast: (id) => {
        set((state) => ({
          toasts: state.toasts.filter(toast => toast.id !== id)
        }))
      },

      clearToasts: () => {
        set({ toasts: [] })
      },

      // Streaming text actions
      // Only parent agent streaming is processed - sub-agent streaming is filtered out in ChatArea
      appendStreamingChunk: (sessionId: string, chunkIndex: number, chunk: string, meta?: StreamingChunkMeta) => {
        if (typeof chunk !== 'string' || !chunk) return

        // Reset inactivity auto-clear timer — if no new chunk arrives in 3s, clear streaming text
        if (_streamingInactivityTimers[sessionId]) {
          clearTimeout(_streamingInactivityTimers[sessionId])
        }
        _streamingInactivityTimers[sessionId] = setTimeout(() => {
          const currentText = useChatStore.getState().streamingText[sessionId]
          if (currentText) {
            useChatStore.getState().clearStreamingText(sessionId)
          }
          delete _streamingInactivityTimers[sessionId]
        }, STREAMING_INACTIVITY_MS)

        set((state) => {
          let lastIndex = state.lastStreamingChunkIndex[sessionId] ?? -1
          let currentText = state.streamingText[sessionId] || ''
          let clearCompleted = false

          // Auto-reset if we see chunk 0 or 1 (start of new generation)
          if (chunkIndex === 0 || chunkIndex === 1) {
             lastIndex = -1
             currentText = ''
             clearCompleted = true
          }

          // Deduplicate: skip chunks already processed (handles concurrent poll overlap)
          if (chunkIndex >= 0 && chunkIndex <= lastIndex) {
            return state
          }

          // Build completedStreamingText update if needed
          const completedUpdate = clearCompleted
            ? (() => { const c = { ...state.completedStreamingText }; delete c[sessionId]; return c })()
            : state.completedStreamingText

          // Route heartbeat/provider/tool progress messages to streamingStatus instead of streamingText.
          // Mixed chunks are split so raw markers like "api-bridge - execute_shell_command (MCP)"
          // cannot leak into the visible assistant markdown.
          const { statusText, text } = splitStreamingStatusAndText(chunk)
          // Trust the backend's own classification when it sent one; only fall
          // back to sniffing the text when the field is absent.
          const isTerminalScreenText = isTerminalSourceChunk(meta) || looksLikeTerminalScreenText(text || chunk)
          const safeText = isTerminalScreenText ? '' : text
          const effectiveStatusText = statusText || (isTerminalScreenText ? 'Agent is working' : null)
          if (effectiveStatusText && !safeText) {
            return {
              streamingStatus: {
                ...state.streamingStatus,
                [sessionId]: effectiveStatusText
              },
              lastStreamingChunkIndex: {
                ...state.lastStreamingChunkIndex,
                [sessionId]: chunkIndex
              },
              ...(clearCompleted ? { completedStreamingText: completedUpdate } : {})
            }
          }

          // Clear status once real content arrives
          const newStreamingStatus = { ...state.streamingStatus }
          if (effectiveStatusText) {
            newStreamingStatus[sessionId] = effectiveStatusText
          } else {
            delete newStreamingStatus[sessionId]
          }

          const nextStreamingText = { ...state.streamingText }
          const nextText = appendStreamingText(currentText, safeText, meta?.isDelta)
          if (nextText) {
            nextStreamingText[sessionId] = nextText
          } else {
            delete nextStreamingText[sessionId]
          }

          return {
            streamingText: nextStreamingText,
            streamingStatus: newStreamingStatus,
            lastStreamingChunkIndex: {
              ...state.lastStreamingChunkIndex,
              [sessionId]: chunkIndex
            },
            ...(clearCompleted ? { completedStreamingText: completedUpdate } : {})
          }
        })
      },

      setStreamingTerminalSnapshot: (sessionId: string, chunkIndex: number, chunk: string) => {
        if (typeof chunk !== 'string' || !chunk) return

        set((state) => {
          const lastIndex = state.lastStreamingTerminalChunkIndex[sessionId] ?? -1
          if (chunkIndex >= 0 && chunkIndex <= lastIndex) {
            return state
          }
          const newStreamingStatus = { ...state.streamingStatus }
          delete newStreamingStatus[sessionId]
          return {
            streamingTerminalText: {
              ...state.streamingTerminalText,
              [sessionId]: chunk
            },
            streamingTerminalActive: {
              ...state.streamingTerminalActive,
              [sessionId]: true
            },
            streamingStatus: newStreamingStatus,
            lastStreamingTerminalChunkIndex: {
              ...state.lastStreamingTerminalChunkIndex,
              [sessionId]: chunkIndex
            }
          }
        })
      },

      setStreamingTerminalActive: (sessionId: string, active: boolean) => {
        set((state) => {
          const nextStreamingTerminalActive = { ...state.streamingTerminalActive }
          if (active) {
            nextStreamingTerminalActive[sessionId] = true
          } else {
            delete nextStreamingTerminalActive[sessionId]
          }
          return { streamingTerminalActive: nextStreamingTerminalActive }
        })
      },

      setTerminalOutputOpen: (sessionId: string, open: boolean) => {
        set((state) => {
          if (!sessionId || state.terminalOutputOpen[sessionId] === open) return state
          return {
            terminalOutputOpen: {
              ...state.terminalOutputOpen,
              [sessionId]: open,
            },
          }
        })
      },

      toggleTerminalOutputOpen: (sessionId: string) => {
        set((state) => {
          if (!sessionId) return state
          const current = state.terminalOutputOpen[sessionId] ?? true
          return {
            terminalOutputOpen: {
              ...state.terminalOutputOpen,
              [sessionId]: !current,
            },
          }
        })
      },

      clearStreamingText: (sessionId: string) => {
        // Cancel any pending inactivity timer
        if (_streamingInactivityTimers[sessionId]) {
          clearTimeout(_streamingInactivityTimers[sessionId])
          delete _streamingInactivityTimers[sessionId]
        }
        set((state) => {
          const newStreamingText = { ...state.streamingText }
          const currentText = newStreamingText[sessionId]
          delete newStreamingText[sessionId]
          const newStreamingStatus = { ...state.streamingStatus }
          delete newStreamingStatus[sessionId]
          const newLastIdx = { ...state.lastStreamingChunkIndex }
          delete newLastIdx[sessionId]
          // Preserve completed streaming text so it stays visible after generation ends.
          // APPEND to existing (don't replace) so multiple thinking rounds accumulate.
          const newCompletedStreamingText = { ...state.completedStreamingText }
          if (currentText) {
            const existing = newCompletedStreamingText[sessionId]
            const nextCompletedText = existing
              ? existing + '\n\n---\n\n' + currentText
              : currentText
            newCompletedStreamingText[sessionId] = capCompletedStreamingText(nextCompletedText)
          }
          return {
            streamingText: newStreamingText,
            streamingStatus: newStreamingStatus,
            lastStreamingChunkIndex: newLastIdx,
            completedStreamingText: newCompletedStreamingText
          }
        })
      },

      clearStreamingTerminal: (sessionId: string) => {
        set((state) => {
          const newStreamingTerminalText = { ...state.streamingTerminalText }
          delete newStreamingTerminalText[sessionId]
          const newStreamingTerminalActive = { ...state.streamingTerminalActive }
          delete newStreamingTerminalActive[sessionId]
          const newLastTerminalIdx = { ...state.lastStreamingTerminalChunkIndex }
          delete newLastTerminalIdx[sessionId]
          return {
            streamingTerminalText: newStreamingTerminalText,
            streamingTerminalActive: newStreamingTerminalActive,
            lastStreamingTerminalChunkIndex: newLastTerminalIdx
          }
        })
      },

      clearStreamingStatus: (sessionId: string) => {
        set((state) => {
          const newStreamingStatus = { ...state.streamingStatus }
          delete newStreamingStatus[sessionId]
          return { streamingStatus: newStreamingStatus }
        })
      },

      // Delegation streaming text actions
      appendDelegationStreamingChunk: (delegationId: string, chunkIndex: number, chunk: string) => {
        if (typeof chunk !== 'string' || !chunk) return
        set((state) => {
          let lastIndex = state.lastDelegationChunkIndex[delegationId] ?? -1
          let currentText = state.delegationStreamingText[delegationId] || ''

          // Auto-reset if we see chunk 0 or 1 (start of new generation)
          if (chunkIndex === 0 || chunkIndex === 1) {
            lastIndex = -1
            currentText = ''
          }

          // Deduplicate: skip chunks already processed
          if (chunkIndex >= 0 && chunkIndex <= lastIndex) {
            return state
          }

          return {
            delegationStreamingText: {
              ...state.delegationStreamingText,
              [delegationId]: currentText + chunk
            },
            lastDelegationChunkIndex: {
              ...state.lastDelegationChunkIndex,
              [delegationId]: chunkIndex
            }
          }
        })
      },

      clearDelegationStreamingText: (delegationId: string) => {
        set((state) => {
          const newText = { ...state.delegationStreamingText }
          delete newText[delegationId]
          const newIdx = { ...state.lastDelegationChunkIndex }
          delete newIdx[delegationId]
          return { delegationStreamingText: newText, lastDelegationChunkIndex: newIdx }
        })
      },

      appendExecutionStreamingChunk: (sessionId: string, executionId: string, chunkIndex: number, chunk: string) => {
        if (typeof chunk !== 'string' || !chunk || !executionId) return

        if (_executionStreamingInactivityTimers[executionId]) {
          clearTimeout(_executionStreamingInactivityTimers[executionId])
        }
        _executionStreamingInactivityTimers[executionId] = setTimeout(() => {
          const current = useChatStore.getState().executionStreaming[executionId]
          if (current?.text || current?.status) {
            useChatStore.getState().clearExecutionStreamingText(executionId)
          }
          delete _executionStreamingInactivityTimers[executionId]
        }, STREAMING_INACTIVITY_MS)

        set((state) => {
          const existing = state.executionStreaming[executionId]
          let lastIndex = existing?.lastChunkIndex ?? -1
          let currentText = existing?.text || ''

          if (chunkIndex === 0 || chunkIndex === 1) {
            lastIndex = -1
            currentText = ''
          }

          if (chunkIndex >= 0 && chunkIndex <= lastIndex) {
            return state
          }

          const { statusText, text } = splitStreamingStatusAndText(chunk)
          const isTerminalScreenText = looksLikeTerminalScreenText(text || chunk)
          const safeText = isTerminalScreenText ? '' : text
          const effectiveStatusText = statusText || (isTerminalScreenText ? 'Agent is working' : undefined)
          const next: ExecutionStreamingActivity = {
            sessionId,
            executionId,
            text: currentText + safeText,
            status: effectiveStatusText,
            lastChunkIndex: chunkIndex,
            updatedAt: Date.now(),
          }

          return {
            executionStreaming: {
              ...state.executionStreaming,
              [executionId]: next,
            },
          }
        })
      },

      clearExecutionStreamingText: (executionId: string) => {
        if (_executionStreamingInactivityTimers[executionId]) {
          clearTimeout(_executionStreamingInactivityTimers[executionId])
          delete _executionStreamingInactivityTimers[executionId]
        }
        set((state) => {
          const next = { ...state.executionStreaming }
          delete next[executionId]
          return { executionStreaming: next }
        })
      },

      clearExecutionStreamingStatus: (executionId: string) => {
        set((state) => {
          const current = state.executionStreaming[executionId]
          if (!current?.status) return state
          return {
            executionStreaming: {
              ...state.executionStreaming,
              [executionId]: { ...current, status: undefined, updatedAt: Date.now() },
            },
          }
        })
      },

      // SSE connection management
      connectSSE: (sessionId, onMessage, onStatus, onError, durableChat = true) => {
        const identity = captureChatIdentity()
        const state = get()
        // Close existing connection for this session if any
        if (state.sseConnections[sessionId]) {
          state.sseConnections[sessionId].close()
        }
        const storedLastIndex = state.tabEventIndices[sessionId] ?? 0
        const lastIndex = durableChat
          ? durableStreamStartCursor(storedLastIndex, state.tabEvents[sessionId] || [])
          : (storedLastIndex < 0 ? 0 : storedLastIndex)
        const conn = new SSEConnection(sessionId, lastIndex, {
          onMessage: message => { if (isChatIdentityCurrent(identity)) onMessage(message) },
          onStatusUpdate: message => { if (isChatIdentityCurrent(identity)) onStatus(message) },
          onError: () => {
            if (!isChatIdentityCurrent(identity)) return
            // Fallback to polling on persistent SSE errors
            logger.warn('ChatStore', `SSE fallback triggered for session ${sessionId}, falling back to polling`)
            // Remove the failed SSE connection
            set((s) => {
              const conns = { ...s.sseConnections }
              delete conns[sessionId]
              return { sseConnections: conns }
            })
            onError?.()
          },
          onOpen: () => {
            logger.debug('ChatStore', `SSE connected for session ${sessionId}`)
          },
        }, durableChat)
        set((s) => ({
          sseConnections: { ...s.sseConnections, [sessionId]: conn },
        }))
      },

      disconnectSSE: (sessionId) => {
        const state = get()
        const conn = state.sseConnections[sessionId]
        if (conn) {
          conn.close()
          set((s) => {
            const conns = { ...s.sseConnections }
            delete conns[sessionId]
            return { sseConnections: conns }
          })
        }
      },

      disconnectAllSSE: () => {
        const state = get()
        Object.values(state.sseConnections).forEach((conn) => conn.close())
        set({ sseConnections: {} })
      },

      // Reset a single tab's chat session without touching other tabs (used by prototype "New Chat")
      resetTabChat: (tabId: string, nextSessionId?: string) => {
        const state = get()
        const tab = state.chatTabs[tabId]
        if (!tab) return

        const oldSessionId = tab.sessionId
        const newSessionId = nextSessionId || globalThis.crypto.randomUUID()

        // Stop any streaming + close SSE for this tab
        if (oldSessionId && state.sseConnections[oldSessionId]) {
          state.sseConnections[oldSessionId].close()
        }

        // Clear per-session data and assign a new session ID
        set((s) => {
          const newTabEvents = { ...s.tabEvents }
          const newTabEventIndices = { ...s.tabEventIndices }
          const newTabHasMore = { ...s.tabHasMoreOlderEvents }
          const newTabHistoryPagination = { ...s.tabHistoryPagination }
          const newStreamingText = { ...s.streamingText }
          const newStreamingStatus = { ...s.streamingStatus }
          const newStreamingTerminalText = { ...s.streamingTerminalText }
          const newStreamingTerminalActive = { ...s.streamingTerminalActive }
          const newTerminalOutputOpen = { ...s.terminalOutputOpen }
          const newLastStreamingChunkIndex = { ...s.lastStreamingChunkIndex }
          const newLastStreamingTerminalChunkIndex = { ...s.lastStreamingTerminalChunkIndex }
          const newCompletedStreamingText = { ...s.completedStreamingText }
          const newExecutionStreaming = { ...s.executionStreaming }
          const newSSE = { ...s.sseConnections }
          if (oldSessionId) {
            delete newTabEvents[oldSessionId]
            delete newTabEventIndices[oldSessionId]
            delete newTabHasMore[oldSessionId]
            delete newTabHistoryPagination[oldSessionId]
            delete newStreamingText[oldSessionId]
            delete newStreamingStatus[oldSessionId]
            delete newStreamingTerminalText[oldSessionId]
            delete newStreamingTerminalActive[oldSessionId]
            delete newTerminalOutputOpen[oldSessionId]
            delete newLastStreamingChunkIndex[oldSessionId]
            delete newLastStreamingTerminalChunkIndex[oldSessionId]
            delete newCompletedStreamingText[oldSessionId]
            for (const [executionId, activity] of Object.entries(newExecutionStreaming)) {
              if (activity.sessionId === oldSessionId) {
                if (_executionStreamingInactivityTimers[executionId]) {
                  clearTimeout(_executionStreamingInactivityTimers[executionId])
                  delete _executionStreamingInactivityTimers[executionId]
                }
                delete newExecutionStreaming[executionId]
              }
            }
            delete newSSE[oldSessionId]
          }
          return {
            chatTabs: {
              ...s.chatTabs,
              [tabId]: {
                ...tab,
                sessionId: newSessionId,
                isStreaming: false,
                lastStreamingStartedAt: undefined,
                viewMode: tab.metadata?.mode === 'multi-agent' ? 'terminal' : tab.viewMode,
                config: stripRestoreOnlyTabConfig(tab.config),
              },
            },
            tabEvents: newTabEvents,
            tabEventIndices: newTabEventIndices,
            tabHasMoreOlderEvents: newTabHasMore,
            tabHistoryPagination: newTabHistoryPagination,
            streamingText: newStreamingText,
            streamingStatus: newStreamingStatus,
            streamingTerminalText: newStreamingTerminalText,
            streamingTerminalActive: newStreamingTerminalActive,
            terminalOutputOpen: newTerminalOutputOpen,
            lastStreamingChunkIndex: newLastStreamingChunkIndex,
            lastStreamingTerminalChunkIndex: newLastStreamingTerminalChunkIndex,
            completedStreamingText: newCompletedStreamingText,
            executionStreaming: newExecutionStreaming,
            sseConnections: newSSE,
          }
        })
      },

      // Helper methods
      resetChatState: () => {
        const state = get()

        // Close all SSE connections
        Object.values(state.sseConnections).forEach((conn) => conn.close())
        Object.values(_executionStreamingInactivityTimers).forEach((timer) => clearTimeout(timer))
        Object.keys(_executionStreamingInactivityTimers).forEach((key) => {
          delete _executionStreamingInactivityTimers[key]
        })

        // Close all tabs and stop sessions
        Object.values(state.chatTabs).forEach(async (tab) => {
          try {
            if (tab.isStreaming && tab.sessionId) {
              await agentApi.stopSession(tab.sessionId)
            }
          } catch (error) {
            logger.error('ChatStore', `Error cleaning up tab ${tab.tabId}:`, error)
          }
        })
        
        set({
          isStreaming: false,
          lastEventIndex: -1,
          pollingInterval: null,
          // Deprecated fields removed
          currentUserMessage: '',
          showUserMessage: true,
          sessionId: null,
          hasActiveChat: false,
          autoScroll: true,
          finalResponse: '',
          isCompleted: false,
          isLoadingHistory: false,
          isApprovingWorkflow: false,
          restoringWorkflowSessions: {},
          sessionState: 'loading',
          isCheckingActiveSessions: false,
          currentWorkflowPhase: 'planning' as WorkflowPhase,
          currentWorkflowQueryId: null,
          toasts: [],
          chatTabs: {},
          activeTabId: null,
          streamingText: {},
          streamingStatus: {},
          streamingTerminalText: {},
          streamingTerminalActive: {},
          terminalOutputOpen: {},
          lastStreamingChunkIndex: {},
          lastStreamingTerminalChunkIndex: {},
          completedStreamingText: {},
          delegationStreamingText: {},
          lastDelegationChunkIndex: {},
          executionStreaming: {}
        })

        // Clear the requiresNewChat flag after successful chat reset
        useAppStore.getState().clearRequiresNewChat()
      },

      discardChatStateForAccountChange: () => {
        invalidateChatIdentity()
        pendingActiveSessionsFetch = null
        const state = get()

        // A browser can be handed from one account to another. Close only the
        // local observers and timers — never call stopSession here, because a
        // tab restored from storage can belong to the user who just signed
        // out and their work must continue independently.
        Object.values(state.sseConnections).forEach((conn) => conn.close())
        Object.values(_streamingInactivityTimers).forEach((timer) => clearTimeout(timer))
        Object.keys(_streamingInactivityTimers).forEach((key) => delete _streamingInactivityTimers[key])
        Object.values(_executionStreamingInactivityTimers).forEach((timer) => clearTimeout(timer))
        Object.keys(_executionStreamingInactivityTimers).forEach((key) => delete _executionStreamingInactivityTimers[key])
        for (const sessionId of _eventBatchTimers.keys()) clearPendingEventBatch(sessionId)
        tabEventIdSets.clear()
        tabAutoNotificationKeys.clear()
        if (state.activeSessionsPollingInterval !== null) clearInterval(state.activeSessionsPollingInterval)

        // Explicit discard removes this owner's cache. switchChatAccount
        // temporarily suppresses persistence during this reset so changing
        // accounts preserves the departing owner's saved tabs.
        useChatStore.persist.clearStorage()
        set({
          isStreaming: false,
          lastEventIndex: -1,
          pollingInterval: null,
          currentUserMessage: '',
          showUserMessage: true,
          sessionId: null,
          hasActiveChat: false,
          autoScroll: true,
          finalResponse: '',
          isCompleted: false,
          isLoadingHistory: false,
          isApprovingWorkflow: false,
          restoringWorkflowSessions: {},
          sessionState: 'loading',
          isCheckingActiveSessions: false,
          currentWorkflowPhase: 'planning' as WorkflowPhase,
          currentWorkflowQueryId: null,
          toasts: [],
          chatTabs: {},
          activeTabId: null,
          tabEvents: {},
          tabEventIndices: {},
          tabHasMoreOlderEvents: {},
          tabHistoryPagination: {},
          tabSessionStatus: {},
          activeSessionsCache: [],
          activeSessionsCacheTimestamp: null,
          activeSessionsPollingInterval: null,
          workflowScheduleSummary: null,
          streamingText: {},
          streamingStatus: {},
          streamingTerminalText: {},
          streamingTerminalActive: {},
          terminalOutputOpen: {},
          lastStreamingChunkIndex: {},
          lastStreamingTerminalChunkIndex: {},
          completedStreamingText: {},
          delegationStreamingText: {},
          lastDelegationChunkIndex: {},
          executionStreaming: {},
          sseConnections: {},
        })
        useAppStore.getState().clearRequiresNewChat()
      },

      isAtBottom: (element) => {
        const threshold = 150 // Generous threshold so scrolling back down re-enables auto-scroll easily
        const isAtBottom = element.scrollTop + element.clientHeight >= element.scrollHeight - threshold

        return isAtBottom;
      },

      // Generic actions
      reset: () => {
        get().resetChatState()
      },

      setLoading: (loading) => {
        set({ isLoadingHistory: loading })
      },

      setError: (error) => {
        if (error) {
          get().addToast(error, 'error')
        }
      },
      
      // Tab management actions
      createChatTab: async (name: string, metadata?: ChatTab['metadata'], existingObserverId?: string) => {
        const identity = captureChatIdentity()
        // Generate unique tab ID
        const timestamp = Date.now()
        const mode = metadata?.mode || 'multi-agent'

        if (mode === 'multi-agent' && !metadata?.agentProfileId) {
          throw new Error('Profile-less AgentWorks Chat has been removed; multi-agent tabs must be owned by a product profile')
        }

        // Product agent profiles have one tab per durable product conversation.
        // The conversation key is authoritative when present; workspace paths
        // and profile versions may change without creating a second chat.
        if (
          mode === 'multi-agent' &&
          metadata &&
          !metadata.agentProfileBuilder &&
          !metadata?.isOrganizationAssistant &&
          !metadata?.isViewOnly &&
          !metadata?.isScheduledRun &&
          !metadata?.isBotRun
        ) {
          const existing = Object.values(get().chatTabs).find(t =>
            t.metadata?.mode === 'multi-agent' &&
            // A product's permanent Builder is a blank launch surface, never
            // the durable conversation being created or restored. Reusing it
            // here would merge agentProfileBuilder:false into the Builder and
            // unpin it, then restoration would create a duplicate chat tab.
            t.metadata?.agentProfileBuilder !== true &&
            !t.metadata?.isOrganizationAssistant &&
            t.metadata?.isViewOnly !== true &&
            t.metadata?.isScheduledRun !== true &&
            t.metadata?.isBotRun !== true &&
            t.metadata?.agentProfileId === metadata.agentProfileId &&
            (metadata.agentProfileConversationKey
              ? (
                  t.metadata?.agentProfileConversationKey === metadata.agentProfileConversationKey ||
                  (!t.metadata?.agentProfileConversationKey && (
                    metadata.agentProfileProjectId
                      ? t.metadata?.agentProfileProjectId === metadata.agentProfileProjectId
                      : true
                  ))
                )
              : metadata.agentProfileWorkspace
                ? t.metadata?.agentProfileWorkspace === metadata.agentProfileWorkspace
                : true)
          )
          if (existing) {
            // Product manifests can opt an existing durable tab into a newer
            // chat contract. Merge the declarative binding on reuse so users
            // keep their existing conversation when a product is upgraded.
            set(state => ({
              chatTabs: {
                ...state.chatTabs,
                [existing.tabId]: {
                  ...state.chatTabs[existing.tabId],
                  metadata: { ...state.chatTabs[existing.tabId].metadata, ...metadata },
                },
              },
            }))
            // Restore binds the single tab to a specific backend session.
            if (existingObserverId && existingObserverId !== existing.sessionId) {
              get().updateTabSessionId(existing.tabId, existingObserverId)
            }
            set({ activeTabId: existing.tabId })
            logger.debug('TabStore', `Reusing single multi-agent tab ${existing.tabId} (single-tab invariant)`)
            return existing.tabId
          }
        }

        const tabIdBase = mode === 'workflow' && metadata?.phaseId
          ? `phase_${metadata.phaseId}_${timestamp}`
          : `chat_${timestamp}`
        // Two independent lanes (for example Schedule + a product chat)
        // can be created in the same millisecond. Preserve the readable ID
        // shape while guaranteeing that the second tab cannot overwrite the
        // first in the chatTabs record.
        let tabId = tabIdBase
        let tabIdSuffix = 1
        while (get().chatTabs[tabId]) {
          tabId = `${tabIdBase}_${tabIdSuffix}`
          tabIdSuffix += 1
        }
        
        // Generate session ID for the new tab if not provided
        let sessionIdForTab: string | null = null
        
        if (existingObserverId) {
          // If existingObserverId is provided, treat it as sessionId
          sessionIdForTab = existingObserverId
          logger.debug('TabStore', `Using existing session ID for tab ${tabId}: ${sessionIdForTab}`)
        } else {
          // Generate new session ID
          sessionIdForTab = globalThis.crypto.randomUUID()
          logger.debug('TabStore', `Generated new session ID for tab ${tabId}: ${sessionIdForTab}`)
        }
        
        assertChatIdentityCurrent(identity)
        // Get default config from current global state (mode-specific)
        const defaultConfig = getDefaultTabConfig(mode)
        if (metadata?.agentProfileId) {
          defaultConfig.selectedServers = ['NO_SERVERS']
          defaultConfig.selectedSkills = []
        }
        
        // Validate session ID before creating tab
        if (!sessionIdForTab || sessionIdForTab.trim() === '') {
          logger.error('TabStore', `Cannot create tab ${tabId} - sessionId is empty!`)
          throw new Error('Session ID is required but was not provided or is empty')
        }
        
        // Create tab with session ID
        const tab: ChatTab = {
          tabId,
          name,
          sessionId: sessionIdForTab,
          isStreaming: false,
          isCompleted: false,
          hasRunningBgAgents: false,
          isSyntheticTurn: false,
          canSteer: false,
          hideToolCalls: true,
          viewMode: normalizeEventViewMode(get().eventViewModePreference),
          config: defaultConfig, // Initialize with default config from global state
          createdAt: timestamp,
          lastAccessedAt: timestamp,
          lastViewedEventCount: 0, // @deprecated - kept for backwards compat
          lastViewedEventCounts: { micro: 0 }, // Initialize all modes to 0
          metadata
        }
        
        logger.debug('TabStore', `Creating tab with session ID:`, {
          tabId,
          name,
          sessionId: sessionIdForTab,
          hasSessionId: !!sessionIdForTab
        })
        
        set((state) => ({
          chatTabs: {
            ...state.chatTabs,
            [tabId]: tab
          },
          activeTabId: tabId
        }))
        
        // Verify the tab was stored correctly
        const storedTab = get().chatTabs[tabId]
        if (!storedTab) {
          logger.error('TabStore', `Tab ${tabId} was not stored in state!`)
          throw new Error('Failed to store tab in state')
        }
        if (!storedTab.sessionId) {
          logger.error('TabStore', `Tab ${tabId} stored but has no sessionId!`, storedTab)
          throw new Error('Tab stored without session ID')
        }
        
        logger.debug('TabStore', `Tab ${tabId} created and stored successfully with session ID: ${storedTab.sessionId}`)
        return tabId
      },
      
      switchTab: (tabId: string) => {
        // Use single atomic set() to avoid race conditions with stale state
        set((state) => {
          if (!state.chatTabs[tabId]) {
            logger.warn('TabStore', `Tab ${tabId} not found`)
            return state
          }

          const updates: Record<string, ChatTab> = {}
          let newTabEvents = state.tabEvents
          let newTabEventIndices = state.tabEventIndices
          let newTabHasMore = state.tabHasMoreOlderEvents
          let newTabHistoryPagination = state.tabHistoryPagination

          // Update previous active tab's lastViewedEventCounts before switching
          if (state.activeTabId && state.activeTabId !== tabId) {
            const previousTab = state.chatTabs[state.activeTabId]
            if (previousTab?.sessionId) {
              const events = state.tabEvents[previousTab.sessionId] || []
              updates[state.activeTabId] = {
                ...previousTab,
                lastViewedEventCount: events.length, // @deprecated - kept for compat
                lastViewedEventCounts: computePerModeCounts(events)
              }
            }
          }

          // Update new tab's lastViewedEventCounts
          const newTab = state.chatTabs[tabId]
          if (newTab?.sessionId) {
            const events = state.tabEvents[newTab.sessionId] || []
            updates[tabId] = {
              ...newTab,
              lastAccessedAt: Date.now(),
              hasUnreadCompletion: false,
              lastViewedEventCount: events.length, // @deprecated - kept for compat
              lastViewedEventCounts: computePerModeCounts(events)
            }
          } else if (newTab) {
            updates[tabId] = {
              ...newTab,
              lastAccessedAt: Date.now(),
              hasUnreadCompletion: false,
            }
          }

          // Clean up orphaned tab events (sessions no longer referenced by any tab)
          const referencedSessionIds = new Set(
            Object.values({ ...state.chatTabs, ...updates })
              .map(tab => tab.sessionId)
              .filter(Boolean)
          )
          for (const sessionId of Object.keys(newTabEvents)) {
            if (!referencedSessionIds.has(sessionId)) {
              if (newTabEvents === state.tabEvents) {
                newTabEvents = { ...newTabEvents }
                newTabEventIndices = { ...newTabEventIndices }
                newTabHasMore = { ...newTabHasMore }
                newTabHistoryPagination = { ...newTabHistoryPagination }
              }
              delete newTabEvents[sessionId]
              delete newTabEventIndices[sessionId]
              delete newTabHasMore[sessionId]
              delete newTabHistoryPagination[sessionId]
              logger.debug('TabStore', `Cleaned up orphaned events for session ${sessionId}`)
            }
          }

          return {
            activeTabId: tabId,
            chatTabs: { ...state.chatTabs, ...updates },
            tabEvents: newTabEvents,
            tabEventIndices: newTabEventIndices,
            tabHasMoreOlderEvents: newTabHasMore,
            tabHistoryPagination: newTabHistoryPagination
          }
        })
      },
      
      closeTab: async (tabId: string, stopSession: boolean = true, keepEvents: boolean = false) => {
        const identity = captureChatIdentity()
        let state = get()
        const tab = state.chatTabs[tabId]

        if (!tab) {
          logger.warn('TabStore', `Tab ${tabId} not found`)
          return
        }

        // Stop session if streaming (unless explicitly disabled, e.g., when minimizing to background)
        if (stopSession && tab.isStreaming && tab.sessionId) {
          try {
            await agentApi.stopSession(tab.sessionId)
          } catch (error) {
            logger.error('SessionStore', `Failed to stop session ${tab.sessionId}:`, error)
          }
        }

        if (!isChatIdentityCurrent(identity)) return
        // Other tabs and this tab's binding may change while stop is in flight.
        state = get()
        if (state.chatTabs[tabId]?.sessionId !== tab.sessionId) return

        // Dismiss session so it won't be auto-restored on page refresh (fire-and-forget)
        // Skip dismiss for workflow tabs — their sessions should remain restorable via DB
        if (tab.sessionId && tab.metadata?.mode !== 'workflow') {
          logger.info('SessionStore', `Dismissing session ${tab.sessionId} (stopSession=${stopSession})`)
          agentApi.dismissSession(tab.sessionId).catch(error => {
            logger.error('SessionStore', `Failed to dismiss session ${tab.sessionId}:`, error)
          })
        } else if (!tab.sessionId) {
          logger.info('SessionStore', `Tab ${tabId} has no sessionId, skipping dismiss`)
        }

        // Check if any OTHER tab shares this sessionId (e.g., duplicate workflow tabs)
        // If so, don't disconnect SSE or clean up session-keyed resources — the other tab needs them
        const otherTabUsesSession = tab.sessionId && Object.values(state.chatTabs).some(
          t => t.tabId !== tabId && t.sessionId === tab.sessionId
        )

        // Disconnect SSE connection for this session (only if no other tab shares it)
        if (tab.sessionId && !otherTabUsesSession && state.sseConnections[tab.sessionId]) {
          state.sseConnections[tab.sessionId].close()
        }

        // Clear tab's events (by sessionId) unless keepEvents is true (e.g., for background workflows)
        let newTabEvents = state.tabEvents
        let newTabEventIndices = state.tabEventIndices
        if (!keepEvents && tab.sessionId && !otherTabUsesSession) {
          newTabEvents = { ...state.tabEvents }
          delete newTabEvents[tab.sessionId]
          newTabEventIndices = { ...state.tabEventIndices }
          delete newTabEventIndices[tab.sessionId]
        }

        // Remove tab
        const newTabs = { ...state.chatTabs }
        delete newTabs[tabId]

        // Switch to another tab if this was active
        let newActiveTabId = state.activeTabId
        if (state.activeTabId === tabId) {
          const remainingTabs = Object.values(newTabs).sort((a, b) => b.createdAt - a.createdAt)
          newActiveTabId = remainingTabs.length > 0 ? remainingTabs[0].tabId : null
        }

        // Clean up all resources associated with this tab
        const updates: Partial<ChatState> = {
          chatTabs: newTabs,
          activeTabId: newActiveTabId,
          tabEvents: newTabEvents,
          tabEventIndices: newTabEventIndices
        }

        // Clean up SSE connection entry (only if no other tab shares the session)
        if (tab.sessionId && !otherTabUsesSession && state.sseConnections[tab.sessionId]) {
          const newConns = { ...state.sseConnections }
          delete newConns[tab.sessionId]
          updates.sseConnections = newConns
        }

        // Clean up session-keyed resources (only if no other tab shares the session)
        if (tab.sessionId && !otherTabUsesSession) {
          if (_streamingInactivityTimers[tab.sessionId]) {
            clearTimeout(_streamingInactivityTimers[tab.sessionId])
            delete _streamingInactivityTimers[tab.sessionId]
          }

          const newStreamingText = { ...state.streamingText }
          delete newStreamingText[tab.sessionId]
          updates.streamingText = newStreamingText

          const newStreamingTerminalText = { ...state.streamingTerminalText }
          delete newStreamingTerminalText[tab.sessionId]
          updates.streamingTerminalText = newStreamingTerminalText

          const newStreamingTerminalActive = { ...state.streamingTerminalActive }
          delete newStreamingTerminalActive[tab.sessionId]
          updates.streamingTerminalActive = newStreamingTerminalActive

          const newTerminalOutputOpen = { ...state.terminalOutputOpen }
          delete newTerminalOutputOpen[tab.sessionId]
          updates.terminalOutputOpen = newTerminalOutputOpen

          const newLastChunkIndex = { ...state.lastStreamingChunkIndex }
          delete newLastChunkIndex[tab.sessionId]
          updates.lastStreamingChunkIndex = newLastChunkIndex

          const newLastTerminalChunkIndex = { ...state.lastStreamingTerminalChunkIndex }
          delete newLastTerminalChunkIndex[tab.sessionId]
          updates.lastStreamingTerminalChunkIndex = newLastTerminalChunkIndex

          const newStreamingStatus = { ...state.streamingStatus }
          delete newStreamingStatus[tab.sessionId]
          updates.streamingStatus = newStreamingStatus

          const newCompletedStreamingText = { ...state.completedStreamingText }
          delete newCompletedStreamingText[tab.sessionId]
          updates.completedStreamingText = newCompletedStreamingText

          const newExecutionStreaming = { ...state.executionStreaming }
          for (const [executionId, activity] of Object.entries(newExecutionStreaming)) {
            if (activity.sessionId === tab.sessionId) {
              if (_executionStreamingInactivityTimers[executionId]) {
                clearTimeout(_executionStreamingInactivityTimers[executionId])
                delete _executionStreamingInactivityTimers[executionId]
              }
              delete newExecutionStreaming[executionId]
            }
          }
          updates.executionStreaming = newExecutionStreaming

          const newHasMore = { ...state.tabHasMoreOlderEvents }
          delete newHasMore[tab.sessionId]
          updates.tabHasMoreOlderEvents = newHasMore

          const newHistoryPagination = { ...state.tabHistoryPagination }
          delete newHistoryPagination[tab.sessionId]
          updates.tabHistoryPagination = newHistoryPagination
        }

        // Clean up tab session status (always — this is keyed by tabId, not sessionId)
        const newTabSessionStatus = { ...state.tabSessionStatus }
        delete newTabSessionStatus[tabId]
        updates.tabSessionStatus = newTabSessionStatus

        set(updates)
      },
      
      getTab: (tabId: string) => {
        return get().chatTabs[tabId]
      },
      
      getActiveTab: () => {
        const state = get()
        if (!state.activeTabId) return undefined
        return state.chatTabs[state.activeTabId]
      },
      
      getTabsByMode: (mode: 'multi-agent' | 'workflow') => {
        const state = get()
        return Object.values(state.chatTabs).filter(tab => tab.metadata?.mode === mode)
      },
      
      getTabsByPhaseId: (phaseId: string, presetQueryId?: string) => {
        const state = get()
        return Object.values(state.chatTabs).filter(
          tab => tab.metadata?.mode === 'workflow' &&
            tab.metadata?.phaseId === phaseId &&
            (!presetQueryId || tab.metadata?.presetQueryId === presetQueryId)
        )
      },
      
      setTabStreaming: (tabId: string, isStreaming: boolean) => {
        const tab = get().chatTabs[tabId]
        if (!tab || tab.isStreaming === isStreaming) return

        set((state) => ({
          chatTabs: {
            ...state.chatTabs,
            [tabId]: {
              ...state.chatTabs[tabId],
              isStreaming,
              hasUnreadCompletion: isStreaming
                ? false
                : (!tab.hasRunningBgAgents && state.activeTabId !== tabId
                    ? true
                    : tab.hasUnreadCompletion),
              lastStreamingStartedAt: isStreaming ? Date.now() : undefined,
            }
          }
        }))
      },

      setTabCompleted: (tabId: string, isCompleted: boolean) => {
        const tab = get().chatTabs[tabId]
        if (!tab) return
        const newStreaming = isCompleted ? false : tab.isStreaming
        if (tab.isCompleted === isCompleted && tab.isStreaming === newStreaming) return

        set((state) => ({
          chatTabs: {
            ...state.chatTabs,
            [tabId]: {
              ...state.chatTabs[tabId],
              isCompleted,
              isStreaming: newStreaming,
              hasUnreadCompletion: !isCompleted
                ? false
                : (state.activeTabId !== tabId && (tab.isStreaming || tab.hasRunningBgAgents)
                    ? true
                    : tab.hasUnreadCompletion),
              lastStreamingStartedAt: newStreaming ? state.chatTabs[tabId].lastStreamingStartedAt : undefined,
            }
          }
        }))
      },

      setTabHasRunningBgAgents: (tabId: string, hasRunningBgAgents: boolean) => {
        const tab = get().chatTabs[tabId]
        if (!tab || tab.hasRunningBgAgents === hasRunningBgAgents) return

        set((state) => ({
          chatTabs: {
            ...state.chatTabs,
            [tabId]: {
              ...state.chatTabs[tabId],
              hasRunningBgAgents,
              hasUnreadCompletion: hasRunningBgAgents
                ? false
                : (!tab.isStreaming && state.activeTabId !== tabId
                    ? true
                    : tab.hasUnreadCompletion),
            }
          }
        }))
      },

      setTabSyntheticTurn: (tabId: string, isSyntheticTurn: boolean) => {
        const tab = get().chatTabs[tabId]
        if (!tab || tab.isSyntheticTurn === isSyntheticTurn) return

        set((state) => ({
          chatTabs: {
            ...state.chatTabs,
            [tabId]: { ...state.chatTabs[tabId], isSyntheticTurn }
          }
        }))
      },

      setTabCanSteer: (tabId: string, canSteer: boolean) => {
        const tab = get().chatTabs[tabId]
        if (!tab || tab.canSteer === canSteer) return

        set((state) => ({
          chatTabs: {
            ...state.chatTabs,
            [tabId]: { ...state.chatTabs[tabId], canSteer }
          }
        }))
      },

      updateTabSessionId: (tabId: string, newSessionId: string) => {
        const state = get()
        const tab = state.chatTabs[tabId]
        if (!tab) return

        const oldSessionId = tab.sessionId

        // If sessionId hasn't changed, nothing to do
        if (oldSessionId === newSessionId) return

        set((state) => {
          const updates: Partial<ChatState> = {
            chatTabs: {
              ...state.chatTabs,
              [tabId]: {
                ...tab,
                sessionId: newSessionId
              }
            }
          }

          // For non-workflow tabs: migrate events to preserve chat history.
          // For restored workflow-builder chat tabs, also migrate because the
          // optimistic user message contains the restored conversation file context.
          // Other workflow tabs still discard old events because re-runs should start fresh.
          if (oldSessionId && state.tabEvents[oldSessionId]) {
            const isWorkflowTab = tab.metadata?.mode === 'workflow'
            const shouldMigrateEvents =
              !isWorkflowTab ||
              tab.metadata?.phaseId === 'workflow-builder' ||
              !!tab.config?.restoredConversationPath

            if (shouldMigrateEvents) {
              // Migrate events to preserve conversation history for chat/multi-agent
              const oldEvents = state.tabEvents[oldSessionId]
              const oldEventIndex = state.tabEventIndices[oldSessionId]
              const oldHasMore = state.tabHasMoreOlderEvents[oldSessionId]
              const oldHistoryPagination = state.tabHistoryPagination[oldSessionId]

              updates.tabEvents = {
                ...state.tabEvents,
                [newSessionId]: oldEvents
              }
              if (oldEventIndex !== undefined) {
                updates.tabEventIndices = {
                  ...state.tabEventIndices,
                  [newSessionId]: oldEventIndex
                }
              }
              if (oldHasMore !== undefined) {
                updates.tabHasMoreOlderEvents = {
                  ...state.tabHasMoreOlderEvents,
                  [newSessionId]: oldHasMore
                }
              }
              if (oldHistoryPagination !== undefined) {
                updates.tabHistoryPagination = {
                  ...state.tabHistoryPagination,
                  [newSessionId]: oldHistoryPagination,
                }
              }
            }

            // Clean up old sessionId entries (always, for both modes)
            const newTabEvents = { ...(updates.tabEvents || state.tabEvents) }
            delete newTabEvents[oldSessionId]
            updates.tabEvents = newTabEvents

            const newTabEventIndices = { ...(updates.tabEventIndices || state.tabEventIndices) }
            delete newTabEventIndices[oldSessionId]
            updates.tabEventIndices = newTabEventIndices

            const newTabHasMore = { ...(updates.tabHasMoreOlderEvents || state.tabHasMoreOlderEvents) }
            delete newTabHasMore[oldSessionId]
            updates.tabHasMoreOlderEvents = newTabHasMore

            const newHistoryPagination = { ...(updates.tabHistoryPagination || state.tabHistoryPagination) }
            delete newHistoryPagination[oldSessionId]
            updates.tabHistoryPagination = newHistoryPagination
          }

          return updates
        })
      },

      renameTab: (tabId: string, name: string) => {
        const normalized = name.replace(/\s+/g, ' ').trim()
        if (!normalized) return
        set(state => {
          const tab = state.chatTabs[tabId]
          if (!tab) return state
          return {
            chatTabs: {
              ...state.chatTabs,
              [tabId]: { ...tab, name: normalized },
            },
          }
        })
      },
      
      // updateTabObserverId removed - observers no longer used
      

      setTabHideToolCalls: (tabId: string, hideToolCalls: boolean) => {
        const state = get()
        const tab = state.chatTabs[tabId]
        if (!tab) return

        set((state) => ({
          chatTabs: {
            ...state.chatTabs,
            [tabId]: {
              ...tab,
              hideToolCalls
            }
          }
        }))
      },

      setTabViewMode: (tabId: string, viewMode: EventViewMode) => {
        const state = get()
        const tab = state.chatTabs[tabId]
        if (!tab) return
        const nextMode = normalizeEventViewMode(viewMode)
        void state
        void tab

        // We do NOT drop tabEvents on entering Terminal mode. Earlier
        // attempts to drop caused two cascading issues: (1) optimistic
        // user messages briefly disappeared on switch-back, and (2)
        // workflow "no conversation" gates flipped on and visually
        // covered the terminal pane.
        // Memory savings come from disconnecting SSE + pausing poll
        // (see ChatArea useEffects); the events that existed at
        // terminal-mode entry stay in memory but don't grow.

        set((state) => ({
          eventViewModePreference: nextMode,
          chatTabs: {
            ...state.chatTabs,
            [tabId]: {
              ...state.chatTabs[tabId],
              viewMode
            }
          }
        }))
      },

      getTabConfig: (tabId: string) => {
        const state = get()
        const tab = state.chatTabs[tabId]
        return tab?.config
      },
      
      setTabConfig: (tabId: string, configUpdate: Partial<ChatTabConfig>) => {
        const state = get()
        const tab = state.chatTabs[tabId]
        if (!tab || Object.entries(configUpdate).every(([key, value]) =>
          Object.is(tab.config[key as keyof ChatTabConfig], value))) return

        set((state) => {
          const freshTab = state.chatTabs[tabId]
          if (!freshTab) return state
          return {
            chatTabs: {
              ...state.chatTabs,
              [tabId]: {
                ...freshTab,
                config: {
                  ...freshTab.config,
                  ...configUpdate,
                  composerRevision: (['inputText', 'pastedAttachments'] as const).some(
                    key => key in configUpdate && configUpdate[key] !== freshTab.config[key],
                  ) ? (freshTab.config.composerRevision ?? 0) + 1 : (freshTab.config.composerRevision ?? 0)
                }
              }
            }
          }
        })

        // Sync last-used settings to AppStore so new tabs inherit them.
        // Only sync for multi-agent tabs — workflow tabs have different settings.
        const tabMode = tab.metadata?.mode
        if (tabMode === 'multi-agent') {
          type SyncUpdate = {
            lastSelectedSkills?: string[]
            lastBrowserMode?: 'none' | 'auto' | 'headless' | 'cdp'
          }
          const sync: SyncUpdate = {}
          if (configUpdate.selectedSkills !== undefined) sync.lastSelectedSkills = configUpdate.selectedSkills
          if (configUpdate.browserMode !== undefined) sync.lastBrowserMode = configUpdate.browserMode
          if (Object.keys(sync).length > 0) {
            console.log('[TabSettings] Syncing to AppStore:', sync)
            useAppStore.getState().syncLastTabSettings(sync)
          }
        }
      },
      
      setTabMetadata: (tabId: string, metadataUpdate: Partial<NonNullable<ChatTab['metadata']>>) => {
        const state = get()
        const tab = state.chatTabs[tabId]
        if (!tab) return
        
        set((state) => ({
          chatTabs: {
            ...state.chatTabs,
            [tabId]: {
              ...tab,
              metadata: {
                ...tab.metadata,
                ...metadataUpdate
              }
            }
          }
        }))
      },
      
      getTabStreamingStatus: (tabId: string) => {
        const state = get()
        const tab = state.chatTabs[tabId]
        if (!tab || tab.isCompleted) return false
        // isStreaming is the authoritative flag — set to true on submit, false on completion/stop.
        // Don't gate on pollingInterval: in SSE mode pollingInterval is always null, but
        // isStreaming is still correctly maintained via SSE status updates.
        return tab.isStreaming === true
      },
      
      checkTabCompletion: (tabId: string, events: Array<{ type: string }>) => {
        const state = get()
        const tab = state.chatTabs[tabId]
        if (!tab) return false
        
        const mode = tab.metadata?.mode || 'multi-agent'
        
        // Check if any events are completion events.
        // The signal is 'orchestrator_end' (BaseOrchestrator.EmitOrchestratorEnd);
        // 'workflow_end' was deleted in batch 4 (never emitted).
        const completionEventTypes = mode === 'workflow'
          ? ['orchestrator_end', 'request_human_feedback']
          : ['unified_completion', 'agent_end', 'conversation_end', 'conversation_error', 'agent_error']
        
        return events.some(event => completionEventTypes.includes(event.type))
      },
      
      // Tab session status actions
      fetchTabSessionStatus: async (tabId: string) => {
        const identity = captureChatIdentity()
        const state = get()
        const tab = state.chatTabs[tabId]
        
        if (!tab || !tab.sessionId) {
          logger.warn('SessionStore', `Cannot fetch session status - tab ${tabId} has no session ID`)
          return
        }
        
        try {
          let status: TabSessionStatus = {
            status: null,
            agentMode: null,
            lastActivity: null
          }
          
          if (tab.sessionId) {
            // Check if this session is in the active sessions list before calling getSessionStatus
            // Use cached active sessions to avoid unnecessary API calls
            try {
              const { getActiveSessions } = get()
              const activeSessions = await getActiveSessions()
              if (!isChatIdentityCurrent(identity)) return
              const activeSessionIds = new Set(activeSessions.map(s => s.session_id))
              
              // Only call getSessionStatus if the session is active
              if (activeSessionIds.has(tab.sessionId)) {
                try {
                  // Get session status
                  const sessionStatus: SessionStatusResponse = await agentApi.getSessionStatus(tab.sessionId)
                  status = {
                    status: sessionStatus.status || null,
                    agentMode: sessionStatus.agent_mode || null,
                    lastActivity: sessionStatus.last_activity || null
                  }
                } catch (sessionError: unknown) {
                  // Handle 404 or other session status errors gracefully
                  const axiosError = sessionError as { response?: { status?: number }; message?: string }
                  if (axiosError?.response?.status === 404) {
                    // Session not found
                    status = {
                      status: null,
                      agentMode: null,
                      lastActivity: null
                    }
                  }
                }
              }
            } catch {
              if (!isChatIdentityCurrent(identity)) return
              // If active sessions check fails, fall back to calling getSessionStatus
              try {
                const sessionStatus: SessionStatusResponse = await agentApi.getSessionStatus(tab.sessionId)
                status = {
                  status: sessionStatus.status || null,
                  agentMode: sessionStatus.agent_mode || null,
                  lastActivity: sessionStatus.last_activity || null
                }
              } catch (sessionError: unknown) {
                // Handle 404 or other session status errors gracefully
                const axiosError = sessionError as { response?: { status?: number }; message?: string }
                if (axiosError?.response?.status === 404) {
                  // Session not found
                  status = {
                    status: null,
                    agentMode: null,
                    lastActivity: null
                  }
                }
              }
            }
          }
          
          if (!isChatIdentityCurrent(identity)) return
          // Update status in store
          set((state) => ({
            tabSessionStatus: {
              ...state.tabSessionStatus,
              [tabId]: status
            }
          }))
        } catch (error: unknown) {
          // Handle session status errors gracefully
          const axiosError = error as { response?: { status?: number }; message?: string }
          if (axiosError?.response?.status === 404) {
            // Session not found - it may have been cleaned up
            logger.debug('SessionStore', `Session ${tab.sessionId} not found (404) for tab ${tabId}`)
          } else {
            logger.warn('SessionStore', `Failed to fetch session status for tab ${tabId}:`, axiosError.message || 'Unknown error')
          }
          
          if (!isChatIdentityCurrent(identity)) return
          // Set status to null on error
          set((state) => ({
            tabSessionStatus: {
              ...state.tabSessionStatus,
              [tabId]: {
                status: null,
                agentMode: null,
                lastActivity: null
              }
            }
          }))
        }
      },
      
      fetchAllTabSessionStatuses: async (tabIds: string[]) => {
        const identity = captureChatIdentity()
        // First check if there are any active sessions using cache
        try {
          const { getActiveSessions } = get()
          const activeSessions = await getActiveSessions()
          if (!isChatIdentityCurrent(identity)) return
          const activeSessionIds = new Set(activeSessions.map(s => s.session_id))
          
          // If no active sessions, skip all status calls (no need to poll)
          if (activeSessionIds.size === 0) {
            logger.debug('SessionStore', 'No active sessions found, skipping status fetch')
            return
          }
          
          // Filter tabs: only fetch status for tabs that either:
          // 1. Have a sessionId that matches an active session, OR
          // 2. Don't have a sessionId yet (need to check observer to get it)
          const state = get()
          const tabsToFetch = tabIds.filter(tabId => {
            const tab = state.chatTabs[tabId]
            if (!tab) return false
            
            // If tab has sessionId, only fetch if it's in active sessions
            if (tab.sessionId) {
              return activeSessionIds.has(tab.sessionId)
            }
            
            // If tab doesn't have sessionId yet, we still need to check observer
            // to see if it has a session (it might be newly created)
            return true
          })
          
          if (tabsToFetch.length === 0) {
            logger.debug('SessionStore', 'No tabs with active sessions to fetch status for')
            return
          }

          logger.debug('SessionStore', `Fetching status for ${tabsToFetch.length} tabs (${activeSessionIds.size} active sessions)`)
          
          // Fetch status for tabs in parallel
          const { fetchTabSessionStatus } = get()
          const promises = tabsToFetch.map(tabId => fetchTabSessionStatus(tabId))
          await Promise.all(promises)
        } catch (error) {
          if (!isChatIdentityCurrent(identity)) return
          logger.warn('SessionStore', 'Failed to check active sessions, falling back to fetching all tab statuses:', error)
          // Fallback: fetch status for all tabs if active sessions check fails
          const { fetchTabSessionStatus } = get()
          const promises = tabIds.map(tabId => fetchTabSessionStatus(tabId))
          await Promise.all(promises)
        }
      },
      
      getTabSessionStatus: (tabId: string) => {
        return get().tabSessionStatus[tabId]
      },
      
      // Active sessions cache actions
      getActiveSessions: async (forceRefresh = false): Promise<ActiveSessionInfo[]> => {
        const identity = captureChatIdentity()
        const state = get()
        const now = Date.now()

        // Return cached data if it's still fresh and not forcing refresh
        if (!forceRefresh &&
            state.activeSessionsCacheTimestamp !== null &&
            (now - state.activeSessionsCacheTimestamp) < ACTIVE_SESSIONS_CACHE_TTL) {
          return state.activeSessionsCache
        }

        // Share one in-flight request across concurrent callers instead of
        // firing a fresh HTTP request (and a fresh reconciliation pass) per
        // caller -- see pendingActiveSessionsFetch's own comment for why
        // overlapping, out-of-order responses here directly caused the
        // composer's isStreaming state to oscillate.
        if (pendingActiveSessionsFetch) {
          return pendingActiveSessionsFetch
        }

        // Fetch fresh data. Combined endpoint: also carries the workflow
        // schedule header counts, so ModePresetBar no longer needs its own poll.
        const fetchPromise = (async (): Promise<ActiveSessionInfo[]> => {
          try {
            const response = await agentApi.getHeaderSummary()
            if (!isChatIdentityCurrent(identity)) return []
            const activeSessions = response.active_sessions || []
            const resolvedAt = Date.now()

            set((currentState) => ({
              activeSessionsCache: activeSessions,
              activeSessionsCacheTimestamp: resolvedAt,
              workflowScheduleSummary: response.schedule_summary ?? null,
              ...reconcileSessionTabStreamingState(currentState, activeSessions, resolvedAt),
            }))

            return activeSessions
          } catch (error) {
            if (!isChatIdentityCurrent(identity)) return []
            logger.error('SessionStore', 'Failed to fetch active sessions:', error)
            // Return cached data even if stale on error
            return get().activeSessionsCache
          } finally {
            if (isChatIdentityCurrent(identity)) pendingActiveSessionsFetch = null
          }
        })()

        pendingActiveSessionsFetch = fetchPromise
        return fetchPromise
      },
      
      getActiveSessionIds: (): Set<string> => {
        const state = get()
        return new Set(state.activeSessionsCache.map(s => s.session_id))
      },
      
      startActiveSessionsPolling: () => {
        const state = get()
        
        // Don't start if already polling
        if (state.activeSessionsPollingInterval !== null) {
          return
        }
        
        // Initial fetch
        const { getActiveSessions } = get()
        getActiveSessions(true).catch(error => {
          logger.error('Polling', 'Failed to fetch active sessions on polling start:', error)
        })
        
        // Safety-net poll every 60 seconds for surfaces without
        // GlobalActivityMonitor mounted (it already force-refreshes this same
        // data every 5s). Not forced: if the 5s poll already ran within the
        // cache TTL, this tick reuses that cached data instead of firing a
        // redundant duplicate network request.
        const interval = setInterval(() => {
          const { getActiveSessions } = get()
          getActiveSessions().catch(error => {
            logger.error('Polling', 'Failed to fetch active sessions during polling:', error)
          })
        }, 60000)
        
        set({ activeSessionsPollingInterval: interval })
      },
      
      stopActiveSessionsPolling: () => {
        const state = get()
        if (state.activeSessionsPollingInterval !== null) {
          clearInterval(state.activeSessionsPollingInterval)
          set({ activeSessionsPollingInterval: null })
        }
      },
      
      }),
      {
        name: getAccountChatStorageKey(readPersistedChatOwner()),
        storage: chatPersistStorage,
        partialize: selectDurableChatState,
        onRehydrateStorage: () => (_state, error) => {
          // Defer until create() has assigned useChatStore. This also makes tab
          // cleanup/migration part of the hydration contract seen by all callers.
          queueMicrotask(() => finalizeChatStoreHydration(error))
        }
      }
    )
)

if (chatPersistStorage) {
  const flushDurableChatState = () => chatPersistStorage.flush()
  const flushDurableChatStateWhenHidden = () => {
    if (globalThis.document?.visibilityState === 'hidden') flushDurableChatState()
  }
  globalThis.addEventListener?.('pagehide', flushDurableChatState)
  globalThis.addEventListener?.('beforeunload', flushDurableChatState)
  globalThis.document?.addEventListener('visibilitychange', flushDurableChatStateWhenHidden)
} else {
  queueMicrotask(() => finalizeChatStoreHydration())
}

function normalizeHydratedChatStore(): void {
  const state = useChatStore.getState()
  const freshTabs: Record<string, ChatTab> = {}
  let migratedTabConfig = false

  for (const [tabId, tab] of Object.entries(state.chatTabs)) {
    let nextTab = tab
    if (tab.config && tab.config.browserMode === undefined) {
      nextTab = {
        ...tab,
        config: {
          ...tab.config,
          browserMode: tab.config.enableBrowserAccess
            ? (tab.config.useCdp ? 'cdp' : 'headless')
            : 'none',
        },
      }
      migratedTabConfig = true
    }
    if (nextTab.config?.isQueueProcessing) {
      nextTab = { ...nextTab, config: { ...nextTab.config, isQueueProcessing: false } }
      migratedTabConfig = true
    }
    freshTabs[tabId] = nextTab
  }

  const tabsChanged = migratedTabConfig
  const activeTabIsValid = !!state.activeTabId && !!freshTabs[state.activeTabId]
  let activeTabId = state.activeTabId
  if (!activeTabIsValid) {
    const oldestTab = Object.values(freshTabs)
      .sort((a, b) => (a.createdAt || 0) - (b.createdAt || 0))[0]
    activeTabId = oldestTab?.tabId ?? null
  }

  if (tabsChanged || activeTabId !== state.activeTabId) {
    useChatStore.setState({
      ...(tabsChanged ? { chatTabs: freshTabs } : {}),
      ...(activeTabId !== state.activeTabId ? { activeTabId } : {}),
    })
  }
}

function finalizeChatStoreHydration(error?: unknown): void {
  const gateIsPending = chatStoreHydrationGate.snapshot().status === 'pending'
  if (error !== undefined) {
    if (gateIsPending) {
      const result = chatStoreHydrationGate.settle(error)
      console.error('[ChatStore] Failed to hydrate persisted chat state', result.error)
    } else {
      console.error('[ChatStore] Persisted chat state failed after hydration backstop', error)
    }
    return
  }

  try {
    // A backstop releases callers but does not cancel Zustand hydration. Always
    // normalize the eventual persisted state, even if the gate already settled.
    normalizeHydratedChatStore()
    if (gateIsPending) chatStoreHydrationGate.settle()
  } catch (normalizationError) {
    if (gateIsPending) {
      const result = chatStoreHydrationGate.settle(normalizationError)
      console.error('[ChatStore] Failed to normalize hydrated chat state', result.error)
    } else {
      console.error('[ChatStore] Failed to normalize late hydrated chat state', normalizationError)
    }
  }
}

/** Returns a promise that resolves once useChatStore has rehydrated from localStorage. */
export async function waitForChatStoreHydration(): Promise<void> {
  const result = await chatStoreHydrationGate.wait()
  if (
    result.status === 'failed' &&
    result.error instanceof HydrationBackstopError &&
    !chatStoreHydrationBackstopReported
  ) {
    chatStoreHydrationBackstopReported = true
    console.error('[ChatStore] Hydration backstop released blocked callers', result.error)
  }
}

/** Exposes hydration failures to diagnostics without making callers poll. */
export function getChatStoreHydrationSnapshot(): HydrationGateSnapshot {
  return chatStoreHydrationGate.snapshot()
}

/** Save the departing user's tabs, clear memory, then hydrate only the new owner. */
export function switchChatAccount(owner: string | null, forceRefresh = false): void {
  const name = getAccountChatStorageKey(owner)
  if (!forceRefresh && useChatStore.persist.getOptions().name === name) return
  chatPersistStorage?.flush()
  // Reset must not overwrite the saved layout of either account.
  const storage = useChatStore.persist.getOptions().storage
  if (storage) useChatStore.persist.setOptions({ storage: { ...storage, setItem: () => {}, removeItem: () => {} } })
  try { useChatStore.getState().discardChatStateForAccountChange() }
  finally { useChatStore.persist.setOptions({ name, storage }) }
  // This store uses synchronous localStorage; rehydration completes before any
  // new account operation can dispatch. No unowned legacy cache is migrated.
  void useChatStore.persist.rehydrate()
}

/** Called only after the server verifies the persisted account (or single-user mode). */
export function migrateLegacyChatStateForVerifiedAccount(owner: string | null): boolean {
  if (owner !== legacyPersistedOwner) return false
  const destination = getAccountChatStorageKey(owner)
  const legacyKey = getWorkspaceScopedStorageKey('chat-store')
  try {
    const storage = globalThis.localStorage
    if (!storage || storage.getItem(destination)) return false
    const raw = storage.getItem(legacyKey)
    if (!raw) return false
    const saved = JSON.parse(raw)
    if (!saved?.state?.chatTabs || typeof saved.state.chatTabs !== 'object') return false
    chatPersistStorage?.flush()
    storage.setItem(destination, raw)
    storage.removeItem(legacyKey)
    if (useChatStore.persist.getOptions().name === destination) {
      switchChatAccount(owner, true)
    }
    return true
  } catch (error) {
    console.error('[ChatStore] Could not migrate verified account chat tabs', error)
    return false
  }
}

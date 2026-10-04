import { openHistoryExecutionLogs } from '../../utils/historyExecutionLogs'
import { WorkflowGoalSetupBar } from './WorkflowGoalSetupBar'
import { usePointerDrag } from '../../hooks/usePointerDrag'
import React, { useMemo, useCallback, useRef, useEffect, forwardRef, useState } from 'react'
import { useShallow } from 'zustand/react/shallow'
import { PanelLeftOpen, PanelRightOpen, Sparkles } from 'lucide-react'
import { WorkflowCanvas, type WorkflowCanvasRef } from './canvas'
import { useGlobalPresetStore } from '../../stores/useGlobalPresetStore'
import { useModeStore } from '../../stores/useModeStore'
import { normalizeEventViewMode, useChatStore, waitForChatStoreHydration, type ChatTab } from '../../stores/useChatStore'
import { hasSavedWorkflowWorkspaceView, useWorkflowStore } from '../../stores/useWorkflowStore'
import { loadWorkspaceLandingView } from './workspaceLandingView'
import { useWorkspaceStore } from '../../stores/useWorkspaceStore'
import { useWorkflowManifestStore } from '../../stores/useWorkflowManifestStore'
import { resolveWorkflowHistoryPath } from '../../utils/workflowHistoryPath'
import ChatArea, { type ChatAreaRef } from '../ChatArea'
import { useWorkflowProductCommands } from '../../commands/useWorkflowProductCommands'
import { WorkflowChatTabs } from './WorkflowChatTabs'
import { resolveWorkspaceLayout } from './workspaceLayoutResolver'
import { useRunningWorkflowsStore, useShowRunningDrawer } from '../../stores/useRunningWorkflowsStore'
import { useAppStore } from '../../stores/useAppStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { sanitizeDisplayNameForFolder } from '../../utils/workflowUtils'
import { logger } from '../../utils/logger'
import { startRestoredTransportTerminal } from '../../utils/restoredTerminal'
import { isInternalChildSession, isScheduledSession, shouldDiscoverWorkflowChatTab } from '../../utils/workflowSessionKinds'
import { activeWorkflowTabHasCachedConversation, activeWorkflowTabIdForPreset } from '../../utils/workflowTabOwnership'
import { activateTab } from '../../utils/activateTab'
import { findLatestRestorableWorkflowChatSession, isRestorableWorkflowChatSession, resolveLatestWorkflowChatAction } from '../../utils/latestWorkflowChat'
import {
  reconcileWorkflowRuntimeTab,
  shouldCatchUpRunningWorkflowTranscript,
  workflowRuntimeTabProjection,
} from './workflowRuntimeTabProjection'
import { isBlankWorkflowBuilderTab, resolveWorkflowTabForSession } from '../../utils/workflowTabResolution'
import {
  PreviousChatHistoryPanel,
  chatHistoryConversationPath,
  chatHistoryRuntimeLabel,
  chatHistorySupportsNativeResume,
  chatHistoryUsesTerminalRestore,
  chatHistoryWorkshopModeLabel,
} from '../PreviousChatHistoryPanel'
import { chatHistorySessionTitle } from '../../utils/chatHistoryTitle'
import { chatHistoryOpenDisposition } from '../../utils/chatHistoryOpenDisposition'
import { chatHistoryWorkshopMode } from '../../utils/chatHistoryWorkshopMode'
import {
  REPORT_PREVIEW_PREFERENCE_CHANGED_EVENT,
  readReportPreviewPreference,
  readWorkflowSplitPreference,
  type ReportPreviewDevice,
  writeReportPreviewPreference,
  writeWorkflowSplitPreference,
} from '../../utils/reportPreviewPreference'
import { WorkspaceSplitRail } from '../workspace/WorkspaceSplitDivider'
import { RelayIntro } from './RelayIntro'
import { AutomationHubPanel } from '../automation/AutomationHubPanel'

// Helper component to get observerId and render ChatArea
// Always renders ChatArea (even without observerId) so it can handle initialization
const ChatAreaWithObserverId = forwardRef<ChatAreaRef, {
  onNewChat: () => void
  hideHeader?: boolean
  hideInput?: boolean
  compact?: boolean
  workflowLandingContent?: React.ReactNode
  showNewChatAction?: boolean
}>(({ onNewChat, hideHeader, hideInput, compact, workflowLandingContent, showNewChatAction }, ref) => {
  // Prefer the active workflow tab when one is selected. The tab strip keeps
  // active workflow tabs visible even while preset metadata is catching up
  // after reload; ChatArea must use the same rule or the input area disappears.
  // Legacy/restored builder tabs may not have presetQueryId, so allow those
  // when there is no exact tab for the active preset.
  const currentPresetId = useGlobalPresetStore(state => state.activePresetIds.workflow)
  const workflowTabId = useChatStore(state =>
    activeWorkflowTabIdForPreset(state.activeTabId, currentPresetId, state.chatTabs)
  )
  const activePhaseId = useChatStore(state => {
    const tabId = activeWorkflowTabIdForPreset(state.activeTabId, currentPresetId, state.chatTabs)
    return tabId ? state.chatTabs[tabId]?.metadata?.phaseId : undefined
  })

  // Show chat input for chat-compatible phases
  const effectiveHideInput = isChatCompatiblePhase(activePhaseId) ? false : hideInput

  // The agent's open_workspace_view calls open the toolbar's views here.
  useWorkflowViewPresentations(workflowTabId)

  const isRelaySurface = useProductSurfaceStore(state => state.productSurface === 'relays')
  useWorkflowProductCommands(isRelaySurface)

  return (
    <ChatArea
      ref={ref}
      onNewChat={onNewChat}
      hideHeader={hideHeader}
      hideInput={effectiveHideInput}
      compact={compact}
      workflowLandingContent={workflowLandingContent}
      showNewChatAction={showNewChatAction}
      // Pass null (not undefined) when no tab matches the active workflow preset.
      // Otherwise ChatArea falls back to the global activeTabId and can briefly
      // render the previous workflow's blocking human-feedback/auth prompt.
      tabId={workflowTabId ?? null}
    />
  )
})

const WorkflowNewChatGuide: React.FC<{ relayMode?: boolean }> = ({ relayMode = false }) => (
  <div className="flex h-full min-h-0 items-center justify-center overflow-y-auto px-6 py-10">
    <div className="w-full max-w-lg rounded-xl border border-border bg-muted/20 p-5">
      <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
        <Sparkles className="h-4 w-4 text-primary" />
        {relayMode ? 'Build your Relay in chat' : 'Start your workflow chat'}
      </div>
      <p className="mt-2 text-sm leading-6 text-muted-foreground">
        {relayMode ? 'Describe the input, agents, scripts, and JSON output you want. Ask the builder to:' : 'This is your persistent conversation for this workflow. Ask the builder to:'}
      </p>
      <ul className="mt-3 space-y-2 text-sm text-muted-foreground">
        {relayMode ? <>
          <li>• Add agents with your own system prompts and message templates</li>
          <li>• Connect them with scripts and define the final JSON output</li>
          <li>• Run a test with sample JSON and inspect the result and logs</li>
          <li>• Set up API triggers or schedules</li>
        </> : <>
          <li>• Build or change the workflow and its plan</li>
          <li>• Create a schedule, webhook, bot, dashboard, or database</li>
          <li>• Review a run, investigate a problem, or improve the workflow</li>
        </>}
      </ul>
    </div>
  </div>
)

const RELAY_WORKSPACE_VIEWS = new Set(['flow', 'workshop', 'costs', 'execution-logs', 'files', 'identity', 'mcp', 'access'])
import { agentApi, workflowManifestApi } from '../../services/api'
import {
  type ActiveSessionInfo,
  type ChatHistorySession,
  type ExecutionOptions,
  type PollingEvent,
  type RunningWorkflowInfo,
  type WorkflowContractUpgradeStatus,
} from '../../services/api-types'
import { findOrCreateWorkflowTab, isChatCompatiblePhase } from '../../utils/chatSubmitHelpers'
import { useWorkflowViewPresentations } from './useWorkflowViewPresentations'
import { appendRestoredLiveTail, hydrateTabEvents } from '../../utils/sessionRestore'
import { hydrateExecutionConversation } from '../../utils/executionConversationRestore'
import { isExecutionConversationTab } from '../../utils/sessionEventSource'
import { resolveLiveInputConfirmations } from '../../utils/liveInputReceipt'
import {
  hydrateWorkflowTabsPrioritized,
  isReadOnlyWorkflowRunTab,
  liveWorkflowSessionsForReconnect,
  workflowTabsNeedingHydration,
} from '../../utils/workflowTabHydration'
import { isVisibleActivitySession } from '../../utils/activitySessions'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'
import { isPreviewView, isWorkspacePaneView } from './workspaceViews'
import { MANUAL_CONTRACT_UPGRADE_MESSAGE, WORKFLOW_CONTRACT_UPGRADE_CHAT_EVENT, WORKFLOW_CONTRACT_UPGRADE_STATUS_EVENT, type WorkflowContractUpgradeChatEvent } from './workflowContractUpgradeEvents'
// Inactive workflow tabs hydrate lazily and fall back to workflow-scoped chat history.

const WORKFLOW_RESTORE_TIMEOUT_MS = 8000
function normalizeWorkflowPath(path?: string | null): string {
  return (path || '').replace(/\/+$/, '')
}

function defaultWorkflowSplitRatio(device: ReportPreviewDevice, width = typeof window === 'undefined' ? 1280 : window.innerWidth): number {
  if (device === 'mobile') return clampWorkflowSplitRatio((width - 480) / width, width)
  if (device === 'desktop') return clampWorkflowSplitRatio(380 / width, width)
  return 0.5
}

function clampWorkflowSplitRatio(ratio: number, width: number): number {
  const minPaneWidth = 240
  const minRatio = Math.max(0.15, Math.min(0.5, minPaneWidth / Math.max(width, minPaneWidth * 2)))
  const maxRatio = Math.min(0.85, 1 - minRatio)
  return Math.max(minRatio, Math.min(maxRatio, ratio))
}

function workflowTabSortTimestamp(tab: ChatTab): number {
  return tab.lastAccessedAt ?? tab.createdAt ?? 0
}

function isInteractiveWorkflowTab(tab: ChatTab): boolean {
  return tab.metadata?.mode === 'workflow' && tab.metadata?.isViewOnly !== true
}

function applyRestoredWorkflowConversationConfig(tabId: string, session: ChatHistorySession): void {
  const chatStore = useChatStore.getState()
  const path = chatHistoryConversationPath(session)
  const useTerminalRestore = chatHistoryUsesTerminalRestore(session)
  const useNativeResume = chatHistorySupportsNativeResume(session)
  const existingContext = chatStore.getTabConfig(tabId)?.fileContext || []
  // The server owns resume/fallback context through restoredConversationPath.
  // Never expose the canonical conversation JSON as a browser file attachment:
  // mature chats can be hundreds of megabytes even though restore needs only
  // the compact user/assistant projection.
  const nextFileContext = existingContext.filter(item => item.path !== path)

  chatStore.setTabConfig(tabId, {
    fileContext: nextFileContext,
    restoredConversationPath: path,
    restoredConversationSummary: undefined,
    restoredConversationTitle: chatHistorySessionTitle(session),
    restoredConversationWorkshopModeLabel: chatHistoryWorkshopModeLabel(session),
    restoredConversationRuntimeLabel: chatHistoryRuntimeLabel(session),
    restoredConversationNativeResume: useTerminalRestore || useNativeResume,
  })
  chatStore.setTabMetadata(tabId, { workshopMode: chatHistoryWorkshopMode(session) })
}

function isRunningWorkflowEntry(entry: RunningWorkflowInfo): boolean {
  const status = (entry.status || '').toLowerCase().trim()
  if (!status) return true
  return (
    status === 'running' ||
    status === 'active' ||
    status === 'in_progress' ||
    status === 'paused' ||
    status === 'waiting' ||
    status === 'waiting_feedback' ||
    status === 'waiting_for_input' ||
    status === 'idle' ||
    entry.needs_user_input === true
  )
}

function runningWorkflowBelongsToPreset(
  entry: RunningWorkflowInfo,
  presetId: string,
  workspacePath?: string | null,
): boolean {
  if (entry.preset_query_id) {
    return entry.preset_query_id === presetId
  }
  return Boolean(
    workspacePath &&
    entry.workspace_path &&
    normalizeWorkflowPath(entry.workspace_path) === normalizeWorkflowPath(workspacePath),
  )
}

// Adapts the already-fetched active-sessions cache (kept fresh by
// GlobalActivityMonitor's 5s poll) into the shape the running-workflow tab
// reconciler needs, so it doesn't have to run its own independent
// /api/workflow/running poll for the same underlying tracked-execution data.
function activeSessionToRunningWorkflowInfo(session: ActiveSessionInfo): RunningWorkflowInfo {
  return {
    query_id: session.session_id,
    session_id: session.session_id,
    preset_query_id: session.preset_query_id,
    preset_name: session.preset_name,
    workspace_path: session.workspace_path || '',
    phase_id: session.phase_id,
    phase_name: session.phase_name,
    status: session.status,
    title: session.title,
    query: session.query,
    triggered_by: session.triggered_by || '',
    started_at: session.created_at,
    needs_user_input: session.needs_user_input,
    waiting_message: session.waiting_message,
    waiting_since: session.waiting_since,
    runtime_state: session.runtime_state,
    display_status: session.display_status,
  }
}

const WorkflowPreviousChatsPanel: React.FC<{
  workspacePath: string
  onHasChatsChange?: (hasChats: boolean) => void
  // When true the panel fills the right-side Workshop workspace view.
  primary?: boolean
  chatOnly?: boolean
  refreshToken?: number
}> = ({ workspacePath, onHasChatsChange, primary = false, chatOnly = false, refreshToken = 0 }) => {
  const activeTabId = useChatStore(state => state.activeTabId)
  const activePresetId = useGlobalPresetStore(state => state.activePresetIds.workflow)
  const setShowChatArea = useWorkflowStore(state => state.setShowChatArea)
  const activeSessionId = useChatStore(state => {
    const tabId = state.activeTabId
    const tab = tabId ? state.chatTabs[tabId] : undefined
    if (!tab?.sessionId || tab.metadata?.mode !== 'workflow') return undefined
    // A workflow owns one persistent chat even before its events have been
    // rehydrated. Workshop must identify that open chat from the tab itself;
    // tying it to event hydration caused the empty-history UI to flash or
    // remain visible whenever restore was slow or failed.
    return tab.sessionId
  })
  const setTabConfig = useChatStore(state => state.setTabConfig)
  const addToast = useChatStore(state => state.addToast)

  // Opens a saved row into the workflow's one persistent Chat.
  const resumeChatSessionIntoTab = useCallback(async (
    session: ChatHistorySession,
    startingTabId: string,
    disposition: ReturnType<typeof chatHistoryOpenDisposition>,
  ) => {
    let targetTabId = startingTabId
    const chatStore = useChatStore.getState()
    const restoredWorkshopMode = chatHistoryWorkshopMode(session)
    let targetTab = chatStore.chatTabs[targetTabId]
    const targetPresetId = targetTab?.metadata?.presetQueryId

    if (
      !targetTab ||
      targetTab.metadata?.mode !== 'workflow' ||
      targetTab.metadata?.isViewOnly === true ||
      (activePresetId && targetPresetId && targetPresetId !== activePresetId) ||
      isBlankWorkflowBuilderTab(targetTab, activePresetId || '', chatStore.tabEvents)
    ) {
      // Restore must target the persistent interactive Chat, never a
      // full-run/Schedule tab (reusing one corrupts its presentation metadata).
      const latestStore = useChatStore.getState()
      targetTabId = (await resolveWorkflowTabForSession({
        getTabs: () => useChatStore.getState().chatTabs,
        getTabEvents: () => useChatStore.getState().tabEvents,
        presetQueryId: activePresetId || '',
        sessionId: session.session_id,
        name: 'Automation Builder',
        metadata: { mode: 'workflow', phaseId: 'workflow-builder', phaseName: 'Automation Builder', presetQueryId: activePresetId || undefined, workshopMode: restoredWorkshopMode },
        createChatTab: latestStore.createChatTab,
        updateTabSessionId: latestStore.updateTabSessionId,
      })).tabId
      targetTab = useChatStore.getState().chatTabs[targetTabId]
    }

    if (!targetTab) {
      addToast('Failed to resume previous chat', 'error')
      return
    }

    if (activePresetId && targetTab.metadata?.presetQueryId !== activePresetId) {
      chatStore.setTabMetadata(targetTabId, {
        phaseId: targetTab.metadata?.phaseId || 'workflow-builder',
        phaseName: targetTab.metadata?.phaseName || 'Automation Builder',
        presetQueryId: activePresetId,
      })
    }
    chatStore.setTabMetadata(targetTabId, { workshopMode: restoredWorkshopMode })

    if (targetTab?.sessionId !== session.session_id && targetTab?.sessionId) {
      chatStore.resetTabChat(targetTabId)
    }
    chatStore.updateTabSessionId(targetTabId, session.session_id)

    const path = chatHistoryConversationPath(session)
    const useTerminalRestore = disposition === 'interactive-transport' && chatHistoryUsesTerminalRestore(session)
    const useNativeResume = disposition === 'interactive-transport' && chatHistorySupportsNativeResume(session)
    const existingContext = useChatStore.getState().getTabConfig(targetTabId)?.fileContext || []
    const nextFileContext = existingContext.filter(item => item.path !== path)

    setTabConfig(targetTabId, {
      fileContext: nextFileContext,
      restoredConversationPath: path,
      restoredConversationSummary: undefined,
      restoredConversationTitle: chatHistorySessionTitle(session),
      restoredConversationWorkshopModeLabel: chatHistoryWorkshopModeLabel(session),
      restoredConversationRuntimeLabel: chatHistoryRuntimeLabel(session),
      restoredConversationNativeResume: useTerminalRestore || useNativeResume,
    })
    // A resume started from the Builder landed in a different (Chat) tab;
    // show it. Reused idle tabs aren't activated by resolution the way
    // freshly created ones are.
    if (targetTabId !== startingTabId) {
      activateTab(targetTabId)
      setShowChatArea(true)
    }
    // Both tmux terminal-restore and native-resume sessions reattach into a
    // coding-agent terminal on the backend. Keep that transport restoration,
    // but present its normalized event transcript by default; Raw remains an
    // explicit diagnostic choice rather than the first thing a user sees.
    if (useTerminalRestore || useNativeResume) {
      chatStore.setTabViewMode(targetTabId, 'formatted')
      activateTab(targetTabId)
      setShowChatArea(true)
      startRestoredTransportTerminal(session.session_id, path, session.session_id, workspacePath)
    }
  }, [activePresetId, addToast, setShowChatArea, setTabConfig, workspacePath])

  const handleResumePreviousChat = useCallback(async (session: ChatHistorySession) => {
    const disposition = chatHistoryOpenDisposition(session)
    if (disposition === 'read-only-schedule' || disposition === 'read-only-history') {
      const isSchedule = disposition === 'read-only-schedule'
      const transcriptLabel = isSchedule ? 'Schedule' : session.bot_platform ? 'Bot chat' : 'History'
      const chatStore = useChatStore.getState()
      const scheduleMetadata: NonNullable<ChatTab['metadata']> = {
        mode: 'workflow',
        presetQueryId: activePresetId || undefined,
        isViewOnly: true,
        isExecutionRun: isSchedule || Boolean(session.bot_platform),
        isScheduledRun: isSchedule,
        isBotRun: Boolean(session.bot_platform),
        botPlatform: session.bot_platform,
        scheduledJobName: isSchedule ? 'Schedule' : undefined,
        readOnlyRestoredAt: Date.now(),
        userInteractiveContinuation: false,
      }
      // Same resolution as every other opener: the tab already on this
      // session, else this schedule's finished lane, else a new tab bound
      // to the session at creation (it used to be minted with a random id
      // and re-pointed afterwards).
      const { tabId: targetTabId, via } = await resolveWorkflowTabForSession({
        getTabs: () => useChatStore.getState().chatTabs,
        presetQueryId: activePresetId || '',
        sessionId: session.session_id,
        name: transcriptLabel,
        metadata: scheduleMetadata,
        createChatTab: chatStore.createChatTab,
        updateTabSessionId: chatStore.updateTabSessionId,
      })
      if (via !== 'created') chatStore.setTabMetadata(targetTabId, scheduleMetadata)
      chatStore.setTabViewMode(targetTabId, 'formatted')
      chatStore.setTabStreaming(targetTabId, false)
      chatStore.setTabCompleted(targetTabId, true)
      chatStore.setTabHasRunningBgAgents(targetTabId, false)
      chatStore.setTabSyntheticTurn(targetTabId, false)
      chatStore.setTabCanSteer(targetTabId, false)

      try {
        const runtime = scheduleMetadata.isExecutionRun
          ? await hydrateExecutionConversation(session.session_id, workspacePath)
          : await hydrateTabEvents(session.session_id, { workspacePath })
        chatStore.setTabStreaming(targetTabId, runtime.status === 'running')
        chatStore.setTabCompleted(targetTabId, runtime.status !== 'running')
        chatStore.setTabHasRunningBgAgents(targetTabId, runtime.hasRunningBackgroundAgents ?? false)
        chatStore.setTabSyntheticTurn(targetTabId, runtime.isSyntheticTurn ?? false)
        chatStore.setTabCanSteer(targetTabId, false)
      } catch (error) {
        logger.warn('WorkflowLayout', 'Failed to restore scheduled-run transcript', {
          sessionId: session.session_id,
          error,
        })
        addToast(`Failed to open the saved ${isSchedule ? 'schedule' : 'bot'} transcript`, 'error')
        return
      }

      activateTab(targetTabId)
      setShowChatArea(true)
      openHistoryExecutionLogs(session)
      return
    }

    if (!activeTabId) {
      addToast('No active automation chat to resume in', 'error')
      return
    }
    await resumeChatSessionIntoTab(session, activeTabId, disposition)
  }, [activeTabId, addToast, resumeChatSessionIntoTab, setShowChatArea, workspacePath, activePresetId])

  return (
    <PreviousChatHistoryPanel
      workspacePath={workspacePath}
      activeSessionId={activeSessionId ?? undefined}
      // The active tab is the conversation workspace. The panel beneath it
      // is only a selector for recent conversations / schedules / bots, so a
      // second "Previous automation chats" heading just repeated the tab.
      title=""
      actionLabel="Open"
      emptyText="No previous automation chats yet."
      onHasChatsChange={onHasChatsChange}
      onSelectSession={handleResumePreviousChat}
      compact={!primary}
      fill={primary}
      showAll={primary}
      recentOnly={chatOnly}
      // Automation > Chats offers the origin filters (History, Schedules,
      // Bots, Triggers) over the workflow's past conversations. The Schedules
      // and Triggers tabs remain the run-centric views with status and
      // delivery metadata.
      includeAutomationChats={chatOnly}
      refreshToken={refreshToken}
    />
  )
}

function workflowSessionMatchesPreset(session: ActiveSessionInfo, presetId: string, workspacePath?: string | null): boolean {
  if (session.agent_mode !== 'workflow' && session.agent_mode !== 'workflow_phase') return false

  if (session.preset_query_id && session.preset_query_id === presetId) return true

  const targetWorkspace = normalizeWorkflowPath(workspacePath)
  return !!targetWorkspace && normalizeWorkflowPath(session.workspace_path) === targetWorkspace
}

function isLiveWorkflowSessionForPreset(session: ActiveSessionInfo, presetId: string, workspacePath?: string | null): boolean {
  if (!workflowSessionMatchesPreset(session, presetId, workspacePath)) return false

  const status = (session.status || '').toLowerCase().trim()
  return (
    session.needs_user_input === true ||
    session.has_running_background_agents === true ||
    (session.running_background_agent_count ?? 0) > 0 ||
    status === 'running' ||
    status === 'active' ||
    status === 'in_progress' ||
    status === 'paused' ||
    status === 'waiting' ||
    status === 'waiting_feedback'
  )
}

/**
 * The persistent Chat always exists for a workflow. Resolve the current one
 * or create the first-time empty Chat. Creating activates it, so callers that
 * already chose another read-only run can preserve that selection.
 */
async function ensureWorkflowBuilderTab(presetId: string, options?: { keepSelection?: boolean }): Promise<string> {
  const store = useChatStore.getState()
  const previousActiveTabId = store.activeTabId
  const { tabId, via } = await resolveWorkflowTabForSession({
    getTabs: () => useChatStore.getState().chatTabs,
    getTabEvents: () => useChatStore.getState().tabEvents,
    presetQueryId: presetId,
    name: 'Automation Builder',
    metadata: { mode: 'workflow', phaseId: 'workflow-builder', phaseName: 'Automation Builder', presetQueryId: presetId },
    createChatTab: store.createChatTab,
    updateTabSessionId: store.updateTabSessionId,
  })
  if (options?.keepSelection && via === 'created' && previousActiveTabId && previousActiveTabId !== tabId) {
    activateTab(previousActiveTabId)
  }
  return tabId
}

// restoreWorkflowStateFromEvents has no timeout of its own (it awaits
// agentApi.getRecentSessionEvents / hydrateTabEvents directly), so every
// caller that increments restoringWorkflowSessions via
// beginWorkflowSessionRestore MUST wrap its restore call in this helper --
// otherwise a hung request leaves that counter incremented forever and the
// chat pane stuck on "Restoring previous session..." with no way out.
function withWorkflowRestoreTimeout<T>(promise: Promise<T>, label: string, timeoutMs = WORKFLOW_RESTORE_TIMEOUT_MS): Promise<T> {
  return new Promise((resolve, reject) => {
    const timeout = window.setTimeout(() => {
      reject(new Error(`${label} timed out after ${timeoutMs}ms`))
    }, timeoutMs)

    promise.then(
      value => {
        window.clearTimeout(timeout)
        resolve(value)
      },
      error => {
        window.clearTimeout(timeout)
        reject(error)
      }
    )
  })
}

/**
 * Helper function to hydrate a workflow tab's transcript from loaded events.
 * Called during workflow reconnection. (Canvas step-status restoration was
 * removed with the step-status machinery on 2026-09-22; activeTabOnly is
 * kept for call-site compatibility.)
 */
async function restoreWorkflowStateFromEvents(
  sessionId: string,
  workspacePath?: string | null,
  allowChatHistoryFallback = false,
  activeTabOnly = false,
): Promise<void> {
  try {
    void activeTabOnly
    const { setTabEvents, setTabLastEventIndex, getTabLastEventIndex, getTabEvents } = useChatStore.getState()

    let events: PollingEvent[] = []
    let lastIndex = -1

    // Load events for this session from the in-memory EventStore. If that
    // buffer has expired, workflow builder chats can still restore their
    // visible transcript from the workflow-scoped conversation file.
    if (allowChatHistoryFallback) {
      await hydrateTabEvents(sessionId, {
        workspacePath: workspacePath || undefined,
        fallbackToChatHistory: true,
        preferChatHistory: true,
      })
      events = getTabEvents(sessionId)
      lastIndex = getTabLastEventIndex(sessionId)
    } else {
      const response = await agentApi.getRecentSessionEvents(sessionId)
      events = response.events as PollingEvent[]
      lastIndex = response.last_processed_index ?? events.length - 1
    }

    if (events.length === 0) {
      return
    }

    // Use setTabEvents (replace) when tab is empty (restoration), addTabEvents (append) when live
    const existingEvents = getTabEvents(sessionId)
    if (!allowChatHistoryFallback) {
      if (existingEvents.length === 0) {
        setTabEvents(sessionId, resolveLiveInputConfirmations(events))
      } else {
        appendRestoredLiveTail(sessionId, events)
      }
    }
    // CRITICAL: Use last_processed_index from backend (not events.length - 1)
    // Backend tracks the actual event index which may be higher due to filtering/cleanup
    // Only advance the index if backend is ahead (SSE may have already advanced it)
    const currentIndex = getTabLastEventIndex(sessionId)
    if (lastIndex > currentIndex) {
      setTabLastEventIndex(sessionId, lastIndex)
    }
  } catch (error) {
    logger.warn('WorkflowLayout', 'Failed to restore workflow state:', error)
  }
}

interface WorkflowLayoutProps {
  className?: string
  onCreatePlan?: () => void
  onNewChat: () => void
}

/**
 * Main layout component for workflow mode
 * Shows React Flow canvas as the main area with ChatArea appearing when a phase is started
 * Uses useWorkflowStore for activePhase and showChatArea state (single source of truth)
 */
export const WorkflowLayout: React.FC<WorkflowLayoutProps> = ({
  className = '',
  onCreatePlan,
  onNewChat
}) => {
  const selectedModeCategory = useModeStore(state => state.selectedModeCategory)
  const isRelaySurface = useProductSurfaceStore(state => state.productSurface === 'relays')
  // Narrow selectors: bare useChatStore() re-renders on every store update (10x/sec with 2 parallel sessions)
  const currentWorkflowPhase = useChatStore(state => state.currentWorkflowPhase)
  const setCurrentWorkflowPhase = useChatStore(state => state.setCurrentWorkflowPhase)
  // Use workflow store for UI state (single source of truth)
  const activePhase = useWorkflowStore(state => state.activePhase)
  const showChatArea = useWorkflowStore(state => state.showChatArea)
  const showWorkspacePane = useWorkflowStore(state => state.showWorkspacePane)
  const focusedPane = useWorkflowStore(state => state.focusedPane)
  const setShowChatArea = useWorkflowStore(state => state.setShowChatArea)
  const setShowWorkspacePane = useWorkflowStore(state => state.setShowWorkspacePane)
  const setFocusedPane = useWorkflowStore(state => state.setFocusedPane)
  const workflowWorkspaceView = useWorkflowStore(state => state.workflowWorkspaceView)
  const setWorkflowWorkspaceView = useWorkflowStore(state => state.setWorkflowWorkspaceView)
  const lastCanvasView = useWorkflowStore(state => state.lastCanvasView)
  const minimizeWorkflow = useRunningWorkflowsStore(state => state.minimizeWorkflow)
  const showRunningDrawer = useShowRunningDrawer()

  const getPhaseById = useWorkflowStore(state => state.getPhaseById)
  
  // Ref for the ChatArea component
  const chatAreaRef = useRef<ChatAreaRef>(null)
  // Ref for the WorkflowCanvas component (for triggering refresh)
  const canvasRef = useRef<WorkflowCanvasRef>(null)
  // Store pending query to submit after ChatArea mounts
  const pendingQueryRef = useRef<{ query: string; executionOptions?: ExecutionOptions } | null>(null)
  // A background refresh of a transcript that is already on screen is not a
  // load the user waits for, so the banner shows only while the tab is empty.
  const isActiveWorkflowSessionRestoring = useChatStore(state => {
    const tab = state.activeTabId ? state.chatTabs[state.activeTabId] : undefined
    return !!tab?.sessionId &&
      (state.restoringWorkflowSessions[tab.sessionId] ?? 0) > 0 &&
      (state.tabEvents[tab.sessionId]?.length ?? 0) === 0
  })
  const revealWorkflowChat = useCallback((tabId: string) => {
    const chatStore = useChatStore.getState()
    if (chatStore.chatTabs[tabId]) {
      chatStore.setTabViewMode(tabId, 'formatted')
      activateTab(tabId)
    }
    setShowChatArea(true)
    setShowWorkspacePane(true)
    setFocusedPane('chat')
  }, [setFocusedPane, setShowChatArea, setShowWorkspacePane])
  // Track the previous preset ID for auto-minimize on preset switch
  const previousPresetIdRef = useRef<string | null>(null)
  const pendingReadOnlyRestoreRef = useRef<{ presetId: string | null; tabId: string } | null>(null)
  useEffect(() => {
    const handleReadOnlyRestore = (event: Event) => {
      const detail = (event as CustomEvent<{ presetId?: string | null; tabId?: string }>).detail
      if (!detail?.tabId) return
      pendingReadOnlyRestoreRef.current = {
        presetId: detail.presetId ?? null,
        tabId: detail.tabId,
      }
      revealWorkflowChat(detail.tabId)
    }

    window.addEventListener('workflow-readonly-run-restored', handleReadOnlyRestore)
    return () => window.removeEventListener('workflow-readonly-run-restored', handleReadOnlyRestore)
  }, [revealWorkflowChat])
  // During workflow execution we do not synchronize the file tree event-by-event.
  // The Workspace component marks the view stale after completed workspace work.

  // Get selected run folder and workspace functions (defined early for use in useEffect)
  const selectedRunFolder = useWorkflowStore(state => state.selectedRunFolder)
  const setStepOverride = useWorkflowStore(state => state.setStepOverride)
  const selectedGroupIds = useWorkflowStore(state => state.selectedGroupIds)
  const variablesManifest = useWorkflowStore(state => state.variablesManifest)
  const { fetchFiles, setExpandedFolders } = useWorkspaceStore(useShallow(state => ({
    fetchFiles: state.fetchFiles,
    setExpandedFolders: state.setExpandedFolders,
  })))
  // Subscribe to workspace minimized state so we can skip fetches when panel is hidden
  const workspaceMinimized = useAppStore(state => state.workspaceMinimized)
  const setWorkspaceMinimized = useAppStore(state => state.setWorkspaceMinimized)
  const lastWorkspaceRunExpansionKeyRef = useRef<string | null>(null)
  const reportAutoMinimizedWorkspaceRef = useRef(false)
  const prevWorkflowWorkspaceViewRef = useRef<string | null>(null)

  const workflowHydrationsInFlight = useRef(new Map<string, Promise<void>>())
  // Tabs of the opened workflow that were not selected: loaded when opened.
  const deferredWorkflowHydrations = useRef(new Map<string, () => Promise<void>>())
  useEffect(() => useChatStore.subscribe((current, previous) => {
    if (current.activeTabId === previous.activeTabId || !current.activeTabId) return
    const sessionId = current.chatTabs[current.activeTabId]?.sessionId
    const load = sessionId ? deferredWorkflowHydrations.current.get(sessionId) : undefined
    if (!sessionId || !load) return
    deferredWorkflowHydrations.current.delete(sessionId)
    void load().catch(error => logger.warn('WorkflowLayout', `Failed to restore ${sessionId}:`, error))
  }), [])
  const rehydrateWorkflowTabs = useCallback(async (tabs: ChatTab[], currentWorkspacePath?: string | null) => {
    // Interactive builder tabs must reconcile against durable history even
    // when the volatile EventStore already supplied a non-empty tail. That
    // tail is capped and can stop before transcript-synced final messages.
    const tabsToHydrate = workflowTabsNeedingHydration(
      tabs,
      sessionId => useChatStore.getState().getTabEvents(sessionId),
    )
    if (tabsToHydrate.length === 0) return 0

    // Start runtime metadata alongside transcripts, never before them.
    const activeSessionsPromise = withWorkflowRestoreTimeout(
      useChatStore.getState().getActiveSessions(), 'Fetching active workflow sessions',
    ).catch(error => {
      logger.warn('WorkflowLayout', 'Failed to fetch active sessions during rehydrate:', error)
      return []
    })
    let historyPromise: Promise<Map<string, ChatHistorySession>> | undefined
    const getWorkflowHistoryBySession = () => {
      if (!historyPromise) {
        historyPromise = (async () => {
          const history = new Map<string, ChatHistorySession>()
          if (!currentWorkspacePath) return history
          try {
            const response = await withWorkflowRestoreTimeout(
              agentApi.listChatHistorySessions(100, 0, currentWorkspacePath), 'Restoring workflow conversation metadata',
            )
            for (const session of response.sessions || []) {
              if (isRestorableWorkflowChatSession(session)) history.set(session.session_id, session)
            }
          } catch (error) {
            logger.warn('WorkflowLayout', 'Failed to load workflow chat history metadata:', error)
          }
          return history
        })()
      }
      return historyPromise
    }
    const hydrate = (tab: ChatTab): Promise<void> => {
      const sessionId = tab.sessionId!
      // The user may close or replace a queued tab before its worker starts.
      if (useChatStore.getState().chatTabs[tab.tabId]?.sessionId !== sessionId) return Promise.resolve()
      const existing = workflowHydrationsInFlight.current.get(sessionId)
      if (existing) return existing
      const stillOwnsSession = () => useChatStore.getState().chatTabs[tab.tabId]?.sessionId === sessionId
      const task = (async () => {
        useChatStore.getState().beginWorkflowSessionRestore(sessionId)
        try {
          if (isReadOnlyWorkflowRunTab(tab)) {
            const runtime = await withWorkflowRestoreTimeout(
              isExecutionConversationTab(tab)
                ? hydrateExecutionConversation(sessionId, currentWorkspacePath || undefined)
                : hydrateTabEvents(sessionId, { workspacePath: currentWorkspacePath || undefined }),
              `Restoring scheduled run transcript for ${sessionId}`,
            )
            if (stillOwnsSession()) {
              useChatStore.getState().setTabStreaming(tab.tabId, runtime.status === 'running')
              useChatStore.getState().setTabCompleted(tab.tabId, runtime.status === 'completed' || runtime.status === 'stopped')
            }
          } else {
            await withWorkflowRestoreTimeout(
              restoreWorkflowStateFromEvents(sessionId, currentWorkspacePath, true, true),
              `Restoring workflow events for ${sessionId}`,
            )
          }
        } finally {
          useChatStore.getState().endWorkflowSessionRestore(sessionId)
        }

        // Status/config enrichment must not hold the transcript spinner or the
        // selected-tab promise open. Check ownership after every async boundary.
        void (async () => {
          const activeSessions = await activeSessionsPromise
          if (!stillOwnsSession()) return
          if (activeSessions.some(session => session.session_id === sessionId &&
            (session.agent_mode === 'workflow' || session.agent_mode === 'workflow_phase') &&
            isVisibleActivitySession(session))) {
            useChatStore.getState().setTabStreaming(tab.tabId, true)
          } else if (!isReadOnlyWorkflowRunTab(tab)) {
            const history = await getWorkflowHistoryBySession()
            const session = history.get(sessionId)
            if (session && stillOwnsSession() && useChatStore.getState().getTabEvents(sessionId).length > 0) {
              // A user may have started another turn while metadata was loading.
              if (!useChatStore.getState().chatTabs[tab.tabId]?.isStreaming) {
                applyRestoredWorkflowConversationConfig(tab.tabId, session)
              }
            }
          }
        })().catch(error => logger.warn('WorkflowLayout', 'Workflow metadata enrichment failed:', error))
      })()
      workflowHydrationsInFlight.current.set(sessionId, task)
      const release = () => { workflowHydrationsInFlight.current.delete(sessionId) }
      void task.then(release, release)
      return task
    }
    return hydrateWorkflowTabsPrioritized(tabsToHydrate, useChatStore.getState().activeTabId, hydrate,
      (tab, error) => logger.warn('WorkflowLayout', `Failed to restore ${tab.sessionId}:`, error),
      tab => { deferredWorkflowHydrations.current.set(tab.sessionId!, () => hydrate(tab)) })
  }, [])

  // Get active workflow preset (file-backed manifests, not DB presets)
  const activePresetId = useGlobalPresetStore(state => state.activePresetIds.workflow)
  const activeWorkflowPreset = useGlobalPresetStore(state => {
    const presetId = state.activePresetIds.workflow
    return presetId ? state.workflowPresets.find(preset => preset.id === presetId) ?? null : null
  })
  // The manifest registry is the canonical workflow identity. Preset objects
  // can briefly lag behind activePresetId during an in-app workflow switch;
  // deriving history from that stale object made the new workflow request the
  // previous/empty path until a page reload rebuilt all stores.
  const workflowManifests = useWorkflowManifestStore(state => state.workflows)
  const activeWorkflowManifest = workflowManifests.find(workflow => workflow.manifest.id === activePresetId || workflow.workspace_path === activeWorkflowPreset?.selectedFolder?.filepath)?.manifest
  const relayMode = activeWorkflowManifest?.kind === 'relay' || isRelaySurface
  const activeWorkflowWorkspacePath = resolveWorkflowHistoryPath(
    activePresetId,
    workflowManifests,
    activeWorkflowPreset,
  )
  // Keep the last concrete workspace path for the active preset during manifest
  // refreshes. A transient null here unmounts the report pane and makes toolbar
  // popups think the user switched workflows.
  const lastWorkspacePathRef = useRef<{ presetId: string | null, path: string | null }>({
    presetId: activePresetId,
    path: activeWorkflowWorkspacePath,
  })

  const workspacePath = useMemo(() => {
    if (activeWorkflowWorkspacePath) {
      lastWorkspacePathRef.current = {
        presetId: activePresetId,
        path: activeWorkflowWorkspacePath,
      }
      return activeWorkflowWorkspacePath
    }

    if (activePresetId && lastWorkspacePathRef.current.presetId === activePresetId) {
      return lastWorkspacePathRef.current.path
    }

    lastWorkspacePathRef.current = {
      presetId: activePresetId,
      path: null,
    }
    return null
  }, [activePresetId, activeWorkflowWorkspacePath])

  const initializedRelayViewPresetRef = useRef<string | null>(null)
  useEffect(() => {
    if (!relayMode || selectedModeCategory !== 'workflow' || !activePresetId || !workspacePath) return
    const currentView = workflowWorkspaceView ?? lastCanvasView
    const firstVisit = initializedRelayViewPresetRef.current !== activePresetId
    initializedRelayViewPresetRef.current = activePresetId
    if ((firstVisit && !hasSavedWorkflowWorkspaceView(activePresetId)) || !RELAY_WORKSPACE_VIEWS.has(currentView)) {
      useWorkflowStore.getState().openWorkspaceView('flow')
    }
  }, [activePresetId, lastCanvasView, relayMode, selectedModeCategory, workflowWorkspaceView, workspacePath])

  useEffect(() => {
    if (relayMode || selectedModeCategory !== 'workflow' || !activePresetId || !workspacePath || hasSavedWorkflowWorkspaceView(activePresetId)) return
    let cancelled = false
    const presetId = activePresetId
    void loadWorkspaceLandingView(workspacePath).then(landing => {
      if (cancelled || useGlobalPresetStore.getState().activePresetIds.workflow !== presetId || hasSavedWorkflowWorkspaceView(presetId)) return
      const view = landing === 'dashboard' ? 'report' : landing === 'plan' ? 'flow' : 'identity'
      useAppStore.getState().setWorkspaceMinimized(true)
      useWorkflowStore.setState({
        workflowWorkspaceView: view,
        showWorkspacePane: true,
        ...(view === 'report' || view === 'flow' ? { lastCanvasView: view } : {}),
      })
    })
    return () => { cancelled = true }
  }, [activePresetId, relayMode, selectedModeCategory, workspacePath])

  const activeWorkflowChatTabId = useChatStore(state => {
    const tabId = activeWorkflowTabIdForPreset(state.activeTabId, activePresetId, state.chatTabs)
    const tab = tabId ? state.chatTabs[tabId] : undefined
    return tab &&
      tab.metadata?.isViewOnly !== true &&
      tab.metadata?.isScheduledRun !== true &&
      tab.metadata?.isBotRun !== true
      ? tabId : null
  })

  const [contractUpgrade, setContractUpgrade] = useState<WorkflowContractUpgradeStatus | null>(null)
  const [startingContractUpgrade, setStartingContractUpgrade] = useState(false)
  const [contractUpgradeChat, setContractUpgradeChat] = useState<{ workspacePath: string; tabId: string } | null>(null)
  useEffect(() => {
    const onChatRequest = (event: Event) => {
      const detail = (event as WorkflowContractUpgradeChatEvent).detail
      if (detail?.workspacePath === workspacePath) setContractUpgradeChat(detail)
    }
    window.addEventListener(WORKFLOW_CONTRACT_UPGRADE_CHAT_EVENT, onChatRequest)
    return () => window.removeEventListener(WORKFLOW_CONTRACT_UPGRADE_CHAT_EVENT, onChatRequest)
  }, [workspacePath])
  const contractUpgradeTab = useChatStore(state =>
    contractUpgradeChat?.workspacePath === workspacePath ? state.chatTabs[contractUpgradeChat.tabId] : undefined
  )
  const contractUpgradeQueued = contractUpgradeTab?.config.queuedMessages?.includes(MANUAL_CONTRACT_UPGRADE_MESSAGE) ?? false
  const contractUpgradeTabId = contractUpgradeTab?.tabId
  const contractUpgradeStreaming = contractUpgradeTab?.isStreaming ?? false
  const activeWorkflowAccess = useMemo(() => (
    workflowManifests.find(workflow => workflow.manifest.id === activePresetId || workflow.workspace_path === workspacePath)?.my_access
  ), [activePresetId, workflowManifests, workspacePath])
  const canStartContractUpgrade = activeWorkflowAccess !== 'read'

  const refreshContractUpgrade = useCallback(async () => {
    if (!workspacePath) {
      setContractUpgrade(null)
      return
    }
    try {
      const response = await workflowManifestApi.getWorkflowManifest(workspacePath)
      const status = response.contract_upgrade ?? null
      setContractUpgrade(status)
      window.dispatchEvent(new CustomEvent(WORKFLOW_CONTRACT_UPGRADE_STATUS_EVENT, {
        detail: { workspacePath, status },
      }))
    } catch {
      setContractUpgrade(null)
    }
  }, [workspacePath])

  useEffect(() => {
    void refreshContractUpgrade()
  }, [activeWorkflowChatTabId, refreshContractUpgrade])

  useEffect(() => {
    if (contractUpgrade && !contractUpgrade.required) setContractUpgradeChat(null)
  }, [contractUpgrade])

  useEffect(() => {
    if (!contractUpgradeTabId || contractUpgradeQueued || contractUpgradeStreaming) return
    void refreshContractUpgrade()
  }, [contractUpgradeTabId, contractUpgradeStreaming, contractUpgradeQueued, refreshContractUpgrade])

  const startManualContractUpgrade = useCallback(async () => {
    if (!workspacePath || startingContractUpgrade || contractUpgradeTab) return
    setStartingContractUpgrade(true)
    try {
      const result = await sendWorkspacePaneMessageToChat({
        workspacePath,
        message: MANUAL_CONTRACT_UPGRADE_MESSAGE,
      })
      setContractUpgradeChat({ workspacePath, tabId: result.tabId })
    } catch (cause) {
      useChatStore.getState().addToast(cause instanceof Error ? cause.message : 'Could not start the workflow update.', 'error')
    } finally {
      setStartingContractUpgrade(false)
    }
  }, [contractUpgradeTab, startingContractUpgrade, workspacePath])

  // Never expose the previous workflow's messages or a false first-time chat
  // while the durable conversation for a newly selected workflow is resolving.
  // This key changes during render, before the reconnect effect runs.
  const workflowConversationKey = `${activePresetId || ''}:${workspacePath || ''}`
  const [resolvedWorkflowConversationKey, setResolvedWorkflowConversationKey] = useState('')
  const resolvedWorkflowConversationKeyRef = useRef('')
  // A workflow seen earlier in this page keeps its transcript in memory. Show
  // it at once; the reconnect below refreshes it in the background.
  const activeTabHasCachedWorkflowConversation = useChatStore(state =>
    activeWorkflowTabHasCachedConversation(state.activeTabId, activePresetId, state.chatTabs, state.tabEvents)
  )
  const isWorkflowConversationResolving = Boolean(
    activePresetId &&
    resolvedWorkflowConversationKey !== workflowConversationKey &&
    !activeTabHasCachedWorkflowConversation,
  )

  const [reportPreviewPreference, setReportPreviewPreference] = useState<ReportPreviewDevice>(
    () => readReportPreviewPreference(workspacePath),
  )
  const splitLayoutRef = useRef<HTMLDivElement>(null)
  const [workspaceSplitRatio, setWorkspaceSplitRatio] = useState(() => (
    readWorkflowSplitPreference(workspacePath, readReportPreviewPreference(workspacePath))
      ?? defaultWorkflowSplitRatio(readReportPreviewPreference(workspacePath))
  ))
  const workspaceSplitRatioRef = useRef(workspaceSplitRatio)

  useEffect(() => {
    const saved = readWorkflowSplitPreference(workspacePath, reportPreviewPreference)
    const width = splitLayoutRef.current?.getBoundingClientRect().width || window.innerWidth
    const next = saved ?? defaultWorkflowSplitRatio(reportPreviewPreference, width)
    workspaceSplitRatioRef.current = next
    setWorkspaceSplitRatio(next)
  }, [reportPreviewPreference, workspacePath])

  const setSplitRatio = useCallback((next: number, persist = false) => {
    const width = splitLayoutRef.current?.getBoundingClientRect().width || window.innerWidth
    const ratio = clampWorkflowSplitRatio(next, width)
    workspaceSplitRatioRef.current = ratio
    setWorkspaceSplitRatio(ratio)
    if (persist) writeWorkflowSplitPreference(workspacePath, ratio, reportPreviewPreference)
  }, [reportPreviewPreference, workspacePath])

  const { start: startSplitDrag, stop: stopSplitDrag } = usePointerDrag()
  useEffect(() => stopSplitDrag, [stopSplitDrag, workspacePath, reportPreviewPreference, showChatArea, showWorkspacePane])
  const handleSplitPointerDown = useCallback((event: React.PointerEvent<HTMLButtonElement>) => {
    // Mobile preview pins the workspace pane to phone width; nothing to drag.
    if (window.innerWidth < 768 || reportPreviewPreference === 'mobile') return
    const container = splitLayoutRef.current
    if (!container) return
    const rect = container.getBoundingClientRect()
    if (!rect.width) return
    startSplitDrag(event, {
      onMove: clientX => setSplitRatio((clientX - rect.left) / rect.width),
      onEnd: () => writeWorkflowSplitPreference(workspacePath, workspaceSplitRatioRef.current, reportPreviewPreference),
    })
  }, [reportPreviewPreference, setSplitRatio, startSplitDrag, workspacePath])

  const collapseWorkspaceFromRail = useCallback(() => {
    setShowWorkspacePane(false)
    setWorkspaceMinimized(true)
  }, [setShowWorkspacePane, setWorkspaceMinimized])

  const collapseChatFromRail = useCallback(() => {
    setShowChatArea(false)
    setFocusedPane('preview')
  }, [setFocusedPane, setShowChatArea])

  // A workflow opens in whatever device and split width the user last chose
  // for it (the two sync effects below re-read both on every switch). Nothing
  // on the open path writes a layout preference -- see the invariant in
  // utils/reportPreviewPreference.ts.

  const createFreshWorkflowBuilderTab = useCallback(async (presetId: string) => {
    const tabId = await ensureWorkflowBuilderTab(presetId)
    activateTab(tabId)
    setShowChatArea(true)
  }, [setShowChatArea])

  useEffect(() => {
    // Re-read this workflow's scoped preference (default Tablet only when it
    // has never been chosen) on mount, on
    // workflow switch, and whenever the device changes (event/storage).
    const syncReportPreviewPreference = () => {
      setReportPreviewPreference(readReportPreviewPreference(workspacePath))
    }
    syncReportPreviewPreference()

    window.addEventListener(REPORT_PREVIEW_PREFERENCE_CHANGED_EVENT, syncReportPreviewPreference as EventListener)
    window.addEventListener('storage', syncReportPreviewPreference)

    return () => {
      window.removeEventListener(REPORT_PREVIEW_PREFERENCE_CHANGED_EVENT, syncReportPreviewPreference as EventListener)
      window.removeEventListener('storage', syncReportPreviewPreference)
    }
  }, [workspacePath])

  // All split classes derive from the shared layout resolver: one decision
  // point for every flag combination (see workspaceLayoutResolver.ts).
  const layout = resolveWorkspaceLayout({
    showChatArea,
    showWorkspacePane,
    focusedPane,
    reportPreviewPreference,
    workspaceSplitRatio,
    isWorkspaceViewActive: isWorkspacePaneView(workflowWorkspaceView),
  })
  const workspacePaneVisible = layout.workspacePaneVisible
  // Below md, layout.canvasPaneClassName above toggles this pane between `hidden`
  // (display:none) and visible as focusedPane changes. Two things inside it
  // size themselves from their own element-level ResizeObserver rather than
  // from this pane's own layout: a report's auto-height iframe
  // (HtmlWidgetFrame's HtmlReportFrame, observing elements inside its own
  // document) and React Flow's plan canvas (observing its wrapper). Chromium
  // suspends a display:none iframe's rendering, and neither observer
  // reliably resumes with an accurate reading once it's un-hidden — the
  // report's outer scroll container gets stuck at a stale (often zero)
  // height, and the canvas renders blank at its last size. Both listen for
  // this window resize and force a fresh measurement instead of trusting
  // their observer to catch up on its own (HtmlWidgetFrame.tsx; WorkflowCanvas's
  // resyncViewport).
  const workspacePaneHiddenByFocus = workspacePaneVisible && focusedPane === 'chat'
  const previousWorkspacePaneHiddenByFocus = useRef(workspacePaneHiddenByFocus)
  useEffect(() => {
    if (previousWorkspacePaneHiddenByFocus.current && !workspacePaneHiddenByFocus) {
      const id = window.setTimeout(() => {
        window.dispatchEvent(new Event('resize'))
        canvasRef.current?.resyncViewport()
      }, 50)
      previousWorkspacePaneHiddenByFocus.current = workspacePaneHiddenByFocus
      return () => window.clearTimeout(id)
    }
    previousWorkspacePaneHiddenByFocus.current = workspacePaneHiddenByFocus
  }, [workspacePaneHiddenByFocus])

  // Load execution_defaults from workflow.json when workspace changes
  useEffect(() => {
    if (!workspacePath) return
    workflowManifestApi.getWorkflowManifest(workspacePath)
      .then(response => {
        const defaults = response?.manifest?.execution_defaults
        if (!defaults) return
        // Load global step overrides from execution_defaults
        const hasOverrides = defaults.disable_learning !== undefined ||
          defaults.disable_parallel_tool_execution !== undefined ||
          (defaults.enabled_custom_tools && defaults.enabled_custom_tools.length > 0)
        if (hasOverrides) {
          setStepOverride({
            disable_learning: defaults.disable_learning !== undefined ? defaults.disable_learning : undefined,
            disable_parallel_tool_execution: defaults.disable_parallel_tool_execution !== undefined ? defaults.disable_parallel_tool_execution : undefined,
            enabled_custom_tools: defaults.enabled_custom_tools,
          })
        } else {
          setStepOverride(null)
        }
      })
      .catch(() => { /* manifest may not exist yet, use defaults */ })
  }, [workspacePath, setStepOverride])

  // Auto-expand selectedRunFolder and selected groups in workspace sidebar whenever they change
  useEffect(() => {
    // Guard: WorkflowLayout stays mounted (hidden via CSS) in non-workflow modes.
    // Without this check, the fetchFiles(workspacePath) below fires in multi-agent
    // mode and overwrites the workspace file tree with workflow-scoped files,
    // leaving the multi-agent sidebar showing "No files found".
    const activeMode = useModeStore.getState().selectedModeCategory
    if (activeMode !== 'workflow') {
      return
    }

    const selectionKey = selectedRunFolder && selectedRunFolder !== 'new' && workspacePath
      ? `${workspacePath}::${selectedRunFolder}::${(selectedGroupIds ?? []).slice().sort().join(',')}`
      : null

    if (!selectionKey) {
      lastWorkspaceRunExpansionKeyRef.current = null
      return
    }

    if (selectedRunFolder && selectedRunFolder !== 'new' && workspacePath) {
      // Skip fetch when workspace panel is minimized — mark stale for manual refresh
      if (workspaceMinimized) {
        lastWorkspaceRunExpansionKeyRef.current = selectionKey
        useWorkspaceStore.getState().setNeedsRefresh(true)
        return
      }

      if (lastWorkspaceRunExpansionKeyRef.current === selectionKey) {
        return
      }

      // Expand folders in workspace sidebar — skip redundant fetch if Workspace.tsx already loaded files.
      // Workspace.tsx:718 fetches activeFolder on mount/change, so files should already be present.
      const ensureFiles = useWorkspaceStore.getState().files.length > 0
        ? Promise.resolve()
        : fetchFiles(workspacePath || undefined)
      ensureFiles.then(() => {
        // Collapse all other iteration folders first
        const workspaceStore = useWorkspaceStore.getState()
        const expandedFolders = workspaceStore.expandedFolders
        const runsPath = `${workspacePath}/runs`

        // Filter out all iteration-related folders from expandedFolders
        const newExpandedFolders = new Set<string>()
        expandedFolders.forEach(folder => {
          // Keep folders that are NOT under runs/iteration-*
          // Check all patterns: full paths, relative paths, and iteration folders
          const isIterationFolder =
            folder.includes('/runs/iteration-') ||           // Full path: "Workflow/ICICI/runs/iteration-3"
            /^runs\/iteration-/.test(folder) ||             // Relative: "runs/iteration-3/group-1"
            /^iteration-\d+/.test(folder)                   // Just iteration: "iteration-3"

          if (!isIterationFolder) {
            newExpandedFolders.add(folder)
          }
        })


        // Add the runs folder itself to keep it expanded (both full and relative paths)
        newExpandedFolders.add(runsPath)
        newExpandedFolders.add('runs') // Relative path

        // Extract iteration folder from selectedRunFolder (e.g., "iteration-3" from "iteration-3/group-1")
        const iterationFolder = selectedRunFolder.includes('/')
          ? selectedRunFolder.split('/')[0]
          : selectedRunFolder

        // Add all parent folders of the iteration
        const iterationPath = `${workspacePath}/runs/${iterationFolder}`
        const iterationPathParts = iterationPath.split('/')
        let currentPath = ''
        for (const part of iterationPathParts) {
          currentPath = currentPath ? `${currentPath}/${part}` : part
          newExpandedFolders.add(currentPath)
        }

        // Also add relative paths for iteration
        newExpandedFolders.add(`runs/${iterationFolder}`)
        newExpandedFolders.add(iterationFolder)

        // If we have selected groups, expand all of them
        if (selectedGroupIds && selectedGroupIds.length > 0 && variablesManifest?.groups) {
          selectedGroupIds.forEach(groupId => {
            // Find the group to get its name
            const group = variablesManifest.groups?.find(g => g.name === groupId)

            // Use sanitized name for folder naming
            const folderName = group?.name
              ? sanitizeDisplayNameForFolder(group.name)
              : groupId

            // Build the full group path
            const groupPath = `${workspacePath}/runs/${iterationFolder}/${folderName}`

            // Add all parent folders of this group path
            const groupPathParts = groupPath.split('/')
            let groupCurrentPath = ''
            for (const part of groupPathParts) {
              groupCurrentPath = groupCurrentPath ? `${groupCurrentPath}/${part}` : part
              newExpandedFolders.add(groupCurrentPath)
            }

            // Also add relative paths
            newExpandedFolders.add(`runs/${iterationFolder}/${folderName}`)
          })
        }
        // Legacy code removed: selectedRunFolder no longer contains group paths
        // Group selection is now exclusively via selectedGroupIds array

        // Update the expanded folders using the proper setter
        setExpandedFolders(newExpandedFolders)
        lastWorkspaceRunExpansionKeyRef.current = selectionKey
      }).catch(error => {
        logger.error('WorkflowLayout', 'Failed to fetch files for auto-expansion:', error)
      })
    }
  }, [selectedRunFolder, selectedGroupIds, workspacePath, variablesManifest, fetchFiles, setExpandedFolders, workspaceMinimized])

  // Callback ref that gets called when ChatArea mounts/unmounts
  const chatAreaCallbackRef = useCallback((node: ChatAreaRef | null) => {
    chatAreaRef.current = node

    // When ChatArea mounts and we have a pending query, submit it
    if (node && pendingQueryRef.current) {
      const { query, executionOptions } = pendingQueryRef.current
      logger.debug('WorkflowLayout', 'ChatArea mounted, submitting pending query:', {
        query,
        hasExecutionOptions: Boolean(executionOptions)
      })
      node.submitQuery(query, executionOptions).catch(error => {
        logger.error('WorkflowLayout', 'Failed to submit pending query:', error)
      })
      pendingQueryRef.current = null // Clear pending query after submission
    }
  }, [])

  // The global workspace toggle now maps to the workflow's right-side Files
  // pane instead of the old app-level far-right file column.
  //
  // The report preview is exempt: un-minimizing the workspace while Report is
  // open leaves it alone instead of auto-switching to Files — click Files to
  // get there. (The exemption was originally added because the
  // pane host remounted the whole pane per view kind, so the forced switch and
  // its immediate reversal flashed the report; WorkspaceViewHost keeps the pane
  // mounted across switches now, but the leave-the-preview-alone behavior is
  // kept as-is.)
  useEffect(() => {
    if (selectedModeCategory !== 'workflow') return
    const onPreviewView = isPreviewView(workflowWorkspaceView)
    if (!workspaceMinimized && !onPreviewView && (workflowWorkspaceView !== 'files' || !showWorkspacePane)) {
      setShowWorkspacePane(true)
      setWorkflowWorkspaceView('files')
      return
    }
    if (workspaceMinimized && workflowWorkspaceView === 'files') {
      setWorkflowWorkspaceView(lastCanvasView)
    }
  }, [selectedModeCategory, workspaceMinimized, workflowWorkspaceView, showWorkspacePane, lastCanvasView, setShowWorkspacePane, setWorkflowWorkspaceView])

  // Auto-minimize the file workspace sidebar when entering Report so the report
  // has room. Do not reopen it on exit: workflow switches can unmount/remount
  // this layout, and auto-reopening makes the workspace look default-open.
  //
  // Act only on workflowWorkspaceView transitions. While Report is active the
  // user is free to manually reopen the workspace — re-running this effect on
  // workspaceMinimized changes must not fight them and re-close it.
  //
  // Gated on workflow mode — this component stays mounted in multiagent mode
  // via `hidden` CSS, and without the guard the Report-minimize would leak
  // into multiagent's workspace.
  useEffect(() => {
    if (selectedModeCategory !== 'workflow') {
      prevWorkflowWorkspaceViewRef.current = workflowWorkspaceView
      return
    }

    const prev = prevWorkflowWorkspaceViewRef.current
    prevWorkflowWorkspaceViewRef.current = workflowWorkspaceView

    if (workflowWorkspaceView === 'report' && prev !== 'report') {
      if (!workspaceMinimized) {
        reportAutoMinimizedWorkspaceRef.current = true
        setWorkspaceMinimized(true)
      }
      return
    }

    if (workflowWorkspaceView !== 'report' && prev === 'report') {
      reportAutoMinimizedWorkspaceRef.current = false
    }
  }, [selectedModeCategory, workflowWorkspaceView, workspaceMinimized, setWorkspaceMinimized])

  useEffect(() => {
    return () => {
      if (reportAutoMinimizedWorkspaceRef.current) {
        reportAutoMinimizedWorkspaceRef.current = false
      }
    }
  }, [])

  const runningWorkflowReconcileInFlightRef = useRef(false)
  const workflowReconnectKeyRef = useRef('')

  // Reconnect on every workflow open so the persistent Chat follows the
  // user's latest durable conversation, including after switching away and
  // back in the same browser session.
  useEffect(() => {
    if (!activePresetId) {
      return
    }
    const reconnectKey = `${activePresetId}:${workspacePath || ''}`
    let cancelled = false

    const reconnectWorkflowTabs = async () => {
      if (
        workflowReconnectKeyRef.current === reconnectKey &&
        resolvedWorkflowConversationKeyRef.current === reconnectKey
      ) {
        setResolvedWorkflowConversationKey(reconnectKey)
        return
      }
      workflowReconnectKeyRef.current = reconnectKey
      // Wait for zustand to rehydrate persisted tabs from localStorage.
      // Without this, chatTabs is empty and dedup fails → duplicate tabs.
      await waitForChatStoreHydration()
      if (cancelled) return
      try {
        const { closeTab, createChatTab, getTabEvents, setTabStreaming } = useChatStore.getState()
        const { getPhaseById } = useWorkflowStore.getState()
        const getExistingWorkflowTabsForPreset = () =>
          Object.values(useChatStore.getState().chatTabs)
            .filter(t =>
              t.metadata?.mode === 'workflow' &&
              t.metadata?.presetQueryId === activePresetId
            )
            .sort((a, b) => workflowTabSortTimestamp(b) - workflowTabSortTimestamp(a))

        // These reads are independent. Start them together: run one after
        // another they put three server round trips in front of the chat.
        const runningWorkflowsPromise = agentApi.listRunningWorkflows()
          .then(response => response, () => null)
        const latestHistoryPromise = workspacePath
          ? agentApi.listChatHistorySessions(5, 0, workspacePath, 'chat')
            .then(history => ({ sessions: history.sessions || [] as ChatHistorySession[] }), (error: unknown) => ({ error }))
          : undefined

        // 1. Get active (running) sessions from in-memory cache
        //    Include both 'workflow' (execution) and 'workflow_phase' (workflow builder, plan-improvement)
        const activeSessions = await useChatStore.getState().getActiveSessions()
        if (cancelled) return
        const internalChildSessionIds = new Set(activeSessions
          .filter(session => isInternalChildSession({
            parentSessionId: session.parent_session_id,
            sessionKind: session.session_kind,
          }))
          .map(session => session.session_id))

        // A previous frontend may already have persisted an internal reviewer as
        // a top-level tab. Remove that UI projection without stopping the child;
        // the backend runtime and its explicit parent link remain intact.
        for (const tab of Object.values(useChatStore.getState().chatTabs)) {
          if (tab.metadata?.mode === 'workflow' && tab.sessionId && internalChildSessionIds.has(tab.sessionId)) {
            await closeTab(tab.tabId, false)
          }
        }

        const activeWorkflowSessions = liveWorkflowSessionsForReconnect(activeSessions)
          .filter(s => !internalChildSessionIds.has(s.session_id))

        // 2. Skip DB session restore — only interactive running sessions may
        // auto-create tabs. Background runs require an explicitly opened tab.
        //    Old completed sessions from DB were creating unwanted tabs every time you
        //    open a workflow. Workflow builder conversations are saved to workspace files,
        //    not restored from DB sessions.
        const dbSessions: import('../../services/api-types').ChatHistorySummary[] = []

        // Build a combined list — active sessions first, then recent DB sessions (deduped)
        const activeSessionIds = new Set(activeWorkflowSessions.map(s => s.session_id))
        const runningWorkflowsBySession = new Map<string, RunningWorkflowInfo>()
        // The running registry is an enhancement here; active sessions still
        // restore below when it fails.
        const runningResponse = await runningWorkflowsPromise
        if (cancelled) return
        for (const running of runningResponse?.running || []) {
          if (running.session_id) {
            runningWorkflowsBySession.set(running.session_id, running)
          }
        }
        const sessionsToRestore: Array<{
          sessionId: string
          query?: string
          title?: string
          status: string
          isActive: boolean
          phaseId?: string
          phaseName?: string
          triggeredBy?: string
          botPlatform?: string
          isScheduledRun?: boolean
          preloadedEvents?: PollingEvent[]
          lastProcessedIndex?: number
          historySession?: ChatHistorySession
        }> = []
        const queuedSessionIds = new Set<string>()

        // Add active sessions that belong to this preset. We read the
        // running-workflow registry (workflow-owned storage) instead of
        // reaching into the chat session metadata.
        // Match live sessions through preset ID first, then workspace path.
        // This covers older workflow_phase sessions where the tracker knows the
        // workspace but not the preset ID, without attaching unknown sessions to
        // whatever workflow happens to be active.
        // Fallback: if no registry lookup resolved a preset, allow the session
        // through only when its persisted chat tab already binds it to the
        // current preset (so reload doesn't drop a tab the user has been using).
        const chatTabsById = useChatStore.getState().chatTabs
        // Registry misses are looked up per session. Issue those lookups
        // together rather than one round trip per loop iteration.
        const registryLookups = new Map(activeWorkflowSessions
          .filter(s => !runningWorkflowsBySession.has(s.session_id) && shouldDiscoverWorkflowChatTab({
            sessionId: s.session_id,
            triggeredBy: s.triggered_by,
            botPlatform: s.bot_platform,
          }, Object.values(chatTabsById).some(tab =>
            tab.sessionId === s.session_id && tab.metadata?.presetQueryId === activePresetId
          )))
          .map(s => [s.session_id, agentApi.getRunningWorkflow(s.session_id).then(running => running, () => undefined)] as const))
        for (const s of activeWorkflowSessions) {
          const registryRunning = runningWorkflowsBySession.get(s.session_id)
          const scheduledSession = isScheduledSession({
            sessionId: s.session_id,
            triggeredBy: s.triggered_by,
          }) || Boolean(registryRunning && isScheduledSession({
            sessionId: registryRunning.session_id,
            triggeredBy: registryRunning.triggered_by,
          }))
          const hasOpenTab = Object.values(useChatStore.getState().chatTabs).some(tab =>
            tab.sessionId === s.session_id && tab.metadata?.presetQueryId === activePresetId
          )
          if (!shouldDiscoverWorkflowChatTab({
            sessionId: s.session_id,
            triggeredBy: s.triggered_by,
            botPlatform: s.bot_platform,
          }, hasOpenTab) || (registryRunning && !shouldDiscoverWorkflowChatTab({
            sessionId: registryRunning.session_id,
            triggeredBy: registryRunning.triggered_by,
          }, hasOpenTab))) {
            continue
          }
          let belongsToPreset = isLiveWorkflowSessionForPreset(s, activePresetId, workspacePath)
          // A registry miss falls through to the persisted-tab check below.
          const running = registryRunning || await registryLookups.get(s.session_id)
          if (running?.preset_query_id) {
            belongsToPreset = running.preset_query_id === activePresetId
          } else if (workspacePath && running?.workspace_path) {
            belongsToPreset = normalizeWorkflowPath(running.workspace_path) === normalizeWorkflowPath(workspacePath)
          }
          if (!belongsToPreset) {
            const persistedTab = Object.values(chatTabsById).find(
              t => t.sessionId === s.session_id && t.metadata?.mode === 'workflow'
            )
            if (persistedTab?.metadata?.presetQueryId === activePresetId) {
              belongsToPreset = true
            }
          }
          if (!belongsToPreset) continue
          queuedSessionIds.add(s.session_id)
          sessionsToRestore.push({
            sessionId: s.session_id,
            query: s.query,
            title: s.title,
            status: s.status,
            isActive: true,
            triggeredBy: s.triggered_by,
            botPlatform: s.bot_platform,
            isScheduledRun: scheduledSession,
          })
        }

        for (const running of runningWorkflowsBySession.values()) {
          if (!running.session_id || queuedSessionIds.has(running.session_id)) continue
          const scheduledSession = isScheduledSession({
            sessionId: running.session_id,
            triggeredBy: running.triggered_by,
          })
          // Registry-only discoveries follow the same opt-in rule as the
          // active-session cache; the monitor exposes runs without chat tabs.
          if (!shouldDiscoverWorkflowChatTab({
            sessionId: running.session_id,
            triggeredBy: running.triggered_by,
          }, Object.values(useChatStore.getState().chatTabs).some(tab =>
            tab.sessionId === running.session_id && tab.metadata?.presetQueryId === activePresetId
          ))) continue
          const belongsToPreset = runningWorkflowBelongsToPreset(running, activePresetId, workspacePath)
          if (!belongsToPreset) continue
          queuedSessionIds.add(running.session_id)
          sessionsToRestore.push({
            sessionId: running.session_id,
            query: running.query,
            title: running.title || running.preset_name || running.phase_name,
            status: running.status || 'running',
            isActive: true,
            phaseId: running.phase_id,
            phaseName: running.phase_name,
            triggeredBy: running.triggered_by,
            isScheduledRun: scheduledSession,
          })
        }

        // The persistent Chat follows the signed-in user's latest saved
        // conversation for this workflow. A live interactive session wins;
        // otherwise restore exactly one durable chat and leave older chats,
        // schedules, bots and webhooks in Workshop.
        const hasLiveInteractiveSession = sessionsToRestore.some(session =>
          session.isActive && !session.isScheduledRun && !session.botPlatform,
        )
        // Hoisted: the latest-first activation below reuses this list, and
        // fetches it itself when live sessions skip this block entirely.
        let historySessions: ChatHistorySession[] | undefined
        if (!hasLiveInteractiveSession && latestHistoryPromise) {
          try {
            const history = await latestHistoryPromise
            if (cancelled) return
            if ('error' in history) throw history.error
            historySessions = history.sessions
            const latestSavedChat = findLatestRestorableWorkflowChatSession(historySessions)
            if (latestSavedChat && !queuedSessionIds.has(latestSavedChat.session_id)) {
              queuedSessionIds.add(latestSavedChat.session_id)
              sessionsToRestore.push({
                sessionId: latestSavedChat.session_id,
                title: chatHistorySessionTitle(latestSavedChat),
                status: latestSavedChat.status || 'completed',
                isActive: false,
                phaseId: 'workflow-builder',
                phaseName: 'Automation Builder',
                historySession: latestSavedChat,
              })
            }
          } catch (error) {
            logger.warn('WorkflowLayout', 'Failed to resolve the latest saved workflow chat', error)
          }
        }

        // Only the latest durable user chat is restored automatically.
        // Older chats and all automated runs remain explicit Workshop opens.

        // Add the most recent DB session not already in active list
        // Only show completed/running/error sessions (skip dismissed/inactive)
        // Only restore the latest session — older ones stay in history
        const recentDbSessions = dbSessions
          .filter(s => !activeSessionIds.has(s.session_id) && s.status !== 'dismissed' && s.status !== 'inactive')
          .slice(0, 1)
        for (const s of recentDbSessions) {
          const config = s.config && typeof s.config === 'object'
            ? s.config as Record<string, unknown>
            : {}
          const wfMeta = config.workflow_metadata && typeof config.workflow_metadata === 'object'
            ? config.workflow_metadata as Record<string, unknown>
            : {}
          // Try to extract phaseId from metadata, config, or agent_mode
          let phaseId = typeof wfMeta.phase_id === 'string' ? wfMeta.phase_id : undefined
          if (!phaseId && s.agent_mode === 'workflow_phase') {
            // workflow_phase sessions store phase_id in config
            phaseId = typeof config.phase_id === 'string' ? config.phase_id : undefined
          }
          if (!phaseId && s.title) {
            // Fallback: try to extract from title
            const match = s.title.match(/(?:(?:workflow|automation)[- ]builder|planning|evaluation[- ]builder)/i)
            if (match) phaseId = match[0].toLowerCase().replace(/\s/g, '-')
          }
          sessionsToRestore.push({
            sessionId: s.session_id,
            query: undefined,
            title: s.title,
            status: s.status,
            isActive: false,
            phaseId,
            phaseName: typeof wfMeta.phase_name === 'string' ? wfMeta.phase_name : undefined
          })
        }

        // 3. Split sessions into (a) those we need to create a tab for and
        //    (b) those whose tab is already persisted in localStorage but whose
        //    events were never hydrated (workflow events live only in the
        //    in-memory EventStore, not in DB/localStorage, so a page refresh
        //    leaves persisted tabs looking empty until we pull them back).
        const { chatTabs } = useChatStore.getState()
        const existingTabsBySession = new Map<string, string>()
        Object.values(chatTabs).forEach(t => {
          if (t.metadata?.mode === 'workflow' && t.sessionId) {
            existingTabsBySession.set(t.sessionId, t.tabId)
          }
        })
        const newSessions = sessionsToRestore.filter(s => !existingTabsBySession.has(s.sessionId))
        const existingWorkflowTabs = getExistingWorkflowTabsForPreset()
        const interactiveExistingWorkflowTabs = existingWorkflowTabs.filter(tab => !tab.metadata?.isViewOnly)
        const chatStoreForViewMode = useChatStore.getState()
        const activeWorkflowViewMode = normalizeEventViewMode(
          (chatStoreForViewMode.activeTabId ? chatStoreForViewMode.chatTabs[chatStoreForViewMode.activeTabId]?.viewMode : undefined) ||
          chatStoreForViewMode.eventViewModePreference
        )
        const shouldHydrateWorkflowEvents = activeWorkflowViewMode === 'formatted'

        // Only restore sessions that don't have tabs yet
        const sessionsToActuallyRestore = newSessions

        // 3a. Rehydrate events for persisted tabs whose event buffer was lost
        //     on refresh -- read-only scheduled/bot run tabs included (see
        //     rehydrateWorkflowTabs); they were excluded here once, which is
        //     how a finished run's tab got stuck on "Restoring previous
        //     session..." after every backend restart.
        const tabsNeedingHydration = shouldHydrateWorkflowEvents
          ? workflowTabsNeedingHydration(existingWorkflowTabs, getTabEvents)
          : []
        if (tabsNeedingHydration.length > 0) {
          await rehydrateWorkflowTabs(existingWorkflowTabs, workspacePath)
        }

        // Restores one durable chat into an Automation Builder tab (reusing the
        // tab when the session already has one) and hydrates its transcript,
        // preferring the list response's indexed tail over a full fetch.
        // Shared by the new-session loop and the latest-first activation below.
        // Mirrors the loop's resolution exactly: same session-scoped reuse,
        // same reset-on-session-change, no idle-tab takeover (no getTabEvents).
        const restoreHistorySessionTab = async (historySession: ChatHistorySession, sessionId: string): Promise<string> => {
          const { tabId } = await resolveWorkflowTabForSession({
            getTabs: () => useChatStore.getState().chatTabs,
            presetQueryId: activePresetId,
            sessionId,
            name: 'Automation Builder',
            metadata: {
              mode: 'workflow',
              phaseId: 'workflow-builder',
              phaseName: 'Automation Builder',
              presetQueryId: activePresetId,
              isViewOnly: false,
            },
            createChatTab,
            updateTabSessionId: (targetTabId, targetSessionId) => {
              const store = useChatStore.getState()
              const currentSessionId = store.chatTabs[targetTabId]?.sessionId
              if (currentSessionId && currentSessionId !== targetSessionId) {
                store.resetTabChat(targetTabId)
              }
              store.updateTabSessionId(targetTabId, targetSessionId)
            },
          })
          const chatStore = useChatStore.getState()
          applyRestoredWorkflowConversationConfig(tabId, historySession)
          chatStore.setTabViewMode(tabId, 'formatted')
          chatStore.beginWorkflowSessionRestore(sessionId)
          try {
            await withWorkflowRestoreTimeout(
              hydrateTabEvents(sessionId, { workspacePath: workspacePath || undefined }),
              `Restoring latest workflow chat ${sessionId}`,
            )
          } finally {
            chatStore.endWorkflowSessionRestore(sessionId)
          }
          chatStore.setTabStreaming(tabId, false)
          chatStore.setTabCompleted(tabId, true)
          return tabId
        }

        // 4. Create tabs and load events for new sessions only
        let lastTabId: string | null = null
        for (const session of sessionsToActuallyRestore) {
          if (cancelled) return
          if (session.historySession) {
            lastTabId = await restoreHistorySessionTab(session.historySession, session.sessionId)
            continue
          }
          // Extract phase ID from workflow metadata, query, or title
          let phaseId: string | null = session.phaseId || null
          if (!phaseId) {
            const queryStr = session.query || session.title || ''
            const match = queryStr.match(/(?:Execute workflow phase:|phase:)\s*(\w+)/i)
            if (match && match[1]) {
              phaseId = match[1]
            }
          }

          const phase = phaseId ? getPhaseById(phaseId) : null
          // Naming priority:
          //   1. Explicit phase / phaseName from the session record
          //   2. The session's Title (scheduled runs get the schedule name
          //      stamped here by stampScheduleNameOnSession on the backend;
          //      regular workflow runs may have a meaningful title too)
          //   3. Fallback to "Schedule" / "Bot" when we know the trigger,
          //      so a scheduled run reconnected on app boot doesn't get
          //      labelled the literal "Workflow"
          //   4. Last resort: phaseId / "Automation Builder" (so the chat
          //      input gating in WorkflowChatTabs treats it as the
          //      builder tab and shows the proper "Chat" label)
          let phaseName: string
          if (session.phaseName || phase?.title || session.title) {
            phaseName = session.phaseName || phase?.title || session.title || ''
          } else if (session.isScheduledRun || session.triggeredBy === 'cron') {
            phaseName = 'Schedule'
          } else if (session.botPlatform) {
            phaseName = session.botPlatform
          } else {
            phaseName = phaseId || 'Automation Builder'
          }

          // Create tab with scheduled-run / bot metadata so downstream
          // UI (chat-input toggle, view-only banner, badge icons) treats
          // it as a read-only observer of an external trigger.
          const isScheduled = session.isScheduledRun || session.triggeredBy === 'cron'
          const isBot = Boolean(session.botPlatform)
          // Same resolution as the running-workflow reconciler: a scheduled
          // run rediscovered on boot takes over its own schedule's finished
          // tab instead of opening a second one beside it.
          const { tabId } = await resolveWorkflowTabForSession({
            getTabs: () => useChatStore.getState().chatTabs,
            presetQueryId: activePresetId,
            sessionId: session.sessionId,
            name: phaseName,
            metadata: {
              mode: 'workflow',
              phaseId: phaseId || undefined,
              phaseName,
              presetQueryId: activePresetId,
              isViewOnly: isScheduled || isBot ? true : undefined,
              isScheduledRun: isScheduled || undefined,
              scheduledJobName: isScheduled ? (session.title || phaseName) : undefined,
              isBotRun: isBot || undefined,
              botPlatform: isBot ? session.botPlatform : undefined,
            },
            createChatTab,
            updateTabSessionId: (targetTabId, targetSessionId) => {
              const store = useChatStore.getState()
              const currentSessionId = store.chatTabs[targetTabId]?.sessionId
              if (session.historySession && currentSessionId && currentSessionId !== targetSessionId) {
                store.resetTabChat(targetTabId)
              }
              store.updateTabSessionId(targetTabId, targetSessionId)
            },
          })

          // Workflow switches default to terminal/report surfaces. Event history
          // is only hydrated for the tree/debug view.
          if (shouldHydrateWorkflowEvents && session.preloadedEvents && session.preloadedEvents.length > 0) {
            useChatStore.getState().setTabEvents(session.sessionId, session.preloadedEvents)
            useChatStore.getState().setTabLastEventIndex(
              session.sessionId,
              session.lastProcessedIndex ?? session.preloadedEvents.length - 1,
            )
            setTabStreaming(tabId, session.isActive)
            useChatStore.getState().setTabCompleted(tabId, !session.isActive)
          } else if (shouldHydrateWorkflowEvents) {
            useChatStore.getState().beginWorkflowSessionRestore(session.sessionId)
            try {
              await withWorkflowRestoreTimeout(
                // A scheduled run can outlive the server's EventStore. Its
                // workspace conversation is durable, so use it as the
                // immediate fallback instead of repeatedly restoring an empty
                // volatile buffer and leaving the Schedule lane on a spinner.
                restoreWorkflowStateFromEvents(session.sessionId, workspacePath, isScheduled),
                `Restoring workflow events for ${session.sessionId}`
              )
              if (session.isActive || session.status === 'running') {
                setTabStreaming(tabId, true)
              }
            } catch (err) {
              console.warn('[WorkflowReconnect] Failed to load events for', session.sessionId, err)
            } finally {
              useChatStore.getState().endWorkflowSessionRestore(session.sessionId)
            }
          } else {
            setTabStreaming(tabId, session.isActive || session.status === 'running')
            useChatStore.getState().setTabCompleted(tabId, !(session.isActive || session.status === 'running'))
          }

          lastTabId = tabId
        }

        // Latest-first: the persisted active tab wins only when it already
        // shows the newest restorable chat. Otherwise the newest chat takes
        // focus so a reload always lands on the latest conversation instead
        // of a stale tab. Runs, schedules, bots, and fresh tabs keep
        // today's preserve behavior (see resolveLatestWorkflowChatAction).
        {
          const store = useChatStore.getState()
          if (activeWorkflowTabIdForPreset(store.activeTabId, activePresetId, store.chatTabs)) {
            try {
              if (!historySessions && latestHistoryPromise) {
                const history = await latestHistoryPromise
                if ('error' in history) throw history.error
                if (!cancelled) historySessions = history.sessions
              }
              if (cancelled) return
              const latestStore = useChatStore.getState()
              const action = resolveLatestWorkflowChatAction({
                sessions: historySessions,
                tabs: latestStore.chatTabs,
                activeTabId: latestStore.activeTabId,
              })
              if (action.action === 'activate-tab') {
                activateTab(action.tabId)
                setShowChatArea(true)
                return
              }
              if (action.action === 'restore-latest') {
                const restoredTabId = await restoreHistorySessionTab(action.session, action.sessionId)
                if (cancelled) return
                activateTab(restoredTabId)
                setShowChatArea(true)
                return
              }
            } catch (error) {
              logger.warn('WorkflowLayout', 'Failed to resolve the latest workflow chat tab', error)
            }
            setShowChatArea(true)
            return
          }
        }

        // 5. Show the chat area with the last tab
        if (lastTabId) {
          activateTab(lastTabId)
          setShowChatArea(true)
        }

        // 6. No saved conversation: show the first-time persistent Chat and
        // its getting-started guide.
        if (!lastTabId) {
          const store = useChatStore.getState()
          if (interactiveExistingWorkflowTabs.length === 0) {
            activateTab(await ensureWorkflowBuilderTab(activePresetId))
            setShowChatArea(true)
          } else {
            const streamingTab = interactiveExistingWorkflowTabs.find(t => t.isStreaming || store.getTabStreamingStatus(t.tabId))
            if (streamingTab) {
              activateTab(streamingTab.tabId)
              setShowChatArea(true)
              return
            } else {
              const builderTab = interactiveExistingWorkflowTabs.find(t => t.metadata?.phaseId === 'workflow-builder')
              if (builderTab) {
                activateTab(builderTab.tabId)
                setShowChatArea(true)
                return
              }
              activateTab(interactiveExistingWorkflowTabs[0].tabId)
              setShowChatArea(true)
              return
            }
          }
        }
      } catch (error) {
        if (workflowReconnectKeyRef.current === reconnectKey) {
          workflowReconnectKeyRef.current = ''
        }
        console.warn('[WorkflowReconnect] Failed to reconnect workflow tabs:', error)
      } finally {
        // Ensure every workflow has its one persistent Chat. Runs after every
        // early return above while preserving any explicit read-only selection.
        if (!cancelled) {
          void ensureWorkflowBuilderTab(activePresetId, { keepSelection: true })
          resolvedWorkflowConversationKeyRef.current = reconnectKey
          setResolvedWorkflowConversationKey(reconnectKey)
        }
      }
    }

    // No settle delay: the chat pane stays hidden until this resolves, and
    // `cancelled` already drops a run superseded by a newer selection.
    const timeoutId = setTimeout(reconnectWorkflowTabs, 0)
    return () => {
      cancelled = true
      clearTimeout(timeoutId)
    }
  }, [activePresetId, workspacePath, setShowChatArea, rehydrateWorkflowTabs, createFreshWorkflowBuilderTab])

  useEffect(() => {
    if (!activePresetId || selectedModeCategory !== 'workflow') return

    let cancelled = false
    const reconcileRunningWorkflowTab = async () => {
      if (runningWorkflowReconcileInFlightRef.current) return
      runningWorkflowReconcileInFlightRef.current = true
      try {
        if (cancelled) return
        // Reuse the store's active-sessions cache instead of independently
        // polling /api/workflow/running — both are sourced from the same
        // backend tracker, and GlobalActivityMonitor already keeps this
        // cache fresh every 5s.
        const runningFromActiveSessions = useChatStore.getState().activeSessionsCache
          .filter(session => session.agent_mode === 'workflow' || session.agent_mode === 'workflow_phase')
          .map(activeSessionToRunningWorkflowInfo)
        const projectedRunningWorkflows = runningFromActiveSessions
          .filter(item => item.session_id && isRunningWorkflowEntry(item))
          .filter(item => runningWorkflowBelongsToPreset(item, activePresetId, workspacePath))
          .filter(item => shouldDiscoverWorkflowChatTab({
            sessionId: item.session_id,
            triggeredBy: item.triggered_by,
          }, Object.values(useChatStore.getState().chatTabs).some(tab =>
            tab.sessionId === item.session_id && tab.metadata?.presetQueryId === activePresetId
          )))
          .sort((a, b) => new Date(b.started_at || 0).getTime() - new Date(a.started_at || 0).getTime())
          .flatMap(running => {
            const projection = workflowRuntimeTabProjection(running, activePresetId)
            return projection ? [{ running, projection }] : []
          })

        if (projectedRunningWorkflows.length === 0) return

        const chatStore = useChatStore.getState()
        const activeTab = chatStore.activeTabId ? chatStore.chatTabs[chatStore.activeTabId] : undefined
        const activeViewMode = normalizeEventViewMode(activeTab?.viewMode || chatStore.eventViewModePreference)
        const activeTabOwnedByPreset = Boolean(
          activeWorkflowTabIdForPreset(chatStore.activeTabId, activePresetId, chatStore.chatTabs)
        )
        const shouldSwitch = !activeTabOwnedByPreset

        let selectedRunningTabId: string | null = null
        for (const { running, projection } of projectedRunningWorkflows) {
          if (!running.session_id) continue

          // Existing tab, a finished lane of the same schedule, or a new
          // tab -- one decision shared with the reconnect path and the
          // activity monitor (utils/workflowTabResolution.ts).
          const latestChatStore = useChatStore.getState()
          const { tabId } = await resolveWorkflowTabForSession({
            getTabs: () => useChatStore.getState().chatTabs,
            presetQueryId: activePresetId,
            sessionId: running.session_id,
            name: projection.name,
            metadata: projection.metadata,
            createChatTab: latestChatStore.createChatTab,
            updateTabSessionId: latestChatStore.updateTabSessionId,
          })
          if (projection.autoActivate) selectedRunningTabId ||= tabId

          // Runtime discovery may refresh status for an observed run, but an
          // explicit Schedule/Bot -> Chat promotion is user-owned state. Apply
          // both name and metadata atomically so a polling tick cannot turn the
          // interactive continuation back into a read-only Schedule tab.
          useChatStore.setState(state => {
            const tab = state.chatTabs[tabId]
            if (!tab) return state
            return {
              chatTabs: {
                ...state.chatTabs,
                [tabId]: reconcileWorkflowRuntimeTab(tab, projection),
              },
            }
          })
          chatStore.setTabStreaming(tabId, true)
          chatStore.setTabCompleted(tabId, false)
          chatStore.setTabViewMode(tabId, activeViewMode)

          // This reconciler often discovers a scheduled run after it has
          // already emitted its opening message and several tool/stream
          // events. SSE only guarantees delivery from the live subscription
          // point onward, so explicitly catch up the formatted transcript
          // before relying on the stream for future events.
          if (shouldCatchUpRunningWorkflowTranscript(
            activeViewMode,
            useChatStore.getState().getTabEvents(running.session_id).length,
          )) {
            useChatStore.getState().beginWorkflowSessionRestore(running.session_id)
            try {
              await withWorkflowRestoreTimeout(
                restoreWorkflowStateFromEvents(
                  running.session_id,
                  workspacePath,
                  projection.metadata.isScheduledRun === true,
                ),
                `Restoring workflow events for ${running.session_id}`
              )
            } catch (error) {
              logger.warn('WorkflowLayout', 'Failed to hydrate newly discovered running workflow tab:', error)
            } finally {
              useChatStore.getState().endWorkflowSessionRestore(running.session_id)
            }
          }

        }

        if (shouldSwitch && selectedRunningTabId) {
          activateTab(selectedRunningTabId)
          setShowChatArea(true)
        } else if (activeTab?.sessionId && projectedRunningWorkflows.some(item => item.running.session_id === activeTab.sessionId)) {
          setShowChatArea(true)
        }
      } catch {
        /* Global activity monitor remains the source of truth if this lightweight reconcile misses. */
      } finally {
        runningWorkflowReconcileInFlightRef.current = false
      }
    }

    void reconcileRunningWorkflowTab()
    // Reconcile only ensures the active tab matches the latest running workflow
    // after a tab switch / app boot — it doesn't need sub-second cadence.
    // useRunningWorkflowsStore polls at 2–10s for live status; this slower tick
    // just catches occasional drift.
    const interval = window.setInterval(reconcileRunningWorkflowTab, 10000)
    return () => {
      cancelled = true
      window.clearInterval(interval)
    }
  }, [activePresetId, selectedModeCategory, setShowChatArea, workspacePath])


  // Auto-minimize workflows when switching to a different preset
  useEffect(() => {
    // Skip on initial mount (when previousPresetIdRef.current is null)
    if (previousPresetIdRef.current === null) {
      previousPresetIdRef.current = activePresetId
      return
    }

    // Skip auto-minimize during restore operations (flag is set by RunningWorkflowsDrawer)
    const isRestoringWorkflow = useRunningWorkflowsStore.getState().isRestoringWorkflow
    if (isRestoringWorkflow) {
      logger.debug('WorkflowLayout', 'Skipping auto-minimize during workflow restore')
      previousPresetIdRef.current = activePresetId
      return
    }

    // Check if preset actually changed (not just deps like selectedRunFolder)
    if (previousPresetIdRef.current !== activePresetId && activePresetId) {
      // Update ref immediately so dep-only re-fires don't re-enter this block
      const oldPreset = previousPresetIdRef.current
      previousPresetIdRef.current = activePresetId

      console.log(`%c[WorkflowLayout] Preset changed: ${oldPreset?.slice(0,8)} → ${activePresetId?.slice(0,8)}`, 'color: #FF9800; font-weight: bold')
      console.time(`[WorkflowLayout] preset-switch-effect-${activePresetId?.slice(0,8)}`)

      const chatStore = useChatStore.getState()
      const chatTabs = chatStore.chatTabs

      // Tabs from the old preset stay in memory with their events (hidden by preset filter).
      // We keep events because workflow events aren't stored in DB — clearing them would lose
      // them permanently if the backend's EventStore has already cleaned up.
      // Side effects (workspace refresh, canvas updates) are already skipped for non-active
      // preset tabs via the isActivePresetTab guard in processEventsResponse.

      // Switch active tab to one belonging to the new preset (or close chat area)
      const newPresetTabs = Object.values(chatTabs)
        .filter(t =>
          t.metadata?.mode === 'workflow' &&
          t.metadata?.presetQueryId === activePresetId
        )
        .sort((a, b) => workflowTabSortTimestamp(b) - workflowTabSortTimestamp(a))

      if (newPresetTabs.length > 0) {
        const pendingReadOnlyRestore = pendingReadOnlyRestoreRef.current
        const restoredReadOnlyTab = pendingReadOnlyRestore?.presetId === activePresetId
          ? newPresetTabs.find(t => t.tabId === pendingReadOnlyRestore.tabId && t.metadata?.isViewOnly)
          : undefined
        pendingReadOnlyRestoreRef.current = null
        // Prefer a read-only Schedule/Bot tab only for the immediate restore action.
        // Normal preset switches should not keep reopening stale scheduled-run tabs.
        const interactiveTabs = newPresetTabs.filter(t => !t.metadata?.isViewOnly)
        if (!restoredReadOnlyTab && interactiveTabs.length === 0) {
          void createFreshWorkflowBuilderTab(activePresetId)
          console.timeEnd(`[WorkflowLayout] preset-switch-effect-${activePresetId?.slice(0,8)}`)
          return
        }
        const streamingTab = interactiveTabs.find(t => chatStore.getTabStreamingStatus(t.tabId))
        const builderTab = interactiveTabs.find(t => t.metadata?.phaseId === 'workflow-builder')
        const targetTab = restoredReadOnlyTab || streamingTab || builderTab || interactiveTabs[0] || newPresetTabs[0]
        console.log(`[WorkflowLayout] Switching to tab: ${targetTab.tabId.slice(0,8)} (${newPresetTabs.length} tabs for preset, restoredReadOnly=${!!restoredReadOnlyTab}, streaming=${!!streamingTab}, builder=${!!builderTab})`)
        if (restoredReadOnlyTab) {
          revealWorkflowChat(restoredReadOnlyTab.tabId)
        } else {
          // The navigation coordinator carries the user's explicit Formatted
          // or Terminal preference across workflows while changing ownership.
          activateTab(targetTab.tabId)
          setShowChatArea(true)
        }
        // The persistent Chat always exists; keep the selection just made.
        void ensureWorkflowBuilderTab(activePresetId, { keepSelection: true })

        const selectedTarget = useChatStore.getState().chatTabs[targetTab.tabId]
        const targetViewMode = normalizeEventViewMode(selectedTarget?.viewMode || chatStore.eventViewModePreference)
        const tabsNeedingHydration = targetViewMode === 'formatted'
          ? workflowTabsNeedingHydration(newPresetTabs, tab => chatStore.getTabEvents(tab))
          : []
        if (tabsNeedingHydration.length > 0) {
          void rehydrateWorkflowTabs(newPresetTabs, workspacePath)
        }
      } else {
        console.log(`[WorkflowLayout] No tabs for new preset, clearing activeTabId`)
        // Clear activeTabId so the old preset's tab events don't bleed into the new preset's view
        useChatStore.setState({ activeTabId: null })
        // Respect restored per-preset showChatArea — don't force-close if it was open
        const restoredShowChatArea = useWorkflowStore.getState().showChatArea
        if (!restoredShowChatArea) {
          setShowChatArea(false)
        }
      }
      console.timeEnd(`[WorkflowLayout] preset-switch-effect-${activePresetId?.slice(0,8)}`)
    } else {
      // Update the ref for non-preset-change re-fires (dep changes only)
      previousPresetIdRef.current = activePresetId
    }
  }, [activePresetId, minimizeWorkflow, selectedRunFolder, setShowChatArea, rehydrateWorkflowTabs, createFreshWorkflowBuilderTab, workspacePath, revealWorkflowChat])

  // Note: Query submission is now handled via chatAreaCallbackRef when ChatArea mounts
  // No need for useEffect with setTimeout - callback ref is the proper React pattern

  // Handle phase start from toolbar (now accepts execution options directly)
  const handleStartPhase = useCallback(async (phaseId: string, executionOptions?: ExecutionOptions) => {
    // Ensure we're in workflow mode before starting phase
    if (activePresetId) {
      const currentMode = useModeStore.getState().selectedModeCategory
      if (currentMode !== 'workflow') {
        useModeStore.getState().setModeCategory('workflow')
      }
    }

    if (typeof phaseId !== 'string') {
      logger.error('WorkflowLayout', 'Invalid phaseId: expected string, got', typeof phaseId)
      return
    }

    if (!activePresetId) return

    const phase = getPhaseById(phaseId)
    const phaseName = phase?.title || phaseId

    // Single-pass tab lookup: find or create workflow tab
    const result = await findOrCreateWorkflowTab({ phaseId, activePresetId, phaseName })
    if (!result) {
      logger.error('WorkflowLayout', 'Failed to get or create tab for phase', phaseId)
      return
    }

    const { tab, isReusingTab } = result

    // If reusing an existing tab that's already running, just switch to view it
    if (isReusingTab && useChatStore.getState().getTabStreamingStatus(tab.tabId)) {
      logger.debug('WorkflowLayout', 'Tab already running, switching to view it')
      setShowChatArea(true)
      return
    }

    // Update workflow status in database (non-blocking)
    agentApi.updateWorkflow(activePresetId, phaseId, null, undefined).catch(error => {
      logger.error('WorkflowLayout', 'Failed to update workflow status:', error)
    })

    setCurrentWorkflowPhase(phaseId)
    // Clear any explicit view so the pane shows the last canvas view.
    setWorkflowWorkspaceView(null)

    // For chat-compatible phases, just open the tab without auto-submitting a query.
    // The user will type naturally in the chat input.
    if (isChatCompatiblePhase(phaseId)) {
      logger.debug('WorkflowLayout', `Chat-compatible phase ${phaseId} — opening tab for conversation`)
      setShowChatArea(true)
      return
    }

    // Submit the execution query
    const query = `Execute workflow phase: ${phaseId}`

    if (chatAreaRef.current) {
      // ChatArea already mounted (e.g. workflow builder was open) — submit directly
      chatAreaRef.current.submitQuery(query, executionOptions).catch(error => {
        logger.error('WorkflowLayout', 'Failed to submit execution query:', error)
      })
    } else {
      // ChatArea not mounted yet — store pending query for callback ref
      pendingQueryRef.current = { query, executionOptions }
    }

    // Show ChatArea (triggers mount if not already shown)
    setShowChatArea(true)
  }, [activePresetId, setCurrentWorkflowPhase, setShowChatArea, getPhaseById, setWorkflowWorkspaceView])

  // Handle create plan - always opens Automation Builder.
  const handleCreatePlan = useCallback(() => {
    // Ensure we're in workflow mode before creating plan (only if we have an active preset)
    if (activePresetId) {
      const currentMode = useModeStore.getState().selectedModeCategory
      if (currentMode !== 'workflow') {
        useModeStore.getState().setModeCategory('workflow')
      }
    }

    const phases = useWorkflowStore.getState().phases
    const workshopPhase = phases.find(p => p.id === 'workflow-builder')
    const phaseId = workshopPhase?.id || 'workflow-builder'
    logger.debug('WorkflowLayout', 'Create plan requested, starting workflow builder phase:', phaseId)
    setShowChatArea(true)
    handleStartPhase(phaseId)
  }, [handleStartPhase, setShowChatArea, activePresetId])

  const handleToggleChatArea = useCallback(() => {
    const newShow = !showChatArea
    if (newShow) {
      // Ensure a workflow tab is active when showing the chat panel
      // (activeTabId might point to a chat/multi-agent tab from a different mode)
      const chatStore = useChatStore.getState()
      const activeTab = chatStore.getActiveTab()
      if (
        !activeTab ||
        activeTab.metadata?.mode !== 'workflow' ||
        activeTab.metadata?.presetQueryId !== activePresetId ||
        activeTab.metadata?.isViewOnly
      ) {
        const workflowTabs = Object.values(chatStore.chatTabs)
          .filter(t =>
            isInteractiveWorkflowTab(t) &&
            t.metadata?.presetQueryId === activePresetId
          )
          .sort((a, b) => workflowTabSortTimestamp(b) - workflowTabSortTimestamp(a))
        if (workflowTabs.length > 0) {
          const builderTab = workflowTabs.find(t => t.metadata?.phaseId === 'workflow-builder')
          activateTab((builderTab || workflowTabs[0]).tabId)
        }
      }
    }
    setShowChatArea(newShow)
  }, [activePresetId, showChatArea, setShowChatArea])

  // Minimize chat area when drawer opens to reduce renders and stop event processing
  // Open chat area when drawer closes (but not on initial mount)
  const drawerMountedRef = useRef(false)
  useEffect(() => {
    if (!drawerMountedRef.current) {
      drawerMountedRef.current = true
      return
    }
    if (showRunningDrawer) {
      // Minimize chat area when drawer opens
      setShowChatArea(false)
      // When ChatArea is hidden, it will unmount, which stops:
      // 1. Event rendering (EventDisplay won't render)
      // 2. Polling management (useEffect hooks won't run)
      // This significantly reduces browser load
    } else {
      // Open chat area when drawer closes (user just closed the running workflows drawer)
      setShowChatArea(true)
    }
  }, [showRunningDrawer, setShowChatArea])

  // These slots own their subscriptions. Reusing their elements lets the
  // memoized workspace host ignore chat-only WorkflowLayout renders.
  const chatTabsSlot = useMemo(() => showChatArea ? <WorkflowChatTabs embedded /> : undefined, [showChatArea])
  const workshopPanel = useMemo(() => workspacePath ? (
    <AutomationHubPanel
      key={`${activePresetId || 'workflow'}:${workspacePath}`}
      entityType="workflow"
      relayMode={relayMode}
      relayWorkflowID={activeWorkflowManifest?.id}
      workspacePath={workspacePath}
      workflowScope={{ presetQueryId: activePresetId || undefined, workspacePath }}
      chatContent={<WorkflowPreviousChatsPanel primary chatOnly workspacePath={workspacePath} />}
    />
  ) : undefined, [activePresetId, workspacePath])

  // No preset selected state
  if (!activeWorkflowPreset && !workspacePath) {
    return (
      <div className={`flex flex-col h-full ${className}`}>
        <div className="flex-1 overflow-y-auto bg-gray-50 dark:bg-gray-900">
          <div className="flex min-h-full items-center justify-center py-6">
            {isRelaySurface ? <RelayIntro /> : (
              <div data-tour="automation-empty-state" className="flex flex-col items-center gap-4 text-center max-w-md">
                <div className="w-20 h-20 rounded-full bg-gray-200 dark:bg-gray-700 flex items-center justify-center">
                  <span className="text-4xl">🚀</span>
                </div>
                <div>
                  <h2 className="text-xl font-semibold text-gray-900 dark:text-gray-100">Select an Automation</h2>
                  <p className="text-sm text-gray-500 dark:text-gray-400 mt-2">
                    Open the workspace selector to choose or create an automation.
                  </p>
                </div>
              </div>
            )}
          </div>
        </div>
      </div>
    )
  }

  const canvasElement = (
    <WorkflowCanvas
      ref={canvasRef}
      workspacePath={workspacePath}
      presetQueryId={activePresetId}
      relayMode={relayMode}
      relayOutputStepID={activeWorkflowManifest?.relay_output_step_id}
      currentPhase={activePhase || currentWorkflowPhase}
      onStartPhase={handleStartPhase}
      onCreatePlan={onCreatePlan || handleCreatePlan}
      showChatArea={showChatArea}
      toolbarOnly={!workspacePaneVisible && showChatArea}
      // When the workspace pane is collapsed, keep the toolbar as a normal
      // first grid item. Spanning a non-existent second column would otherwise
      // leave the full-width chat occupying only half of a desktop viewport.
      sharedToolbar={showChatArea && workspacePaneVisible}
      chatTabsSlot={chatTabsSlot}
      workshopPanel={workshopPanel}
      paneClassName={layout.canvasPaneClassName}
      onToggleChatArea={handleToggleChatArea}
      className={showChatArea && !workspacePaneVisible ? '!h-auto shrink-0' : 'h-full'}
    />
  )

  return (
    <div className={`relative flex flex-col h-full ${className}`}>
      {/* Right-edge tab to re-open the preview pane after it's been collapsed
          (the on-pane collapse button hides it). Only when chat is shown and the
          pane is hidden. */}
      {showChatArea && !workspacePaneVisible && (
        <button
          type="button"
          onClick={() => setShowWorkspacePane(true)}
          title="Show dashboard / plan panel"
          aria-label="Show dashboard / plan panel"
          className="absolute right-0 top-1/2 z-30 hidden -translate-y-1/2 flex-col items-center gap-1.5 rounded-l-lg border border-r-0 border-border bg-background/95 py-3 pl-1.5 pr-1 text-muted-foreground shadow-md backdrop-blur-sm transition-colors hover:bg-muted hover:text-foreground md:flex"
        >
          <PanelRightOpen className="h-4 w-4" />
          <span className="[writing-mode:vertical-rl] text-[10px] font-semibold uppercase tracking-wider">Panel</span>
        </button>
      )}
      {!showChatArea && (
        <button
          type="button"
          onClick={handleToggleChatArea}
          title="Show chat panel"
          aria-label="Show chat panel"
          className="absolute left-0 top-1/2 z-30 hidden -translate-y-1/2 flex-col items-center gap-1.5 rounded-r-lg border border-l-0 border-border bg-background/95 py-3 pl-1 pr-1.5 text-muted-foreground shadow-md backdrop-blur-sm transition-colors hover:bg-muted hover:text-foreground md:flex"
        >
          <PanelLeftOpen className="h-4 w-4" />
          <span className="[writing-mode:vertical-rl] text-[10px] font-semibold uppercase tracking-wider">Chat</span>
        </button>
      )}
      {/* Main Content */}
      {/* Narrow layouts show one pane at a time, so clicks choose that pane.
          Desktop keeps both panes visible and must not mutate focus on ordinary
          clicks: that store update can make stateful workspace views repaint. */}
      <div
        ref={splitLayoutRef}
        className={layout.splitLayoutClassName}
        style={layout.splitLayoutStyle}
        onMouseDownCapture={showChatArea && workspacePaneVisible ? () => {
          if (window.innerWidth < 768) setFocusedPane('preview')
        } : undefined}
      >
        {layout.renderCanvasStandalone && canvasElement}

        {showChatArea && (
          <div
            data-tour="workflow-chat-pane"
            data-testid="tour-workflow-chat-pane"
            onMouseDownCapture={() => {
              if (window.innerWidth < 768) setFocusedPane('chat')
            }}
            className={layout.chatPaneClassName}>
            {/* WorkflowChatTabs now renders inline in the WorkflowToolbar (chatTabsSlot
                on canvasElement above) so the tabs + status + tools share one bar. */}

            {isActiveWorkflowSessionRestoring && (
              <div className="flex items-center gap-2 border-b border-blue-100 bg-blue-50 px-3 py-1.5 dark:border-blue-800/50 dark:bg-blue-900/20">
                <div className="h-3 w-3 animate-spin rounded-full border-2 border-gray-300 border-t-blue-600 dark:border-gray-600 dark:border-t-blue-400"></div>
                <span className="text-xs text-blue-600 dark:text-blue-400">Loading conversation...</span>
              </div>
            )}

            {contractUpgrade?.required && (
              <div className="flex items-center justify-between gap-3 border-b border-border bg-muted/60 px-4 py-2 text-sm">
                <span className="min-w-0 flex-1 text-muted-foreground">
                  {startingContractUpgrade
                    ? 'Opening the workflow update in chat…'
                    : contractUpgradeTab
                      ? contractUpgradeQueued
                        ? 'Workflow update requested. The message is queued in chat and will start when the chat is ready.'
                        : contractUpgradeStreaming
                          ? 'Workflow update is in progress in chat. The pending notice will clear when the migrations are applied.'
                          : 'Workflow update requested in chat. Continue there to finish the migrations or answer any questions.'
                      : <>
                        This workflow needs a manual platform update before you can start it from chat
                        {contractUpgrade.current_version && contractUpgrade.platform_version
                          ? contractUpgrade.current_version === contractUpgrade.platform_version
                            ? ' (code layout update required).'
                            : ` (v${contractUpgrade.current_version} → v${contractUpgrade.platform_version}).`
                          : '.'}
                        {' '}Schedules keep running the saved version and will not update it automatically.
                      </>}
                </span>
                {canStartContractUpgrade ? (
                  <button
                    type="button"
                    onClick={() => {
                      if (contractUpgradeTab) {
                        activateTab(contractUpgradeTab.tabId)
                        setFocusedPane('chat')
                      } else {
                        void startManualContractUpgrade()
                      }
                    }}
                    disabled={startingContractUpgrade}
                    className="shrink-0 rounded-md bg-primary px-2.5 py-1 text-xs font-medium text-primary-foreground hover:bg-primary/90 disabled:cursor-not-allowed disabled:opacity-60"
                  >
                    {startingContractUpgrade ? 'Opening…' : contractUpgradeTab ? 'Open chat' : 'Update workflow'}
                  </button>
                ) : null}
              </div>
            )}

            {/* Initial goal setup (goal -> plan -> metrics); optional and dismissible. */}
            {!relayMode && <WorkflowGoalSetupBar workspacePath={workspacePath} canEdit={activeWorkflowAccess !== 'read'} />}

            <div className="relative min-h-0 flex-1 overflow-hidden">
              {isWorkflowConversationResolving && (
                <div className="absolute inset-0 z-10 flex items-center justify-center gap-2 bg-background text-sm text-muted-foreground">
                  <div className="h-4 w-4 animate-spin rounded-full border-2 border-muted-foreground/30 border-t-muted-foreground" />
                  Loading conversation...
                </div>
              )}
              <div className={isWorkflowConversationResolving ? 'invisible h-full pointer-events-none' : 'h-full'}>
                <ChatAreaWithObserverId
                  ref={chatAreaCallbackRef}
                  onNewChat={onNewChat}
                  hideHeader
                  compact
                  workflowLandingContent={<WorkflowNewChatGuide relayMode={relayMode} />}
                  // Writers only: a reader of a shared workflow has no Builder chat to replace. The previous
                  // conversation stays in Previous chats (each Builder session keeps its own transcript file).
                  showNewChatAction={activeWorkflowAccess !== 'read'}
                />
              </div>
            </div>
          </div>
        )}

        {workspacePaneVisible && canvasElement}

        {showChatArea && workspacePaneVisible && (
          <WorkspaceSplitRail
            ratio={workspaceSplitRatio}
            onPointerDown={handleSplitPointerDown}
            onStep={delta => { if (reportPreviewPreference !== 'mobile') setSplitRatio(workspaceSplitRatioRef.current + delta, true) }}
            className="md:row-start-2"
            previewDevice={reportPreviewPreference}
            onPreviewDeviceChange={device => writeReportPreviewPreference(workspacePath, device)}
            onCollapseChat={collapseChatFromRail}
            onCollapseWorkspace={collapseWorkspaceFromRail}
          />
        )}
      </div>
    </div>
  )
}

export default WorkflowLayout

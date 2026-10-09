import { activateTab } from './activateTab'
import { requestChatScrollToBottom } from './chatScrollRequest'
import { restoreSession } from './sessionRestore'
import { hydrateExecutionConversation } from './executionConversationRestore'
import { agentApi } from '../services/api'
import type { ActiveSessionInfo, RunningWorkflowInfo } from '../services/api-types'
import { useChatStore, type ChatTab } from '../stores/useChatStore'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { useRunningWorkflowsStore } from '../stores/useRunningWorkflowsStore'
import { useWorkflowStore } from '../stores/useWorkflowStore'
import type { CustomPreset, PredefinedPreset } from '../types/preset'
import { isScheduledSession, isInternalChildSession } from './workflowSessionKinds'
import { isVisibleActivitySession } from './activitySessions'
import { normalizeWorkspacePath } from './workspacePathUtils'
import { activateWorkflowTab, beginWorkflowNavigation, isCurrentWorkflowNavigation, selectWorkflowPreset } from './workflowNavigation'
import { scheduleTabLabel } from './scheduleTabLabel'
import { resolveWorkflowTabForSession } from './workflowTabResolution'
import { cachedWorkflowTabIdForPreset } from './workflowTabOwnership'

type RestoreWorkflowSessionOptions = {
  preset?: CustomPreset | PredefinedPreset
  runningWorkflow?: RunningWorkflowInfo
  scrollToBottom?: boolean
  navigationGeneration?: number
}

type OpenWorkflowPresetPageOptions = {
  activeSession?: ActiveSessionInfo
  runningWorkflow?: RunningWorkflowInfo
  title?: string
  source?: string
  scrollToBottom?: boolean
}

function isPresetStillActive(presetId?: string | null): boolean {
  return !presetId || useGlobalPresetStore.getState().activePresetIds.workflow === presetId
}

function activateRestoredTab(tabId: string, generation?: number): boolean {
  const tab = useChatStore.getState().chatTabs[tabId]
  // Legacy run records can lack workflow ownership metadata. The base
  // coordinator still reveals their chat without changing the report owner.
  if (!tab?.metadata?.presetQueryId) return activateTab(tabId)
  return activateWorkflowTab(tabId, { expectedGeneration: generation })
}

function isActiveWorkflowSession(session: ActiveSessionInfo): boolean {
  const status = (session.status || '').toLowerCase().trim()
  return (
    session.needs_user_input === true ||
    session.has_running_background_agents === true ||
    (session.running_background_agent_count ?? 0) > 0 ||
    status === 'running' ||
    status === 'active' ||
    status === 'in_progress' ||
    status === 'paused' ||
    status === 'idle' ||
    status === 'waiting' ||
    status === 'waiting_feedback'
  )
}

function findTabForSession(tabs: Record<string, ChatTab>, sessionId: string): ChatTab | undefined {
  return Object.values(tabs).find(tab => tab.sessionId === sessionId)
}

function isWorkflowSession(session: ActiveSessionInfo): boolean {
  return session.agent_mode === 'workflow' ||
    session.agent_mode === 'workflow_phase' ||
    !!session.workflow_name ||
    !!session.workflow_label ||
    !!session.workspace_path ||
    !!session.preset_query_id
}

function workflowSessionMatchesPreset(
  session: ActiveSessionInfo,
  preset: CustomPreset | PredefinedPreset,
  tabs: Record<string, ChatTab>,
): boolean {
  if (!isWorkflowSession(session)) return false
  if (session.preset_query_id === preset.id) return true
  if (
    normalizeWorkspacePath(session.workspace_path) &&
    normalizeWorkspacePath(session.workspace_path) === normalizeWorkspacePath(preset.selectedFolder?.filepath)
  ) return true
  const tab = findTabForSession(tabs, session.session_id)
  return tab?.metadata?.presetQueryId === preset.id
}

function workflowSessionPriority(session: ActiveSessionInfo): number {
  const status = (session.status || '').toLowerCase()
  let score = 0
  if (isScheduledWorkflowSession(session) || isBotWorkflowSession(session)) score += 100
  // A retained/live tmux session is the only thing that can actually render the
  // terminal pane. Prefer it over a plain running-session registry row so global
  // workflow switches don't land on an empty Schedule tab.
  if (session.has_retained_tmux_session) score -= 25
  if (session.needs_user_input) score -= 30
  if (session.has_running_background_agents || (session.running_background_agent_count ?? 0) > 0) score -= 20
  if (status === 'running' || status === 'active' || status === 'in_progress') score -= 10
  return score
}

export function pickWorkflowActiveSession(
  sessions: ActiveSessionInfo[],
  preset: CustomPreset | PredefinedPreset,
  tabs: Record<string, ChatTab>,
): ActiveSessionInfo | undefined {
  return sessions
    .filter(session => !isInternalChildSession({
      parentSessionId: session.parent_session_id,
      sessionKind: session.session_kind,
    }))
    .filter(isVisibleActivitySession)
    .filter(session => workflowSessionMatchesPreset(session, preset, tabs))
    .sort((a, b) => {
      const priorityDelta = workflowSessionPriority(a) - workflowSessionPriority(b)
      if (priorityDelta !== 0) return priorityDelta
      return Date.parse(b.last_activity || b.created_at || '') - Date.parse(a.last_activity || a.created_at || '')
    })[0]
}

function runningWorkflowFallbackName(workflow: RunningWorkflowInfo): string {
  return workflow.preset_name ||
    workflow.title ||
    workflow.workspace_path?.split('/').filter(Boolean).pop() ||
    workflow.query ||
    'Automation'
}

function sessionFromRunningWorkflow(workflow: RunningWorkflowInfo): ActiveSessionInfo {
  const timestamp = workflow.started_at || new Date().toISOString()
  const label = runningWorkflowFallbackName(workflow)
  return {
    session_id: workflow.session_id,
    observer_id: '',
    agent_mode: 'workflow',
    status: workflow.status || 'running',
    last_activity: timestamp,
    created_at: timestamp,
    query: workflow.query || label,
    title: workflow.title || label,
    workflow_name: label,
    workflow_label: label,
    workspace_path: workflow.workspace_path,
    preset_name: workflow.preset_name,
    preset_query_id: workflow.preset_query_id,
    triggered_by: workflow.triggered_by,
    current_execution_name: workflow.current_step_title || workflow.phase_name || workflow.title,
    needs_user_input: workflow.needs_user_input,
    waiting_message: workflow.waiting_message,
    waiting_since: workflow.waiting_since,
  }
}

function isRunningWorkflowEntry(entry: RunningWorkflowInfo): boolean {
  const status = (entry.status || '').toLowerCase()
  return status === 'running' ||
    status === 'active' ||
    status === 'in_progress' ||
    status === 'paused' ||
    status === 'waiting' ||
    status === 'waiting_for_input' ||
    status === 'waiting_feedback' ||
    !!entry.needs_user_input
}

function runningWorkflowMatchesPreset(
  workflow: RunningWorkflowInfo,
  preset: CustomPreset | PredefinedPreset,
): boolean {
  if (workflow.preset_query_id && workflow.preset_query_id === preset.id) return true
  const workflowPath = normalizeWorkspacePath(workflow.workspace_path)
  const presetPath = normalizeWorkspacePath(preset.selectedFolder?.filepath)
  return !!workflowPath && !!presetPath && workflowPath === presetPath
}

async function findRunningWorkflowForPreset(
  preset: CustomPreset | PredefinedPreset,
): Promise<RunningWorkflowInfo | undefined> {
  try {
    const response = await agentApi.listRunningWorkflows()
    return (response.running || [])
      .filter(workflow => workflow.session_id && isRunningWorkflowEntry(workflow))
      .filter(workflow => runningWorkflowMatchesPreset(workflow, preset))
      .sort((a, b) => new Date(b.started_at || 0).getTime() - new Date(a.started_at || 0).getTime())[0]
  } catch {
    return undefined
  }
}

export function isScheduledWorkflowSession(session: ActiveSessionInfo, runningWorkflow?: RunningWorkflowInfo): boolean {
  return isScheduledSession({ sessionId: session.session_id, triggeredBy: session.triggered_by || runningWorkflow?.triggered_by })
}

export function workflowSessionBotPlatform(
  session: ActiveSessionInfo,
  runningWorkflow?: RunningWorkflowInfo,
): string | undefined {
  const rawPlatform = (session.bot_platform || '').trim()
  const candidates = [
    rawPlatform,
    session.triggered_by,
    runningWorkflow?.triggered_by,
    session.session_id,
  ].map(value => (value || '').toLowerCase())

  if (rawPlatform) {
    if (rawPlatform.toLowerCase() === 'whatsapp') return 'WhatsApp'
    if (rawPlatform.toLowerCase() === 'slack') return 'Slack'
    return rawPlatform
  }
  if (candidates.some(value => value.includes('whatsapp'))) return 'WhatsApp'
  if (candidates.some(value => value.includes('slack'))) return 'Slack'
  if (candidates.some(value => value.includes('bot:') || value === 'bot' || value.includes('_bot'))) return 'Bot'
  return undefined
}

function isBotWorkflowSession(session: ActiveSessionInfo, runningWorkflow?: RunningWorkflowInfo): boolean {
  return !!workflowSessionBotPlatform(session, runningWorkflow)
}

// A plain switch to a workflow follows a live session only when it is the
// user's own conversation with it. Schedule, webhook, bot and external runs
// are separate read-only lanes: following them took the user to a schedule's
// chat instead of their main chat (server A 2026-09-28). They stay reachable from
// their run tabs, Active work and Ctrl+K, which open them explicitly.
export function followsOnWorkflowSwitch(session: ActiveSessionInfo, runningWorkflow?: RunningWorkflowInfo): boolean {
  if (isScheduledWorkflowSession(session, runningWorkflow) || isBotWorkflowSession(session, runningWorkflow)) return false
  const trigger = (session.triggered_by || runningWorkflow?.triggered_by || '').toLowerCase()
  return trigger !== 'external'
}

function findWorkflowPresetForSession(
  session: ActiveSessionInfo,
  runningWorkflow?: RunningWorkflowInfo,
): CustomPreset | PredefinedPreset | undefined {
  const presetStore = useGlobalPresetStore.getState()
  const presetId = session.preset_query_id || runningWorkflow?.preset_query_id
  if (presetId) {
    const byId = presetStore.workflowPresets.find(preset => preset.id === presetId)
    if (byId) return byId
  }

  const workspacePath = normalizeWorkspacePath(runningWorkflow?.workspace_path || session.workspace_path)
  if (!workspacePath) return undefined
  return presetStore.workflowPresets.find(preset =>
    normalizeWorkspacePath(preset.selectedFolder?.filepath) === workspacePath
  )
}

// Bring the chat pane forward. Deliberately does not touch the workflow's
// saved preview device / split width -- see utils/reportPreviewPreference.ts.
function revealWorkflowChat(): void {
  const workflowStore = useWorkflowStore.getState()
  workflowStore.setShowChatArea(true)
  workflowStore.setShowWorkspacePane(true)
  workflowStore.setFocusedPane('chat')
}

async function restoreWorkflowSessionChat(
  session: ActiveSessionInfo,
  options: RestoreWorkflowSessionOptions = {},
): Promise<string> {
  const resolvedPreset = options.preset || findWorkflowPresetForSession(session, options.runningWorkflow)
  const presetId = resolvedPreset?.id || session.preset_query_id || options.runningWorkflow?.preset_query_id
  const isActive = isActiveWorkflowSession(session)

  useRunningWorkflowsStore.getState().setIsRestoringWorkflow(true)
  try {
    if (resolvedPreset) {
      selectWorkflowPreset(resolvedPreset)
    } else if (presetId) {
      selectWorkflowPreset(presetId)
    }

    // One Chat tab per workflow: a tab already on this session, else the
    // workflow's blank/idle Chat tab rebound to it, else a new one. A tab
    // the composer renamed on first message is still this session's tab --
    // it used to be closed and re-created here, which is one way a second
    // "Chat" appeared.
    const latestChatStore = useChatStore.getState()
    const { tabId, via } = await resolveWorkflowTabForSession({
      getTabs: () => useChatStore.getState().chatTabs,
      getTabEvents: () => useChatStore.getState().tabEvents,
      presetQueryId: presetId ?? '',
      sessionId: session.session_id,
      name: 'Automation Builder',
      metadata: {
        mode: 'workflow',
        phaseId: 'workflow-builder',
        phaseName: 'Automation Builder',
        presetQueryId: presetId,
      },
      createChatTab: latestChatStore.createChatTab,
      updateTabSessionId: latestChatStore.updateTabSessionId,
    })

    // Tab creation can yield while the user selects another workflow. Keep the
    // cached tab, but never let the stale restore activate it over the newer
    // report/workspace selection.
    if (!isPresetStillActive(presetId)) return tabId

    const hasExistingEvents = latestChatStore.getTabEvents(session.session_id).length > 0
    // Fast path for switching back to an already-open running workflow:
    // keep the in-memory event buffer and SSE connection intact. Re-fetching
    // recent events here replaces the tab event array and makes Ctrl+K feel
    // like a reload even though the workflow chat is already live.
    if (via === 'existing' && hasExistingEvents) {
      latestChatStore.setTabStreaming(tabId, isActive)
      latestChatStore.setTabCompleted(tabId, !isActive)
      if (!activateRestoredTab(tabId, options.navigationGeneration)) return tabId
      revealWorkflowChat()
      if (options.scrollToBottom === true) requestChatScrollToBottom()
      return tabId
    }

    // Reveal the terminal/chat IMMEDIATELY. Event history is intentionally not
    // hydrated here; workflow switches default to terminal/report surfaces, and
    // the tree/debug view lazy-loads events only when the user opens it.
    latestChatStore.setTabStreaming(tabId, isActive)
    latestChatStore.setTabCompleted(tabId, !isActive)
    if (!activateRestoredTab(tabId, options.navigationGeneration)) return tabId
    revealWorkflowChat()
    if (options.scrollToBottom === true) requestChatScrollToBottom()

    return tabId
  } finally {
    useRunningWorkflowsStore.getState().setIsRestoringWorkflow(false)
  }
}

async function restoreScheduledWorkflowRunChat(
  session: ActiveSessionInfo,
  options: RestoreWorkflowSessionOptions = {},
): Promise<string> {
  // The scheduler stamps the job name on Title. preset_name identifies its
  // owning workflow; preferring it renamed a schedule tab on Ctrl+K/monitor
  // opens (e.g. "News Briefing" became "trading"). Keep a known job name
  // when a sparse activity record omits its title.
  const existingJobName = findTabForSession(useChatStore.getState().chatTabs, session.session_id)
    ?.metadata?.scheduledJobName
  const jobName = session.title?.trim() ||
    existingJobName?.trim() ||
    options.runningWorkflow?.title?.trim() ||
    options.runningWorkflow?.preset_name?.trim() ||
    session.preset_name?.trim() ||
    'Scheduled run'

  return restoreReadOnlyWorkflowRunChat(session, {
    ...options,
    tabName: scheduleTabLabel(jobName),
    metadata: {
      isScheduledRun: true,
      scheduledJobName: jobName,
      isBotRun: false,
      botPlatform: undefined,
    },
  })
}

async function restoreBotWorkflowRunChat(
  session: ActiveSessionInfo,
  options: RestoreWorkflowSessionOptions = {},
): Promise<string> {
  const platform = workflowSessionBotPlatform(session, options.runningWorkflow) || 'Bot'
  return restoreReadOnlyWorkflowRunChat(session, {
    ...options,
    tabName: platform,
    metadata: {
      isScheduledRun: false,
      scheduledJobName: undefined,
      isBotRun: true,
      botPlatform: platform,
    },
  })
}

export async function openWorkflowPresetPage(
  preset: CustomPreset | PredefinedPreset,
  options: OpenWorkflowPresetPageOptions = {},
): Promise<void> {
  const navigationGeneration = beginWorkflowNavigation(preset.id)
  selectWorkflowPreset(preset)

  const title = options.title || preset.label || 'Automation'
  const chatStore = useChatStore.getState()

  // Switching back to a workflow seen earlier in this page: show its last
  // conversation from memory at once. The lookups below still run and move
  // to another tab only when they resolve a different conversation.
  const cachedTabId = options.activeSession
    ? undefined
    : cachedWorkflowTabIdForPreset(preset.id, chatStore.chatTabs, chatStore.tabEvents)
  if (cachedTabId) {
    activateWorkflowTab(cachedTabId, { expectedGeneration: navigationGeneration })
    useWorkflowStore.getState().setShowChatArea(true)
  }

  if (options.activeSession) {
    await openActiveSession(options.activeSession, {
      preset,
      runningWorkflow: options.runningWorkflow,
      title,
      source: options.source,
      navigationGeneration,
    })
    return
  }

  // Both reads are independent; start the running-registry read alongside
  // the active-session read instead of after it.
  const runningWorkflowPromise = options.runningWorkflow
    ? Promise.resolve(options.runningWorkflow)
    : findRunningWorkflowForPreset(preset)
  const activeSession = pickWorkflowActiveSession(await chatStore.getActiveSessions(), preset, useChatStore.getState().chatTabs)

  if (!isCurrentWorkflowNavigation(navigationGeneration, preset.id)) return

  if (activeSession && followsOnWorkflowSwitch(activeSession)) {
    await openActiveSession(activeSession, {
      preset,
      runningWorkflow: options.runningWorkflow,
      title,
      source: options.source,
      navigationGeneration,
    })
    return
  }

  const runningWorkflow = await runningWorkflowPromise
  if (!isCurrentWorkflowNavigation(navigationGeneration, preset.id)) return
  if (runningWorkflow?.session_id && followsOnWorkflowSwitch(sessionFromRunningWorkflow(runningWorkflow), runningWorkflow)) {
    await openActiveSession(sessionFromRunningWorkflow(runningWorkflow), {
      preset,
      runningWorkflow,
      title,
      source: options.source,
      navigationGeneration,
    })
    return
  }

  const latestStore = useChatStore.getState()
  const tabId = (await resolveWorkflowTabForSession({
    getTabs: () => useChatStore.getState().chatTabs,
    getTabEvents: () => useChatStore.getState().tabEvents,
    presetQueryId: preset.id,
    name: 'Automation Builder',
    metadata: { mode: 'workflow', phaseId: 'workflow-builder', phaseName: 'Automation Builder', presetQueryId: preset.id },
    createChatTab: latestStore.createChatTab,
    updateTabSessionId: latestStore.updateTabSessionId,
  })).tabId

  if (!isCurrentWorkflowNavigation(navigationGeneration, preset.id)) return

  activateWorkflowTab(tabId, { expectedGeneration: navigationGeneration })
  useWorkflowStore.getState().setShowChatArea(true)
  if (options.scrollToBottom === true) requestChatScrollToBottom()
}

type ReadOnlyWorkflowRunOptions = RestoreWorkflowSessionOptions & {
  tabName: string
  metadata: NonNullable<ChatTab['metadata']>
}

async function restoreReadOnlyWorkflowRunChat(
  session: ActiveSessionInfo,
  options: ReadOnlyWorkflowRunOptions,
): Promise<string> {
  const resolvedPreset = options.preset || findWorkflowPresetForSession(session, options.runningWorkflow)
  const presetId = resolvedPreset?.id || session.preset_query_id || options.runningWorkflow?.preset_query_id
  const workspacePath = resolvedPreset?.selectedFolder?.filepath || options.runningWorkflow?.workspace_path || session.workspace_path || null

  useRunningWorkflowsStore.getState().setIsRestoringWorkflow(true)
  try {
  if (resolvedPreset) {
    selectWorkflowPreset(resolvedPreset)
  } else if (presetId) {
    selectWorkflowPreset(presetId)
  }

  const chatStore = useChatStore.getState()
  const metadata = {
    mode: 'workflow' as const,
    phaseId: undefined,
    phaseName: undefined,
    ...(presetId ? { presetQueryId: presetId } : {}),
    isViewOnly: true,
    isExecutionRun: true,
    ...options.metadata,
    readOnlyRestoredAt: Date.now(),
  }
  const desiredName = options.tabName

  // If the user already converted this run into an interactive chat (same
  // sessionId, view-only cleared via WorkflowChatTabs.handleMakeInteractive),
  // don't recreate a read-only run tab or revert it back to view-only — just
  // focus the existing interactive tab. Otherwise the header pill / activity
  // monitor would spawn a duplicate 'Schedule' tab for the same session.
  const interactiveTab = findTabForSession(chatStore.chatTabs, session.session_id)
  if (interactiveTab && !interactiveTab.metadata?.isViewOnly) {
    if (!activateRestoredTab(interactiveTab.tabId, options.navigationGeneration)) return interactiveTab.tabId
    revealWorkflowChat()
    if (options.scrollToBottom === true) requestChatScrollToBottom()
    return interactiveTab.tabId
  }

  // Opened from the Global Activity Monitor / activity list. Same resolution
  // as WorkflowLayout's reconciler: a new run of a schedule lands in that
  // schedule's existing tab rather than opening one beside it.
  const { tabId, via } = await resolveWorkflowTabForSession({
    getTabs: () => useChatStore.getState().chatTabs,
    presetQueryId: metadata.presetQueryId ?? presetId ?? '',
    sessionId: session.session_id,
    name: desiredName,
    metadata,
    createChatTab: chatStore.createChatTab,
    updateTabSessionId: chatStore.updateTabSessionId,
  })
  if (!isPresetStillActive(presetId)) return tabId
  chatStore.setTabMetadata(tabId, metadata)
  if (via !== 'created' && useChatStore.getState().chatTabs[tabId]?.name !== desiredName) {
    useChatStore.setState((state) => {
      const tab = state.chatTabs[tabId]
      if (!tab) return state
      return { chatTabs: { ...state.chatTabs, [tabId]: { ...tab, name: desiredName } } }
    })
  }

  // Reveal the tab IMMEDIATELY, same as the plain workflow-builder restore
  // path (restoreWorkflowSessionChat) -- don't make the user stare at a blank
  // switch while a scheduled run's transcript loads. Hydration below fills the
  // tab in once it lands instead of gating the reveal on it.
  const isActive = isActiveWorkflowSession(session)
  chatStore.setTabStreaming(tabId, isActive)
  chatStore.setTabCompleted(tabId, !isActive)
  if (!activateRestoredTab(tabId, options.navigationGeneration)) return tabId
  revealWorkflowChat()
  window.dispatchEvent(new CustomEvent('workflow-readonly-run-restored', {
    detail: { presetId, tabId, workspacePath }
  }))
  if (options.scrollToBottom === true) requestChatScrollToBottom()

  // Revisiting an already-open run preserves its loaded history and cursor.
  // Polling/SSE keep a live run current; a first open still restores its JSON.
  const store = useChatStore.getState()
  if (via === 'existing' && store.getTabEvents(session.session_id).length > 0 &&
      store.tabEventIndices[session.session_id] !== undefined) return tabId

  // A scheduled run is an execution diagnostic, not an interactive chat.
  // Read its saved JSON explicitly without admitting it to SQLite chat.
  void hydrateExecutionConversation(session.session_id, workspacePath || undefined).catch(error => {
    console.warn('[WorkflowSessionRestore] could not hydrate saved schedule transcript', error)
  })

  return tabId
  } finally {
    useRunningWorkflowsStore.getState().setIsRestoringWorkflow(false)
  }
}

// openActiveSession is the SINGLE shared path for opening an active session row
// from a global surface (the Ctrl+K quick-switcher and the header activity
// monitor). Both call this so clicking the same session behaves identically.
//
// Workflow sessions go through the thorough restore family, which already: jumps
// to an existing tab, closes a stale builder tab, applies the preset, switches to
// workflow mode, clears the Workflows Overview, and scrolls to bottom. Plain chat
// sessions activate their existing tab or restore a fresh one.
async function openActiveSession(
  session: ActiveSessionInfo,
  options: { preset?: CustomPreset | PredefinedPreset; runningWorkflow?: RunningWorkflowInfo; title?: string; source?: string; navigationGeneration?: number } = {},
): Promise<void> {
  const isWorkflow = (session.agent_mode || '').toLowerCase().includes('workflow')
  if (isWorkflow) {
    if (isScheduledWorkflowSession(session, options.runningWorkflow)) {
      await restoreScheduledWorkflowRunChat(session, { preset: options.preset, runningWorkflow: options.runningWorkflow, navigationGeneration: options.navigationGeneration })
    } else if (isBotWorkflowSession(session, options.runningWorkflow)) {
      await restoreBotWorkflowRunChat(session, { preset: options.preset, runningWorkflow: options.runningWorkflow, navigationGeneration: options.navigationGeneration })
    } else {
      await restoreWorkflowSessionChat(session, { preset: options.preset, runningWorkflow: options.runningWorkflow, navigationGeneration: options.navigationGeneration })
    }
    return
  }


  const chatStore = useChatStore.getState()
  const existingTab = findTabForSession(chatStore.chatTabs, session.session_id)
  if (existingTab) {
    activateTab(existingTab.tabId)
    return
  }
  const tabId = await restoreSession(session.session_id, { title: options.title, source: options.source })
  activateTab(tabId)
}

// Global workflow navigation is workflow-scoped, not child-execution-scoped.
// Resolve the workflow preset first so every global entry point lands on the
// canonical root/main session selected by openWorkflowPresetPage.
export async function openCanonicalActivitySession(
  session: ActiveSessionInfo,
  options: { title?: string; source?: string } = {},
): Promise<void> {
  const isWorkflow = (session.agent_mode || '').toLowerCase().includes('workflow')
  if (isWorkflow) {
    const preset = findWorkflowPresetForSession(session)
    if (preset) {
      // The user picked THIS session (Ctrl+K, an activity pill). Carry it
      // through; without it openWorkflowPresetPage re-derives "the" session
      // for the workflow and can land on a blank Chat first.
      await openWorkflowPresetPage(preset, { ...options, activeSession: session })
      return
    }
  }
  await openActiveSession(session, options)
}

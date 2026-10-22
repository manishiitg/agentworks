import { setWorkspaceViewTarget } from '../../hooks/useWorkspaceViewTarget'
import { closeWorkspaceChatTab } from '../../utils/closeWorkspaceChatTab'
import { useWorkspaceReturn } from '../../hooks/useWorkspaceReturn'
import { currentProductRoute, routeView } from '../../utils/productRoutes'
import { useProductUrlView } from '../../hooks/useProductUrlView'
import { replaceProductSelection } from '../../utils/productUrlNavigation'
import { markFeatureUsed } from '../../utils/featureUsage'
import { recordTabReorderTelemetry } from '../../utils/chatDeliveryTelemetry'
import { readWorkspaceViewPreference, writeWorkspaceViewPreference } from '../../utils/workspaceViewPreference'
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type DragEvent as ReactDragEvent, type PointerEvent as ReactPointerEvent } from 'react'
import { ArrowDown, ArrowUp, CalendarClock, UsersRound, Sparkles, Check, ChevronDown, GripVertical, Loader2, Pencil, Pin, PinOff, Plus, Trash2, X } from 'lucide-react'
import { useShallow } from 'zustand/react/shallow'
import ChatArea from '../../components/ChatArea'
import { ProductChatLandingCard } from '../../components/chat/ProductChatLandingCard'
import { CrewStarterGuide } from '../../components/chat/ProductStarterGuide'
import { NewTabHint } from '../../components/chat/ShortcutHint'
import { ProductIntro } from '../../components/ProductIntro'
import { GlobalHumanFeedbackPrompt } from '../../components/GlobalHumanFeedbackPrompt'
import { ModePresetBar } from '../../components/ModePresetBar'
import { TerminalFocusLayout } from '../../components/TerminalFocusLayout'
import SchedulesPage from '../../components/SchedulesPage'
import AdminPages from '../../components/AdminPages'
import LlmModalHost from '../../components/topbar/LlmModalHost'
import { TopBarEntitySelector } from '../../components/topbar/TopBarEntitySelector'
import { UpdateProgressToast } from '../../components/UpdateProgressToast'
import api, { agentApi } from '../../services/api'
import type { AgentProfileSideChat } from '../../services/api-types'
import { useWorkChatModels } from './useWorkChatModels'
import { useAppStore } from '../../stores/useAppStore'
import { useChatStore, waitForChatStoreHydration, type ChatTab } from '../../stores/useChatStore'
import { useModeStore } from '../../stores/useModeStore'
import { useLLMStore } from '../../stores/useLLMStore'
import { hydrateTabEvents } from '../../utils/sessionRestore'
import { activateTab } from '../../utils/activateTab'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'
import { loadWorkProductCommands } from './workData'
import { CREW_PRODUCT, ProjectProductProvider, type ProjectProductConfig } from './projectProduct'
import { workProjectActivity, workSideChatDisplayName } from './workProjectPresentation'
import { CreateCrewWizard } from './CreateCrewWizard'
import { prepareCrewOnboarding } from './crewOnboarding'
import type { CrewOnboarding } from './crewStarterTypes'
import { projectMenuHistoryScope } from '../../stores/useProjectMenuHistoryStore'
import { setProductCommands } from '../../commands/registry'
import { toProductCommandDefinitions } from './productCommands'
import { createWorkSession, deleteWorkSession, installWorkSessionTemplate, removeWorkSessionTemplate, loadWorkSessionsIncludingShared, isWorkSessionRun, updateWorkSessionIdentity, workLLMConfigFromSelection, workLLMSelectionFromConfig, type WorkSession } from './workSessions'
import { WorkWorkspacePane, WorkWorkspaceToolbar, type WorkWorkspaceView } from './WorkWorkspacePane'
import { loadWorkspaceLandingView } from '../../components/workflow/workspaceLandingView'
import { isWorkWorkspaceViewEnabled } from './workViewGating'
import { setProjectMode } from './codeLocalFiles'
import { useProductCreateRequest } from '../../hooks/useProductCreateRequest'
import { usePointerDrag } from '../../hooks/usePointerDrag'
import { WorkspaceSplitRail } from '../../components/workspace/WorkspaceSplitDivider'
import { clampWorkSplitRatio } from './workSurfaceLayoutResolver'
import { ProductWorkspaceShell } from '../../components/workspace/ProductWorkspaceShell'
import { loadAgentProfileInteractionKinds, loadAgentProfileUIPanels } from '../../utils/agentProfileCapabilities'
import { parseProductInteraction } from '../../../shared/session/interactions'
import { belongsToWorkProject, findCanonicalWorkProjectTab, isWorkSideChatTab, markWorkProjectRuntimeDirty, setWorkProjectRuntimeSelection, workChatTabShortcut, workSideChatKey, workTabToKeepActive, WORK_SIDE_CHAT_LIMIT, type WorkRuntimeSelection } from './workTabs'
import { productMode, updateProductProjectLLMConfig, updateProductProjectNativeAgentTools, updateProductProjectSelections, type ProductIdentityPatch, type ProductMode } from '../../platform/chat/productProjects'
import { type RunsOnSelection } from './RunsOnPicker'
import { rememberRunsOn } from './runsOnMemory'
import { crewTemplates, type CrewTemplateId } from './crewTemplates'
import { WorkTemplateSetup } from './WorkTemplateSetup'
import { useWorkspaceUIControl, type WorkspaceUIControlAdapter } from '../../platform/ui-control/useWorkspaceUIControl'
import { usePresentationEvents } from '../../platform/presentations/usePresentationEvents'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { hasProductCreateAccess } from '../../utils/workflowPermissions'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { EntityIdentityIcon } from '../../components/ui/EntityIdentityIcon'
import ConfirmationDialog from '../../components/ui/ConfirmationDialog'
import { AgentWorksChatTabItem } from '../../components/chat/AgentWorksChatTabItem'
import {
  REPORT_PREVIEW_PREFERENCE_CHANGED_EVENT,
  readReportPreviewPreference,
  type ReportPreviewDevice,
  writeReportPreviewPreference,
} from '../../utils/reportPreviewPreference'

const WORK_SPLIT_PREFERENCE_KEY = 'work_workspace_split_ratio'
const WORK_VIEW_PREFERENCE_KEY = 'work_workspace_view'
const WORK_UI_PRESENTATION_VIEWS = {
  history: 'history', context: 'context', report: 'dashboard', plan: 'plan', memory: 'memory', database: 'database', browser: 'browser', costs: 'costs', workshop: 'schedules', schedules: 'schedules', files: 'files',
  suggestions: 'suggestions', identity: 'identity', mcp: 'mcp', shell: 'shell',
  // Legacy agent + preference ids land on the consolidated Setup views.
  llm: 'identity', email: 'mcp', folders: 'identity',
} as const satisfies Record<string, WorkWorkspaceView>
type WorkUIPresentationView = keyof typeof WORK_UI_PRESENTATION_VIEWS
const WORK_UI_LABELS: Record<WorkUIPresentationView, string> = {
  history: 'Chat history', context: 'Context', report: 'Dashboard', plan: 'Plan', memory: 'Memory & skills', database: 'Database', browser: 'Browser', costs: 'Costs and usage', workshop: 'Automations', schedules: 'Automations', files: 'Files',
  suggestions: 'Suggestions', identity: 'Settings', mcp: 'Connections', shell: 'Terminal',
  llm: 'Agent configuration', email: 'Gmail', folders: 'Attached folders',
}

function workPresentationView(view: WorkWorkspaceView): WorkUIPresentationView {
  return (Object.entries(WORK_UI_PRESENTATION_VIEWS).find(([, panel]) => panel === view)?.[0] ?? 'report') as WorkUIPresentationView
}

const WORKSPACE_VIEW_IDS = new Set<WorkWorkspaceView>(Object.values(WORK_UI_PRESENTATION_VIEWS))

// Someone else's Crew (owner decision 2026-10-14): a person who uses it sees the chat and, when the Crew has one,
// its dashboard. No Files, Memory, Terminal or setup: those belong to the Crew's owner. With no dashboard the
// right pane stays closed and only the chat shows.
export const SHARED_CREW_WORKSPACE_PANELS: Set<string> = new Set(['dashboard'])

export function normalizeWorkWorkspaceView(saved: unknown): WorkWorkspaceView | null {
  if (saved === 'history') return 'schedules'
  if (typeof saved !== 'string') return null
  if (Object.hasOwn(WORK_UI_PRESENTATION_VIEWS, saved)) return WORK_UI_PRESENTATION_VIEWS[saved as WorkUIPresentationView]
  return WORKSPACE_VIEW_IDS.has(saved as WorkWorkspaceView) ? saved as WorkWorkspaceView : null
}

function readWorkWorkspaceView(projectId: string | undefined, product: 'work'): WorkWorkspaceView | null {
  return readWorkspaceViewPreference(product, projectId, normalizeWorkWorkspaceView, () =>
    normalizeWorkWorkspaceView(window.localStorage.getItem(`${WORK_VIEW_PREFERENCE_KEY}:${projectId}`)))
}

function writeWorkWorkspaceView(projectId: string | undefined, product: 'work', view: WorkWorkspaceView) {
  writeWorkspaceViewPreference(product, projectId, view)
}

function readWorkSplitRatio(projectId?: string): number {
  if (typeof window === 'undefined' || !projectId) return 0.5
  try {
    const value = Number.parseFloat(window.localStorage.getItem(`${WORK_SPLIT_PREFERENCE_KEY}:${projectId}`) || '')
    return Number.isFinite(value) && value >= 0.15 && value <= 0.85 ? value : 0.5
  } catch {
    return 0.5
  }
}

function writeWorkSplitRatio(projectId: string | undefined, ratio: number) {
  if (typeof window === 'undefined' || !projectId) return
  try { window.localStorage.setItem(`${WORK_SPLIT_PREFERENCE_KEY}:${projectId}`, String(ratio)) } catch { /* UI preference only. */ }
}

// Mobile preview shows the workspace as a phone-width column; the person may still drag the rail a little, so that width is
// remembered per project (px), separately from the split ratio of the other previews.
const MOBILE_PANE_MIN = 320
function readMobilePaneWidth(projectId?: string): number | null {
  if (typeof window === 'undefined' || !projectId) return null
  try {
    const value = Number.parseFloat(window.localStorage.getItem(`${WORK_SPLIT_PREFERENCE_KEY}:mobile:${projectId}`) || '')
    return Number.isFinite(value) && value >= MOBILE_PANE_MIN && value <= 1600 ? value : null
  } catch { return null }
}
function writeMobilePaneWidth(projectId: string | undefined, width: number) {
  if (typeof window === 'undefined' || !projectId) return
  try { window.localStorage.setItem(`${WORK_SPLIT_PREFERENCE_KEY}:mobile:${projectId}`, String(Math.round(width))) } catch { /* UI preference only. */ }
}

async function restoreWorkRuntimeSelection(tabId: string, sessionId: string, workspacePath: string): Promise<WorkRuntimeSelection | null> {
  const cachedRuntime = useChatStore.getState().activeSessionsCache.find(
    session => session.session_id === sessionId,
  )?.runtime
  try {
    const history = await agentApi.getChatHistoryConversation(sessionId, workspacePath, 1)
    const provider = history.runtime?.provider?.trim() || cachedRuntime?.provider?.trim()
    const modelId = history.runtime?.model_id?.trim() || cachedRuntime?.model_id?.trim()
    if (!provider || !modelId) return null
    useChatStore.getState().setTabMetadata(tabId, {
      agentProfileEngine: provider,
      agentProfileModelID: modelId,
      agentProfileReasoningEffort: undefined,
    })
    return { engine: provider, provider, modelId }
  } catch {
    // A new project has no history yet. A restored session can still carry the
    // authoritative native runtime in the activity cache.
    const provider = cachedRuntime?.provider?.trim()
    const modelId = cachedRuntime?.model_id?.trim()
    if (!provider || !modelId) return null
    useChatStore.getState().setTabMetadata(tabId, {
      agentProfileEngine: provider,
      agentProfileModelID: modelId,
      agentProfileReasoningEffort: undefined,
    })
    return { engine: provider, provider, modelId }
  }
}

// Crew and Code remember their selected project separately.
function selectedProjectIdFor(product: ProjectProductConfig): string | null {
  const state = useProductSurfaceStore.getState()
  return state.selectedWorkProjectId
}

// Reuse the last list when switching products; each user, workspace, and
// product has its own cache and still refreshes from the server on entry.
const workSessionLists = new Map<string, WorkSession[]>()

function useWorkSessions(product: ProjectProductConfig) {
  const allowedProducts = useAuthStore(state => state.user?.allowed_products)
  const userId = useAuthStore(state => state.user?.id ?? 'local')
  const activeWorkspaceId = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const cacheKey = `${product.profileId}:${activeWorkspaceId}:${userId}`
  const cached = useRef(workSessionLists.get(cacheKey) ?? null)
  const [sessions, setSessions] = useState<WorkSession[]>(() => cached.current ?? [])
  const selectedId = useProductSurfaceStore(state => state.selectedWorkProjectId)
  const setSelectedId = useProductSurfaceStore(state => state.setSelectedWorkProjectId)
  const [loading, setLoading] = useState(cached.current === null)
  const [error, setError] = useState<string | null>(null)
  const mutationRevision = useRef(0)

  const updateSessions = useCallback((update: WorkSession[] | ((current: WorkSession[]) => WorkSession[]), fromLoad = false) => {
    if (!fromLoad) mutationRevision.current += 1
    setSessions(current => {
      const next = typeof update === 'function' ? update(current) : update
      workSessionLists.set(cacheKey, next)
      return next
    })
  }, [cacheKey])

  const refresh = useCallback(async () => {
    const listed = await loadWorkSessionsIncludingShared(product)
    updateSessions(listed, true)
    const current = selectedProjectIdFor(product)
    replaceProductSelection(() => setSelectedId(current && listed.some(item => item.product === product.profileId && item.id === current) ? current : (currentProductRoute()?.surface === product.profileId && !currentProductRoute()?.id ? null : listed.find(item => item.product === product.profileId)?.id ?? null)))
    return listed
  }, [product, allowedProducts, setSelectedId, updateSessions])

  useEffect(() => {
    let cancelled = false
    const revisionAtStart = mutationRevision.current
    void loadWorkSessionsIncludingShared(product)
      .then((listed) => {
        if (cancelled || mutationRevision.current !== revisionAtStart) return
        updateSessions(listed, true)
        const current = selectedProjectIdFor(product)
        replaceProductSelection(() => setSelectedId(current && listed.some(item => item.product === product.profileId && item.id === current) ? current : (currentProductRoute()?.surface === product.profileId && !currentProductRoute()?.id ? null : listed.find(item => item.product === product.profileId)?.id ?? null)))
      })
      .catch((cause) => {
        if (!cancelled && mutationRevision.current === revisionAtStart) {
          setError(cause instanceof Error ? cause.message : 'Could not load projects.')
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => { cancelled = true }
  }, [product, allowedProducts, setSelectedId, updateSessions])

  const create = useCallback(async (title: string, description: string, icon?: string, templateId?: CrewTemplateId, runsOn?: RunsOnSelection, mode?: ProductMode) => {
    const session = await createWorkSession(title, description, icon, templateId, product, runsOn, mode)
    if (runsOn?.provider) rememberRunsOn(product.profileId, runsOn.provider)
    updateSessions((current) => [session, ...current])
    setSelectedId(session.id)
    return session
  }, [product, allowedProducts, setSelectedId, updateSessions])

  const applyTemplateUpdate = useCallback((projectId: string, updated: WorkSession) => {
    updateSessions(current => current.map(item => item.product === updated.product && item.id === projectId ? updated : item))
    for (const tab of Object.values(useChatStore.getState().chatTabs)) {
      if (tab.metadata?.agentProfileId !== updated.product || !belongsToWorkProject(tab, projectId)) continue
      useChatStore.getState().setTabConfig(tab.tabId, { selectedSkills: updated.selectedSkills })
      useChatStore.getState().setTabMetadata(tab.tabId, { agentProfileRuntimeDirty: true })
    }
    markWorkProjectRuntimeDirty(projectId)
  }, [updateSessions])

  const installTemplate = useCallback(async (projectId: string, templateId: CrewTemplateId) => {
    const session = sessions.find(item => item.product === product.profileId && item.id === projectId)
    if (!session) throw new Error(`This ${product.itemNoun} is no longer available.`)
    const updated = await installWorkSessionTemplate(session, templateId)
    applyTemplateUpdate(projectId, updated)
    return updated
  }, [product, sessions, applyTemplateUpdate])

  const removeTemplate = useCallback(async (projectId: string, templateId: CrewTemplateId) => {
    const session = sessions.find(item => item.product === product.profileId && item.id === projectId)
    if (!session) throw new Error(`This ${product.itemNoun} is no longer available.`)
    const updated = await removeWorkSessionTemplate(session, templateId)
    applyTemplateUpdate(projectId, updated)
    return updated
  }, [product, sessions, applyTemplateUpdate])

  const remove = useCallback(async (projectId: string, origin: WorkSession['product'] = product.profileId) => {
    const project = sessions.find(item => item.product === origin && item.id === projectId)
    if (!project) throw new Error(`This ${product.itemNoun} is no longer available.`)
    // Shared rows fail in deleteWorkSession below; skip the session stop for
    // the reader-side stub, which has no session of its own to stop.
    if (!project.shared) {
      try {
        await agentApi.stopSession(project.sessionId, true)
      } catch (cause) {
        const status = (cause as { response?: { status?: number } })?.response?.status
        if (status !== 404) throw cause
      }
    }
    await deleteWorkSession(project)

    const chatStore = useChatStore.getState()
    const projectTabs = Object.values(chatStore.chatTabs).filter(tab => tab.metadata?.agentProfileId === origin && belongsToWorkProject(tab, projectId))
    for (const tab of projectTabs) await useChatStore.getState().closeTab(tab.tabId, false)

    try {
      window.localStorage.removeItem(`${WORK_VIEW_PREFERENCE_KEY}:${projectId}`)
      window.localStorage.removeItem(`${WORK_SPLIT_PREFERENCE_KEY}:${projectId}`)
    } catch { /* UI preferences only. */ }

    writeWorkspaceViewPreference(origin, projectId, null)
    const remaining = sessions.filter(item => item.product !== origin || item.id !== projectId)
    updateSessions(remaining)
    if (origin === product.profileId && selectedProjectIdFor(product) === projectId) {
      replaceProductSelection(() => setSelectedId(remaining.find(item => item.product === product.profileId)?.id ?? null))
    }
  }, [product, sessions, setSelectedId, updateSessions])

  const updateLLMConfig = useCallback(async (projectId: string, selection: WorkRuntimeSelection) => {
    const project = sessions.find(item => item.product === product.profileId && item.id === projectId)
    if (!project) throw new Error(`This ${product.itemNoun} is no longer available.`)
    if (isWorkSessionRun(project)) throw new Error(`Only the ${product.noun} owner can change this.`)
    // The account travels with the provider: without it "Use account" saved only the provider,
    // and the Crew or Code kept running on the server account (server B/server C, 2026-09-30).
    const llmConfig = workLLMConfigFromSelection({
      connectionId: selection.connectionId,
      provider: selection.provider || selection.engine,
      modelId: selection.modelId,
      reasoningEffort: selection.reasoningEffort,
    })
    const updated = await updateProductProjectLLMConfig(project, llmConfig, `Update ${product.noun} project model ${project.title}`, 'workflow.json')
    updateSessions(current => current.map(item => item.product === updated.product && item.id === projectId ? updated : item))
    return updated
  }, [product, sessions, updateSessions])

  const updateNativeAgentTools = useCallback(async (projectId: string, enabled: boolean) => {
    const project = sessions.find(item => item.product === product.profileId && item.id === projectId)
    if (!project) throw new Error(`This ${product.itemNoun} is no longer available.`)
    if (isWorkSessionRun(project)) throw new Error(`Only the ${product.noun} owner can change this.`)
    const updated = await updateProductProjectNativeAgentTools(project, enabled, `${enabled ? 'Enable' : 'Disable'} native agent tools for ${product.noun} project ${project.title}`, 'workflow.json')
    updateSessions(current => current.map(item => item.product === updated.product && item.id === projectId ? updated : item))
    return updated
  }, [product, sessions, updateSessions])

  const updateSelections = useCallback(async (projectId: string, patch: { selectedServers?: string[]; selectedSkills?: string[]; selectedSecrets?: string[]; selectedGlobalSecrets?: string[]; workflowContextPaths?: string[] }) => {
    const project = sessions.find(item => item.product === product.profileId && item.id === projectId)
    if (!project) throw new Error(`This ${product.itemNoun} is no longer available.`)
    if (isWorkSessionRun(project)) throw new Error(`Only the ${product.noun} owner can change this.`)
    const updated = await updateProductProjectSelections(project, patch, `Update ${product.noun} project integrations ${project.title}`, 'workflow.json')
    updateSessions(current => current.map(item => item.product === updated.product && item.id === projectId ? updated : item))
    return updated
  }, [product, sessions, updateSessions])

  const updateIdentity = useCallback(async (projectId: string, patch: ProductIdentityPatch) => {
    const project = sessions.find(item => item.product === product.profileId && item.id === projectId)
    if (!project) throw new Error(`This ${product.itemNoun} is no longer available.`)
    const updated = await updateWorkSessionIdentity(project, patch)
    updateSessions(current => current.map(item => item.product === updated.product && item.id === projectId ? updated : item))
    return updated
  }, [product, sessions, updateSessions])



  return {
    sessions,
    selected: sessions.find((session) => session.product === product.profileId && session.id === selectedId) ?? null,
    select: setSelectedId,
    create,
    installTemplate,
    removeTemplate,
    remove,
    updateLLMConfig,
    updateNativeAgentTools,
    updateSelections,
    updateIdentity,
    refresh,
    loading,
    error,
  }
}

// selectWorkChatTabIds picks only the ids useWorkChatTab needs from the chat
// store. It returns primitives, so with useShallow a change elsewhere in a
// tab (the chat input's saved draft) does not re-render the Work surface.
export function selectWorkChatTabIds(
  state: { chatTabs: Record<string, ChatTab>; activeTabId: string | null },
  sessionId: string | undefined,
): { canonicalTabId: string | undefined; activeProjectTabId: string | undefined } {
  if (!sessionId) return { canonicalTabId: undefined, activeProjectTabId: undefined }
  const canonical = Object.values(state.chatTabs).find(tab =>
    belongsToWorkProject(tab, sessionId) &&
    tab.metadata?.agentProfileBuilder !== true &&
    (tab.metadata?.agentProfileIsMainChat === true || tab.metadata?.agentProfileConversationKey === sessionId))
  const active = state.activeTabId ? state.chatTabs[state.activeTabId] : undefined
  return {
    canonicalTabId: canonical?.tabId,
    activeProjectTabId: active && belongsToWorkProject(active, sessionId) ? active.tabId : undefined,
  }
}

function useWorkChatTab(
  product: ProjectProductConfig,
  session: WorkSession | null,
  onLegacyRuntimeDiscovered: (selection: WorkRuntimeSelection) => void | Promise<void>,
) {
  const [failure, setFailure] = useState<{ projectId: string; message: string } | null>(null)
  // Subscribe to the two tab ids this hook needs, never to chatTabs itself:
  // the chat input saves its draft into its tab's config, and a whole-map
  // subscription re-rendered the Work surface (and the workspace pane beside
  // the chat) on every keystroke.
  const sessionId = session?.id
  const globalPageOpen = useAppStore(state => state.adminPage !== null || state.showSchedulesOverview || state.showWorkflowsOverview)
  const showProviders = useLLMStore(state => state.showLLMModal)
  const { canonicalTabId, activeProjectTabId } = useChatStore(useShallow(state => selectWorkChatTabIds(state, sessionId)))
  const sessionRef = useRef(session)
  const legacyRuntimeHandlerRef = useRef(onLegacyRuntimeDiscovered)
  sessionRef.current = session
  legacyRuntimeHandlerRef.current = onLegacyRuntimeDiscovered
  const projectId = session?.id
  useWorkChatModels(product.profileId, projectId, session?.llmConfig, canonicalTabId)

  useEffect(() => {
    const target = sessionRef.current
    if (!target || target.id !== projectId) return
    let cancelled = false
    const prepare = async () => {
      try {
        useModeStore.getState().setModeCategory('multi-agent')
        useAppStore.getState().setAgentMode('multi-agent')
        await waitForChatStoreHydration()
        if (cancelled) return

        const chatStore = useChatStore.getState()
        const savedRuntime = workLLMSelectionFromConfig(target.llmConfig)
        const savedServers = target.selectedServers.length > 0 ? target.selectedServers : ['NO_SERVERS']
        const savedSkills = target.selectedSkills
        const conversation = await agentApi.resolveAgentProfileConversation(product.profileId, {
          conversation_key: target.id,
        })
        if (cancelled) return

        for (const tab of Object.values(chatStore.chatTabs)) {
          if (belongsToWorkProject(tab, target.id) && tab.sessionId !== conversation.session_id && tab.metadata?.agentProfileIsMainChat) chatStore.setTabMetadata(tab.tabId, { agentProfileIsMainChat: false })
        }
        const projectMetadata = {
          mode: 'multi-agent',
          agentProfileId: product.profileId,
          agentProfileVersion: product.profileVersion,
          agentProfileWorkspace: target.workspacePath,
          agentProfileProjectId: target.id,
          agentProfileProjectTitle: target.title,
          agentProfileProjectIcon: target.identity?.icon,
          agentProfileIdentityName: target.identity?.name,
          agentProfileChatContract: 'profile-v1',
          agentProfileBuilder: false,
          agentProfileIsMainChat: true,
          agentProfileTabOwnerID: conversation.tab_owner_id,
          agentProfileTabOwnerName: conversation.tab_owner_name,
          agentProfileTabCanWrite: conversation.can_write,
          agentProfileConversationKey: conversation.conversation_key,
          agentProfileConversationId: conversation.conversation_id,
          agentProfileChatProvider: conversation.provider,
          agentProfileChatConnectionID: conversation.connection_id,
          ...(savedRuntime ? {
            agentProfileEngine: savedRuntime.provider,
            agentProfileConnectionID: savedRuntime.connectionId,
            agentProfileModelID: savedRuntime.modelId,
            agentProfileReasoningEffort: savedRuntime.reasoningEffort,
          } : {}),
        } as const

        // Reuse the local projection of the server-owned canonical session when
        // one exists, including the old blank Builder after migration.
        const matching = findCanonicalWorkProjectTab(chatStore.chatTabs, target.id, conversation.session_id)
        if (matching) chatStore.setTabMetadata(matching.tabId, projectMetadata)

        const previousActiveTabId = useChatStore.getState().activeTabId
        const canonicalTabId = await chatStore.createChatTab('Chat', projectMetadata, conversation.session_id)
        if (cancelled) return
        // createChatTab activates the primary; keep a side chat the user picked (PLAT-571).
        const keepActive = workTabToKeepActive(useChatStore.getState().chatTabs, previousActiveTabId, target.id, canonicalTabId)
        if (keepActive) activateTab(keepActive)
        chatStore.setTabMetadata(canonicalTabId, { ...projectMetadata, agentProfileMCPSelectionInitialized: true })
        chatStore.setTabConfig(canonicalTabId, { selectedServers: savedServers, selectedSkills: savedSkills })

        // Earlier UI versions could open the canonical session as a read-only
        // history tab. Remove only those local duplicate projections; the
        // server-owned conversation and its events remain attached to Chat.
        useChatStore.setState(state => {
          const duplicateIds = Object.values(state.chatTabs)
            .filter(tab => tab.tabId !== canonicalTabId && tab.metadata?.isViewOnly === true &&
              belongsToWorkProject(tab, target.id) && tab.sessionId === conversation.session_id)
            .map(tab => tab.tabId)
          if (duplicateIds.length === 0) return state
          const nextTabs = { ...state.chatTabs }
          for (const duplicateId of duplicateIds) delete nextTabs[duplicateId]
          return {
            chatTabs: nextTabs,
            activeTabId: duplicateIds.includes(state.activeTabId || '') ? canonicalTabId : state.activeTabId,
          }
        })

        if ((useChatStore.getState().tabEvents[conversation.session_id]?.length ?? 0) === 0) {
          await hydrateTabEvents(conversation.session_id, {
            workspacePath: target.workspacePath,
            fallbackToChatHistory: true,
            preferChatHistory: true,
          })
        }
        if (!savedRuntime) {
          const restored = await restoreWorkRuntimeSelection(canonicalTabId, conversation.session_id, target.workspacePath)
          if (restored) await legacyRuntimeHandlerRef.current(restored)
        }
        if (cancelled) return
        setFailure(current => current?.projectId === target.id ? null : current)
      } catch (cause) {
        if (!cancelled) setFailure({ projectId: target.id, message: cause instanceof Error ? cause.message : `Could not open ${product.noun}.` })
      }
    }
    void prepare()
    return () => { cancelled = true }
  }, [product, projectId])

  // Manifest changes (identity, model, MCPs, or skills) update the existing
  // local tab in place. They must not restart the expensive conversation
  // resolution and history hydration performed above.
  useEffect(() => {
    if (!session) return
    // The primary chat and every side chat of the project share its identity,
    // model, MCPs and skills (PLAT-571).
    const projectTabs = Object.values(useChatStore.getState().chatTabs).filter(candidate =>
      belongsToWorkProject(candidate, session.id) &&
      candidate.metadata?.agentProfileBuilder !== true &&
      (candidate.metadata?.agentProfileIsMainChat || candidate.metadata?.agentProfileConversationKey === session.id || isWorkSideChatTab(candidate, session.id)))
    for (const tab of projectTabs) {
    if (tab.metadata?.agentProfileConversationKey?.startsWith(`${session.id}:chat:agent-`)) continue
    const savedRuntime = workLLMSelectionFromConfig(session.llmConfig)
    const metadata = {
      agentProfileWorkspace: session.workspacePath,
      agentProfileProjectTitle: session.title,
      agentProfileProjectIcon: session.identity?.icon,
      agentProfileIdentityName: session.identity?.name,
      ...(savedRuntime && !tab.metadata?.agentProfileChatModelOverride ? {
        agentProfileEngine: savedRuntime.provider,
        agentProfileConnectionID: savedRuntime.connectionId,
        agentProfileModelID: savedRuntime.modelId,
        agentProfileReasoningEffort: savedRuntime.reasoningEffort,
      } : {}),
    }
    if (Object.entries(metadata).some(([key, value]) => tab.metadata?.[key as keyof typeof tab.metadata] !== value)) {
      useChatStore.getState().setTabMetadata(tab.tabId, metadata)
    }
    const selectedServers = session.selectedServers.length > 0 ? session.selectedServers : ['NO_SERVERS']
    const selectedSkills = session.selectedSkills
    const sameList = (left: string[] | undefined, right: string[]) =>
      left?.length === right.length && left.every((value, index) => value === right[index])
    if (!sameList(tab.config.selectedServers, selectedServers) || !sameList(tab.config.selectedSkills, selectedSkills)) {
      useChatStore.getState().setTabConfig(tab.tabId, { selectedServers, selectedSkills })
    }
    }
  }, [session])

  useLayoutEffect(() => {
    // Resolving a chat after navigating here must not dismiss the requested
    // Users, Providers or Schedules page. Activate it when returning to work.
    if (canonicalTabId && !activeProjectTabId && !globalPageOpen && !showProviders) activateTab(canonicalTabId)
  }, [activeProjectTabId, canonicalTabId, globalPageOpen, showProviders])
  return {
    // A previously prepared Crew tab is safe to display immediately while its
    // durable binding is revalidated in the background.
    tabId: activeProjectTabId ?? canonicalTabId ?? null,
    canonicalTabId: canonicalTabId ?? null,
    error: failure && failure.projectId === session?.id ? failure.message : null,
  }
}

export function WorkChatTabs({ projectId, projectName, canonicalTabId, profileId, allowSideChats }: {
  projectId: string
  projectName: string
  canonicalTabId: string
  profileId: string
  allowSideChats: boolean
}) {
  const { chatTabs, activeTabId } = useChatStore(useShallow(state => ({
    chatTabs: state.chatTabs,
    activeTabId: state.activeTabId,
  })))
  const [opening, setOpening] = useState(false)
  // A new tab is a new agent and always gets a name first (owner, 2026-10-19); null = not naming.
  const [newTabName, setNewTabName] = useState<string | null>(null)
  const canonicalSessionId = chatTabs[canonicalTabId]?.sessionId
  // Which agents this project has, their names, order and pins come from the server's list (the person's
  // product-conversations record, also changed over MCP). A browser tab is only the open window of a listed chat: a tab
  // the server does not list is not shown, whatever this browser remembered about it. null = not loaded yet.
  const [listed, setListed] = useState<AgentProfileSideChat[] | null>(null)
  const listedFor = (tab: ChatTab) => listed?.find(chat => tab.metadata?.agentProfileConversationKey === chat.conversation_key || tab.sessionId === chat.session_id)
  const allTabs = Object.values(chatTabs)
  const sideChats = (listed || [])
    .map(chat => allTabs.find(tab => tab.tabId !== canonicalTabId && tab.metadata?.isViewOnly !== true && (tab.metadata?.agentProfileConversationKey === chat.conversation_key || tab.sessionId === chat.session_id)))
    .filter((tab): tab is ChatTab => !!tab)
  const tabs = [
    ...(chatTabs[canonicalTabId] ? [chatTabs[canonicalTabId]] : []),
    ...sideChats,
    // Run and activity views this browser opened are not agents; they stay until closed.
    ...allTabs.filter(tab => belongsToWorkProject(tab, projectId) && tab.metadata?.isViewOnly === true && tab.sessionId !== canonicalSessionId),
  ]
  const isAgentTab = (tab: ChatTab) => !tab.metadata?.isViewOnly && !tab.metadata?.isScheduledRun && !tab.metadata?.isBotRun
  const agentCount = tabs.filter(isAgentTab).length
  const sideChatKey = (tab: ChatTab) => listedFor(tab)?.conversation_key
  const sideChatName = (tab: ChatTab) => workSideChatDisplayName(listedFor(tab)?.name || tab.name)
  const canControl = (tab: ChatTab) => tab.metadata?.agentProfileTabCanWrite !== false
  const sideChatPinned = (tab: ChatTab) => listedFor(tab)?.pinned !== false
  // The strip shows the main chat and the tabs the person pinned, as many as they pin (owner, 2026-10-20); the rest are
  // in the Agents menu. The tab the person is in is always on the strip (in the last place when it is not pinned), so it
  // can be seen and dragged.
  const onStrip = (tab: ChatTab) => tab.tabId === canonicalTabId || !listedFor(tab) || sideChatPinned(tab)
  const activeTab = tabs.find(tab => tab.tabId === activeTabId)
  const visibleTabs = activeTab && !onStrip(activeTab) ? [...tabs.filter(onStrip), activeTab] : tabs.filter(onStrip)
  const [moreOpen, setMoreOpen] = useState(false)
  const [reordering, setReordering] = useState(false)
  const canReorderInMenu = tabs.length > 5
  useEffect(() => { if (!moreOpen || !canReorderInMenu) setReordering(false) }, [moreOpen, canReorderInMenu])
  const [renaming, setRenaming] = useState<string | null>(null)
  const [renameDraft, setRenameDraft] = useState('')
  const moreRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!moreOpen) return
    const close = (event: MouseEvent) => { if (!moreRef.current?.contains(event.target as Node)) setMoreOpen(false) }
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') { setMoreOpen(false); moreRef.current?.querySelector<HTMLButtonElement>('button')?.focus() } }
    document.addEventListener('mousedown', close)
    document.addEventListener('keydown', escape)
    return () => { document.removeEventListener('mousedown', close); document.removeEventListener('keydown', escape) }
  }, [moreOpen])
  // Dragging a side tab onto another saves the new order on the server; the strip then follows the server's order.
  const [draggingKey, setDraggingKey] = useState<string | null>(null)
  const [dropKey, setDropKey] = useState<string | null>(null)
  const drag = useRef<{ key: string; tabId: string; attemptId: string; dropped: boolean } | null>(null)
  const dragMime = 'application/x-agentworks-chat-key'
  const startDrag = (event: ReactDragEvent<HTMLDivElement>, key: string, tabId: string) => {
    if ((event.target as Element).closest('button,input')) { event.preventDefault(); return }
    // A payload is required by native browser drag/drop. A ref keeps source
    // identity stable while React renders the selected tab or drop indicator.
    event.dataTransfer.setData(dragMime, key)
    event.dataTransfer.setData('text/plain', key)
    event.dataTransfer.effectAllowed = 'move'
    drag.current = { key, tabId, attemptId: crypto.randomUUID(), dropped: false }
    setDraggingKey(key)
    if (canonicalSessionId) recordTabReorderTelemetry(canonicalSessionId, drag.current.attemptId, 'start', tabId)
  }
  const endDrag = () => {
    const attempt = drag.current
    if (attempt && !attempt.dropped && canonicalSessionId) recordTabReorderTelemetry(canonicalSessionId, attempt.attemptId, 'cancel', attempt.tabId)
    drag.current = null; setDraggingKey(null); setDropKey(null)
  }
  const overTab = (event: ReactDragEvent<HTMLDivElement>, key: string | undefined) => {
    if (!drag.current || !key) return
    event.preventDefault(); event.dataTransfer.dropEffect = 'move'; setDropKey(key)
  }
  const dropTab = (event: ReactDragEvent<HTMLDivElement>, key: string | undefined) => {
    const attempt = drag.current
    if (!attempt || !key) return
    event.preventDefault(); event.stopPropagation()
    attempt.dropped = true
    void moveSideChat(attempt.key, key, attempt)
    setDropKey(null)
  }
  const applyServerOrder = (listed: AgentProfileSideChat[]) => {
    setListed(listed)
    const state = useChatStore.getState()
    for (const chat of listed) {
      const tab = Object.values(state.chatTabs).find(item => item.metadata?.agentProfileConversationKey === chat.conversation_key)
      if (tab && ((tab.metadata?.agentProfileTabPosition || 0) !== (chat.position || 0) || (tab.metadata?.agentProfileTabPinned !== false) !== (chat.pinned !== false))) state.setTabMetadata(tab.tabId, { agentProfileTabPosition: chat.position || 0, agentProfileTabPinned: chat.pinned !== false })
    }
  }
  const moveSideChat = async (fromKey: string, toKey: string, attempt: { key: string; tabId: string; attemptId: string; dropped: boolean } = { key: fromKey, tabId: sideChats.find(tab => sideChatKey(tab) === fromKey)?.tabId || '', attemptId: crypto.randomUUID(), dropped: true }) => {
    const keys = (listed || []).map(chat => chat.conversation_key)
    const from = keys.indexOf(fromKey)
    const to = keys.indexOf(toKey)
    const targetId = sideChats.find(tab => sideChatKey(tab) === toKey)?.tabId
    const trace = (status: 'drop' | 'no_change' | 'saved' | 'failed') => { if (canonicalSessionId) recordTabReorderTelemetry(canonicalSessionId, attempt.attemptId, status, attempt.tabId, targetId) }
    trace('drop')
    if (from < 0 || to < 0 || from === to) { trace('no_change'); return }
    keys.splice(to, 0, ...keys.splice(from, 1))
    try {
      applyServerOrder(await agentApi.orderAgentProfileSideChats(profileId, projectId, keys))
      trace('saved')
    } catch (cause) {
      trace('failed')
      useChatStore.getState().addToast(cause instanceof Error ? cause.message : 'Could not save the tab order.', 'error')
    }
  }
  const pinSideChat = async (key: string, pinned: boolean) => {
    try {
      applyServerOrder(await agentApi.pinAgentProfileSideChat(profileId, projectId, key, pinned))
    } catch (cause) {
      useChatStore.getState().addToast(cause instanceof Error ? cause.message : 'Could not pin this tab.', 'error')
    }
  }
  const projectActivity = useChatStore(state => workProjectActivity(state.activeSessionsCache, tabs, projectId))
  const selectTab = (nextTabId: string) => { activateTab(nextTabId) }
  const closeProjectTab = (closingTabId: string) => {
    void closeWorkspaceChatTab(closingTabId, canonicalTabId).catch(() => {
      useChatStore.getState().addToast('Could not stop this agent. Try closing it again.', 'error')
    }).finally(() => { void syncRef.current() })
  }
  const renameSideChat = async (tab: ChatTab, name: string) => {
    const title = name.trim()
    if (!title || !tab.sessionId) return false
    await agentApi.renameChatHistorySession(tab.sessionId, title, tab.metadata?.agentProfileWorkspace)
    useChatStore.getState().renameTab(tab.tabId, title)
    void syncRef.current()
    return true
  }
  // A side chat is a second full Builder chat in the same project: same
  // folder, model, MCPs and skills as the primary, its own conversation.
  const openSideChat = async (requestedName: string) => {
    const name = requestedName.trim()
    if (!name || opening || sideChats.length >= WORK_SIDE_CHAT_LIMIT) return
    const primary = chatTabs[canonicalTabId]
    if (!primary) return
    setOpening(true)
    try {
      const key = workSideChatKey(projectId, globalThis.crypto.randomUUID().slice(0, 8))
      // The server names the new tab's agent and its chat title from this name.
      const conversation = await agentApi.resolveAgentProfileConversation(profileId, { conversation_key: key, name })
      // The server now holds this chat; show its row at once rather than after the next list.
      setListed(current => [...(current || []).filter(chat => chat.conversation_key !== conversation.conversation_key), { conversation_key: conversation.conversation_key, conversation_id: conversation.conversation_id, session_id: conversation.session_id, name, pinned: true }])
      setNewTabName(null)
      const chatStore = useChatStore.getState()
      const tabId = await chatStore.createChatTab(name, {
        ...primary.metadata,
        agentProfileIsMainChat: false,
        agentProfileBuilder: false,
        agentProfileChatModelOverride: false,
        agentProfileTabOwnerID: conversation.tab_owner_id,
        agentProfileTabOwnerName: conversation.tab_owner_name,
        agentProfileTabCanWrite: conversation.can_write,
        agentProfileConversationKey: conversation.conversation_key,
        agentProfileConversationId: conversation.conversation_id,
        agentProfileMCPSelectionInitialized: true,
      }, conversation.session_id)
      chatStore.renameTab(tabId, name)
      chatStore.setTabConfig(tabId, {
        selectedServers: primary.config.selectedServers,
        selectedSkills: primary.config.selectedSkills,
      })
      activateTab(tabId)
    } catch (cause) {
      useChatStore.getState().addToast(cause instanceof Error ? cause.message : 'Could not open a new agent.', 'error')
      return
    } finally {
      setOpening(false)
    }
    openingRef.current = false
    void syncRef.current()
  }
  // A tab another chat opened (open_project_chat, the code tool) is known to the server only: open the ones this browser
  // does not show yet, on load and while the project is on screen. The active tab stays the one the person is in.
  const openingRef = useRef(false)
  openingRef.current = opening
  const syncRef = useRef<() => Promise<void>>(async () => {})
  useEffect(() => {
    if (!allowSideChats) return
    let cancelled = false
    let busy = false
    const sync = async () => {
      if (busy || openingRef.current || document.visibilityState === 'hidden') return
      busy = true
      try {
        const listed = await agentApi.listAgentProfileSideChats(profileId, projectId)
        if (cancelled) return
        setListed(listed)
        for (const chat of listed) {
          const state = useChatStore.getState()
          const primary = state.chatTabs[canonicalTabId]
          if (cancelled || openingRef.current || !primary) return
          const tabs = Object.values(state.chatTabs)
          const existing = tabs.find(tab => tab.metadata?.agentProfileConversationKey === chat.conversation_key || tab.sessionId === chat.session_id)
          if (existing) {
            state.setTabMetadata(existing.tabId, { agentProfileTabOwnerID: chat.tab_owner_id, agentProfileTabOwnerName: chat.tab_owner_name, agentProfileTabCanWrite: chat.can_write })
            // A tab this browser kept from before the project's product changed still names the old one, which hides it
            // from this strip; the server's list decides which project and product the chat is in.
            if (existing.metadata?.agentProfileConversationKey !== chat.conversation_key || existing.metadata?.agentProfileIsMainChat || existing.metadata?.agentProfileId !== profileId || existing.metadata?.agentProfileProjectId !== projectId) state.setTabMetadata(existing.tabId, { agentProfileTabOwnerID: chat.tab_owner_id,
            agentProfileTabOwnerName: chat.tab_owner_name,
            agentProfileTabCanWrite: chat.can_write,
            agentProfileConversationKey: chat.conversation_key, agentProfileIsMainChat: false, agentProfileId: profileId, agentProfileProjectId: projectId })
            if (chat.agent_id) {
              state.setTabMetadata(existing.tabId, { agentProfileEngine: chat.provider, agentProfileModelID: chat.model_id, agentProfileConnectionID: chat.connection_id, agentProfileChatProvider: chat.provider, agentProfileChatConnectionID: chat.connection_id })
            }
            if (existing.name !== chat.name) state.renameTab(existing.tabId, chat.name)
            if ((existing.metadata?.agentProfileTabPosition || 0) !== (chat.position || 0) || (existing.metadata?.agentProfileTabPinned !== false) !== (chat.pinned !== false)) state.setTabMetadata(existing.tabId, { agentProfileTabPosition: chat.position || 0, agentProfileTabPinned: chat.pinned !== false })
            continue
          }
          const previousActive = state.activeTabId
          const tabId = await state.createChatTab(chat.name, {
            ...primary.metadata,
            agentProfileIsMainChat: false,
            agentProfileBuilder: false,
            agentProfileTabOwnerID: chat.tab_owner_id,
            agentProfileTabOwnerName: chat.tab_owner_name,
            agentProfileTabCanWrite: chat.can_write,
            agentProfileConversationKey: chat.conversation_key,
            agentProfileConversationId: chat.conversation_id,
            agentProfileTabPosition: chat.position || 0,
            agentProfileTabPinned: chat.pinned !== false,
            ...(chat.agent_id ? { agentProfileEngine: chat.provider, agentProfileModelID: chat.model_id, agentProfileConnectionID: chat.connection_id, agentProfileChatProvider: chat.provider, agentProfileChatConnectionID: chat.connection_id } : {}),
            agentProfileMCPSelectionInitialized: true,
          }, chat.session_id)
          useChatStore.getState().renameTab(tabId, chat.name)
          useChatStore.getState().setTabConfig(tabId, {
            selectedServers: primary.config.selectedServers,
            selectedSkills: primary.config.selectedSkills,
          })
          if (previousActive) activateTab(previousActive)
          await hydrateTabEvents(chat.session_id, {
            workspacePath: primary.metadata?.agentProfileWorkspace,
            fallbackToChatHistory: true,
            preferChatHistory: true,
          })
        }
      } catch {
        // The list is a convenience; the next pass tries again.
      } finally {
        busy = false
      }
    }
    syncRef.current = sync
    void sync()
    const timer = window.setInterval(() => { void sync() }, 15000)
    window.addEventListener('focus', sync)
    return () => {
      cancelled = true
      window.clearInterval(timer)
      window.removeEventListener('focus', sync)
    }
  }, [allowSideChats, canonicalTabId, profileId, projectId])
  // Alt+1..5 / Option+1..5 pick a chat, Alt+Shift+T / Option+Shift+T opens one
  // (PLAT-571), only while Code is the product on screen and never for a key
  // the terminal already took (it marks its own Alt keys handled).
  const tabsRef = useRef(tabs)
  tabsRef.current = tabs
  useEffect(() => {
    if (!allowSideChats) return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || useProductSurfaceStore.getState().productSurface !== 'work') return
      if (event.target instanceof Element && event.target.closest('.xterm')) return
      const action = workChatTabShortcut(event)
      if (!action) return
      event.preventDefault()
      markFeatureUsed(action.kind === 'new' ? 'chat-tab-new' : 'chat-tab-keys')
      if (action.kind === 'new') {
        setNewTabName(current => current ?? '')
        return
      }
      const target = tabsRef.current[action.index]
      if (target) activateTab(target.tabId)
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [allowSideChats])
  // With many tabs the strip scrolls sideways (tabs keep their width, "+" stays in view) and the
  // active tab is brought into view when it changes.
  const stripRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const active = stripRef.current?.querySelector('[data-tab-active="true"]')
    if (active && typeof (active as HTMLElement).scrollIntoView === 'function') (active as HTMLElement).scrollIntoView({ inline: 'nearest', block: 'nearest' })
  }, [activeTabId, tabs.length])
  return (
    <div className="flex min-w-0 flex-1 items-center gap-1">
      <div ref={stripRef} className="flex min-w-0 items-center gap-1 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden [&>*]:shrink-0" data-testid="work-chat-tab-strip">
      {visibleTabs.map(tab => {
        const key = tab.tabId === canonicalTabId || !canControl(tab) ? undefined : sideChatKey(tab)
        const targetKey = key || (tab.tabId === canonicalTabId ? listed?.[0]?.conversation_key : undefined)
        return <div
          key={tab.tabId}
          data-chat-tab-id={tab.tabId}
          draggable={!!key}
          onDragStart={key ? event => startDrag(event, key, tab.tabId) : undefined}
          onDragOver={event => overTab(event, targetKey)}
          onDrop={event => dropTab(event, targetKey)}
          onDragEnd={endDrag}
          className={`flex items-center rounded border-l-2 ${dropKey === targetKey && draggingKey !== key ? 'border-primary bg-primary/10' : 'border-transparent'} ${draggingKey === key ? 'opacity-50' : ''}`}
          title={key ? 'Drag to reorder this agent' : undefined}
        ><AgentWorksChatTabItem
        tab={tab}
        isActive={tab.tabId === activeTabId}
        canClose={false}
        isBlank={false}
        displayName={tab.tabId === canonicalTabId ? (tab.metadata?.agentProfileTabOwnerName ? `${tab.metadata.agentProfileTabOwnerName} · Builder` : projectName) : sideChatName(tab)}
        workspaceActivity={tab.tabId === canonicalTabId ? projectActivity : undefined}
        titleOverride={tab.tabId === canonicalTabId ? projectName : key ? sideChatName(tab) : undefined}
        productSurface={tab.tabId === canonicalTabId ? 'work' : undefined}
        onTabClick={selectTab}
        onCloseTab={closeProjectTab}
        closeStopsSession={!!key}
      /></div>
      })}
      </div>
      {tabs.length > 1 && (
        <div ref={moreRef} className="relative shrink-0">
          <button
            type="button"
            onClick={() => setMoreOpen(open => !open)}
            aria-expanded={moreOpen}
            className="inline-flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            title="Switch agent"
            aria-label={`Agents: switch, reorder or close (${agentCount})`}
            aria-haspopup="dialog"
            data-testid="work-more-tabs"
          >
            <ChevronDown className="h-3.5 w-3.5" aria-hidden="true" />
          </button>
          {moreOpen && (
            <div className="absolute right-0 top-8 z-50 flex max-h-[min(26rem,70dvh)] w-80 max-w-[calc(100vw-2rem)] flex-col rounded-xl border border-border bg-popover p-1.5 shadow-lg" role="dialog" aria-label="Agents">
              <div className="flex shrink-0 items-center justify-between gap-3 border-b border-border px-2 pb-2 pt-1">
                <h2 className="text-xs font-semibold">Agents & activity</h2>
                {canReorderInMenu && <button type="button" aria-pressed={reordering} onClick={() => setReordering(value => !value)} className="rounded px-2 py-1 text-xs text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">{reordering ? 'Done' : 'Reorder'}</button>}
              </div>
              {reordering && <p className="shrink-0 px-2 py-2 text-[11px] text-muted-foreground">Drag agents or use the arrows to change their order.</p>}
              <div className="workspace-menu-scroll min-h-0 overflow-y-auto overscroll-contain pt-1">
              {tabs.map(tab => {
                const key = tab.tabId === canonicalTabId || !canControl(tab) ? undefined : sideChatKey(tab)
                const label = tab.tabId === canonicalTabId ? (tab.metadata?.agentProfileTabOwnerName ? `${tab.metadata.agentProfileTabOwnerName} · Builder` : projectName) : sideChatName(tab)
                const position = (listed || []).findIndex(chat => chat.conversation_key === key)
                return <div
                  key={tab.tabId}
                  data-chat-list-id={tab.tabId}
                  draggable={reordering && !!key}
                  onDragStart={reordering && key ? event => startDrag(event, key, tab.tabId) : undefined}
                  onDragOver={event => overTab(event, key || listed?.[0]?.conversation_key)}
                  onDrop={event => dropTab(event, key || listed?.[0]?.conversation_key)}
                  onDragEnd={endDrag}
                  className={`group flex min-h-10 items-center gap-1 rounded-lg px-1 ${tab.tabId === activeTabId ? 'bg-muted/70' : 'hover:bg-muted/50'} ${dropKey === key && draggingKey !== key ? 'ring-1 ring-inset ring-primary' : ''}`}
                >
                  {reordering && key ? <GripVertical aria-hidden="true" className="h-3.5 w-3.5 shrink-0 cursor-grab text-muted-foreground" /> : <span className="flex w-4 shrink-0 justify-center">{tab.tabId === activeTabId && <Check aria-hidden="true" className="h-3.5 w-3.5 text-primary" />}</span>}
                  {renaming === tab.tabId ? <input
                    autoFocus
                    aria-label={`New name for ${label}`}
                    value={renameDraft}
                    onChange={event => setRenameDraft(event.target.value)}
                    onKeyDown={event => {
                      if (event.key === 'Escape') { event.stopPropagation(); setRenaming(null) }
                      if (event.key === 'Enter') { void renameSideChat(tab, renameDraft).finally(() => setRenaming(null)) }
                    }}
                    onBlur={() => setRenaming(null)}
                    className="min-w-0 flex-1 rounded border border-border bg-background px-2 py-1 text-xs"
                  /> : <button
                    type="button"
                    onClick={() => { setMoreOpen(false); selectTab(tab.tabId) }}
                    title={label}
                    className={`min-w-0 flex-1 rounded py-2 text-left text-xs focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${tab.tabId === activeTabId ? 'font-medium text-foreground' : 'text-foreground/85'}`}
                  >
                    <span className="block truncate">{label}</span>{!canControl(tab) && <span className="block text-[10px] font-normal text-muted-foreground">{tab.metadata?.agentProfileTabOwnerName} controls this tab</span>}{!isAgentTab(tab) && <span className="block text-[10px] font-normal text-muted-foreground">Activity</span>}
                  </button>}
                  {reordering && key ? <div className="flex shrink-0 items-center">
                    <button type="button" aria-label={`Move ${label} earlier`} disabled={position <= 0} onClick={() => { const target = listed?.[position - 1]; if (target) void moveSideChat(key, target.conversation_key) }} className="inline-flex h-7 w-7 items-center justify-center rounded text-muted-foreground hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-30"><ArrowUp className="h-3.5 w-3.5" /></button>
                    <button type="button" aria-label={`Move ${label} later`} disabled={position < 0 || position === (listed?.length || 0) - 1} onClick={() => { const target = listed?.[position + 1]; if (target) void moveSideChat(key, target.conversation_key) }} className="inline-flex h-7 w-7 items-center justify-center rounded text-muted-foreground hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-30"><ArrowDown className="h-3.5 w-3.5" /></button>
                  </div> : <>
                  {key && renaming !== tab.tabId && <button type="button" aria-label={`Rename ${label}`} title="Rename" className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring" onClick={() => { setRenameDraft(sideChatName(tab)); setRenaming(tab.tabId) }}><Pencil className="h-3.5 w-3.5" /></button>}
                  {key && <button type="button" aria-pressed={sideChatPinned(tab)} aria-label={sideChatPinned(tab) ? `Unpin ${label}` : `Pin ${label} to the tab bar`} title={sideChatPinned(tab) ? 'Pinned to the tab bar. Click to unpin' : 'Pin to the tab bar'} className={`inline-flex h-7 w-7 shrink-0 items-center justify-center rounded hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring ${sideChatPinned(tab) ? 'text-foreground' : 'text-muted-foreground'}`} onClick={() => { void pinSideChat(key, !sideChatPinned(tab)) }}>{sideChatPinned(tab) ? <PinOff className="h-3.5 w-3.5" /> : <Pin className="h-3.5 w-3.5" />}</button>}
                  {tab.tabId !== canonicalTabId && canControl(tab) && (
                    <button
                      type="button"
                      onClick={() => { setMoreOpen(false); closeProjectTab(tab.tabId) }}
                      className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
                      aria-label={`Close ${label}`}
                      title={isAgentTab(tab) ? "Close agent" : "Close activity view"}
                    >
                      <X className="h-3 w-3" />
                    </button>
                  )}
                  </>}
                </div>
              })}
              </div>
            </div>
          )}
        </div>
      )}

      {allowSideChats && newTabName !== null && (
        <form
          className="flex shrink-0 items-center"
          onSubmit={event => { event.preventDefault(); void openSideChat(newTabName) }}
        >
          <input
            autoFocus
            value={newTabName}
            onChange={event => setNewTabName(event.target.value)}
            onKeyDown={event => { if (event.key === 'Escape') setNewTabName(null) }}
            onBlur={() => { if (!opening && !newTabName.trim()) setNewTabName(null) }}
            maxLength={120}
            disabled={opening}
            placeholder="Name this agent, e.g. research"
            aria-label="Agent name"
            className="h-7 w-48 rounded-md border border-input bg-background px-2 text-xs focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            data-testid="work-new-chat-tab-name"
          />
        </form>
      )}
      {allowSideChats && newTabName === null && (
        <button
          type="button"
          onClick={() => setNewTabName('')}
          disabled={opening || sideChats.length >= WORK_SIDE_CHAT_LIMIT}
          className={`ml-0.5 inline-flex h-7 shrink-0 items-center justify-center gap-1 rounded-md text-xs text-primary transition-colors hover:bg-primary/10 hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-40 ${sideChats.length === 0 ? 'px-2' : 'w-7'}`}
          aria-label="New agent"
          title={sideChats.length >= WORK_SIDE_CHAT_LIMIT
            ? `At most ${WORK_SIDE_CHAT_LIMIT + 1} agents per project`
            : 'New agent (⌥⇧T on Mac, Alt+Shift+T on Windows): another agent in this project. The main agent keeps Slack, WhatsApp, MCP and schedules'}
          data-testid="work-new-chat-tab"
        >
          {opening ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Plus className="h-3.5 w-3.5" />}
          {/* Labelled until the first side chat exists, then a browser-style "+" next to the tabs. */}
          {sideChats.length === 0 && <span>New agent</span>}
        </button>
      )}
    </div>
  )
}

function WorkNewChatGuide({ sharedBy, product, cowork }: { sharedBy?: string; product: ProjectProductConfig; cowork?: boolean }) {
  if (cowork) return <CrewStarterGuide />
  if (!product.hasIdentity) {
    return <ProductChatLandingCard
      title="Work on this project"
      description="Use this conversation to build, investigate or improve the existing project."
      examples={[
        'Clone a repository into code/ and explain how it works',
        'Build, run, test and debug in the workspace terminal',
        'Call your Crews and workflows when it needs them',
      ]}
      shortcutHint={<NewTabHint />}
    />
  }
  if (sharedBy) {
    return <ProductChatLandingCard
      title={`Explore ${sharedBy}’s ${product.noun}`}
      description={`This ${product.noun} is shared with you on Run. Ask it to:`}
      examples={[
        `Ask what this ${product.noun} can help you with`,
        'Ask a question about its dashboard',
        'Run its attached workflows when you ask',
      ]}
      footer="Your conversation stays private to you — the owner never sees it."
      shortcutHint
    />
  }
  return <CrewStarterGuide />
}

function WorkTopBarControl({
  product,
  sessions,
  selected,
  onSelect,
  onNewProject,
  onDelete,
  creating,
  canCreate,
  deletingProjectId,
}: {
  product: ProjectProductConfig
  sessions: WorkSession[]
  selected: WorkSession | null
  onSelect: (session: WorkSession) => void
  onNewProject: () => void
  onDelete: (session: WorkSession) => void
  creating: boolean
  /** The account may create in this product (PLAT-767). */
  canCreate: boolean
  deletingProjectId: string | null
}) {
  const [open, setOpen] = useState(false)

  return (
    <TopBarEntitySelector
      dataTour="crew-selector"
      label={selected?.identity?.name || selected?.title}
      leading={selected ? <EntityIdentityIcon icon={selected.identity?.icon} label={selected.identity?.name || selected.title} /> : undefined}
      compactOnNarrow
      title={selected ? `${selected.identity?.name || selected.title}${selected.identity?.name && selected.identity.name !== selected.title ? ` · ${selected.title}` : ''}${selected.shared ? ` · shared by ${selected.shared.ownerUsername || selected.shared.ownerId} (${isWorkSessionRun(selected) ? 'Run' : 'Builder'})` : ''}` : `${sessions.length ? 'Choose a' : 'New'} ${product.itemNoun}`}
      placeholder={`${sessions.length ? 'Choose a' : 'New'} ${product.itemNoun}`}
      open={open}
      onToggle={() => setOpen(current => !current)}
      onClose={() => setOpen(false)}
      onAdd={onNewProject}
      addLabel={`New ${product.itemNoun}`}
      addTitle={canCreate ? `New ${product.itemNoun}` : `Your account cannot create a ${product.itemNoun}. Ask an administrator to allow it.`}
      addDisabled={creating || !canCreate}
    >
      <div role="menu" aria-label="Projects" className="max-h-96 space-y-1 overflow-y-auto p-2">
        <button
          type="button"
          onClick={() => { setOpen(false); onNewProject() }}
          disabled={creating || !canCreate}
          title={canCreate ? undefined : `Your account cannot create a ${product.itemNoun}. Ask an administrator to allow it.`}
          className="w-full rounded-md p-2 text-left text-sm text-gray-700 hover:bg-gray-100 disabled:opacity-50 dark:text-gray-300 dark:hover:bg-slate-700"
        >
          <span className="flex items-center gap-2 font-medium">
            <span className="h-2 w-2 rounded-full bg-blue-500" />
            {creating ? `Creating ${product.itemNoun}…` : `+ New ${product.itemNoun}`}
          </span>
        </button>
        {sessions.length === 0 ? (
          <div className="p-2 text-center text-sm text-gray-500 dark:text-gray-400">No projects yet. Create one to get started.</div>
        ) : (<>
          {sessions.filter(session => !session.shared).map(session => (
            <div
              key={`${session.product}:${session.id}`}
              className={`flex items-center rounded-md text-sm transition-colors ${session.product === selected?.product && session.id === selected?.id ? 'bg-blue-100 text-blue-900 dark:bg-blue-900/30 dark:text-blue-100' : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-slate-700'}`}
            >
              <button
                type="button"
                role="menuitemradio"
                aria-checked={session.product === selected?.product && session.id === selected?.id}
                onClick={() => { onSelect(session); setOpen(false) }}
                className="min-w-0 flex-1 p-2 text-left"
              >
                <span className="flex items-center gap-2">
                  <EntityIdentityIcon icon={session.identity?.icon} label={session.identity?.name || session.title} />
                  <span className="min-w-0">
                    <span className="block truncate font-medium">{session.identity?.name || session.title}</span>
                    {session.identity?.name && session.identity.name !== session.title && !/^(?:new project|new crew)$/i.test(session.title.trim())
                      ? <span className="block truncate text-xs text-muted-foreground">{session.title}</span>
                      : null}
                  </span>
                </span>
              </button>
              <button
                type="button"
                aria-label={`Delete ${product.noun} ${session.identity?.name || session.title}`}
                title={`Delete ${product.noun}`}
                disabled={deletingProjectId !== null}
                onClick={() => { setOpen(false); onDelete(session) }}
                className="mr-1 rounded p-2 text-gray-400 transition-colors hover:bg-red-100 hover:text-red-600 disabled:opacity-50 dark:hover:bg-red-950/40 dark:hover:text-red-400"
              >
                {deletingProjectId === session.id ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Trash2 className="h-3.5 w-3.5" />}
              </button>
            </div>
          ))}
          {sessions.some(session => session.shared) && (
            <div aria-hidden="true" className="px-2 pb-1 pt-2 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
              {product.listsSharedProjects && !product.hasIdentity ? 'Shared with you' : 'Shared with you'}
            </div>
          )}
          {sessions.filter(session => session.shared).map(session => (
            <div
              key={`shared:${session.product}:${session.id}`}
              className={`flex items-center rounded-md text-sm transition-colors ${session.product === selected?.product && session.id === selected?.id ? 'bg-blue-100 text-blue-900 dark:bg-blue-900/30 dark:text-blue-100' : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-slate-700'}`}
            >
              <button
                type="button"
                role="menuitemradio"
                aria-checked={session.product === selected?.product && session.id === selected?.id}
                onClick={() => { onSelect(session); setOpen(false) }}
                className="min-w-0 flex-1 p-2 text-left"
              >
                <span className="flex items-center gap-2">
                  <EntityIdentityIcon icon={session.identity?.icon} label={session.identity?.name || session.title} />
                  <span className="min-w-0">
                    <span className="block truncate font-medium">{session.identity?.name || session.title}</span>
                    <span className="block truncate text-xs text-muted-foreground">
                      {session.shared?.ownerUsername || session.shared?.ownerId || 'Another user'}
                      {session.identity?.name && session.identity.name !== session.title ? ` · ${session.title}` : ''}
                    </span>
                  </span>
                </span>
              </button>
            </div>
          ))}
        </>)}
      </div>
    </TopBarEntitySelector>
  )
}

function WorkSurfaceContent({ product }: { product: ProjectProductConfig }) {
  const { sessions, selected, select, create, installTemplate, removeTemplate, remove, updateLLMConfig, updateNativeAgentTools, updateSelections, updateIdentity, refresh, loading: sessionsLoading, error: sessionsError } = useWorkSessions(product)
  const selectedTemplates = !product.hasTemplates ? [] : crewTemplates.filter(template => selected?.templates.some(installed => installed.id === template.id && installed.version === template.version))
  const workflowContextSignature = selected?.workflowContextPaths.join('\u0000') || ''
  const persistLegacyRuntime = useCallback(async (selection: WorkRuntimeSelection) => {
    if (!selected || isWorkSessionRun(selected)) return
    await updateLLMConfig(selected.id, selection)
  }, [selected, updateLLMConfig])
  const { tabId, canonicalTabId, error: chatError } = useWorkChatTab(product, selected, persistLegacyRuntime)



  const selectedMode = selected ? productMode(selected) : undefined
  useEffect(() => {
    if (selected?.id && selectedMode) setProjectMode(selected.id, selectedMode)
  }, [selected?.id, selectedMode])

  // A browser reload loses transient tab metadata while the durable references
  // remain in workflow.json. Force the first follow-up through the full profile
  // route so an old retained CLI cannot bypass the current read-only grants.
  useEffect(() => {
    if (selected?.id && workflowContextSignature) markWorkProjectRuntimeDirty(selected.id)
  }, [selected?.id, workflowContextSignature])
  const [projectRefreshInteractionKinds, setProjectRefreshInteractionKinds] = useState<Set<string>>(() => new Set())
  useEffect(() => {
    let cancelled = false
    void loadAgentProfileInteractionKinds(product.profileId, 'product.refresh', product.profileVersion).then(kinds => {
      if (cancelled) return
      setProjectRefreshInteractionKinds(kinds)
    })
    return () => { cancelled = true }
  }, [product])
  // Product slash commands come from the same profile the provider options do,
  // and are cleared on unmount so leaving Crew does not leave its commands
  // offered in another product's chat.
  useEffect(() => {
    let cancelled = false
    void loadWorkProductCommands(product.profileId, product.profileVersion, product.noun)
      .then((commands) => { if (!cancelled) setProductCommands(toProductCommandDefinitions(commands)) })
      .catch(() => { if (!cancelled) setProductCommands([]) })
    return () => { cancelled = true; setProductCommands([]) }
  }, [product])
  const projectConfigRefreshToken = useChatStore(state => {
    const sessionId = tabId ? state.chatTabs[tabId]?.sessionId : undefined
    return (sessionId ? state.tabEvents[sessionId] || [] : [])
    .filter(event => {
      const interaction = parseProductInteraction(event)
      return (interaction?.product === product.profileId && projectRefreshInteractionKinds.has(interaction.kind)) ||
        // Backward compatibility for events persisted before typed product interactions.
        event.type === 'work_workflow_references_updated'
    })
    .map(event => event.id || event.timestamp || '')
    .join('|')
  })
  const handledProjectConfigRefreshToken = useRef('')

  useEffect(() => {
    if (!projectConfigRefreshToken || projectConfigRefreshToken === handledProjectConfigRefreshToken.current) return
    handledProjectConfigRefreshToken.current = projectConfigRefreshToken
    void refresh()
  }, [projectConfigRefreshToken, refresh])
  const [creating, setCreating] = useState(false)
  const [createOpen, setCreateOpen] = useState(false)
  const [createMode, setCreateMode] = useState<ProductMode>('dev')
  const [deleteCandidate, setDeleteCandidate] = useState<WorkSession | null>(null)
  const [deletingProjectId, setDeletingProjectId] = useState<string | null>(null)
  const [chatOpen, setChatOpen] = useState(true)
  const [panelOpen, setPanelOpen] = useState(true)
  const [workspaceView, setWorkspaceView] = useState<WorkWorkspaceView>(() => routeView(product.profileId, selected?.id, normalizeWorkWorkspaceView, product.defaultView) ?? readWorkWorkspaceView(selected?.id, product.profileId) ?? product.defaultView)
  const [viewNavigationRevision, setViewNavigationRevision] = useState(0)
  useProductUrlView(product.profileId, { id: selected?.id, view: selected ? workspaceView : undefined }, viewNavigationRevision)
  const pendingWorkView = useProductSurfaceStore(state => state.pendingWorkView)
  const setPendingWorkView = useProductSurfaceStore(state => state.setPendingWorkView)
  const [workspaceViewRefresh, setWorkspaceViewRefresh] = useState(0)
  const [enabledWorkspacePanels, setEnabledWorkspacePanels] = useState<Set<string> | undefined>()
  const splitLayoutRef = useRef<HTMLDivElement>(null)
  const [splitRatio, setSplitRatioState] = useState(() => readWorkSplitRatio(selected?.id))
  const splitRatioRef = useRef(splitRatio)
  const [mobilePaneWidth, setMobilePaneWidthState] = useState<number | null>(() => readMobilePaneWidth(selected?.id))
  const mobilePaneWidthRef = useRef(mobilePaneWidth)
  useEffect(() => { const next = readMobilePaneWidth(selected?.id); mobilePaneWidthRef.current = next; setMobilePaneWidthState(next) }, [selected?.id])
  const [reportPreviewPreference, setReportPreviewPreference] = useState<ReportPreviewDevice>(() => readReportPreviewPreference(selected?.workspacePath))
  // All split classes derive from the shared layout resolver: one decision
  // point for every flag combination (see workSurfaceLayoutResolver.ts).
  const { start: startSplitDrag, stop: stopSplitDrag } = usePointerDrag()
  const [createError, setCreateError] = useState<string | null>(null)
  const showProviders = useLLMStore((state) => state.showLLMModal)
  const showSchedulesOverview = useAppStore(state => state.showSchedulesOverview)
  const adminPage = useAppStore(state => state.adminPage)
  const activeSessionId = useChatStore(state => tabId ? state.chatTabs[tabId]?.sessionId : undefined)
  const legacyViewEvents = usePresentationEvents(activeSessionId ?? undefined, ['workflow.view'])
  const handledLegacyViewEvents = useRef<{ session?: string; count: number }>({ session: activeSessionId ?? undefined, count: legacyViewEvents.length })
  const selectWorkspaceView = useCallback((view: WorkWorkspaceView, navigation = true) => {
    const allowedView = isWorkWorkspaceViewEnabled(view, undefined) ? view : 'files'
    setWorkspaceView(allowedView)
    setPanelOpen(true)
    if (navigation) setViewNavigationRevision(value => value + 1)
    writeWorkWorkspaceView(selected?.id, product.profileId, allowedView)
  }, [selected?.id, product.profileId])


  // The server's can_build response keeps owners/co-owners in the Builder
  // workspace. People the Crew is only shared with use its dashboard.
  const showShell = product.hasTerminal && !isWorkSessionRun(selected) && selectedMode !== 'cowork'
  const isShared = isWorkSessionRun(selected)
  const sharedPanels = SHARED_CREW_WORKSPACE_PANELS
  const workspacePanels = useMemo(() => isShared ? sharedPanels : enabledWorkspacePanels, [enabledWorkspacePanels, isShared, sharedPanels])
  const openWorkPresentationView = useCallback((view: string, target?: string) => {
    if (!(view in WORK_UI_PRESENTATION_VIEWS)) return
    const panel = WORK_UI_PRESENTATION_VIEWS[view as WorkUIPresentationView]
    if (!isWorkWorkspaceViewEnabled(panel, workspacePanels)) return
    if (isWorkSessionRun(selected) && !sharedPanels.has(panel)) return
    if (panel === 'schedules') {
      const automationTarget = target === 'webhooks' ? 'triggers' : target || 'schedules'
      useWorkflowStore.getState().openWorkspaceView('workshop', automationTarget)
    }
    if (panel === 'memory' && target) setWorkspaceViewTarget('memory', target)
    if (panel === 'mcp' && target) {
      setWorkspaceViewTarget('mcp', target)
    }
    setPanelOpen(true)
    selectWorkspaceView(panel)
  }, [selected?.shared, selectWorkspaceView, sharedPanels, showShell, workspacePanels])
  useEffect(() => {
    const open = (event: Event) => {
      const detail = (event as CustomEvent<{ projectId: string; conversationKey?: string }>).detail
      if (detail?.projectId !== selected?.id || isShared || !isWorkWorkspaceViewEnabled('identity', workspacePanels)) return
      setWorkspaceViewTarget('identity', 'models')
      selectWorkspaceView('identity')
    }
    window.addEventListener('agentworks:open-chat-models', open)
    return () => window.removeEventListener('agentworks:open-chat-models', open)
  }, [selected?.id, isShared, workspacePanels, selectWorkspaceView])
  useEffect(() => {
    const open = (event: Event) => {
      const detail = (event as CustomEvent<{ projectId?: string }>).detail
      if (detail?.projectId !== selected?.id || isShared || !isWorkWorkspaceViewEnabled('identity', workspacePanels)) return
      setWorkspaceViewTarget('identity', 'location')
      selectWorkspaceView('identity')
    }
    window.addEventListener('agentworks:open-chat-laptop', open)
    return () => window.removeEventListener('agentworks:open-chat-laptop', open)
  }, [selected?.id, isShared, workspacePanels, selectWorkspaceView])
  useEffect(() => {
    if (!pendingWorkView) return
    if (pendingWorkView === 'triggers') {
      if (selected?.id !== selectedProjectIdFor(product)) return
      openWorkPresentationView('schedules', 'triggers')
    } else {
      openWorkPresentationView(pendingWorkView)
    }
    setPendingWorkView(null)
  }, [openWorkPresentationView, pendingWorkView, product, selected?.id, setPendingWorkView])
  const workUIAdapter = useMemo<WorkspaceUIControlAdapter>(() => ({
    getView: () => workPresentationView(workspaceView),
    openView: openWorkPresentationView,
    refreshView: () => setWorkspaceViewRefresh(value => value + 1),
    isViewSupported: (view) => view in WORK_UI_PRESENTATION_VIEWS &&
      isWorkWorkspaceViewEnabled(WORK_UI_PRESENTATION_VIEWS[view as WorkUIPresentationView], workspacePanels),
    labelForView: (view) => WORK_UI_LABELS[view as WorkUIPresentationView] ?? view,
    actorLabel: product.noun,
    getTarget: (view, host) => view === 'mcp' || view === 'memory' ? host?.querySelector<HTMLElement>('[data-ui-view-section]')?.dataset.uiViewSection : view === 'workshop' || view === 'schedules' ? useWorkflowStore.getState().workspaceViewTarget?.target : undefined,
  }), [openWorkPresentationView, product.noun, workspaceView, workspacePanels])
  useWorkspaceUIControl(activeSessionId ?? undefined, workUIAdapter)

  useEffect(() => {
    const session = activeSessionId ?? undefined
    if (handledLegacyViewEvents.current.session !== session) {
      handledLegacyViewEvents.current = { session, count: legacyViewEvents.length }
      return
    }
    for (const event of legacyViewEvents.slice(handledLegacyViewEvents.current.count)) {
      const view = event.payload.view
      if (typeof view === 'string') {
        openWorkPresentationView(view)
        if (event.payload.action === 'refresh') setWorkspaceViewRefresh(value => value + 1)
      }
    }
    handledLegacyViewEvents.current = { session, count: legacyViewEvents.length }
  }, [activeSessionId, legacyViewEvents, openWorkPresentationView])

  const changeWorkRuntime = useCallback(async (selection: WorkRuntimeSelection) => {
    if (!selected || !tabId) return
    const previous = workLLMSelectionFromConfig(selected.llmConfig)
    const providerChanged = Boolean(previous?.provider && selection.provider && (previous.provider !== selection.provider || previous.connectionId !== selection.connectionId))
    try {
      await updateLLMConfig(selected.id, selection)
      setWorkProjectRuntimeSelection(selected.id, tabId, selection)
      if (providerChanged) {
        const chatStore = useChatStore.getState()
        const label = selection.engine === 'claude-code' ? 'Claude Code'
          : selection.engine === 'codex-cli' ? 'Codex'
            : selection.engine === 'cursor-cli' ? 'Cursor'
              : selection.engine === 'pi-cli' ? 'Pi'
                : selection.engine === 'muse-cli' ? 'Muse'
                  : selection.engine
        chatStore.addToast(`Coding agent changed to ${label}. This conversation will continue with ${label} on your next message.`, 'success')
      }
    } catch (cause) {
      useChatStore.getState().addToast(cause instanceof Error ? cause.message : 'Could not save the project model.', 'error')
    }
  }, [selected, tabId, updateLLMConfig])

  // Keep the workspace's inputs stable while chat state changes. The pane is
  // memoized, and each callback only changes when its project or action changes.
  const workspaceProjectId = selected?.id
  const selectedProjectId = selected?.id
  // Stable, so the memoized workspace pane never re-renders because of it.
  const installSelectedTemplate = useCallback(async (templateId: CrewTemplateId) => {
    if (!selectedProjectId) return
    await installTemplate(selectedProjectId, templateId)
    setChatOpen(true)
  }, [installTemplate, selectedProjectId])
  const sharedWorkspaceOwner = useMemo(() => isWorkSessionRun(selected) && selected?.shared
    ? { ownerId: selected.shared.ownerId, ownerUsername: selected.shared.ownerUsername }
    : undefined, [selected?.shared])
  const changeNativeAgentTools = useCallback(async (enabled: boolean) => {
    await updateNativeAgentTools(workspaceProjectId!, enabled)
    markWorkProjectRuntimeDirty(workspaceProjectId!)
  }, [workspaceProjectId, updateNativeAgentTools])
  const changeSelectedServers = useCallback((servers: string[]) => updateSelections(workspaceProjectId!, { selectedServers: servers }), [workspaceProjectId, updateSelections])
  const changeSelectedSkills = useCallback((skills: string[]) => updateSelections(workspaceProjectId!, { selectedSkills: skills }), [workspaceProjectId, updateSelections])
  const changeSelectedSecrets = useCallback((secrets: string[]) => updateSelections(workspaceProjectId!, { selectedSecrets: secrets }), [workspaceProjectId, updateSelections])
  const changeSelectedGlobalSecrets = useCallback((secrets: string[]) => updateSelections(workspaceProjectId!, { selectedGlobalSecrets: secrets }), [workspaceProjectId, updateSelections])
  const changeWorkflowContextPaths = useCallback(async (paths: string[]) => {
    if (!workspaceProjectId) return
    await updateSelections(workspaceProjectId, { workflowContextPaths: paths })
    markWorkProjectRuntimeDirty(workspaceProjectId)
  }, [workspaceProjectId, updateSelections])
  const changeProjectIdentity = useCallback((patch: ProductIdentityPatch) => updateIdentity(workspaceProjectId!, patch), [workspaceProjectId, updateIdentity])
  const requestDeleteProject = useCallback(() => setDeleteCandidate(selected ?? null), [selected])

  useEffect(() => {
    useModeStore.getState().setModeCategory('multi-agent')
    useAppStore.getState().setAgentMode('multi-agent')
    useAppStore.getState().setShowWorkflowsOverview(false)
  }, [])

  useEffect(() => {
    let cancelled = false
    const load = async () => {
      const panels = await loadAgentProfileUIPanels(product.profileId, product.profileVersion)
      if (selected?.id) {
        try {
          const { data } = await api.post<{ ui_panels?: string[] }>('/api/workspaces', { action: 'get', workspace_id: `${product.profileId}:${selected.id}` })
          if (data.ui_panels?.length) { if (!cancelled) setEnabledWorkspacePanels(new Set(data.ui_panels)); return }
        } catch { /* Older servers keep their profile-declared panels. */ }
      }
      if (!cancelled) setEnabledWorkspacePanels(panels.size > 0 ? panels : undefined)
    }
    const settingsChanged = () => { markWorkProjectRuntimeDirty(selected?.id || ''); void load() }
    void load()
    window.addEventListener('agentworks:project-settings-changed', settingsChanged)
    return () => { cancelled = true; window.removeEventListener('agentworks:project-settings-changed', settingsChanged) }
  }, [product, selected?.id])

  // Capture the deep link's view on first render, before the URL projection
  // or any later effect can rewrite the address bar and hide it.
  const initialRouteView = useRef<{ id: string; view: WorkWorkspaceView } | null | undefined>(undefined)
  if (initialRouteView.current === undefined) {
    const route = currentProductRoute()
    const view = route?.surface === product.profileId && route.id ? normalizeWorkWorkspaceView(route.view) : null
    initialRouteView.current = view && route?.id ? { id: route.id, view } : null
  }
  const routeSuppliedView = selected
    ? (initialRouteView.current?.id === selected.id ? initialRouteView.current.view : routeView(product.profileId, selected.id, normalizeWorkWorkspaceView, product.defaultView))
    : null

  useLayoutEffect(() => {
    const savedView = routeSuppliedView ?? routeView(product.profileId, selected?.id, normalizeWorkWorkspaceView, product.defaultView) ?? readWorkWorkspaceView(selected?.id, product.profileId)
    // The captured link view is spent once applied (and is keyed to its
    // project), so switching projects never reapplies an old ?view.
    if (initialRouteView.current?.id === selected?.id) initialRouteView.current = null
    setWorkspaceView(isWorkSessionRun(selected) ? 'dashboard' : savedView ?? (selected && productMode(selected) === 'cowork' ? 'dashboard' : product.defaultView))
    const nextRatio = readWorkSplitRatio(selected?.id)
    splitRatioRef.current = nextRatio
    setSplitRatioState(nextRatio)
    setReportPreviewPreference(readReportPreviewPreference(selected?.workspacePath))
  }, [product.defaultView, product.profileId, selected?.id, selected?.shared, selected?.workspacePath])

  useWorkspaceReturn(product.profileId, selected?.id, selected ? workspaceView : undefined,
    { chat_open: chatOpen, panel_open: (!selected?.shared && workspaceView === 'context') || panelOpen, split_ratio: splitRatio, navigation_mode: 'fixed', preview_device: reportPreviewPreference, mobile_pane_width: mobilePaneWidth ?? undefined },
    saved => {
      setChatOpen(saved.chat_open); setPanelOpen(saved.panel_open)
      splitRatioRef.current = saved.split_ratio; setSplitRatioState(saved.split_ratio); writeWorkSplitRatio(selected?.id, saved.split_ratio)
      if (saved.preview_device) writeReportPreviewPreference(selected?.workspacePath, saved.preview_device)
      if (saved.mobile_pane_width) { mobilePaneWidthRef.current = saved.mobile_pane_width; setMobilePaneWidthState(saved.mobile_pane_width); writeMobilePaneWidth(selected?.id, saved.mobile_pane_width) }
    },
  )

  // Every panel starts beside chat. Manual expansion lasts until the next page.
  const previousPanelLocation = useRef<string | null>(null)
  useEffect(() => {
    const location = `${selected?.id ?? ''}:${workspaceView}`
    if (previousPanelLocation.current === location) return
    previousPanelLocation.current = location
    setChatOpen(true)
  }, [selected?.id, workspaceView])

  const landingProjectId = selected?.id
  const landingWorkspacePath = selected?.workspacePath
  const landingIsShared = isWorkSessionRun(selected)
  const dashboardAllowed = isWorkWorkspaceViewEnabled('dashboard', enabledWorkspacePanels)
  // A fresh deep link names its view (?view=shell): it is the person's
  // explicit choice, so neither the content-based landing guess nor the
  // panel list arriving later may replace it on first load.
  useEffect(() => {
    // Code is files-first: it never lands on the dashboard on its own.
    if (!product.hasIdentity || !landingProjectId || !landingWorkspacePath || landingIsShared || routeSuppliedView || readWorkWorkspaceView(landingProjectId, product.profileId)) return
    let cancelled = false
    void Promise.resolve('context' as const).then(view => {
      if (cancelled || selectedProjectIdFor(product) !== landingProjectId || readWorkWorkspaceView(landingProjectId, product.profileId)) return
      setWorkspaceView(view)
    })
    return () => { cancelled = true }
  }, [landingProjectId, landingWorkspacePath, landingIsShared, dashboardAllowed, product])

  useEffect(() => {
    const sync = () => setReportPreviewPreference(readReportPreviewPreference(selected?.workspacePath))
    window.addEventListener(REPORT_PREVIEW_PREFERENCE_CHANGED_EVENT, sync as EventListener)
    window.addEventListener('storage', sync)
    return () => {
      window.removeEventListener(REPORT_PREVIEW_PREFERENCE_CHANGED_EVENT, sync as EventListener)
      window.removeEventListener('storage', sync)
    }
  }, [selected?.workspacePath])

  useEffect(() => {
    if (isWorkSessionRun(selected)) {
      // Identity is always "enabled", so shared Crews need their own
      // fallback: a stale saved view must land on the inspect surface.
      if (!SHARED_CREW_WORKSPACE_PANELS.has(workspaceView)) selectWorkspaceView('dashboard', false)
      return
    }
    // Keep a route-supplied view only while it is enabled for this product
    // and person; a link to a disabled view still falls back below.
    if (routeSuppliedView && workspaceView === routeSuppliedView && isWorkWorkspaceViewEnabled(routeSuppliedView, enabledWorkspacePanels)) return
    if (!isWorkWorkspaceViewEnabled(workspaceView, enabledWorkspacePanels)) {
      // A disabled saved view cannot be shown. For an unsaved project, keep
      // Identity temporary while the content-based landing check runs.
      if (readWorkWorkspaceView(selected?.id, product.profileId)) selectWorkspaceView(product.defaultView, false)
      else setWorkspaceView(product.defaultView)
    }
  }, [enabledWorkspacePanels, product.defaultView, product.profileId, selectWorkspaceView, selected?.id, selected?.shared, showShell, workspaceView, routeSuppliedView])

  useEffect(() => stopSplitDrag, [selected?.id, stopSplitDrag])

  const setSplitRatio = useCallback((next: number, persist = false) => {
    const width = splitLayoutRef.current?.getBoundingClientRect().width || window.innerWidth
    const ratio = clampWorkSplitRatio(next, width)
    splitRatioRef.current = ratio
    setSplitRatioState(ratio)
    if (persist) writeWorkSplitRatio(selected?.id, ratio)
  }, [selected?.id])

  const handleSplitPointerDown = useCallback((event: ReactPointerEvent<HTMLButtonElement>) => {
    if (window.innerWidth < 768) return
    const rect = splitLayoutRef.current?.getBoundingClientRect()
    if (!rect?.width) return
    if (workspaceView === 'dashboard' && reportPreviewPreference === 'mobile') {
      // Mobile preview keeps a phone-width column, but the rail still drags: the column's width follows the pointer.
      startSplitDrag(event, {
        onMove: clientX => {
          const width = Math.max(MOBILE_PANE_MIN, Math.min(rect.width - 240, rect.right - clientX))
          mobilePaneWidthRef.current = width
          setMobilePaneWidthState(width)
        },
        onEnd: () => { if (mobilePaneWidthRef.current) writeMobilePaneWidth(selected?.id, mobilePaneWidthRef.current) },
      })
      return
    }
    startSplitDrag(event, {
      onMove: clientX => setSplitRatio((clientX - rect.left) / rect.width),
      onEnd: () => writeWorkSplitRatio(selected?.id, splitRatioRef.current),
    })
  }, [reportPreviewPreference, workspaceView, selected?.id, setSplitRatio, startSplitDrag])

  const createProject = useCallback(async (title: string, description: string, icon?: string, templateId?: CrewTemplateId, runsOn?: RunsOnSelection, mode?: ProductMode, onboarding?: CrewOnboarding) => {
    if (creating) return
    const user = useAuthStore.getState().user
    const server = useWorkspaceConnectionStore.getState().activeWorkspaceId
    if (!hasProductCreateAccess(user, useAuthStore.getState().isMultiUserMode, product.profileId)) return
    const isCurrent = () => useAuthStore.getState().user?.id === user?.id && useWorkspaceConnectionStore.getState().activeWorkspaceId === server
    setCreating(true)
    setCreateError(null)
    try {
      const created = await create(title, description, icon, templateId, runsOn, mode)
      if (!isCurrent()) return
      if (created.creationWarnings?.length) useChatStore.getState().addToast(created.creationWarnings.join(' '), 'warning')
      if (onboarding) {
        const next = await prepareCrewOnboarding({
          scope: projectMenuHistoryScope(server || 'local', user?.id || 'pending', product.profileId, created.id),
          workspacePath: created.workspacePath, onboarding, isCurrent,
        })
        if (next === 'stale') return
        const view = next === 'sharing' ? 'mcp' : 'context'
        if (next === 'sharing') setWorkspaceViewTarget('mcp', 'share')
        if (next === 'sharing-unavailable') useChatStore.getState().addToast('Crew created. Sharing setup is unavailable right now; you can check People & access from the project menu.', 'info')
        setPanelOpen(true)
        setWorkspaceView(view)
        writeWorkWorkspaceView(created.id, product.profileId, view)
      } else if (templateId) {
        setPanelOpen(true)
        setWorkspaceView('files')
        writeWorkWorkspaceView(created.id, product.profileId, 'files')
      }
      setCreateOpen(false)
    } catch (cause) {
      setCreateError(cause instanceof Error ? cause.message : `Could not create ${product.itemNoun}.`)
    } finally {
      setCreating(false)
    }
  }, [create, creating, product.itemNoun, product.profileId])

  const canCreateProjects = useAuthStore(state => hasProductCreateAccess(state.user, state.isMultiUserMode, product.profileId))
  const refuseCreate = useCallback(() => {
    useChatStore.getState().addToast(`Your account cannot create a ${product.itemNoun}. Ask an administrator to allow it.`, 'info')
  }, [product.itemNoun])
  const openCreateProject = useCallback(() => {
    if (!canCreateProjects) {
      refuseCreate()
      return
    }
    setCreateError(null)
    setCreateMode('dev')
    setCreateOpen(true)
  }, [canCreateProjects, refuseCreate])
  useProductCreateRequest('work', openCreateProject)

  // "Switch mode" in a project's settings sends people here: a project keeps the mode it was created in, so they make a new one.
  useEffect(() => {
    const open = (event: Event) => {
      if (!canCreateProjects) {
        refuseCreate()
        return
      }
      const mode = (event as CustomEvent<{ mode?: ProductMode }>).detail?.mode
      setCreateError(null)
      setCreateMode(mode ?? 'dev')
      setCreateOpen(true)
    }
    window.addEventListener('agentworks:new-crew-project', open)
    return () => window.removeEventListener('agentworks:new-crew-project', open)
  }, [canCreateProjects, product.profileId, refuseCreate])

  const deleteProject = useCallback(async () => {
    if (!deleteCandidate || deletingProjectId) return
    setDeletingProjectId(deleteCandidate.id)
    try {
      await remove(deleteCandidate.id, deleteCandidate.product)
      setDeleteCandidate(null)
      useChatStore.getState().addToast(`Deleted ${product.noun} “${deleteCandidate.identity?.name || deleteCandidate.title}”.`, 'success')
    } catch (cause) {
      const serverMessage = (cause as { response?: { data?: { error?: string } } })?.response?.data?.error
      const message = serverMessage || (cause instanceof Error ? cause.message : `Could not delete ${product.noun}.`)
      useChatStore.getState().addToast(`Failed to delete ${product.noun}: ${message}`, 'error')
    } finally {
      setDeletingProjectId(null)
    }
  }, [deleteCandidate, deletingProjectId, product.noun, remove])

  const topBarControl = useMemo(() => (
    <WorkTopBarControl
      product={product}
      sessions={sessions}
      selected={selected}
      onSelect={session => {
        if (session.product === product.profileId) { select(session.id); return }
        const navigation = useProductSurfaceStore.getState()
        navigation.setSelectedWorkProjectId(session.id)
        navigation.setProductSurface(session.product)
      }}
      onNewProject={openCreateProject}
      canCreate={canCreateProjects}
      onDelete={setDeleteCandidate}
      creating={creating}
      deletingProjectId={deletingProjectId}
    />
  ), [canCreateProjects, creating, deletingProjectId, openCreateProject, product, select, selected, sessions])

  const error = sessionsError || chatError

  return (
    <ProjectProductProvider value={product}>
    <TerminalFocusLayout tabId={tabId} enabled={!isShared && chatOpen && !showProviders && !showSchedulesOverview && !adminPage} className="flex h-screen min-h-0 bg-background">
      <UpdateProgressToast />
      <GlobalHumanFeedbackPrompt />
      <ModePresetBar
        productControl={topBarControl}
        reduced
        activityMonitorInWorkspace={Boolean(selected) && !showProviders && !showSchedulesOverview && !adminPage}
        walkthroughSurface={selected ? 'crew' : 'empty-crew'}
        walkthroughReady={!sessionsLoading && !creating && !error}
        // Both products have their own tour; only active dialogs pause it.
        walkthroughPaused={createOpen || deleteCandidate !== null}
      />
      {createOpen ? (
        <CreateCrewWizard
          onClose={() => { if (!creating) setCreateOpen(false) }}
          onCreate={(title, purpose, runsOn, mode, onboarding) => createProject(title, purpose, undefined, undefined, runsOn, mode, onboarding)}
          initialMode={createMode}
          profileVersion={product.profileVersion}
          submitting={creating}
          error={createError}
        />
      ) : null}
      <ConfirmationDialog
        isOpen={deleteCandidate !== null}
        onClose={() => { if (!deletingProjectId) setDeleteCandidate(null) }}
        onConfirm={() => { void deleteProject() }}
        title={`Delete ${product.noun}`}
        message={deleteCandidate
          ? `Delete ${product.noun} “${deleteCandidate.identity?.name || deleteCandidate.title}” and permanently remove its project files, chat history, ${product.hasIdentity ? 'schedules, triggers, bots, ' : ''}dashboard, and database?${product.profileId === 'work' ? ' Remove this Crew from every workflow before deleting it.' : ''} This cannot be undone.`
          : ''}
        confirmText={`Delete ${product.noun}`}
        loadingText={`Deleting ${product.noun}…`}
        type="danger"
        isLoading={deletingProjectId !== null}
        requireText={deleteCandidate ? deleteCandidate.identity?.name || deleteCandidate.title : undefined}
      />
      <div
        data-ui-workspace={selected?.workspacePath}
        data-ui-view={selected ? workPresentationView(workspaceView) : undefined}
        className="relative min-h-0 min-w-0 flex-1 overflow-hidden"
      >
        <LlmModalHost />
        {showSchedulesOverview && !showProviders && <SchedulesPage />}
        {adminPage && !showProviders && <AdminPages />}
        <div className={showProviders || showSchedulesOverview || adminPage ? 'hidden' : 'h-full'}>
          {error ? (
            <div className="grid h-full place-items-center p-6 text-center text-sm text-destructive">{error}</div>
          ) : !selected ? (
            <div className="flex h-full items-center justify-center bg-background">
              {sessionsLoading || creating ? (
                <span className="text-sm text-muted-foreground"><Loader2 className="mr-2 inline h-4 w-4 animate-spin" />Opening {product.noun}…</span>
              ) : (
                <ProductIntro
                  tour="crew-empty-state"
                  product={product.noun}
                  icon={<span className="font-mono text-3xl font-semibold text-gray-600 dark:text-gray-200">{'<>'}</span>}
                  title={sessions.length ? `Choose a ${product.itemNoun}` : 'An assistant for work you do again'}
                  description={sessions.length ? 'Open an existing workspace from the project menu, or create another.' : 'Prepare reports, research a topic or draft replies. Give your Crew the skills and connections it needs, then reuse it with your team.'}
                  features={[
                    { icon: Sparkles, title: 'Teach it your work', description: 'Add instructions, files and reusable skills.' },
                    { icon: UsersRound, title: 'Share with your team', description: 'Publish a dashboard and useful functions.' },
                    { icon: CalendarClock, title: 'Set a schedule', description: 'Have recurring work happen at the right time.' },
                  ]}
                  createTour="crew-create"
                  createLabel={`Create ${sessions.length ? 'another' : 'your first'} ${product.itemNoun}`}
                  onCreate={openCreateProject}
                />
              )}
            </div>
          ) : (
            <ProductWorkspaceShell
              splitRef={splitLayoutRef}
              compactContext={!isWorkSessionRun(selected) && workspaceView==='context'}
              toolbarWhenPanelClosed={!isWorkSessionRun(selected)}
              chatOpen={!isShared && chatOpen} panelOpen={isShared || panelOpen} splitRatio={splitRatio}
              showChatReopenControl={!isShared}
              mobilePreview={workspaceView === 'dashboard' && reportPreviewPreference === 'mobile'} mobilePaneWidth={mobilePaneWidth}
              onOpenChat={() => setChatOpen(true)} onOpenWorkspace={() => setPanelOpen(true)}
              tabs={!isShared && tabId && canonicalTabId && selected ? <WorkChatTabs projectId={selected.id} projectName={selected.identity?.name?.trim() || selected.title.trim() || product.noun} canonicalTabId={canonicalTabId} profileId={product.profileId} allowSideChats /> : <div className="min-w-0 flex-1" />}
              toolbar={<><WorkWorkspaceToolbar projectId={selected.id} sessionId={activeSessionId || ''} workspacePath={selected.workspacePath} view={workspaceView} onViewChange={selectWorkspaceView} enabledPanels={workspacePanels} readOnly={isWorkSessionRun(selected)} showShell={showShell} showActivityMonitor={!showProviders && !showSchedulesOverview && !adminPage} /></>}
              chatProps={{ 'data-tour': 'crew-chat' } as React.HTMLAttributes<HTMLElement>}
              workspaceProps={{ 'data-tour': 'crew-workspace', 'data-ui-workspace': selected.workspacePath, 'data-ui-view': workPresentationView(workspaceView) } as React.HTMLAttributes<HTMLElement>}
              chat={<>
                  {!isWorkSessionRun(selected) && selectedTemplates.length > 0 ? (
                    // Many templates used to stack their setup rows and push the chat off the screen. The rows share one
                    // capped area that scrolls, so the chat always keeps most of the height.
                    <div className="max-h-[min(8rem,25vh)] shrink-0 overflow-y-auto overscroll-contain" data-testid="template-setup-list">
                  {selectedTemplates.map(template => (
                    <WorkTemplateSetup
                      key={`${selected.id}:${template.id}`}
                      template={template}
                      workspacePath={selected.workspacePath}
                      chatReady={Boolean(tabId)}
                      onRemove={async () => {
                        await removeTemplate(selected.id, template.id)
                        useChatStore.getState().addToast(`${template.name} removed.`, 'success')
                      }}
                      onStartSetup={async () => {
                        setChatOpen(true)
                        await sendWorkspacePaneMessageToChat({ profileId: product.profileId, conversationKey: selected.id, message: `Help me set up the ${template.name} template in this ${product.noun}. Read ${template.setupPath} and ${template.setupGuidePath} in this project's files. Work through the pending checks, ask me for missing decisions or access, and add a check ID to completed_steps only after you verify it. Preserve the checklist and earlier progress. Keep this ${product.noun}'s identity and other templates intact. Tell me what remains and when setup is complete.` })
                      }}
                    />
                  ))}
                    </div>
                  ) : null}
                  {tabId ? (
                      <div className="min-h-0 flex-1">
                        <ChatArea
                          tabId={tabId}
                          compact
                          landingContent={workspaceView === 'dashboard' && panelOpen ? <></> : <WorkNewChatGuide product={product} cowork={selectedMode === 'cowork'} sharedBy={isWorkSessionRun(selected) && selected.shared ? (selected.shared.ownerUsername || selected.shared.ownerId) : undefined} />}
                          composerPlaceholder={'Ask this Crew to help with your work…'}
                          showProductSteerAction
                          showProductTerminalControl
                          // Code only, and only its owner: New chat replaces this project's conversation in the same
                          // tab (the server rotates the project's session), never a second, parallel chat.
                          showNewChatAction={!isWorkSessionRun(selected) && tabId === canonicalTabId}
                        />
                      </div>
                  ) : (
                    <div className="grid h-full place-items-center p-6 text-center text-sm text-muted-foreground">
                      <span><Loader2 className="mr-2 inline h-4 w-4 animate-spin" />Opening project…</span>
                    </div>
                  )}
              </>}
              divider={
                  <WorkspaceSplitRail
                    ratio={splitRatio}
                    onPointerDown={handleSplitPointerDown}
                    onStep={delta => {
                      if (workspaceView !== 'dashboard' || reportPreviewPreference !== 'mobile') { setSplitRatio(splitRatioRef.current + delta, true); return }
                      const width = Math.max(MOBILE_PANE_MIN, (mobilePaneWidthRef.current ?? 494) - Math.round(delta * 600))
                      mobilePaneWidthRef.current = width; setMobilePaneWidthState(width); writeMobilePaneWidth(selected?.id, width)
                    }}
                    className="max-md:hidden md:row-start-2"
                    previewDevice={reportPreviewPreference}
                    onPreviewDeviceChange={workspaceView === 'dashboard' ? device => writeReportPreviewPreference(selected.workspacePath, device) : undefined}
                    onCollapseChat={() => setChatOpen(false)}
                    onCollapseWorkspace={() => setPanelOpen(false)}
                  />
              }
              workspace={<>
                  {tabId || isWorkSessionRun(selected) ? (
                    <><span hidden data-ui-view-mounted /><WorkWorkspacePane
                        key={`${selected.id}:${workspaceViewRefresh}`}
                        workspacePath={selected.workspacePath}
                        projectId={selected.id}
                        projectTitle={selected.title}
                        projectDescription={selected.description}
                        projectIdentity={selected.identity}
                        projectTemplates={selected.templates}
                        onInstallTemplate={installSelectedTemplate}
                        tabId={tabId ?? ''}
                        onOpenChat={() => setChatOpen(true)}
                        view={workspaceView}
                        onViewChange={selectWorkspaceView}
                        enabledPanels={workspacePanels}
                        shared={sharedWorkspaceOwner}
                        showShell={showShell}
                        projectLLMConfig={selected.llmConfig}
                        selectedSecrets={selected.selectedSecrets}
                        selectedGlobalSecrets={selected.selectedGlobalSecrets}
                        workflowContextPaths={selected.workflowContextPaths}
                        onRuntimeChange={changeWorkRuntime}
                        nativeAgentTools={!!selected.nativeAgentTools}
                        onNativeAgentToolsChange={changeNativeAgentTools}
                        onSelectedServersChange={changeSelectedServers}
                        onSelectedSkillsChange={changeSelectedSkills}
                        onSelectedSecretsChange={changeSelectedSecrets}
                        onSelectedGlobalSecretsChange={changeSelectedGlobalSecrets}
                        onWorkflowContextPathsChange={changeWorkflowContextPaths}
                        onUpdateIdentity={changeProjectIdentity}
                        onDeleteRequest={requestDeleteProject}
                      /></>
                  ) : (
                    <div className="grid h-full place-items-center text-sm text-muted-foreground">Opening workspace…</div>
                  )}
              </>}
            />
          )}
        </div>
      </div>
    </TerminalFocusLayout>
    </ProjectProductProvider>
  )
}

export function WorkSurface({ product = CREW_PRODUCT }: { product?: ProjectProductConfig } = {}) {
  const userId = useAuthStore(state => state.user?.id ?? 'local')
  const activeWorkspaceId = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  return <WorkSurfaceContent key={`${product.profileId}:${activeWorkspaceId}:${userId}`} product={product} />
}

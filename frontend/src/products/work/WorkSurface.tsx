import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent } from 'react'
import { Loader2, PanelLeftOpen, PanelRightOpen, Plus, Sparkles, Trash2 } from 'lucide-react'
import { useShallow } from 'zustand/react/shallow'
import ChatArea from '../../components/ChatArea'
import { GlobalHumanFeedbackPrompt } from '../../components/GlobalHumanFeedbackPrompt'
import { ModePresetBar } from '../../components/ModePresetBar'
import LlmModalHost from '../../components/topbar/LlmModalHost'
import { TopBarEntitySelector } from '../../components/topbar/TopBarEntitySelector'
import { UpdateProgressToast } from '../../components/UpdateProgressToast'
import { agentApi } from '../../services/api'
import { useAppStore } from '../../stores/useAppStore'
import { useChatStore, waitForChatStoreHydration, type ChatTab } from '../../stores/useChatStore'
import { useModeStore } from '../../stores/useModeStore'
import { useLLMStore } from '../../stores/useLLMStore'
import { hydrateTabEvents } from '../../utils/sessionRestore'
import { activateTab } from '../../utils/activateTab'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'
import { WORK_PROFILE_ID, WORK_PROFILE_VERSION, loadWorkProductCommands } from './workData'
import { isWorkIdentityComplete } from './workIdentity'
import { setProductCommands } from '../../commands/registry'
import { toProductCommandDefinitions } from './productCommands'
import { createWorkSession, deleteWorkSession, installWorkSessionTemplate, loadWorkSessionsIncludingShared, updateWorkSessionIdentity, workLLMConfigFromSelection, workLLMSelectionFromConfig, type WorkSession } from './workSessions'
import { WorkWorkspacePane, WorkWorkspaceToolbar, type WorkWorkspaceView } from './WorkWorkspacePane'
import { loadWorkspaceLandingView } from '../../components/workflow/workspaceLandingView'
import { isWorkWorkspaceViewEnabled } from './workViewGating'
import { usePointerDrag } from '../../hooks/usePointerDrag'
import { WorkspaceSplitRail } from '../../components/workspace/WorkspaceSplitDivider'
import { resolveWorkSurfaceLayout } from './workSurfaceLayoutResolver'
import { WorkspaceTopToolbar } from '../../components/workspace/WorkspaceTopToolbar'
import { loadAgentProfileInteractionKinds, loadAgentProfileUIPanels } from '../../utils/agentProfileCapabilities'
import { parseProductInteraction } from '../../../shared/session/interactions'
import { belongsToWorkProject, findCanonicalWorkProjectTab, markWorkProjectRuntimeDirty, setWorkProjectRuntimeSelection, type ProductEngineSelectionDetail, type WorkRuntimeSelection } from './workTabs'
import { updateProductProjectLLMConfig, updateProductProjectNativeAgentTools, updateProductProjectSelections, type ProductIdentityPatch } from '../../platform/chat/productProjects'
import { CreateWorkProjectDialog } from './CreateWorkProjectDialog'
import { crewTemplates, type CrewTemplateId } from './crewTemplates'
import { WorkTemplateSetup } from './WorkTemplateSetup'
import { useWorkspaceUIControl, type WorkspaceUIControlAdapter } from '../../platform/ui-control/useWorkspaceUIControl'
import { usePresentationEvents } from '../../platform/presentations/usePresentationEvents'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
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
  report: 'dashboard', plan: 'plan', memory: 'memory', database: 'database', browser: 'browser', costs: 'costs', workshop: 'schedules', schedules: 'schedules', files: 'files',
  identity: 'identity', mcp: 'mcp',
  // Legacy agent + preference ids land on the consolidated Setup views.
  skills: 'mcp', secrets: 'identity', llm: 'identity', bots: 'mcp', email: 'mcp', folders: 'identity',
} as const satisfies Record<string, WorkWorkspaceView>
type WorkUIPresentationView = keyof typeof WORK_UI_PRESENTATION_VIEWS
const WORK_UI_LABELS: Record<WorkUIPresentationView, string> = {
  report: 'Dashboard', plan: 'Plan', memory: 'Memory', database: 'Database', browser: 'Browser', costs: 'Costs and usage', workshop: 'Automation', schedules: 'Automation', files: 'Files',
  identity: 'Identity', mcp: 'Integrations',
  skills: 'Skills', secrets: 'Secrets', llm: 'Agent configuration', bots: 'Bots', email: 'Gmail', folders: 'Attached folders',
}

function workPresentationView(view: WorkWorkspaceView): WorkUIPresentationView {
  return (Object.entries(WORK_UI_PRESENTATION_VIEWS).find(([, panel]) => panel === view)?.[0] ?? 'report') as WorkUIPresentationView
}

const WORKSPACE_VIEW_IDS = new Set<WorkWorkspaceView>(Object.values(WORK_UI_PRESENTATION_VIEWS))

// Crew Run mode: someone else's Crew opens read-only. Memory and files are
// the inspect surface (both served through the mediated shared endpoints);
// identity, integrations, automation, database, browser, costs, and the
// dashboard stay owner-only because they edit state or read owner-private
// data (transcripts, run databases, usage) the proxy will not serve
// cross-user.
const SHARED_CREW_WORKSPACE_PANELS: Set<string> = new Set(['memory', 'files'])

function readWorkWorkspaceView(projectId?: string): WorkWorkspaceView | null {
  if (typeof window === 'undefined' || !projectId) return null
  try {
    const saved = window.localStorage.getItem(`${WORK_VIEW_PREFERENCE_KEY}:${projectId}`)
    if (saved === 'history') return 'schedules'
    if (saved && saved in WORK_UI_PRESENTATION_VIEWS) return WORK_UI_PRESENTATION_VIEWS[saved as WorkUIPresentationView]
    return saved && WORKSPACE_VIEW_IDS.has(saved as WorkWorkspaceView) ? saved as WorkWorkspaceView : null
  } catch {
    return null
  }
}

function writeWorkWorkspaceView(projectId: string | undefined, view: WorkWorkspaceView) {
  if (typeof window === 'undefined' || !projectId) return
  try { window.localStorage.setItem(`${WORK_VIEW_PREFERENCE_KEY}:${projectId}`, view) } catch { /* UI preference only. */ }
}

function clampWorkSplitRatio(ratio: number, width: number): number {
  const minPaneWidth = 240
  const minRatio = Math.max(0.15, Math.min(0.5, minPaneWidth / Math.max(width, minPaneWidth * 2)))
  return Math.max(minRatio, Math.min(Math.min(0.85, 1 - minRatio), ratio))
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

function useWorkSessions() {
  const [sessions, setSessions] = useState<WorkSession[]>([])
  const selectedId = useProductSurfaceStore(state => state.selectedWorkProjectId)
  const setSelectedId = useProductSurfaceStore(state => state.setSelectedWorkProjectId)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    const listed = await loadWorkSessionsIncludingShared()
    setSessions(listed)
    const current = useProductSurfaceStore.getState().selectedWorkProjectId
    setSelectedId(current && listed.some(item => item.id === current) ? current : listed[0]?.id ?? null)
    return listed
  }, [setSelectedId])

  useEffect(() => {
    let cancelled = false
    void loadWorkSessionsIncludingShared()
      .then((listed) => {
        if (cancelled) return
        setSessions(listed)
        const current = useProductSurfaceStore.getState().selectedWorkProjectId
        setSelectedId(current && listed.some(item => item.id === current) ? current : listed[0]?.id ?? null)
      })
      .catch((cause) => {
        if (!cancelled) setError(cause instanceof Error ? cause.message : 'Could not load projects.')
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => { cancelled = true }
  }, [setSelectedId])

  const create = useCallback(async (title: string, description: string, icon?: string, templateId?: CrewTemplateId) => {
    const session = await createWorkSession(title, description, icon, templateId)
    setSessions((current) => [session, ...current])
    setSelectedId(session.id)
    return session
  }, [setSelectedId])

  const installTemplate = useCallback(async (projectId: string, templateId: CrewTemplateId) => {
    const session = sessions.find(item => item.id === projectId)
    if (!session) throw new Error('This Crew project is no longer available.')
    const updated = await installWorkSessionTemplate(session, templateId)
    setSessions(current => current.map(item => item.id === projectId ? updated : item))
    for (const tab of Object.values(useChatStore.getState().chatTabs)) {
      if (!belongsToWorkProject(tab, projectId)) continue
      useChatStore.getState().setTabConfig(tab.tabId, { selectedSkills: updated.selectedSkills })
      useChatStore.getState().setTabMetadata(tab.tabId, { agentProfileRuntimeDirty: true })
    }
    markWorkProjectRuntimeDirty(projectId)
    return updated
  }, [sessions])

  const remove = useCallback(async (projectId: string) => {
    const project = sessions.find(item => item.id === projectId)
    if (!project) throw new Error('This Crew project is no longer available.')
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
    const projectTabs = Object.values(chatStore.chatTabs).filter(tab => belongsToWorkProject(tab, projectId))
    for (const tab of projectTabs) await useChatStore.getState().closeTab(tab.tabId, false)

    try {
      window.localStorage.removeItem(`${WORK_VIEW_PREFERENCE_KEY}:${projectId}`)
      window.localStorage.removeItem(`${WORK_SPLIT_PREFERENCE_KEY}:${projectId}`)
    } catch { /* UI preferences only. */ }

    const remaining = sessions.filter(item => item.id !== projectId)
    setSessions(remaining)
    if (useProductSurfaceStore.getState().selectedWorkProjectId === projectId) {
      setSelectedId(remaining[0]?.id ?? null)
    }
  }, [sessions, setSelectedId])

  const updateLLMConfig = useCallback(async (projectId: string, selection: WorkRuntimeSelection) => {
    const project = sessions.find(item => item.id === projectId)
    if (!project) throw new Error('This Crew project is no longer available.')
    if (project.shared) throw new Error('Only the Crew owner can change this.')
    const llmConfig = workLLMConfigFromSelection({
      provider: selection.provider || selection.engine,
      modelId: selection.modelId,
      reasoningEffort: selection.reasoningEffort,
    })
    const updated = await updateProductProjectLLMConfig(project, llmConfig, `Update Crew project model ${project.title}`, 'workflow.json')
    setSessions(current => current.map(item => item.id === projectId ? updated : item))
    return updated
  }, [sessions])

  const updateNativeAgentTools = useCallback(async (projectId: string, enabled: boolean) => {
    const project = sessions.find(item => item.id === projectId)
    if (!project) throw new Error('This Crew project is no longer available.')
    if (project.shared) throw new Error('Only the Crew owner can change this.')
    const updated = await updateProductProjectNativeAgentTools(project, enabled, `${enabled ? 'Enable' : 'Disable'} native agent tools for Crew project ${project.title}`, 'workflow.json')
    setSessions(current => current.map(item => item.id === projectId ? updated : item))
    return updated
  }, [sessions])

  const updateSelections = useCallback(async (projectId: string, patch: { selectedServers?: string[]; selectedSkills?: string[]; selectedSecrets?: string[]; selectedGlobalSecrets?: string[]; workflowContextPaths?: string[] }) => {
    const project = sessions.find(item => item.id === projectId)
    if (!project) throw new Error('This Crew project is no longer available.')
    if (project.shared) throw new Error('Only the Crew owner can change this.')
    const updated = await updateProductProjectSelections(project, patch, `Update Crew project integrations ${project.title}`, 'workflow.json')
    setSessions(current => current.map(item => item.id === projectId ? updated : item))
    return updated
  }, [sessions])

  const updateIdentity = useCallback(async (projectId: string, patch: ProductIdentityPatch) => {
    const project = sessions.find(item => item.id === projectId)
    if (!project) throw new Error('This Crew project is no longer available.')
    const updated = await updateWorkSessionIdentity(project, patch)
    setSessions(current => current.map(item => item.id === projectId ? updated : item))
    return updated
  }, [sessions])

  return {
    sessions,
    selected: sessions.find((session) => session.id === selectedId) ?? null,
    select: setSelectedId,
    create,
    installTemplate,
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
    tab.metadata?.agentProfileConversationKey === sessionId)
  const active = state.activeTabId ? state.chatTabs[state.activeTabId] : undefined
  return {
    canonicalTabId: canonical?.tabId,
    activeProjectTabId: active && belongsToWorkProject(active, sessionId) ? active.tabId : undefined,
  }
}

function useWorkChatTab(
  session: WorkSession | null,
  onLegacyRuntimeDiscovered: (selection: WorkRuntimeSelection) => void | Promise<void>,
) {
  const [failure, setFailure] = useState<{ projectId: string; message: string } | null>(null)
  // Subscribe to the two tab ids this hook needs, never to chatTabs itself:
  // the chat input saves its draft into its tab's config, and a whole-map
  // subscription re-rendered the Work surface (and the workspace pane beside
  // the chat) on every keystroke.
  const sessionId = session?.id
  const { canonicalTabId, activeProjectTabId } = useChatStore(useShallow(state => selectWorkChatTabIds(state, sessionId)))
  const sessionRef = useRef(session)
  const legacyRuntimeHandlerRef = useRef(onLegacyRuntimeDiscovered)
  sessionRef.current = session
  legacyRuntimeHandlerRef.current = onLegacyRuntimeDiscovered
  const projectId = session?.id

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
        const conversation = await agentApi.resolveAgentProfileConversation(WORK_PROFILE_ID, {
          conversation_key: target.id,
        })
        if (cancelled) return

        const projectMetadata = {
          mode: 'multi-agent',
          agentProfileId: WORK_PROFILE_ID,
          agentProfileVersion: WORK_PROFILE_VERSION,
          agentProfileWorkspace: target.workspacePath,
          agentProfileProjectId: target.id,
          agentProfileProjectTitle: target.title,
          agentProfileProjectIcon: target.identity?.icon,
          agentProfileIdentityName: target.identity?.name,
          agentProfileChatContract: 'profile-v1',
          agentProfileBuilder: false,
          agentProfileConversationKey: conversation.conversation_key,
          agentProfileConversationId: conversation.conversation_id,
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

        const canonicalTabId = await chatStore.createChatTab('Chat', projectMetadata, conversation.session_id)
        if (cancelled) return
        chatStore.renameTab(canonicalTabId, 'Chat')
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
        if (!cancelled) setFailure({ projectId: target.id, message: cause instanceof Error ? cause.message : 'Could not open Crew.' })
      }
    }
    void prepare()
    return () => { cancelled = true }
  }, [projectId])

  // Manifest changes (identity, model, MCPs, or skills) update the existing
  // local tab in place. They must not restart the expensive conversation
  // resolution and history hydration performed above.
  useEffect(() => {
    if (!session) return
    const tab = Object.values(useChatStore.getState().chatTabs).find(candidate =>
      belongsToWorkProject(candidate, session.id) &&
      candidate.metadata?.agentProfileBuilder !== true &&
      candidate.metadata?.agentProfileConversationKey === session.id)
    if (!tab) return
    const savedRuntime = workLLMSelectionFromConfig(session.llmConfig)
    const metadata = {
      agentProfileWorkspace: session.workspacePath,
      agentProfileProjectTitle: session.title,
      agentProfileProjectIcon: session.identity?.icon,
      agentProfileIdentityName: session.identity?.name,
      ...(savedRuntime ? {
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
  }, [session])

  useLayoutEffect(() => {
    if (canonicalTabId && !activeProjectTabId) activateTab(canonicalTabId)
  }, [activeProjectTabId, canonicalTabId])
  return {
    // A previously prepared Crew tab is safe to display immediately while its
    // durable binding is revalidated in the background.
    tabId: activeProjectTabId ?? canonicalTabId ?? null,
    canonicalTabId: canonicalTabId ?? null,
    error: failure && failure.projectId === session?.id ? failure.message : null,
  }
}

function WorkChatTabs({ projectId, canonicalTabId }: { projectId: string; canonicalTabId: string }) {
  const { chatTabs, activeTabId, closeTab } = useChatStore(useShallow(state => ({
    chatTabs: state.chatTabs,
    activeTabId: state.activeTabId,
    closeTab: state.closeTab,
  })))
  const canonicalSessionId = chatTabs[canonicalTabId]?.sessionId
  const tabs = Object.values(chatTabs)
    .filter(tab => belongsToWorkProject(tab, projectId) && (
      tab.tabId === canonicalTabId ||
      (tab.metadata?.isViewOnly === true && tab.sessionId !== canonicalSessionId)
    ))
    .sort((a, b) => a.tabId === canonicalTabId ? -1 : b.tabId === canonicalTabId ? 1 : a.createdAt - b.createdAt)
  const selectTab = (nextTabId: string) => { activateTab(nextTabId) }
  const closeHistoryTab = (closingTabId: string) => {
    void closeTab(closingTabId, false).then(() => {
      if (activeTabId === closingTabId) activateTab(canonicalTabId)
    })
  }
  return (
    <div className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto px-1">
      {tabs.map(tab => <AgentWorksChatTabItem
        key={tab.tabId}
        tab={tab}
        isActive={tab.tabId === activeTabId}
        canClose={tab.tabId !== canonicalTabId}
        isBlank={false}
        displayName={tab.tabId === canonicalTabId ? 'Chat' : tab.name}
        onTabClick={selectTab}
        onCloseTab={closeHistoryTab}
      />)}
    </div>
  )
}

function WorkNewChatGuide({ sharedBy }: { sharedBy?: string }) {
  if (sharedBy) {
    return (
      <div className="flex h-full min-h-0 items-center justify-center overflow-y-auto px-6 py-10">
        <div className="w-full max-w-lg rounded-xl border border-border bg-muted/20 p-5">
          <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
            <Sparkles className="h-4 w-4 text-primary" />
            Explore {sharedBy}’s Crew
          </div>
          <p className="mt-2 text-sm leading-6 text-muted-foreground">
            This Crew is read-only for you. Ask it to:
          </p>
          <ul className="mt-3 space-y-2 text-sm text-muted-foreground">
            <li>• Explain how the Crew works and what it can do</li>
            <li>• Walk through its files, memory, and configuration</li>
            <li>• Run its attached workflows when you ask</li>
          </ul>
          <p className="mt-3 text-sm leading-6 text-muted-foreground">
            Your conversation stays private to you — the owner never sees it.
          </p>
        </div>
      </div>
    )
  }
  return (
    <div className="flex h-full min-h-0 items-center justify-center overflow-y-auto px-6 py-10">
      <div className="w-full max-w-lg rounded-xl border border-border bg-muted/20 p-5">
        <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
          <Sparkles className="h-4 w-4 text-primary" />
          Start your Crew chat
        </div>
        <p className="mt-2 text-sm leading-6 text-muted-foreground">
          This is the persistent conversation for this Crew project. Ask Crew to:
        </p>
        <ul className="mt-3 space-y-2 text-sm text-muted-foreground">
          <li>• Research, write, analyze, or plan ongoing work</li>
          <li>• Work with project files, code, browser, terminal, and connected tools</li>
          <li>• Create dashboards, schedules, webhooks, bots, or project memory</li>
        </ul>
      </div>
    </div>
  )
}

function WorkTopBarControl({
  sessions,
  selected,
  onSelect,
  onNewProject,
  onDelete,
  creating,
  deletingProjectId,
}: {
  sessions: WorkSession[]
  selected: WorkSession | null
  onSelect: (id: string) => void
  onNewProject: () => void
  onDelete: (session: WorkSession) => void
  creating: boolean
  deletingProjectId: string | null
}) {
  const [open, setOpen] = useState(false)

  return (
    <TopBarEntitySelector
      dataTour="crew-selector"
      label={selected?.identity?.name || selected?.title}
      leading={selected ? <EntityIdentityIcon icon={selected.identity?.icon} label={selected.identity?.name || selected.title} /> : undefined}
      compactOnNarrow
      title={selected ? `${selected.identity?.name || selected.title}${selected.identity?.name && selected.identity.name !== selected.title ? ` · ${selected.title}` : ''}${selected.shared ? ` · shared by ${selected.shared.ownerUsername || selected.shared.ownerId} (read-only)` : ''}` : 'New Crew'}
      placeholder="New Crew member"
      open={open}
      onToggle={() => setOpen(current => !current)}
      onClose={() => setOpen(false)}
      onAdd={onNewProject}
      addLabel="New Crew member"
      addDisabled={creating}
    >
      <div role="menu" aria-label="Projects" className="max-h-96 space-y-1 overflow-y-auto p-2">
        <button
          type="button"
          onClick={() => { setOpen(false); onNewProject() }}
          disabled={creating}
          className="w-full rounded-md p-2 text-left text-sm text-gray-700 hover:bg-gray-100 disabled:opacity-50 dark:text-gray-300 dark:hover:bg-slate-700"
        >
          <span className="flex items-center gap-2 font-medium">
            <span className="h-2 w-2 rounded-full bg-blue-500" />
            {creating ? 'Creating Crew member…' : '+ New Crew member'}
          </span>
        </button>
        {sessions.length === 0 ? (
          <div className="p-2 text-center text-sm text-gray-500 dark:text-gray-400">No projects yet. Create one to get started.</div>
        ) : (<>
          {sessions.filter(session => !session.shared).map(session => (
            <div
              key={session.id}
              className={`flex items-center rounded-md text-sm transition-colors ${session.id === selected?.id ? 'bg-blue-100 text-blue-900 dark:bg-blue-900/30 dark:text-blue-100' : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-slate-700'}`}
            >
              <button
                type="button"
                role="menuitemradio"
                aria-checked={session.id === selected?.id}
                onClick={() => { onSelect(session.id); setOpen(false) }}
                className="min-w-0 flex-1 p-2 text-left"
              >
                <span className="flex items-center gap-2">
                  <EntityIdentityIcon icon={session.identity?.icon} label={session.identity?.name || session.title} />
                  <span className="min-w-0">
                    <span className="block truncate font-medium">{session.identity?.name || session.title}</span>
                    {session.identity?.name && session.identity.name !== session.title
                      ? <span className="block truncate text-xs text-muted-foreground">{session.title}</span>
                      : null}
                  </span>
                </span>
              </button>
              <button
                type="button"
                aria-label={`Delete Crew ${session.identity?.name || session.title}`}
                title="Delete Crew"
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
              Shared by others · read-only
            </div>
          )}
          {sessions.filter(session => session.shared).map(session => (
            <div
              key={`shared:${session.id}`}
              className={`flex items-center rounded-md text-sm transition-colors ${session.id === selected?.id ? 'bg-blue-100 text-blue-900 dark:bg-blue-900/30 dark:text-blue-100' : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-slate-700'}`}
            >
              <button
                type="button"
                role="menuitemradio"
                aria-checked={session.id === selected?.id}
                onClick={() => { onSelect(session.id); setOpen(false) }}
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

export function WorkSurface() {
  const { sessions, selected, select, create, installTemplate, remove, updateLLMConfig, updateNativeAgentTools, updateSelections, updateIdentity, refresh, loading: sessionsLoading, error: sessionsError } = useWorkSessions()
  const selectedTemplates = crewTemplates.filter(template => selected?.templates.some(installed => installed.id === template.id && installed.version === template.version))
  const workflowContextSignature = selected?.workflowContextPaths.join('\u0000') || ''
  const persistLegacyRuntime = useCallback(async (selection: WorkRuntimeSelection) => {
    if (!selected || selected.shared) return
    await updateLLMConfig(selected.id, selection)
  }, [selected, updateLLMConfig])
  const { tabId, canonicalTabId, error: chatError } = useWorkChatTab(selected, persistLegacyRuntime)

  // A browser reload loses transient tab metadata while the durable references
  // remain in workflow.json. Force the first follow-up through the full profile
  // route so an old retained CLI cannot bypass the current read-only grants.
  useEffect(() => {
    if (selected?.id && workflowContextSignature) markWorkProjectRuntimeDirty(selected.id)
  }, [selected?.id, workflowContextSignature])
  const [projectRefreshInteractionKinds, setProjectRefreshInteractionKinds] = useState<Set<string>>(() => new Set())
  useEffect(() => {
    let cancelled = false
    void loadAgentProfileInteractionKinds(WORK_PROFILE_ID, 'product.refresh', WORK_PROFILE_VERSION).then(kinds => {
      if (cancelled) return
      setProjectRefreshInteractionKinds(kinds)
    })
    return () => { cancelled = true }
  }, [])
  // Product slash commands come from the same profile the provider options do,
  // and are cleared on unmount so leaving Crew does not leave its commands
  // offered in another product's chat.
  useEffect(() => {
    let cancelled = false
    void loadWorkProductCommands()
      .then((commands) => { if (!cancelled) setProductCommands(toProductCommandDefinitions(commands)) })
      .catch(() => { if (!cancelled) setProductCommands([]) })
    return () => { cancelled = true; setProductCommands([]) }
  }, [])
  const projectConfigRefreshToken = useChatStore(state => {
    const sessionId = tabId ? state.chatTabs[tabId]?.sessionId : undefined
    return (sessionId ? state.tabEvents[sessionId] || [] : [])
    .filter(event => {
      const interaction = parseProductInteraction(event)
      return (interaction?.product === 'work' && projectRefreshInteractionKinds.has(interaction.kind)) ||
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
  const [deleteCandidate, setDeleteCandidate] = useState<WorkSession | null>(null)
  const [deletingProjectId, setDeletingProjectId] = useState<string | null>(null)
  const [chatOpen, setChatOpen] = useState(true)
  const [panelOpen, setPanelOpen] = useState(true)
  const [workspaceView, setWorkspaceView] = useState<WorkWorkspaceView>(() => readWorkWorkspaceView(selected?.id) ?? 'identity')
  const pendingWorkView = useProductSurfaceStore(state => state.pendingWorkView)
  const setPendingWorkView = useProductSurfaceStore(state => state.setPendingWorkView)
  const [workspaceViewRefresh, setWorkspaceViewRefresh] = useState(0)
  const [enabledWorkspacePanels, setEnabledWorkspacePanels] = useState<Set<string> | undefined>()
  const splitLayoutRef = useRef<HTMLDivElement>(null)
  const [splitRatio, setSplitRatioState] = useState(() => readWorkSplitRatio(selected?.id))
  const splitRatioRef = useRef(splitRatio)
  const [reportPreviewPreference, setReportPreviewPreference] = useState<ReportPreviewDevice>(() => readReportPreviewPreference(selected?.workspacePath))
  // All split classes derive from the shared layout resolver: one decision
  // point for every flag combination (see workSurfaceLayoutResolver.ts).
  const layout = resolveWorkSurfaceLayout({ chatOpen, panelOpen, splitRatio, mobilePreview: reportPreviewPreference === 'mobile' })
  const { start: startSplitDrag, stop: stopSplitDrag } = usePointerDrag()
  const [createError, setCreateError] = useState<string | null>(null)
  const showProviders = useLLMStore((state) => state.showLLMModal)
  const activeSessionId = useChatStore(state => tabId ? state.chatTabs[tabId]?.sessionId : undefined)
  const legacyViewEvents = usePresentationEvents(activeSessionId ?? undefined, ['workflow.view'])
  const handledLegacyViewEvents = useRef<{ session?: string; count: number }>({ session: activeSessionId ?? undefined, count: legacyViewEvents.length })
  const selectWorkspaceView = useCallback((view: WorkWorkspaceView) => {
    setWorkspaceView(view)
    writeWorkWorkspaceView(selected?.id, view)
  }, [selected?.id])

  // Someone else's Crew offers only the read-only inspect surface, no
  // matter what the server's feature list enables for owned Crews.
  const workspacePanels = selected?.shared ? SHARED_CREW_WORKSPACE_PANELS : enabledWorkspacePanels
  const openWorkPresentationView = useCallback((view: string, target?: string) => {
    if (!(view in WORK_UI_PRESENTATION_VIEWS)) return
    const panel = WORK_UI_PRESENTATION_VIEWS[view as WorkUIPresentationView]
    if (!isWorkWorkspaceViewEnabled(panel, workspacePanels)) return
    if (selected?.shared && !SHARED_CREW_WORKSPACE_PANELS.has(panel)) return
    if (panel === 'schedules') {
      const automationTarget = view === 'bots' ? 'bots' : target === 'webhooks' ? 'triggers' : target || 'schedules'
      useWorkflowStore.getState().openWorkspaceView('workshop', automationTarget)
    }
    setPanelOpen(true)
    selectWorkspaceView(panel)
  }, [selected?.shared, selectWorkspaceView, workspacePanels])
  useEffect(() => {
    if (!pendingWorkView) return
    openWorkPresentationView(pendingWorkView)
    setPendingWorkView(null)
  }, [openWorkPresentationView, pendingWorkView, setPendingWorkView])
  const workUIAdapter = useMemo<WorkspaceUIControlAdapter>(() => ({
    getView: () => workPresentationView(workspaceView),
    openView: openWorkPresentationView,
    refreshView: () => setWorkspaceViewRefresh(value => value + 1),
    isViewSupported: (view) => view in WORK_UI_PRESENTATION_VIEWS,
    labelForView: (view) => WORK_UI_LABELS[view as WorkUIPresentationView] ?? view,
    actorLabel: 'Crew',
    getTarget: (view) => view === 'workshop' || view === 'schedules' ? useWorkflowStore.getState().workspaceViewTarget?.target : undefined,
  }), [openWorkPresentationView, workspaceView])
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
  const sharedWorkspaceOwner = useMemo(() => selected?.shared
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
    useAppStore.getState().setShowSchedulesOverview(false)
  }, [])

  useEffect(() => {
    let cancelled = false
    void loadAgentProfileUIPanels(WORK_PROFILE_ID, WORK_PROFILE_VERSION).then(panels => {
      // An unavailable/mismatched profile must not turn the entire project
      // workspace into an empty capability set. Crew has a complete local
      // panel implementation, so retain that safe UI fallback until the
      // resolved backend feature list is available.
      if (!cancelled) setEnabledWorkspacePanels(panels.size > 0 ? panels : undefined)
    })
    return () => { cancelled = true }
  }, [])

  useLayoutEffect(() => {
    const savedView = readWorkWorkspaceView(selected?.id)
    setWorkspaceView(selected?.shared ? (savedView && SHARED_CREW_WORKSPACE_PANELS.has(savedView) ? savedView : 'files') : savedView ?? 'identity')
    const nextRatio = readWorkSplitRatio(selected?.id)
    splitRatioRef.current = nextRatio
    setSplitRatioState(nextRatio)
    setReportPreviewPreference(readReportPreviewPreference(selected?.workspacePath))
  }, [selected?.id, selected?.shared, selected?.workspacePath])

  const landingProjectId = selected?.id
  const landingWorkspacePath = selected?.workspacePath
  const landingIsShared = Boolean(selected?.shared)
  const dashboardAllowed = isWorkWorkspaceViewEnabled('dashboard', enabledWorkspacePanels)
  useEffect(() => {
    if (!landingProjectId || !landingWorkspacePath || landingIsShared || readWorkWorkspaceView(landingProjectId)) return
    let cancelled = false
    void loadWorkspaceLandingView(landingWorkspacePath, { dashboardAllowed }).then(view => {
      if (cancelled || useProductSurfaceStore.getState().selectedWorkProjectId !== landingProjectId || readWorkWorkspaceView(landingProjectId)) return
      setWorkspaceView(view)
    })
    return () => { cancelled = true }
  }, [landingProjectId, landingWorkspacePath, landingIsShared, dashboardAllowed])

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
    if (selected?.shared) {
      // Identity is always "enabled", so shared Crews need their own
      // fallback: a stale saved view must land on the inspect surface.
      if (!SHARED_CREW_WORKSPACE_PANELS.has(workspaceView)) selectWorkspaceView('files')
      return
    }
    if (!isWorkWorkspaceViewEnabled(workspaceView, enabledWorkspacePanels)) {
      // A disabled saved view cannot be shown. For an unsaved project, keep
      // Identity temporary while the content-based landing check runs.
      if (readWorkWorkspaceView(selected?.id)) selectWorkspaceView('identity')
      else setWorkspaceView('identity')
    }
  }, [enabledWorkspacePanels, selectWorkspaceView, selected?.id, selected?.shared, workspaceView])

  useEffect(() => {
    if (!selected) return
    const handleEngineSelection = (event: Event) => {
      const detail = (event as CustomEvent<ProductEngineSelectionDetail>).detail
      const sourceTabId = detail?.tabId || tabId
      const sourceTab = sourceTabId ? useChatStore.getState().chatTabs[sourceTabId] : undefined
      if (detail?.profileId !== WORK_PROFILE_ID || !detail.engine || !detail.modelId || !sourceTab || !belongsToWorkProject(sourceTab, selected.id)) return
      void changeWorkRuntime({
        engine: detail.engine!,
        provider: detail.provider,
        modelId: detail.modelId!,
        reasoningEffort: detail.reasoningEffort,
      })
    }
    window.addEventListener('agentworks:product-engine-selected', handleEngineSelection)
    return () => window.removeEventListener('agentworks:product-engine-selected', handleEngineSelection)
  }, [changeWorkRuntime, selected, tabId])

  useEffect(() => stopSplitDrag, [selected?.id, stopSplitDrag])

  const setSplitRatio = useCallback((next: number, persist = false) => {
    const width = splitLayoutRef.current?.getBoundingClientRect().width || window.innerWidth
    const ratio = clampWorkSplitRatio(next, width)
    splitRatioRef.current = ratio
    setSplitRatioState(ratio)
    if (persist) writeWorkSplitRatio(selected?.id, ratio)
  }, [selected?.id])

  const handleSplitPointerDown = useCallback((event: ReactPointerEvent<HTMLButtonElement>) => {
    // Mobile preview pins the panel to phone width; nothing to drag.
    if (window.innerWidth < 768 || reportPreviewPreference === 'mobile') return
    const rect = splitLayoutRef.current?.getBoundingClientRect()
    if (!rect?.width) return
    startSplitDrag(event, {
      onMove: clientX => setSplitRatio((clientX - rect.left) / rect.width),
      onEnd: () => writeWorkSplitRatio(selected?.id, splitRatioRef.current),
    })
  }, [reportPreviewPreference, selected?.id, setSplitRatio, startSplitDrag])

  const createProject = useCallback(async (title: string, description: string, icon?: string, templateId?: CrewTemplateId) => {
    if (creating) return
    setCreating(true)
    setCreateError(null)
    try {
      const created = await create(title, description, icon, templateId)
      if (templateId) {
        setPanelOpen(true)
        setWorkspaceView('files')
        writeWorkWorkspaceView(created.id, 'files')
      }
      setCreateOpen(false)
    } catch (cause) {
      setCreateError(cause instanceof Error ? cause.message : 'Could not create Crew member.')
    } finally {
      setCreating(false)
    }
  }, [create, creating])

  const openCreateProject = useCallback(() => {
    setCreateError(null)
    setCreateOpen(true)
  }, [])

  const deleteProject = useCallback(async () => {
    if (!deleteCandidate || deletingProjectId) return
    setDeletingProjectId(deleteCandidate.id)
    try {
      await remove(deleteCandidate.id)
      setDeleteCandidate(null)
      useChatStore.getState().addToast(`Deleted Crew “${deleteCandidate.identity?.name || deleteCandidate.title}”.`, 'success')
    } catch (cause) {
      const message = cause instanceof Error ? cause.message : 'Could not delete Crew.'
      useChatStore.getState().addToast(`Failed to delete Crew: ${message}`, 'error')
    } finally {
      setDeletingProjectId(null)
    }
  }, [deleteCandidate, deletingProjectId, remove])

  const topBarControl = useMemo(() => (
    <WorkTopBarControl
      sessions={sessions}
      selected={selected}
      onSelect={select}
      onNewProject={openCreateProject}
      onDelete={setDeleteCandidate}
      creating={creating}
      deletingProjectId={deletingProjectId}
    />
  ), [creating, deletingProjectId, openCreateProject, select, selected, sessions])

  const error = sessionsError || chatError

  return (
    <div className="flex h-screen min-h-0 flex-col bg-background">
      <UpdateProgressToast />
      <GlobalHumanFeedbackPrompt />
      <ModePresetBar
        productControl={topBarControl}
        reduced
        walkthroughSurface={selected ? 'crew' : 'empty-crew'}
        walkthroughReady={!sessionsLoading && !creating && !error}
        walkthroughPaused={createOpen || deleteCandidate !== null}
      />
      {createOpen ? (
        <CreateWorkProjectDialog
          onClose={() => { if (!creating) setCreateOpen(false) }}
          onCreate={createProject}
          submitting={creating}
          error={createError}
        />
      ) : null}
      <ConfirmationDialog
        isOpen={deleteCandidate !== null}
        onClose={() => { if (!deletingProjectId) setDeleteCandidate(null) }}
        onConfirm={() => { void deleteProject() }}
        title="Delete Crew"
        message={deleteCandidate
          ? `Delete Crew “${deleteCandidate.identity?.name || deleteCandidate.title}” and permanently remove its project files, chat history, schedules, triggers, bots, dashboard, and database? This cannot be undone.`
          : ''}
        confirmText="Delete Crew"
        loadingText="Deleting Crew…"
        type="danger"
        isLoading={deletingProjectId !== null}
        requireText={deleteCandidate ? deleteCandidate.identity?.name || deleteCandidate.title : undefined}
      />
      <div
        data-ui-workspace={selected?.workspacePath}
        data-ui-view={selected ? workPresentationView(workspaceView) : undefined}
        className="relative min-h-0 flex-1 overflow-hidden"
      >
        <LlmModalHost />
        <div className={showProviders ? 'hidden' : 'h-full'}>
          {error ? (
            <div className="grid h-full place-items-center p-6 text-center text-sm text-destructive">{error}</div>
          ) : !selected ? (
            <div className="flex h-full items-center justify-center bg-gray-50 dark:bg-gray-900">
              {sessionsLoading || creating ? (
                <span className="text-sm text-muted-foreground"><Loader2 className="mr-2 inline h-4 w-4 animate-spin" />Opening Crew…</span>
              ) : (
                <div data-tour="crew-empty-state" className="flex max-w-xl flex-col items-center px-6 text-center">
                  <div className="flex h-20 w-20 items-center justify-center rounded-full bg-gray-200 dark:bg-gray-700">
                    <span className="font-mono text-3xl font-semibold text-gray-600 dark:text-gray-200">&lt;&gt;</span>
                  </div>
                  <div className="mt-5">
                    <p className="text-xs font-semibold uppercase tracking-[0.18em] text-primary">Crew</p>
                    <h2 className="mt-2 text-2xl font-semibold text-gray-900 dark:text-gray-100">Your AI workspace for any project</h2>
                    <p className="mx-auto mt-2 max-w-lg text-sm leading-6 text-muted-foreground">
                      Create a persistent crew member for everyday questions, research, coding, and ongoing work. It can use your project files, browser, terminal, MCP servers, and connected tools.
                    </p>
                    <div className="mt-5 grid grid-cols-1 gap-2 text-left text-xs text-muted-foreground sm:grid-cols-3">
                      <div className="rounded-lg border border-border bg-background/70 px-3 py-2.5">
                        <span className="block font-medium text-foreground">Chat and create</span>
                        Ask questions, research, write, analyze, and keep the context together.
                      </div>
                      <div className="rounded-lg border border-border bg-background/70 px-3 py-2.5">
                        <span className="block font-medium text-foreground">Code and operate</span>
                        Work with files, code, the browser, terminal, skills, and MCP tools.
                      </div>
                      <div className="rounded-lg border border-border bg-background/70 px-3 py-2.5">
                        <span className="block font-medium text-foreground">Run automatically</span>
                        Continue work with schedules, triggers, background tasks, and bots.
                      </div>
                    </div>
                    <button
                      data-tour="crew-create"
                      type="button"
                      onClick={openCreateProject}
                      className="mt-5 inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90"
                    >
                      <Plus className="h-3.5 w-3.5" /> Create your first Crew member
                    </button>
                  </div>
                </div>
              )}
            </div>
          ) : (
            <>
              {!panelOpen ? (
                <button
                  type="button"
                  onClick={() => setPanelOpen(true)}
                  title="Show workspace"
                  aria-label="Show workspace"
                  className="absolute right-0 top-1/2 z-30 hidden -translate-y-1/2 flex-col items-center gap-1.5 rounded-l-lg border border-r-0 border-border bg-background/95 py-3 pl-1.5 pr-1 text-muted-foreground shadow-md backdrop-blur-sm transition-colors hover:bg-muted hover:text-foreground md:flex"
                >
                  <PanelRightOpen className="h-4 w-4" />
                  <span className="[writing-mode:vertical-rl] text-[10px] font-semibold uppercase tracking-wider">Workspace</span>
                </button>
              ) : null}
              {!chatOpen ? (
                <button
                  type="button"
                  onClick={() => setChatOpen(true)}
                  title="Show chat panel"
                  aria-label="Show chat panel"
                  className="absolute left-0 top-1/2 z-30 hidden -translate-y-1/2 flex-col items-center gap-1.5 rounded-r-lg border border-l-0 border-border bg-background/95 py-3 pl-1 pr-1.5 text-muted-foreground shadow-md backdrop-blur-sm transition-colors hover:bg-muted hover:text-foreground md:flex"
                >
                  <PanelLeftOpen className="h-4 w-4" />
                  <span className="[writing-mode:vertical-rl] text-[10px] font-semibold uppercase tracking-wider">Chat</span>
                </button>
              ) : null}
              <div
                ref={splitLayoutRef}
                className={layout.gridClassName}
                style={layout.gridStyle}
              >
                <WorkspaceTopToolbar className={layout.toolbarClassName}>
                  {tabId && canonicalTabId && selected ? <WorkChatTabs projectId={selected.id} canonicalTabId={canonicalTabId} /> : <div className="min-w-0 flex-1" />}
                  {panelOpen ? <WorkWorkspaceToolbar workspacePath={selected.workspacePath} view={workspaceView} onViewChange={selectWorkspaceView} enabledPanels={workspacePanels} readOnly={Boolean(selected.shared)} /> : null}
                </WorkspaceTopToolbar>
                {layout.showChat ? <main data-tour="crew-chat" className={layout.chatClassName}>
                  {selected.shared ? (
                    <div className="flex items-center gap-3 border-b border-border bg-muted/60 px-4 py-2 text-sm">
                      <span className="min-w-0 flex-1 text-muted-foreground">
                        {selected.shared.ownerUsername || selected.shared.ownerId}’s Crew · read-only. Your chats stay private to you.
                      </span>
                    </div>
                  ) : null}
                  {!selected.shared && !isWorkIdentityComplete(selected.identity, selected.description) ? (
                    <div className="flex items-center justify-between gap-3 border-b border-border bg-muted/60 px-4 py-2 text-sm">
                      <span className="min-w-0 flex-1 text-muted-foreground">This Crew needs a role and purpose before it can help at its best.</span>
                      <button
                        type="button"
                        onClick={() => {
                          setPanelOpen(true)
                          selectWorkspaceView('identity')
                          sendWorkspacePaneMessageToChat({ profileId: 'work', conversationKey: selected.id, message: 'Help me set up this Crew: ask me what it is for and what role you should take, then save both.' }).catch(cause => {
                            useChatStore.getState().addToast(cause instanceof Error ? cause.message : 'Could not start the setup chat.', 'error')
                          })
                        }}
                        className="shrink-0 rounded-md bg-primary px-2.5 py-1 text-xs font-medium text-primary-foreground hover:bg-primary/90"
                      >
                        Set them up
                      </button>
                    </div>
                  ) : null}
                  {!selected.shared && selectedTemplates.map(template => (
                    <WorkTemplateSetup
                      key={`${selected.id}:${template.id}`}
                      template={template}
                      workspacePath={selected.workspacePath}
                      chatReady={Boolean(tabId)}
                      onStartSetup={async () => {
                        setChatOpen(true)
                        await sendWorkspacePaneMessageToChat({ profileId: 'work', conversationKey: selected.id, message: `Help me set up the ${template.name} template in this Crew. Read ${template.setupPath} and ${template.setupGuidePath} in this project's files. Work through the pending checks, ask me for missing decisions or access, and add a check ID to completed_steps only after you verify it. Preserve the checklist and earlier progress. Keep this Crew's identity and other templates intact. Tell me what remains and when setup is complete.` })
                      }}
                    />
                  ))}
                  {tabId ? (
                      <div className="min-h-0 flex-1">
                        <ChatArea
                          tabId={tabId}
                          compact
                          landingContent={<WorkNewChatGuide sharedBy={selected.shared ? (selected.shared.ownerUsername || selected.shared.ownerId) : undefined} />}
                          composerPlaceholder="Describe what you want to build… (@ files, # references)"
                          showCompactRuntimeLoading
                          showProductSteerAction
                          showProductTerminalControl
                        />
                      </div>
                  ) : (
                    <div className="grid h-full place-items-center p-6 text-center text-sm text-muted-foreground">
                      <span><Loader2 className="mr-2 inline h-4 w-4 animate-spin" />Opening project…</span>
                    </div>
                  )}
                </main> : null}
                {layout.showDivider ? (
                  <WorkspaceSplitRail
                    ratio={splitRatio}
                    onPointerDown={handleSplitPointerDown}
                    onStep={delta => { if (reportPreviewPreference !== 'mobile') setSplitRatio(splitRatioRef.current + delta, true) }}
                    className="md:row-start-2"
                    previewDevice={reportPreviewPreference}
                    onPreviewDeviceChange={device => writeReportPreviewPreference(selected.workspacePath, device)}
                    onCollapseChat={() => setChatOpen(false)}
                    onCollapseWorkspace={() => setPanelOpen(false)}
                  />
                ) : null}
                {layout.showPanel ? (
                  <aside
                    data-tour="crew-workspace"
                    data-ui-workspace={selected.workspacePath}
                    data-ui-view={workPresentationView(workspaceView)}
                    className={layout.panelClassName}
                  >
                  {tabId ? (
                    <><span hidden data-ui-view-mounted /><WorkWorkspacePane
                        key={`${selected.id}:${workspaceViewRefresh}`}
                        workspacePath={selected.workspacePath}
                        projectId={selected.id}
                        projectTitle={selected.title}
                        projectDescription={selected.description}
                        projectIdentity={selected.identity}
                        projectTemplates={selected.templates}
                        onInstallTemplate={installSelectedTemplate}
                        tabId={tabId}
                        view={workspaceView}
                        onViewChange={selectWorkspaceView}
                        enabledPanels={workspacePanels}
                        shared={sharedWorkspaceOwner}
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
                  </aside>
                ) : null}
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  )
}

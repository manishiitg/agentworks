import { agentApi } from '../../services/api'
import type { ActiveSessionInfo } from '../../services/api-types'
import { useAppStore } from '../../stores/useAppStore'
import { useChatStore } from '../../stores/useChatStore'
import { useModeStore } from '../../stores/useModeStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { hydrateExecutionConversation } from '../../utils/executionConversationRestore'
import { WORK_PROFILE_ID, WORK_PROFILE_VERSION } from './workData'
import { loadWorkSessions, workLLMSelectionFromConfig, type WorkSession } from './workSessions'

const normalizeRunWorkspace = (value?: string | null): string =>
  (value || '')
    .trim()
    .replace(/\\/g, '/')
    .replace(/^\/+|\/+$/g, '')
    .replace(/^_users\/[^/]+\//i, '')

function runWorkspaceMatches(projectPath: string, sessionPath: string): boolean {
  if (!projectPath || !sessionPath) return false
  if (projectPath === sessionPath) return true
  return projectPath.endsWith(`/${sessionPath}`) || sessionPath.endsWith(`/${projectPath}`)
}

/**
 * Resolve the Crew project that owns a product-schedule run session. The
 * session carries the project's workspace but no project id (no preset, no
 * work:project: prefix), so match the workspace against the loaded projects.
 * An already-open tab bound to the run wins when it names its project.
 */
async function resolveRunProject(session: ActiveSessionInfo): Promise<WorkSession | null> {
  const projects = await loadWorkSessions()
  const boundProjectId = Object.values(useChatStore.getState().chatTabs)
    .find(tab => tab.sessionId === session.session_id && tab.metadata?.agentProfileId === WORK_PROFILE_ID)
    ?.metadata?.agentProfileProjectId
  if (boundProjectId) {
    const byTab = projects.find(project => project.id === boundProjectId)
    if (byTab) return byTab
  }
  const presetId = session.preset_query_id?.trim()
  if (presetId) {
    const byPreset = projects.find(project => project.id === presetId)
    if (byPreset) return byPreset
  }
  const sessionWorkspace = normalizeRunWorkspace(session.workspace_path)
  if (sessionWorkspace) {
    const byWorkspace = projects.find(project =>
      runWorkspaceMatches(normalizeRunWorkspace(project.workspacePath), sessionWorkspace),
    )
    if (byWorkspace) return byWorkspace
  }
  return null
}

/**
 * Open a Crew trigger/schedule run in its own read-only run tab.
 *
 * Product-schedule runs are multi-agent sessions on an isolated conversation
 * (project:trigger:<id>:<run>); they have no workflow preset, so the
 * AgentWorks restore path cannot open them. Mirror the blessed history
 * recipe instead: select the owning project, ensure its canonical Chat tab
 * (the metadata donor), then open the run transcript beside it without ever
 * rebinding the crew's interactive conversation.
 */
export async function openWorkAutomationRunChat(
  session: ActiveSessionInfo,
  options: { title?: string } = {},
): Promise<void> {
  const project = await resolveRunProject(session)
  if (!project) {
    throw new Error('This Crew project is no longer available.')
  }

  const surfaces = useProductSurfaceStore.getState()
  surfaces.setSelectedWorkProjectId(project.id)
  surfaces.setPendingWorkView(null)
  surfaces.setProductSurface('work')
  useModeStore.getState().setModeCategory('multi-agent')
  useAppStore.getState().setAgentMode('multi-agent')

  const chatStore = useChatStore.getState()
  const savedRuntime = workLLMSelectionFromConfig(project.llmConfig)
  const conversation = await agentApi.resolveAgentProfileConversation(WORK_PROFILE_ID, {
    conversation_key: project.id,
  })
  const projectMetadata = {
    mode: 'multi-agent',
    agentProfileId: WORK_PROFILE_ID,
    agentProfileVersion: WORK_PROFILE_VERSION,
    agentProfileWorkspace: project.workspacePath,
    agentProfileProjectId: project.id,
    agentProfileProjectTitle: project.title,
    agentProfileProjectIcon: project.identity?.icon,
    agentProfileIdentityName: project.identity?.name,
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
  // createChatTab reuses the canonical tab when one already exists for this
  // project conversation, so this is safe alongside the surface's own effect.
  const canonicalTabId = await chatStore.createChatTab('Chat', projectMetadata, conversation.session_id)
  chatStore.renameTab(canonicalTabId, 'Chat')
  chatStore.setTabMetadata(canonicalTabId, { ...projectMetadata, agentProfileMCPSelectionInitialized: true })
  chatStore.setTabConfig(canonicalTabId, {
    selectedServers: project.selectedServers.length > 0 ? project.selectedServers : ['NO_SERVERS'],
    selectedSkills: project.selectedSkills,
  })
  const canonical = useChatStore.getState().chatTabs[canonicalTabId]
  if (!canonical?.metadata?.agentProfileId) {
    throw new Error('Could not open the Crew chat for this run.')
  }

  // Full title: the tab strip truncates with CSS and the hover shows it whole.
  const runTitle = (options.title || session.title || session.query || 'Trigger run').trim() || 'Trigger run'
  const existing = Object.values(useChatStore.getState().chatTabs).find(tab =>
    tab.sessionId === session.session_id &&
    tab.metadata?.agentProfileId === WORK_PROFILE_ID &&
    tab.metadata?.agentProfileProjectId === project.id &&
    tab.metadata?.isViewOnly === true,
  )
  const runTabId = existing?.tabId || await chatStore.createChatTab(runTitle, {
    ...canonical.metadata,
    agentProfileBuilder: false,
    agentProfileConversationKey: `${project.id}:history:${session.session_id}`,
    agentProfileConversationId: undefined,
    agentProfileRuntimeDirty: false,
    isViewOnly: true,
    isExecutionRun: true,
    isBotRun: Boolean(session.bot_platform),
    botPlatform: session.bot_platform,
    readOnlyRestoredAt: Date.now(),
    userInteractiveContinuation: false,
  }, session.session_id)
  if (existing) chatStore.setTabMetadata(runTabId, { isExecutionRun: true })
  chatStore.setTabCanSteer(runTabId, false)
  const runtime = await hydrateExecutionConversation(session.session_id, session.workspace_path || project.workspacePath)
  chatStore.setTabStreaming(runTabId, runtime.status === 'running')
  chatStore.setTabCompleted(runTabId, runtime.status !== 'running')
  chatStore.setTabViewMode(runTabId, 'formatted')
  chatStore.switchTab(runTabId)
  chatStore.setAutoScroll(true)
  window.dispatchEvent(new CustomEvent('chat-scroll-to-bottom'))
}

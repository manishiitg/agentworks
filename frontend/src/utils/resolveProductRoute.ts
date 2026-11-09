import type { AgentProfileSideChat } from '../services/api-types'
import { useChatStore } from '../stores/useChatStore'
import { activateTab } from './activateTab'
import api, { agentApi } from '../services/api'
import { buildWorkflowPresetsFromManifests, useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { useWorkflowManifestStore } from '../stores/useWorkflowManifestStore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import { useAppStore } from '../stores/useAppStore'
import { useModeStore } from '../stores/useModeStore'
import { useWorkflowStore } from '../stores/useWorkflowStore'
import { openProductWorkspace } from './productWorkspaceNavigation'
import { selectWorkflowPreset } from './workflowNavigation'
import type { ProductRoute } from './productRoutes'

export class ProductUnavailable extends Error {}

/** Resolve only through the caller's existing mediated inventories. No writes. */
export async function resolveProductRoute(route: ProductRoute, signal: AbortSignal): Promise<() => void> {
  let activateResource = () => {}
  if (route.id && (route.surface === 'agentworks')) {
    const response = await agentApi.listWorkflowManifests()
    const item = response.workflows.find(item => item.manifest.id === route.id)
    if (!item) throw new ProductUnavailable()
    const { normalizeWorkspaceViewId } = await import('../components/workflow/workspaceViews')
    const view = route.view ? normalizeWorkspaceViewId(route.view) : null
    if (route.view && !view) throw new ProductUnavailable()
    if (signal.aborted) return () => {}
    activateResource = () => {
      useWorkflowManifestStore.setState({ workflows: response.workflows })
      useGlobalPresetStore.setState({ workflowPresets: buildWorkflowPresetsFromManifests(response.workflows), workflowPresetsLoaded: true })
      selectWorkflowPreset(route.id!)
      useModeStore.getState().completeInitialSetup()
      if (view) useWorkflowStore.getState().openWorkspaceView(view)
    }
  } else if (route.id && (route.surface === 'work')) {
    const { cachedWorkSession, rememberWorkAgentList, normalizeWorkWorkspaceView, SHARED_CREW_WORKSPACE_PANELS } = await import('../products/work/WorkSurface')
    // Captured here: `route` is reassigned below, which would drop the narrowing of surface and id.
    const workSurface = route.surface
    const projectId = route.id
    const project = cachedWorkSession(route.id)
    const key = route.agent === 'main' ? route.id : route.agent ? `${route.id}:chat:${route.agent}` : undefined
    const cached = project && (!project.shared || project.shared.canBuild) && key ? Object.values(useChatStore.getState().chatTabs).find(tab =>
      tab.metadata?.agentProfileId === 'work' && tab.metadata.agentProfileProjectId === route.id &&
      (tab.metadata.agentProfileConversationKey === key || route.agent === 'main' && tab.metadata.agentProfileIsMainChat) &&
      !tab.metadata.isViewOnly && !tab.metadata.isScheduledRun && !tab.metadata.isBotRun && tab.metadata.agentProfileTabCanWrite !== false && tab.sessionId) : undefined
    let reused = false
    if (cached) {
      // Fresh project/tab admission, not a browser-side permission cache. Avoid
      // reloading every project manifest just to show an already-open own chat.
      const [models, chats] = await Promise.all([
        api.get<{ can_open_models: boolean }>('/api/agent-profiles/work/chat-models', { params: { project_id: route.id }, signal, skipSessionContext: true }),
        api.get<AgentProfileSideChat[]>('/api/agent-profiles/work/side-chats', { params: { project_id: route.id, with_main: 1 }, signal, skipSessionContext: true }),
      ])
      if (signal.aborted) return () => {}
      const confirmed = Array.isArray(chats.data) && chats.data.some(chat => chat.can_write && chat.session_id === cached.sessionId && chat.conversation_key === cached.metadata?.agentProfileConversationKey)
      if (models.data.can_open_models && confirmed) {
        rememberWorkAgentList(route.id, chats.data)
        const view = route.view ? normalizeWorkWorkspaceView(route.view) : null
        if (route.view && !view) route = { ...route, view: undefined }
        activateResource = () => {
          useProductSurfaceStore.getState().setSelectedWorkProjectId(route.id!)
          activateTab(cached.tabId)
        }
        reused = true
      }
    }
    if (!reused) {
      const [{ loadProductProjects }, { projectProductConfig }, { loadSharedWorkSessions }] = await Promise.all([
        import('../platform/chat/productProjects'), import('../products/work/projectProduct'), import('../products/work/workSessions'),
      ])
      const product = projectProductConfig(workSurface)
      const [owned, shared] = await Promise.all([
        loadProductProjects(product.projectsRoot, product.profileId, { includeOwnSharedProjects: product.listsSharedProjects }),
        loadSharedWorkSessions(product),
      ])
      const item = [...owned, ...shared].find(item => item.id === projectId)
      if (!item) {
        // The server gives a plain, non-identifying reason. Missing inventory is
        // not a browser-side decision about membership or why it was removed.
        try {
          await api.get(`/api/crew-teams/${encodeURIComponent(projectId)}`, { signal, skipSessionContext: true })
        } catch (error) {
          const detail = (error as { response?: { data?: { error?: { code?: string; message?: string } } } })?.response?.data?.error
          if (detail?.code === 'team_forbidden' && detail.message) throw new ProductUnavailable(detail.message)
          throw error
        }
        throw new ProductUnavailable()
      }
      // An unknown or not-allowed view name falls back to the product default
      // instead of making the whole project link "not available".
      const view = route.view ? normalizeWorkWorkspaceView(route.view) : null
      if (route.view && (!view || (item.shared && !SHARED_CREW_WORKSPACE_PANELS.has(view)))) route = { ...route, view: undefined }
      activateResource = () => {
        const state = useProductSurfaceStore.getState()
        state.setSelectedWorkProjectId(route.id!)
      }
    }
  } else if (route.folder) {
    const { knowledgebaseApi } = await import('../services/knowledgebaseApi')
    const access = await knowledgebaseApi.access(route.folder, signal)
    if (!access.effective_role) throw new ProductUnavailable()
  }
  return () => {
    if (signal.aborted) return
    openProductWorkspace(route.surface)
    if (!route.id) {
      const state = useProductSurfaceStore.getState()
      if (route.surface === 'work') state.setSelectedWorkProjectId(null)
      if (route.surface === 'agentworks') {
        useGlobalPresetStore.getState().clearActivePreset('workflow')
        useAppStore.getState().setShowWorkflowsOverview(true)
        useModeStore.getState().completeInitialSetup()
      }
    }
    activateResource()
  }
}

import { agentApi } from '../services/api'
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
  if (route.id && (route.surface === 'agentworks' || route.surface === 'relays')) {
    const response = await agentApi.listWorkflowManifests()
    const item = response.workflows.find(item => item.manifest.id === route.id)
    if (!item || (item.manifest.kind === 'relay') !== (route.surface === 'relays')) throw new ProductUnavailable()
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
  } else if (route.id && (route.surface === 'work' || route.surface === 'code')) {
    const [{ loadProductProjects }, { projectProductConfig }, { loadSharedWorkSessions }] = await Promise.all([
      import('../platform/chat/productProjects'), import('../products/work/projectProduct'), import('../products/work/workSessions'),
    ])
    const product = projectProductConfig(route.surface)
    const [owned, shared] = await Promise.all([
      loadProductProjects(product.projectsRoot, product.profileId, { includeOwnSharedProjects: product.listsSharedProjects }),
      loadSharedWorkSessions(product),
    ])
    const item = [...owned, ...shared].find(item => item.id === route.id)
    if (!item) throw new ProductUnavailable()
    const { normalizeWorkWorkspaceView, SHARED_CREW_WORKSPACE_PANELS } = await import('../products/work/WorkSurface')
    // An unknown or not-allowed view name falls back to the product default
    // instead of making the whole project link "not available".
    const view = route.view ? normalizeWorkWorkspaceView(route.view) : null
    if (route.view && (!view || (item.shared && !SHARED_CREW_WORKSPACE_PANELS.has(view)))) route = { ...route, view: undefined }
    activateResource = () => {
      const state = useProductSurfaceStore.getState()
      if (route.surface === 'code') state.setSelectedCodeProjectId(route.id!)
      else state.setSelectedWorkProjectId(route.id!)
    }
  } else if (route.id && route.surface === 'video-studio') {
    const { loadVideoProjects } = await import('../products/video-studio/videoStudioData')
    if (!(await loadVideoProjects()).some(item => item.id === route.id)) throw new ProductUnavailable()
    activateResource = () => useProductSurfaceStore.getState().setLastVideoProjectId(route.id!)
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
      if (route.surface === 'code') state.setSelectedCodeProjectId(null)
      if (route.surface === 'video-studio') state.setLastVideoProjectId(null)
      if (route.surface === 'agentworks' || route.surface === 'relays') {
        useGlobalPresetStore.getState().clearActivePreset('workflow')
        useAppStore.getState().setShowWorkflowsOverview(true)
        useModeStore.getState().completeInitialSetup()
      }
    }
    activateResource()
  }
}

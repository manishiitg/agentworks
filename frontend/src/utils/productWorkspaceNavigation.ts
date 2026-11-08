import { workspaceSettingsPage } from './workspaceSettingsRoutes'
import { useCommandDialogStore } from '../stores/useCommandDialogStore'
import { useAppStore } from '../stores/useAppStore'
import { useLLMStore } from '../stores/useLLMStore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import type { ProductSurface } from '../products/productSurfaceConfig'
import { cancelPendingWorkflowNavigation } from './workflowNavigation'
import { productUrlViewChanged } from './productUrlNavigation'

/** Product icons and page Back buttons share the same workspace destination. */
export function openProductWorkspace(surface: ProductSurface, options?: { keepHome?: boolean }) {
  // A product click is newer intent than any in-flight workflow lookup.
  cancelPendingWorkflowNavigation()
  useCommandDialogStore.getState().requestProductCreate(null)
  const app = useAppStore.getState()
  useLLMStore.getState().setShowLLMModal(false)
  app.setShowSchedulesOverview(false)
  app.setAdminPage(null)
  app.setShowWorkflowsOverview(false)
  app.setActivityWorkflowPath(null)
  if (surface === 'agentworks') {
    app.setModeCategory('workflow')
    app.setAgentMode('workflow')
  } else if (surface === 'work' || surface === 'knowledgebase') {
    // Set both mode projections before mounting the product chat. Waiting for
    // its mount effect lets the legacy workflow handler restore Goals first.
    app.setModeCategory('multi-agent')
    app.setAgentMode('multi-agent')
  }
  useProductSurfaceStore.getState().setProductSurface(surface)
  if (workspaceSettingsPage() || window.location.pathname === '/dashboards' || (window.location.pathname === '/home' && !options?.keepHome)) productUrlViewChanged()
}

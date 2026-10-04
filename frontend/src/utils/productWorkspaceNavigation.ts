import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { useAppStore } from '../stores/useAppStore'
import { useLLMStore } from '../stores/useLLMStore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import type { ProductSurface } from '../products/productSurfaceConfig'

/** Product icons and page Back buttons share the same workspace destination. */
export function openProductWorkspace(surface: ProductSurface) {
  const app = useAppStore.getState()
  useLLMStore.getState().setShowLLMModal(false)
  app.setShowSchedulesOverview(false)
  app.setAdminPage(null)
  app.setShowWorkflowsOverview(false)
  app.setActivityWorkflowPath(null)
  if (surface === 'agentworks' || surface === 'relays') {
    const presets = useGlobalPresetStore.getState()
    const activePreset = presets.getActivePreset('workflow')
    if (activePreset && (activePreset.workflowKind === 'relay') !== (surface === 'relays')) {
      presets.clearActivePreset('workflow')
      presets.setSelectedPresetFolder(null)
    }
    app.setModeCategory('workflow')
  }
  useProductSurfaceStore.getState().setProductSurface(surface)
}

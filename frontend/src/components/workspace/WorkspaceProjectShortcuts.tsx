import { useAuthStore } from '../../stores/useAuthStore'
import { useAppStore } from '../../stores/useAppStore'
import { useLLMStore } from '../../stores/useLLMStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { useSidebarHistoryStore } from '../../stores/useSidebarHistoryStore'
import { enabledProductSurfaces, intersectAllowedProductSurfaces, PRODUCT_SURFACE_LABELS } from '../../products/productSurfaceConfig'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import { ProductSurfaceIcon } from '../ProductSurfaceIcon'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '../ui/tooltip'

export const PROJECT_SIDEBAR_ORDER = ['work', 'agentworks', 'relays'] as const

/** Project destinations stay reachable when the work list is collapsed. */
export function WorkspaceProjectShortcuts() {
  const user = useAuthStore(state => state.user)
  const product = useProductSurfaceStore(state => state.productSurface)
  const globalPageOpen = useAppStore(state => state.showWorkflowsOverview || state.showSchedulesOverview || state.adminPage !== null)
  const providersOpen = useLLMStore(state => state.showLLMModal)
  const allowed = intersectAllowedProductSurfaces(enabledProductSurfaces(), user?.allowed_products)
  return <TooltipProvider delayDuration={250}>
    <nav aria-label="Project products" className="flex flex-col items-center gap-1">
      {PROJECT_SIDEBAR_ORDER.filter(surface => allowed.includes(surface)).map(surface => {
        const label = PRODUCT_SURFACE_LABELS[surface]
        const selected = product === surface && !globalPageOpen && !providersOpen && !['/home', '/dashboards'].includes(window.location.pathname)
        return <Tooltip key={surface}>
          <TooltipTrigger asChild>
            <button type="button" aria-label={label} aria-current={selected ? 'page' : undefined}
              className={`flex h-10 w-10 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${selected ? 'bg-foreground/[0.08] text-foreground' : ''}`}
              onClick={() => {
                if (!intersectAllowedProductSurfaces(enabledProductSurfaces(), useAuthStore.getState().user?.allowed_products).includes(surface)) return
                useSidebarHistoryStore.setState(state => ({ request: null, generation: state.generation + 1 }))
                openProductWorkspace(surface)
              }}>
              <ProductSurfaceIcon surface={surface} className="h-[18px] w-[18px] shrink-0" />
            </button>
          </TooltipTrigger>
          <TooltipContent side="right" sideOffset={8}>{label}</TooltipContent>
        </Tooltip>
      })}
    </nav>
  </TooltipProvider>
}

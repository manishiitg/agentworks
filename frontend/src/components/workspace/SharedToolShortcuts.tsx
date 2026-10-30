import { openWorkspaceSettings } from '../../utils/workspaceSettingsNavigation'
import { followAppLink } from '../../utils/appNavigation'
import { type ReactElement } from 'react'
import { LayoutDashboard, Terminal } from 'lucide-react'
import { useAuthStore } from '../../stores/useAuthStore'
import { useAppStore } from '../../stores/useAppStore'
import { useLLMStore } from '../../stores/useLLMStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { enabledProductSurfaces, intersectAllowedProductSurfaces, PRODUCT_SURFACE_LABELS } from '../../products/productSurfaceConfig'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import { ProductSurfaceIcon } from '../ProductSurfaceIcon'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '../ui/tooltip'

/** Permitted shared tools in the expanded sidebar. */
export function SharedToolShortcuts() {
  const user = useAuthStore(state => state.user)
  const product = useProductSurfaceStore(state => state.productSurface)
  const globalPageOpen = useAppStore(state => state.showWorkflowsOverview || state.showSchedulesOverview || state.adminPage !== null)
  const connecting = useAppStore(state => state.adminPage === 'mcp')
  const providersOpen = useLLMStore(state => state.showLLMModal)
  const allowed = intersectAllowedProductSurfaces(enabledProductSurfaces(), user?.allowed_products)
  const resources = (['knowledgebase', 'mcp-gateway', 'bullpen'] as const).filter(surface => allowed.includes(surface))
  const dashboardPage = window.location.pathname === '/dashboards'
  const itemClass = (selected: boolean) => `flex min-w-0 items-center justify-center rounded-md transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring h-10 w-full flex-1 ${selected ? 'bg-foreground/[0.08] text-foreground' : 'text-foreground/75 hover:text-foreground'}`
  const withTooltip = (label: string, child: ReactElement) => <Tooltip>
    <TooltipTrigger asChild>{child}</TooltipTrigger>
    <TooltipContent side="top" sideOffset={8}>{label}</TooltipContent>
  </Tooltip>

  return <TooltipProvider delayDuration={250}>
    <div className="flex shrink-0 gap-0.5 border-t border-border/60 px-1 py-2" data-testid="shared-tool-shortcuts">
      <nav aria-label="Dashboards" className="min-w-0 flex-1">
        {withTooltip('Dashboards', <a href="/dashboards" onClick={followAppLink} aria-label="Dashboards" aria-current={dashboardPage ? 'page' : undefined} className={itemClass(dashboardPage)}>
          <LayoutDashboard className="h-[18px] w-[18px] shrink-0" aria-hidden="true" />
        </a>)}
      </nav>
      {resources.length > 0 && <nav aria-label="Shared tools" className="flex min-w-0 gap-0.5" style={{ flex: resources.length }}>
        {resources.map(surface => {
          const title = PRODUCT_SURFACE_LABELS[surface]
          const selected = !dashboardPage && product === surface && !globalPageOpen && !providersOpen
          return <span key={surface} className="flex min-w-0 flex-1">{withTooltip(title,
            <button type="button" aria-label={title} aria-current={selected ? 'page' : undefined} className={itemClass(selected)}
              onClick={() => {
                if (!intersectAllowedProductSurfaces(enabledProductSurfaces(), useAuthStore.getState().user?.allowed_products).includes(surface)) return
                openProductWorkspace(surface)
              }}>
              <span aria-hidden="true" className="flex h-[18px] w-[18px] items-center justify-center"><ProductSurfaceIcon surface={surface} className="h-[18px] w-[18px] shrink-0" /></span>
            </button>
          )}</span>
        })}
      </nav>}
      <nav aria-label="AI connections" className="min-w-0 flex-1">
        {withTooltip('AgentWorks CLI & MCP', <button type="button" aria-label="AgentWorks CLI & MCP" aria-current={connecting ? 'page' : undefined} className={itemClass(connecting)} onClick={() => openWorkspaceSettings('mcp')}>
          <Terminal className="h-[18px] w-[18px] shrink-0" aria-hidden="true" />
        </button>)}
      </nav>
    </div>
  </TooltipProvider>
}

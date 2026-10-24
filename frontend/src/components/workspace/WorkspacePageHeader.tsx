import { closeWorkspaceSettings } from '../../utils/workspaceSettingsNavigation'
import { workspaceSettingsPage } from '../../utils/workspaceSettingsRoutes'
import type { HTMLAttributes, ReactNode } from 'react'
import { ArrowLeft, type LucideIcon } from 'lucide-react'
import { cn } from '../../lib/utils'
import type { ProductMenuItem } from './ProductMenuItems'
import { ProductBreadcrumb } from './ProductBreadcrumb'
import { PRODUCT_SURFACE_LABELS } from '../../products/productSurfaceConfig'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import { WorkspaceTopToolbar } from './WorkspaceTopToolbar'
import { useAppStore } from '../../stores/useAppStore'
import { useLLMStore } from '../../stores/useLLMStore'
import { WORKSPACE_PAGE_GUTTER } from './WorkspacePageContent'

/** Shared pages use the workspace toolbar's spacing and existing navigation controls. */
export function WorkspacePageHeader({ title, icon: Icon, description, onBack, onProductHome, showBack = true, below, page, productMenuItems, children, className, ...props }: Omit<HTMLAttributes<HTMLElement>, 'title'> & {
  title: string
  icon?: LucideIcon
  description?: string
  onBack?: () => void
  onProductHome?: () => void
  showBack?: boolean
  below?: ReactNode
  productMenuItems?: readonly ProductMenuItem[]
  page?: 'activity' | 'schedules' | 'providers' | 'users' | 'mcp' | 'vaults'
}) {
  const surface = useProductSurfaceStore(state => state.productSurface)
  const currentPage = useAppStore(state => state.adminPage ?? (state.showSchedulesOverview ? 'schedules' : state.showWorkflowsOverview ? 'activity' : null))
  const providers = useLLMStore(state => state.showLLMModal)
  // Visited pages intentionally stay mounted (including an in-progress provider login).
  // Only the page being shown may register the shared navigation slots.
  const navigationActive = providers ? page === 'providers' : page ? page === currentPage : currentPage === null
  const globalSetup = page && ['providers', 'users', 'mcp', 'vaults'].includes(page)
  return <header {...props} className={cn('shrink-0 border-b border-border', className)}>
    <WorkspaceTopToolbar navigationKind="page" navigationActive={navigationActive} className="w-full border-b-0 bg-transparent">
      {showBack && (page === 'activity' || page === 'schedules') && <button type="button" aria-label={`Back to ${PRODUCT_SURFACE_LABELS[surface]}`} title={`Back to ${PRODUCT_SURFACE_LABELS[surface]}`} onClick={() => { if (onBack) onBack(); else if (workspaceSettingsPage()) closeWorkspaceSettings(); else openProductWorkspace(surface) }} className="inline-flex h-8 shrink-0 items-center gap-1.5 rounded-md px-2 text-xs text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><ArrowLeft className="h-4 w-4" aria-hidden="true" /><span>Back</span></button>}
      {globalSetup ? <div className="flex min-w-0 items-center gap-2">
        {showBack && <button type="button" aria-label="Back to workspace" title="Back to workspace" onClick={() => { if (onBack) onBack(); else if (workspaceSettingsPage()) closeWorkspaceSettings(); else openProductWorkspace(surface) }} className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><ArrowLeft className="h-4 w-4" aria-hidden="true" /></button>}
        {Icon && <Icon aria-hidden="true" className="h-4 w-4 shrink-0 text-muted-foreground" />}
        <h1 className="min-w-0 truncate text-sm font-semibold text-foreground" title={title}>{title}</h1>
      </div> : showBack || onProductHome || title === PRODUCT_SURFACE_LABELS[surface] ? <ProductBreadcrumb surface={surface} projectPickerActive={navigationActive} menuItems={productMenuItems} section={title === PRODUCT_SURFACE_LABELS[surface] ? undefined : title} onHome={() => { if (onProductHome) { onProductHome(); return }; onBack?.(); openProductWorkspace(surface); window.dispatchEvent(new CustomEvent('open-product-home', { detail: surface })) }} /> : <>
        {Icon && <Icon className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />}
        <h1 className="min-w-0 truncate text-sm font-semibold text-foreground" title={title}>{title}</h1>
      </>}
      {description && <p className="sr-only">{description}</p>}
      {children}
    </WorkspaceTopToolbar>
    {below && <div className={cn(WORKSPACE_PAGE_GUTTER, 'pb-2')}>{below}</div>}
  </header>
}

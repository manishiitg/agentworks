import type { HTMLAttributes, ReactNode } from 'react'
import { Settings, type LucideIcon } from 'lucide-react'
import { cn } from '../../lib/utils'
import { ProductBreadcrumb } from './ProductBreadcrumb'
import { PRODUCT_SURFACE_LABELS } from '../../products/productSurfaceConfig'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import { WorkspaceTopToolbar } from './WorkspaceTopToolbar'
import { useAppStore } from '../../stores/useAppStore'
import { useLLMStore } from '../../stores/useLLMStore'

/** Shared pages use the workspace toolbar's spacing and existing navigation controls. */
export function WorkspacePageHeader({ title, icon: Icon, description, onBack, onProductHome, showBack = true, below, page, children, className, ...props }: Omit<HTMLAttributes<HTMLElement>, 'title'> & {
  title: string
  icon?: LucideIcon
  description?: string
  onBack?: () => void
  onProductHome?: () => void
  showBack?: boolean
  below?: ReactNode
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
      {globalSetup ? <div className="flex min-w-0 items-center gap-2"><Settings aria-hidden="true" className="h-4 w-4 shrink-0 text-muted-foreground" /><h1 className="shrink-0 text-sm font-semibold">Setup</h1><span aria-hidden="true" className="text-muted-foreground/50">/</span><span className="truncate text-xs text-muted-foreground">{title}</span></div> : showBack || onProductHome ? <ProductBreadcrumb surface={surface} section={title === PRODUCT_SURFACE_LABELS[surface] ? undefined : title} onHome={() => { if (onProductHome) { onProductHome(); return }; onBack?.(); openProductWorkspace(surface); window.dispatchEvent(new CustomEvent('open-product-home', { detail: surface })) }} /> : <>
        {Icon && <Icon className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />}
        <h1 className="min-w-0 truncate text-sm font-semibold text-foreground" title={title}>{title}</h1>
      </>}
      {description && <p className="sr-only">{description}</p>}
      {children}
    </WorkspaceTopToolbar>
    {below && <div className="px-3 pb-2">{below}</div>}
  </header>
}

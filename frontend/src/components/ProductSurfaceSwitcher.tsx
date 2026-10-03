import { useProductNavigationSidebar } from './workspace/ProductTopBar'
import { useEffect, useMemo, useRef, useState, type ComponentType } from 'react'
import { Check, ChevronDown, Waypoints } from 'lucide-react'
import { RunloopMark } from './branding/RunloopLogo'
import { VideoStudioMark } from '../products/video-studio/VideoStudioMark'
import { DominionMark } from '../products/dominion/DominionMark'
import { SparkQuillMark } from '../products/sparkquill/SparkQuillMark'
import { WorkMark } from '../products/work/WorkMark'
import { VaultMark } from '../products/mcp-gateway/VaultMark'
import { CodeMark } from '../products/work/CodeMark'
import { useProductSurfaceStore, type ProductSurface } from '../stores/useProductSurfaceStore'
import { openProductWorkspace } from '../utils/productWorkspaceNavigation'
import { useAuthStore } from '../stores/useAuthStore'
import { gatewayAdminUrl, visibleProductSurfaceIDs } from '../products/productSurfaceConfig'
import { applyRuntimeBranding } from '../runtime-branding'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from './ui/tooltip'
import { cn } from '../lib/utils'

type ProductSurfaceSwitcherProps = {
  className?: string
  /** The gateway's independent build has no AgentWorks app router. */
  standalone?: boolean
}

// Product marks can render any element; callers only rely on the common
// className/title surface, not SVG-specific props.
type ProductMarkComponent = ComponentType<{ className?: string; title?: string }>

const products: Array<{
  id: ProductSurface
  label: string
  description: string
  icon: ProductMarkComponent
}> = [
  { id: 'agentworks', label: 'Goals', description: 'Set a goal, give the agents a metric, and watch them hit it', icon: RunloopMark },
  { id: 'relays', label: 'Relays', description: 'Build and run an API callable agent graph', icon: Waypoints },
  { id: 'video-studio', label: 'Video Studio', description: 'Projects and video production', icon: VideoStudioMark },
  { id: 'dominion', label: 'Dominion', description: 'Paper-trading watchlist and portfolio', icon: DominionMark },
  { id: 'sparkquill', label: 'SparkQuill', description: 'Family learning with Quill', icon: SparkQuillMark },
  { id: 'work', label: 'Crew', description: 'Specialist agents with their own memory and skills, working together', icon: WorkMark },
  { id: 'code', label: 'Code', description: 'A private coding workspace: files, editor, terminal and a coding agent', icon: CodeMark },
  { id: 'mcp-gateway', label: 'Vault', description: 'Tools, skills, and access', icon: VaultMark },
]

export function ProductSurfaceSwitcher({ className, standalone = false }: ProductSurfaceSwitcherProps) {
  const sidebar = useProductNavigationSidebar()
  const productSurface = useProductSurfaceStore((state) => state.productSurface)
  const allowedProducts = useAuthStore((state) => state.user?.allowed_products)
  const [open, setOpen] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)
  const visibleProductIDs = useMemo(() => visibleProductSurfaceIDs(allowedProducts), [allowedProducts])
  const gatewayUrl = gatewayAdminUrl()
  // The gateway renders inside the app like any other surface, but only
  // exists when a gateway URL is configured for this deployment.
  const visibleProducts = useMemo(() => standalone
    ? products.filter(product => product.id === 'mcp-gateway')
    : products.filter(product => visibleProductIDs.includes(product.id) && (product.id !== 'mcp-gateway' || gatewayUrl !== null)),
  [visibleProductIDs, gatewayUrl, standalone])
  const currentProduct = visibleProducts.find((product) => product.id === (standalone ? 'mcp-gateway' : productSurface)) ?? visibleProducts[0] ?? products[0]
  const CurrentIcon = currentProduct.icon

  useEffect(() => {
    if (standalone || productSurface === 'mcp-gateway') {
      document.title = 'Vault'
      const favicon = document.querySelector<HTMLLinkElement>('link[rel~="icon"]')
      if (favicon) favicon.href = '/vault.svg'
      return
    }
    document.title = 'AgentWorks'
    applyRuntimeBranding(window.__APP_RUNTIME_CONFIG__ as Parameters<typeof applyRuntimeBranding>[0])
  }, [productSurface, standalone])

  const activateProduct = (product: ProductSurface) => {
    setOpen(false)
    if (standalone) return
    openProductWorkspace(product)
  }

  useEffect(() => {
    if (!open) return
    const closeOnOutsideClick = (event: MouseEvent) => {
      if (!menuRef.current?.contains(event.target as Node)) setOpen(false)
    }
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', closeOnOutsideClick)
    document.addEventListener('keydown', closeOnEscape)
    return () => {
      document.removeEventListener('mousedown', closeOnOutsideClick)
      document.removeEventListener('keydown', closeOnEscape)
    }
  }, [open])

  if (sidebar) return (
    <TooltipProvider delayDuration={200}>
      <div role="group" aria-label="Products" className="flex flex-col gap-1 border-b border-border pb-3">
        {visibleProducts.map(product => {
          const Icon = product.icon
          const active = product.id === (standalone ? 'mcp-gateway' : productSurface)
          return <Tooltip key={product.id}>
            <TooltipTrigger asChild>
              <button type="button" aria-label={product.label} aria-current={active ? 'page' : undefined}
                data-tour-products={visibleProductIDs.join(' ')}
                onClick={() => activateProduct(product.id)}
                data-product-navigation-action
                className={`relative grid h-9 w-full place-items-center rounded-lg transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary ${active
                  ? 'bg-secondary text-foreground'
                  : 'text-muted-foreground hover:bg-secondary hover:text-foreground'}`}>
                {active && <span aria-hidden="true" className="absolute -left-2 h-4 w-0.5 rounded-r bg-primary" />}
                <Icon className="h-5 w-5 shrink-0" title="" />
              </button>
            </TooltipTrigger>
            <TooltipContent side="right">{product.label}</TooltipContent>
          </Tooltip>
        })}
      </div>
    </TooltipProvider>
  )

  return (
    <div
      ref={menuRef}
      className={cn('relative shrink-0', className)}
    >
      <button
        type="button"
        onClick={() => setOpen((current) => !current)}
        aria-label="Switch product"
        data-tour-products={visibleProductIDs.join(' ')}
        aria-haspopup="menu"
        aria-expanded={open}
        title={currentProduct.label}
        className="flex items-center gap-2.5 rounded-xl border border-slate-200 bg-white px-2 py-1.5 text-left text-slate-900 shadow-sm transition hover:bg-slate-50 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100 dark:hover:bg-slate-800"
      >
        <CurrentIcon className="h-7 w-7 shrink-0" title="" />
        <span className="whitespace-nowrap text-xs font-semibold">{currentProduct.label}</span>
        <ChevronDown className={`h-3.5 w-3.5 text-slate-400 transition-transform ${open ? 'rotate-180' : ''}`} />
      </button>
      {open ? (
        <div role="menu" aria-label="Products" className="absolute left-0 top-[calc(100%+8px)] z-50 w-64 max-w-[calc(100vw-5rem)] rounded-2xl border border-slate-200 bg-white p-1.5 shadow-2xl shadow-slate-950/15 dark:border-slate-700 dark:bg-slate-900">
          {visibleProducts.map((product) => {
            const active = (standalone ? 'mcp-gateway' : productSurface) === product.id
            const Icon = product.icon
            return (
              <button
                key={product.id}
                type="button"
                role="menuitem"
                aria-current={active ? 'page' : undefined}
                onClick={() => {
                  activateProduct(product.id)
                }}
                className="flex w-full items-center gap-3 rounded-xl px-3 py-2.5 text-left transition-colors hover:bg-slate-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-violet-500 dark:hover:bg-slate-800"
              >
                <Icon className="h-8 w-8 shrink-0" title="" />
                <span className="min-w-0 flex-1">
                  <strong className="block text-xs text-slate-900 dark:text-slate-100">{product.label}</strong>
                  <small className="mt-0.5 block text-[10px] text-slate-600 dark:text-slate-300">
                    {product.description}
                  </small>
                </span>
                {active ? <Check className="h-4 w-4 shrink-0 text-violet-600 dark:text-violet-300" /> : null}
              </button>
            )
          })}
        </div>
      ) : null}
    </div>
  )
}

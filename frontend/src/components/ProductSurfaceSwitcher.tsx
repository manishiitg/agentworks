import { useEffect, useRef, useState, type ComponentType } from 'react'
import { ArrowUpRight, Check, ChevronDown } from 'lucide-react'
import { RunloopMark } from './branding/RunloopLogo'
import { VideoStudioMark } from '../products/video-studio/VideoStudioMark'
import { DominionMark } from '../products/dominion/DominionMark'
import { SparkQuillMark } from '../products/sparkquill/SparkQuillMark'
import { WorkMark } from '../products/work/WorkMark'
import { GatewayMark } from '../products/mcp-gateway/GatewayMark'
import { useProductSurfaceStore, type ProductSurface } from '../stores/useProductSurfaceStore'
import { useAppStore } from '../stores/useAppStore'
import { useAuthStore } from '../stores/useAuthStore'
import { gatewayAdminUrl, isEnabledProductSurface, intersectAllowedProductSurfaces } from '../products/productSurfaceConfig'
import { cn } from '../lib/utils'

type ProductSurfaceSwitcherProps = {
  className?: string
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
  { id: 'agentworks', label: 'AgentWorks', description: 'Set a goal, give the agents a metric, and watch them hit it', icon: RunloopMark },
  { id: 'video-studio', label: 'Video Studio', description: 'Projects and video production', icon: VideoStudioMark },
  { id: 'dominion', label: 'Dominion', description: 'Paper-trading watchlist and portfolio', icon: DominionMark },
  { id: 'sparkquill', label: 'SparkQuill', description: 'Family learning with Quill', icon: SparkQuillMark },
  { id: 'work', label: 'Crew', description: 'Specialist agents with their own memory and skills, working together', icon: WorkMark },
]

export function visibleProductSurfaceIDs(allowedProducts?: string[] | null): ProductSurface[] {
  const deploymentSurfaces = products.filter((product) => isEnabledProductSurface(product.id)).map((product) => product.id)
  return intersectAllowedProductSurfaces(deploymentSurfaces, allowedProducts)
}

export function ProductSurfaceSwitcher({ className }: ProductSurfaceSwitcherProps) {
  const productSurface = useProductSurfaceStore((state) => state.productSurface)
  const setProductSurface = useProductSurfaceStore((state) => state.setProductSurface)
  const allowedProducts = useAuthStore((state) => state.user?.allowed_products)
  const [open, setOpen] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)
  const visibleProductIDs = visibleProductSurfaceIDs(allowedProducts)
  const visibleProducts = products.filter((product) => visibleProductIDs.includes(product.id))
  const currentProduct = visibleProducts.find((product) => product.id === productSurface) ?? visibleProducts[0] ?? products[0]
  const CurrentIcon = currentProduct.icon
  const gatewayUrl = gatewayAdminUrl()

  const activateProduct = (product: ProductSurface) => {
    setOpen(false)
    setProductSurface(product)
    if (product !== 'agentworks') return

    // AgentWorks opens the automation overview; parked products are not exposed here.
    const appStore = useAppStore.getState()
    appStore.setModeCategory('workflow')
    appStore.setShowWorkflowsOverview(true)
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

  return (
    <div
      ref={menuRef}
      className={cn('relative shrink-0', className)}
    >
      <button
        type="button"
        onClick={() => setOpen((current) => !current)}
        aria-label="Switch product"
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
        <div role="menu" aria-label="Products" className="absolute left-0 top-[calc(100%+8px)] z-50 w-64 rounded-2xl border border-slate-200 bg-white p-1.5 shadow-2xl shadow-slate-950/15 dark:border-slate-700 dark:bg-slate-900">
          {visibleProducts.map((product) => {
            const active = productSurface === product.id
            const Icon = product.icon
            return (
              <button
                key={product.id}
                type="button"
                role="menuitem"
                onClick={() => {
                  activateProduct(product.id)
                }}
                className={cn(
                  'flex w-full items-center gap-3 rounded-xl px-3 py-2.5 text-left transition-colors',
                  active
                    ? 'bg-violet-50 dark:bg-violet-950/40'
                    : 'hover:bg-slate-100 dark:hover:bg-slate-800',
                )}
              >
                <Icon className="h-8 w-8 shrink-0" title="" />
                <span className="min-w-0 flex-1">
                  <strong className={cn('block text-xs', active ? 'text-violet-800 dark:text-violet-200' : 'text-slate-900 dark:text-slate-100')}>{product.label}</strong>
                  <small className={cn('mt-0.5 block text-[10px]', active ? 'text-violet-500 dark:text-violet-400' : 'text-slate-400')}>
                    {product.description}
                  </small>
                </span>
                {active ? <Check className="h-4 w-4 shrink-0 text-violet-600 dark:text-violet-300" /> : null}
              </button>
            )
          })}
          {gatewayUrl ? (
            <a
              href={gatewayUrl}
              target="_blank"
              rel="noreferrer"
              role="menuitem"
              onClick={() => setOpen(false)}
              className="flex w-full items-center gap-3 rounded-xl px-3 py-2.5 text-left transition-colors hover:bg-slate-100 dark:hover:bg-slate-800"
            >
              <GatewayMark className="h-8 w-8 shrink-0" title="" />
              <span className="min-w-0 flex-1">
                <strong className="block text-xs text-slate-900 dark:text-slate-100">MCP Gateway</strong>
                <small className="mt-0.5 block text-[10px] text-slate-400">
                  Governed MCP tools for every AI client
                </small>
              </span>
              <ArrowUpRight className="h-4 w-4 shrink-0 text-slate-400" />
            </a>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}

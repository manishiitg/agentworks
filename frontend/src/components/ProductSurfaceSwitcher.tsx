import { useEffect, useMemo, useRef, useState, type ComponentType } from 'react'
import { Check, ChevronDown } from 'lucide-react'
import { RunloopMark } from './branding/RunloopLogo'
import { VideoStudioMark } from '../products/video-studio/VideoStudioMark'
import { DominionMark } from '../products/dominion/DominionMark'
import { SparkQuillMark } from '../products/sparkquill/SparkQuillMark'
import { WorkMark } from '../products/work/WorkMark'
import { GatewayMark } from '../products/mcp-gateway/GatewayMark'
import { useProductSurfaceStore, type ProductSurface } from '../stores/useProductSurfaceStore'
import { useAppStore } from '../stores/useAppStore'
import { useAuthStore } from '../stores/useAuthStore'
import { gatewayAdminUrl, visibleProductSurfaceIDs } from '../products/productSurfaceConfig'
import { preloadProductSurface } from '../products/productSurfacePreload'
import { cn } from '../lib/utils'

type ProductSurfaceSwitcherProps = {
  className?: string
  preloadOnIdle?: boolean
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
  { id: 'mcp-gateway', label: 'MCP Gateway', description: 'Governed MCP tools for every AI client', icon: GatewayMark },
]

export function ProductSurfaceSwitcher({ className, preloadOnIdle = true }: ProductSurfaceSwitcherProps) {
  const productSurface = useProductSurfaceStore((state) => state.productSurface)
  const setProductSurface = useProductSurfaceStore((state) => state.setProductSurface)
  const allowedProducts = useAuthStore((state) => state.user?.allowed_products)
  const [open, setOpen] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)
  const visibleProductIDs = useMemo(() => visibleProductSurfaceIDs(allowedProducts), [allowedProducts])
  const gatewayUrl = gatewayAdminUrl()
  // The gateway renders inside the app like any other surface, but only
  // exists when a gateway URL is configured for this deployment.
  const visibleProducts = useMemo(() => products.filter(
    (product) => visibleProductIDs.includes(product.id) && (product.id !== 'mcp-gateway' || gatewayUrl !== null),
  ), [visibleProductIDs, gatewayUrl])
  const currentProduct = visibleProducts.find((product) => product.id === productSurface) ?? visibleProducts[0] ?? products[0]
  const CurrentIcon = currentProduct.icon

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

  useEffect(() => {
    if (!preloadOnIdle) return
    // The auth bootstrap has already supplied this user's allowed products by
    // the time the switcher mounts. Warm only those product modules after the
    // first paint, one idle slot at a time, so the first switch avoids a chunk
    // download without making the current product wait for every other one.
    const candidates = visibleProducts.map(product => product.id).filter(id => id !== productSurface && id !== 'agentworks')
    let cancelled = false
    let cancelScheduled: (() => void) | null = null

    const scheduleNext = () => {
      if (cancelled || candidates.length === 0) return
      const run = () => {
        if (cancelled) return
        const next = candidates.shift()!
        const loading = preloadProductSurface(next)
        if (loading) void loading.catch(() => {}).finally(scheduleNext)
        else scheduleNext()
      }
      if (typeof window.requestIdleCallback === 'function') {
        const handle = window.requestIdleCallback(run)
        cancelScheduled = () => window.cancelIdleCallback(handle)
      } else {
        const handle = window.setTimeout(run, 200)
        cancelScheduled = () => window.clearTimeout(handle)
      }
    }

    scheduleNext()
    return () => {
      cancelled = true
      cancelScheduled?.()
    }
  }, [visibleProducts, productSurface, preloadOnIdle])

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
                onMouseEnter={() => { void preloadProductSurface(product.id)?.catch(() => {}) }}
                onFocus={() => { void preloadProductSurface(product.id)?.catch(() => {}) }}
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
        </div>
      ) : null}
    </div>
  )
}

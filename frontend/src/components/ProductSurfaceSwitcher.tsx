import { useEffect, useMemo, useRef, useState, type ComponentType } from 'react'
import { Check, ChevronDown, Waypoints } from 'lucide-react'
import { RunloopMark } from './branding/RunloopLogo'
import { VideoStudioMark } from '../products/video-studio/VideoStudioMark'
import { DominionMark } from '../products/dominion/DominionMark'
import { SparkQuillMark } from '../products/sparkquill/SparkQuillMark'
import { WorkMark } from '../products/work/WorkMark'
import { CapLayerMark } from '../products/mcp-gateway/CapLayerMark'
import { CodeMark } from '../products/work/CodeMark'
import { useProductSurfaceStore, type ProductSurface } from '../stores/useProductSurfaceStore'
import { useAppStore } from '../stores/useAppStore'
import { useAuthStore } from '../stores/useAuthStore'
import { gatewayAdminUrl, visibleProductSurfaceIDs } from '../products/productSurfaceConfig'
import { applyRuntimeBranding } from '../runtime-branding'
import { cn } from '../lib/utils'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'

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
  { id: 'mcp-gateway', label: 'CapLayer', description: 'Governed access to tools for every AI agent', icon: CapLayerMark },
]

export function ProductSurfaceSwitcher({ className, standalone = false }: ProductSurfaceSwitcherProps) {
  const productSurface = useProductSurfaceStore((state) => state.productSurface)
  const setProductSurface = useProductSurfaceStore((state) => state.setProductSurface)
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
      document.title = 'CapLayer'
      const favicon = document.querySelector<HTMLLinkElement>('link[rel~="icon"]')
      if (favicon) favicon.href = '/caplayer.svg'
      return
    }
    document.title = 'AgentWorks'
    applyRuntimeBranding(window.__APP_RUNTIME_CONFIG__ as Parameters<typeof applyRuntimeBranding>[0])
  }, [productSurface, standalone])

  const activateProduct = (product: ProductSurface) => {
    setOpen(false)
    if (standalone) return
    setProductSurface(product)
    if (product !== 'agentworks' && product !== 'relays') return

    const presetStore = useGlobalPresetStore.getState()
    const activePreset = presetStore.getActivePreset('workflow')
    if (activePreset && (activePreset.workflowKind === 'relay') !== (product === 'relays')) {
      presetStore.clearActivePreset('workflow')
      presetStore.setSelectedPresetFolder(null)
    }
    const appStore = useAppStore.getState()
    appStore.setModeCategory('workflow')
    appStore.setShowWorkflowsOverview(product === 'agentworks')
    appStore.setShowSchedulesOverview(false)
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
        <div role="menu" aria-label="Products" className="absolute left-0 top-[calc(100%+8px)] z-50 w-64 rounded-2xl border border-slate-200 bg-white p-1.5 shadow-2xl shadow-slate-950/15 dark:border-slate-700 dark:bg-slate-900">
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

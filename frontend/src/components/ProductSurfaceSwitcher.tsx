import { useCompactProductNavigation, useProductNavigationSidebar } from './workspace/ProductTopBar'
import { useEffect, useMemo, useRef, useState, type ComponentType, type ReactNode } from 'react'
import { ChevronDown, LayoutDashboard, MessagesSquare, Network, Terminal, Waypoints } from 'lucide-react'
import { RunloopMark } from './branding/RunloopLogo'
import { WorkMark } from '../products/work/WorkMark'
import { VaultMark } from '../products/mcp-gateway/VaultMark'
import { BrainMark } from '../products/knowledgebase/BrainMark'
import { useProductSurfaceStore, type ProductSurface } from '../stores/useProductSurfaceStore'
import { openAppPage } from '../utils/appNavigation'
import { openProductWorkspace } from '../utils/productWorkspaceNavigation'
import { useAuthStore } from '../stores/useAuthStore'
import { gatewayAdminUrl, navigationProductSurfaceIDs } from '../products/productSurfaceConfig'
import { applyRuntimeBranding } from '../runtime-branding'
import { cn } from '../lib/utils'
import { useProductNavigationToolbarStore } from '../stores/useProductNavigationToolbarStore'
import { ProductSwitchMenu } from './workspace/ProductSwitchMenu'
import { ProductPickerMenu, useInProductPicker } from './workspace/ProductPickerMenu'
import { useProductSettingsMenu } from './workspace/productNavigationContexts'
import { openWorkspaceSettings } from '../utils/workspaceSettingsNavigation'

type ProductSurfaceSwitcherProps = {
  className?: string
  /** The gateway's independent build has no AgentWorks app router. */
  standalone?: boolean
  menuOnly?: boolean
  onSelect?: () => void
  variant?: 'default' | 'header' | 'chevron'
  menuColumns?: 2 | 3
  menuTitle?: string
  menuContent?: (close: () => void) => ReactNode
  includeConnectTools?: boolean
}

// Product marks can render any element; callers only rely on the common
// className/title surface, not SVG-specific props.
type ProductMarkComponent = ComponentType<{ className?: string; title?: string }>

const dashboards = { id: 'dashboards', label: 'Dashboards', description: 'Open your published dashboards and dashboards shared with you.', icon: LayoutDashboard }
const connectTools = { id: 'connect-tools', label: 'CLI / MCP', description: 'Connect your AI app or coding agent to AgentWorks.', icon: Terminal }

const products: Array<{
  id: ProductSurface
  label: string
  description: string
  icon: ProductMarkComponent
}> = [
  { id: 'agentworks', label: 'Goals', description: 'Give an agent a measurable goal. It runs the work, checks results and suggests improvements.', icon: RunloopMark },
  { id: 'relays', label: 'Flows', description: 'Connect agents, decisions and scripts into repeatable steps that other systems can run through an API.', icon: Waypoints },
  { id: 'work', label: 'Crew', description: 'Private or shared projects with agents, functions, files and tools', icon: WorkMark },
  { id: 'mcp-gateway', label: 'Vault', description: 'Share secrets and MCP tools with the right users and projects. Check access and inspect call logs.', icon: VaultMark },
  { id: 'llm-gateway', label: 'LLM Gateway', description: 'Give users one endpoint for AI models, with model access, request logs and costs tracked per person.', icon: Network },
  { id: 'knowledgebase', label: 'Brain', description: 'Keep shared knowledge and skills in one place so your agents and team can reuse them.', icon: BrainMark },
  { id: 'bullpen', label: 'AgentBridge', description: 'Connect supported coding chats so agents can find each other, exchange messages and coordinate work.', icon: MessagesSquare },
]

/** The product list with labels, descriptions and marks, shared with the welcome page. */
export const PRODUCT_CARDS = products

export function ProductSurfaceSwitcher({ className, standalone = false, menuOnly = false, onSelect, variant = 'default', menuColumns = 3, menuTitle, menuContent, includeConnectTools = false }: ProductSurfaceSwitcherProps) {
  const sidebar = useProductNavigationSidebar()
  const inSettings = useProductSettingsMenu()
  const inPicker = useInProductPicker()
  const compact = useCompactProductNavigation()
  const pageHeader = useProductNavigationToolbarStore(state => state.targetKind === 'page')
  const productSurface = useProductSurfaceStore((state) => state.productSurface)
  const resourceProduct = productSurface === 'knowledgebase' || productSurface === 'mcp-gateway'
  const allowedProducts = useAuthStore((state) => state.user?.allowed_products)
  const [open, setOpen] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)
  const visibleProductIDs = useMemo(() => navigationProductSurfaceIDs(allowedProducts), [allowedProducts])
  const gatewayUrl = gatewayAdminUrl()
  // The gateway renders inside the app like any other surface, but only
  // exists when a gateway URL is configured for this deployment.
  const visibleProducts = useMemo(() => standalone
    ? products.filter(product => product.id === 'mcp-gateway')
    : products.filter(product => visibleProductIDs.includes(product.id) && (product.id !== 'mcp-gateway' || gatewayUrl !== null)),
  [visibleProductIDs, gatewayUrl, standalone])
  // Dashboards collects existing product publications; it needs no separate product grant.
  const menuProducts = standalone ? visibleProducts : [...visibleProducts, dashboards, ...(includeConnectTools ? [connectTools] : [])]
  const dashboardPage = !standalone && window.location.pathname === '/dashboards'
  const currentProduct = dashboardPage ? dashboards : visibleProducts.find((product) => product.id === (standalone ? 'mcp-gateway' : productSurface)) ?? visibleProducts[0] ?? products[0]
  const CurrentIcon = currentProduct.icon

  useEffect(() => {
    if (standalone || productSurface === 'mcp-gateway') {
      document.title = 'Vault'
      const favicon = document.querySelector<HTMLLinkElement>('link[rel~="icon"]')
      if (favicon) favicon.href = '/vault.svg'
    } else {
      document.title = 'AgentWorks'
    }
    // Deployment branding also owns the Vault title and favicon. The product
    // fallback above is only used when no white label is configured.
    applyRuntimeBranding(window.__APP_RUNTIME_CONFIG__ as Parameters<typeof applyRuntimeBranding>[0])
  }, [productSurface, standalone])

  const activateProduct = (product: ProductSurface) => {
    setOpen(false)
    onSelect?.()
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

  const closeMenu = () => {
    setOpen(false)
    menuRef.current?.querySelector<HTMLButtonElement>('[aria-haspopup="menu"]')?.focus()
  }
  const productGrid = (popup: boolean, className = '') => <ProductSwitchMenu products={menuProducts} current={currentProduct.id} popup={false} nestedMenu={popup || (menuOnly && (inSettings || inPicker))} columns={menuColumns} className={className}
    onSelect={id => {
      if (id === 'dashboards') { setOpen(false); onSelect?.(); openAppPage('/dashboards'); return }
      if (id === 'connect-tools') { setOpen(false); onSelect?.(); openWorkspaceSettings('mcp'); return }
      const product = visibleProducts.find(item => item.id === id); if (product) activateProduct(product.id)
    }}
    onEscape={closeMenu} />
  const productMenu = (popup: boolean, className = '') => popup ? <div role="menu" aria-label={`${menuTitle || currentProduct.label} navigation`}
    className={`flex max-h-[calc(100dvh-64px)] flex-col overflow-hidden rounded-lg border border-border bg-popover text-popover-foreground shadow-xl ${className}`}>
    <ProductPickerMenu title={menuTitle || currentProduct.label} onEscape={closeMenu} products={productGrid(true)}>
      {menuContent ? menuContent(closeMenu) : <div className="p-1"><button type="button" role="menuitem" onClick={() => { closeMenu(); if (currentProduct.id === 'dashboards') openAppPage('/dashboards'); else { const product = visibleProducts.find(item => item.id === currentProduct.id); if (product) activateProduct(product.id) } }} className="flex min-h-8 w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs hover:bg-muted"><CurrentIcon className="h-4 w-4" />{currentProduct.label}</button></div>}
    </ProductPickerMenu>
  </div> : productGrid(false, className)
  if (menuOnly) return productMenu(false, className)

  if (variant !== 'default') {
    const trigger = <button type="button" aria-label={`Switch product: ${currentProduct.label}`} aria-haspopup="menu" aria-expanded={open}
      onClick={() => setOpen(value => !value)} title="Switch product"
      onKeyDown={event => {
        if (event.key !== 'ArrowDown') return
        event.preventDefault(); setOpen(true)
        requestAnimationFrame(() => menuRef.current?.querySelector<HTMLButtonElement>('[role="menuitem"]')?.focus())
      }}
      className={`inline-flex h-7 items-center gap-2 rounded-md text-sm font-semibold hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${variant === 'chevron' ? 'w-6 justify-center text-muted-foreground' : 'text-foreground'}`}>
      {variant === 'header' && <><CurrentIcon className="h-4 w-4 shrink-0" title="" /><span>{currentProduct.label}</span></>}
      <ChevronDown aria-hidden="true" className={`h-3 w-3 transition-transform ${open ? 'rotate-180' : ''}`} />
    </button>
    return <div ref={menuRef} className={cn('relative shrink-0', className)}>
      {variant === 'header' ? <h1>{trigger}</h1> : trigger}
      {open && <div className="absolute left-0 top-full z-50 pt-2">{productMenu(true, 'w-[420px] max-w-[calc(100vw-24px)]')}</div>}
    </div>
  }

  if (compact) return (
    <div ref={menuRef} role="group" aria-label="Products"
      className={sidebar ? 'relative border-b border-border pb-3' : 'relative'}
      onMouseEnter={() => { if (menuProducts.length > 1) setOpen(true) }}
      onMouseLeave={() => setOpen(false)}
      onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false) }}>
      <button type="button" aria-label={`Switch product: ${currentProduct.label}`}
        aria-haspopup={menuProducts.length > 1 ? 'menu' : undefined} aria-expanded={open}
        data-tour-products={visibleProductIDs.join(' ')} data-product-navigation-action
        title={currentProduct.label}
        onClick={() => menuProducts.length > 1 ? setOpen(true) : activateProduct(productSurface)}
        onKeyDown={(event) => {
          if (event.key === 'ArrowDown' && menuProducts.length > 1) {
            event.preventDefault(); setOpen(true)
            requestAnimationFrame(() => menuRef.current?.querySelector<HTMLButtonElement>('[role="menuitem"]')?.focus())
          }
        }}
        className={`relative flex h-8 items-center gap-1.5 rounded-lg px-2 text-foreground transition-colors hover:bg-secondary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary`}>
        {sidebar && <span aria-hidden="true" className="absolute -left-1.5 h-4 w-0.5 rounded-r bg-primary" />}
        <CurrentIcon className="h-4 w-4 shrink-0" title="" />
        <span className={pageHeader || resourceProduct ? 'sr-only' : 'text-xs font-medium'}>{currentProduct.label}</span>{!pageHeader && <ChevronDown className="h-3 w-3" aria-hidden="true" />}
      </button>
      {open && <div className={sidebar ? 'absolute left-full top-0 z-50 pl-2' : 'absolute right-0 top-full z-50 pt-2'}>
        {productMenu(true, 'w-[420px] max-w-[calc(100vw-24px)]')}
      </div>}
    </div>
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
        className="flex h-8 items-center gap-1.5 rounded-lg px-2 text-left text-foreground transition-colors hover:bg-secondary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary"
      >
        <CurrentIcon className="h-4 w-4 shrink-0" title="" />
        <span className={resourceProduct ? 'sr-only' : 'whitespace-nowrap text-xs font-medium'}>{currentProduct.label}</span>
        <ChevronDown className={`h-3 w-3 text-muted-foreground transition-transform ${open ? 'rotate-180' : ''}`} />
      </button>
      {open ? (
        <div className="absolute left-0 top-full z-50 pt-2">{productMenu(true, 'w-[420px] max-w-[calc(100vw-24px)]')}</div>
      ) : null}
    </div>
  )
}

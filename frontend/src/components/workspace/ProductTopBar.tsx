import { lazy, Suspense, useCallback, useEffect, useRef, useState, type CSSProperties, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { useProductNavigationToolbarStore } from '../../stores/useProductNavigationToolbarStore'
import { Home, PanelLeftClose } from 'lucide-react'
import { usePersistentTab } from '../../hooks/usePersistentTab'
import { useWorkspaceReturnStore } from '../../stores/useWorkspaceReturnStore'
import { ProductNavigationContext, CompactProductNavigationContext, useCompactProductNavigation, useProductSettingsMenu, useProductSetupTiles } from './productNavigationContexts'
export { useProductNavigationSidebar, useCompactProductNavigation } from './productNavigationContexts'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { usePointerDrag } from '../../hooks/usePointerDrag'
import { visibleProductSurfaceIDs } from '../../products/productSurfaceConfig'
import { SharedToolShortcuts } from './SharedToolShortcuts'

const WorkspaceChatsSidebar = lazy(() => import('./WorkspaceChatsSidebar'))
const DEFAULT_SIDEBAR_WIDTH = 260
const clampWidth = (width: number) => Math.max(220, Math.min(360, width))
function readWidth(key: string) {
  try {
    const value = Number(window.localStorage.getItem(key))
    return Number.isFinite(value) && value >= 220 && value <= 360 ? value : DEFAULT_SIDEBAR_WIDTH
  } catch { return DEFAULT_SIDEBAR_WIDTH }
}

/** Global controls stay in the header; only the chat list opens and closes. */
export function ProductTopBar({ children, sidebar = true }: { children: ReactNode; sidebar?: boolean }) {
  // Preserve existing workspace-return preferences. The legacy auto-hide value
  // now means explicitly closed; nothing disappears on a timer or pointer leave.
  const [navigationMode, setNavigationMode] = usePersistentTab(
    'product_navigation_mode', 'fixed', ['fixed', 'auto-hide'] as const,
  )
  const restoreNavigation = useWorkspaceReturnStore(state => state.restore?.layout?.navigation_mode)
  useEffect(() => { if (restoreNavigation) setNavigationMode(restoreNavigation) }, [restoreNavigation, setNavigationMode])
  useEffect(() => { useWorkspaceReturnStore.setState({ navigationMode }) }, [navigationMode])
  const open = navigationMode === 'fixed'
  useEffect(() => {
    const showHome = () => setNavigationMode('fixed')
    if (window.location.pathname === '/home') showHome()
    window.addEventListener('open-welcome-home', showHome)
    return () => window.removeEventListener('open-welcome-home', showHome)
  }, [setNavigationMode])
  const ownSidebarSettings = useRef<HTMLDivElement | null>(null)
  const registerSidebarSettings = useCallback((node: HTMLDivElement | null) => {
    const previous = ownSidebarSettings.current
    ownSidebarSettings.current = node
    const state = useProductNavigationToolbarStore.getState()
    if (node && open) useProductNavigationToolbarStore.setState({ sidebarSettingsTarget: node })
    else if (state.sidebarSettingsTarget === previous) useProductNavigationToolbarStore.setState({ sidebarSettingsTarget: null })
  }, [open])
  const ownCompactSettings = useRef<HTMLDivElement | null>(null)
  const registerCompactSettings = useCallback((node: HTMLDivElement | null) => {
    const previous = ownCompactSettings.current
    ownCompactSettings.current = node
    if (node || useProductNavigationToolbarStore.getState().compactSettingsTarget === previous) {
      useProductNavigationToolbarStore.setState({ compactSettingsTarget: node })
    }
  }, [])
  const userId = useAuthStore(state => state.user?.id)
  const allowedProducts = useAuthStore(state => state.user?.allowed_products)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const widthKey = `product_sidebar_width:${server || 'local'}:${userId || 'pending'}`
  const [sizing, setSizing] = useState(() => ({ key: widthKey, width: readWidth(widthKey) }))
  const width = sizing.key === widthKey ? sizing.width : readWidth(widthKey)
  useEffect(() => {
    useProductNavigationToolbarStore.setState({ sidebarWidth: sidebar ? (open ? width : 48) : 0 })
    return () => useProductNavigationToolbarStore.setState({ sidebarWidth: 0 })
  }, [sidebar, open, width])
  const drag = usePointerDrag()
  useEffect(() => { drag.stop(); setSizing({ key: widthKey, width: readWidth(widthKey) }) }, [widthKey, drag.stop])
  const resize = (next: number) => {
    const value = clampWidth(next)
    setSizing({ key: widthKey, width: value })
    try { window.localStorage.setItem(widthKey, String(value)) } catch { /* Optional preference. */ }
  }
  const target = useProductNavigationToolbarStore(state => state.target)
  const leadingTarget = useProductNavigationToolbarStore(state => state.leadingTarget)
  const toggle = sidebar && <button type="button" data-navigation-toggle
    aria-label={open ? 'Close sidebar' : 'Home'} aria-expanded={open}
    title={open ? 'Close your work list' : 'Getting started'}
    onClick={() => {
      if (open) setNavigationMode('auto-hide')
      else window.dispatchEvent(new Event('open-welcome-home'))
    }}
    className="inline-flex h-7 shrink-0 items-center justify-center gap-1.5 rounded-md px-1 text-muted-foreground hover:bg-secondary hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary">
    {open ? <PanelLeftClose className="h-4 w-4" aria-hidden="true" /> : <Home className="h-4 w-4" aria-hidden="true" />}
  </button>
  // Branding stays in the open sidebar; Home also opens Getting started.
  const leading = <div className="flex shrink-0 items-center gap-2 md:hidden">{toggle}</div>
  const controls = <nav aria-label="Product navigation" data-product-navigation="topbar" data-terminal-focus-chrome="header" data-tour-products={visibleProductSurfaceIDs(allowedProducts).join(' ')}
    className="flex min-w-0 max-w-full flex-wrap items-center justify-end gap-1.5">
    {children}
  </nav>
  return <ProductNavigationContext.Provider value={false}>
    <CompactProductNavigationContext.Provider value={sidebar}>
      {sidebar && <div data-terminal-focus-chrome="header" data-product-navigation-mode={navigationMode}
        style={{ '--chat-sidebar-width': `${width}px` } as CSSProperties}
        className={`relative z-40 shrink-0 ${open ? 'w-0 md:w-[var(--chat-sidebar-width)]' : 'w-0 md:w-12'}`}>
        <aside aria-label="Chat navigation" className={`${open ? 'flex' : 'hidden'} absolute inset-y-0 left-0 w-[var(--chat-sidebar-width)] bg-background md:relative md:h-full`}>
          <Suspense fallback={<div className="w-full border-r border-border">{toggle}</div>}><WorkspaceChatsSidebar toggle={toggle} footer={<div ref={registerSidebarSettings} data-sidebar-setup-slot />} /></Suspense>
        </aside>
        {!open && <aside aria-label="Product shortcuts" className="absolute inset-y-0 left-0 hidden w-12 flex-col border-r border-border/70 bg-muted/20 md:flex">
          <div className="flex h-[50px] shrink-0 items-center justify-center">{toggle}</div>
          <div className="mt-auto pb-14"><SharedToolShortcuts compact /></div>
        </aside>}
        {/* The account icon belongs to the rail's stacking context. A fixed
            descendant of the workspace toolbar is covered by this rail. */}
        {!open && <div ref={registerCompactSettings} data-compact-setup-slot className="absolute bottom-3 left-2 z-10 w-8" />}
        {open && <div role="separator" aria-label="Resize your work list" aria-orientation="vertical" aria-valuemin={220} aria-valuemax={360} aria-valuenow={width} tabIndex={0}
          onDoubleClick={() => resize(DEFAULT_SIDEBAR_WIDTH)}
          onPointerDown={event => {
            const left = event.currentTarget.parentElement!.getBoundingClientRect().left
            drag.start(event, { onMove: clientX => resize(clientX - left), onEnd: () => {} })
          }}
          onKeyDown={event => {
            if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
            event.preventDefault()
            resize(event.key === 'Home' ? 220 : event.key === 'End' ? 360 : width + (event.key === 'ArrowLeft' ? -20 : 20))
          }}
          className="absolute inset-y-0 -right-1 z-50 hidden w-2 cursor-col-resize touch-none hover:bg-primary/15 focus-visible:bg-primary/15 focus-visible:outline-none md:block" />}
      </div>}
      {sidebar && !open && (leadingTarget ? createPortal(leading, leadingTarget) : <div data-terminal-focus-chrome="header" className="absolute left-3 top-2 z-40 rounded-lg bg-background/95">{leading}</div>)}
      {target ? createPortal(controls, target) : <div data-terminal-focus-chrome="header" className="absolute right-3 top-2 z-40 max-w-[calc(100%-24px)] rounded-lg bg-background/95">{controls}</div>}
    </CompactProductNavigationContext.Provider>
  </ProductNavigationContext.Provider>
}

export function ProductTopBarMain({ children }: { children: ReactNode }) {
  return <div className="flex min-w-0 flex-wrap items-center gap-2">{children}</div>
}

export function ProductTopBarActions({ children }: { children: ReactNode }) {
  return <div data-product-navigation-section="global-actions" className="ml-auto flex shrink-0 items-center gap-1">{children}</div>
}

export function ProductNavigationLabel({ children }: { children: ReactNode }) {
  const compact = useCompactProductNavigation()
  const inSettings = useProductSettingsMenu()
  return inSettings ? <span data-product-navigation-label>{children}</span> : compact ? <span className="sr-only">{children}</span> : null
}

/** Short help appears only on Setup tiles; compact header controls stay compact. */
export function ProductNavigationDescription({ children }: { children: ReactNode }) {
  const tiles = useProductSetupTiles()
  return tiles ? <span data-product-navigation-description>{children}</span> : null
}

import { lazy, Suspense, useCallback, useEffect, useRef, useState, type CSSProperties } from 'react'
import { createPortal } from 'react-dom'
import { useProductNavigationToolbarStore } from '../../stores/useProductNavigationToolbarStore'
import { PanelLeftClose, PanelLeftOpen } from 'lucide-react'
import { usePersistentTab } from '../../hooks/usePersistentTab'
import { useWorkspaceReturnStore } from '../../stores/useWorkspaceReturnStore'
import { CompactProductNavigationContext } from './productNavigationContexts'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { usePointerDrag } from '../../hooks/usePointerDrag'

import { useAppStore } from '../../stores/useAppStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { useMobileViewport } from '../../hooks/useMobileViewport'

// Warm navigation code alongside the shell; mounting still follows authentication.
const sidebarModule = import('./WorkspaceChatsSidebar')
const WorkspaceChatsSidebar = lazy(() => sidebarModule)
const DEFAULT_SIDEBAR_WIDTH = 260
const clampWidth = (width: number) => Math.max(220, Math.min(360, width))
function readWidth(key: string) {
  try {
    const value = Number(window.localStorage.getItem(key))
    return Number.isFinite(value) && value >= 220 && value <= 360 ? value : DEFAULT_SIDEBAR_WIDTH
  } catch { return DEFAULT_SIDEBAR_WIDTH }
}

/** Persistent work list, rail and sizing. Product pages never own this component. */
export function AppSidebar() {
  // Preserve existing workspace-return preferences. The legacy auto-hide value
  // now means explicitly closed; nothing disappears on a timer or pointer leave.
  const [navigationMode, setNavigationMode] = usePersistentTab(
    'product_navigation_mode', 'fixed', ['fixed', 'auto-hide'] as const,
  )
  const restoredNavigation = useRef(false)
  const restoreNavigation = useWorkspaceReturnStore(state => state.restore?.layout?.navigation_mode)
  useEffect(() => {
    if (restoreNavigation && !restoredNavigation.current) {
      restoredNavigation.current = true
      setNavigationMode(restoreNavigation)
    }
  }, [restoreNavigation, setNavigationMode])
  useEffect(() => { useWorkspaceReturnStore.setState({ navigationMode }) }, [navigationMode])
  const product = useProductSurfaceStore(state => state.productSurface)
  const page = useAppStore(state => `${state.adminPage}:${state.showSchedulesOverview}:${state.showWorkflowsOverview}`)
  const isMobile = useMobileViewport()
  const [mobileOpen, setMobileOpen] = useState(false)
  const drawer = useRef<HTMLDivElement>(null)
  useEffect(() => setMobileOpen(false), [product, page])
  const open = isMobile ? mobileOpen : navigationMode === 'fixed'
  // Closed on a computer means gone (owner, 5 Nov): no icon rail. Moving the pointer to the left edge slides the full
  // menu out over the page (peek); leaving it hides it again; its own button pins it open.
  const [peek, setPeek] = useState(false)
  const peekTimer = useRef<number | undefined>(undefined)
  const peeking = !isMobile && !open && peek
  const shown = open || peeking
  const peekIn = () => { window.clearTimeout(peekTimer.current); setPeek(true) }
  const peekOut = () => { window.clearTimeout(peekTimer.current); peekTimer.current = window.setTimeout(() => setPeek(false), 250) }
  useEffect(() => { if (open) setPeek(false) }, [open])
  const peekPanel = useRef<HTMLElement>(null)
  useEffect(() => {
    if (!peeking) return
    const key = (event: KeyboardEvent) => { if (event.key === 'Escape' && !event.defaultPrevented) setPeek(false) }
    // The menu slides out under a still pointer, so the browser may never send it mouseleave: watch where the pointer
    // is instead. Off the menu (and not in a menu or popover it opened) starts the same short close.
    const move = (event: MouseEvent) => {
      const panel = peekPanel.current
      const target = event.target as Element | null
      if (!panel || panel.contains(target) || target?.closest?.('[data-radix-popper-content-wrapper],[role="menu"],[role="dialog"]')) {
        window.clearTimeout(peekTimer.current)
        return
      }
      if (event.clientX > panel.getBoundingClientRect().right) peekOut()
    }
    document.addEventListener('keydown', key)
    document.addEventListener('mousemove', move)
    return () => { document.removeEventListener('keydown', key); document.removeEventListener('mousemove', move) }
  }, [peeking])
  useEffect(() => () => window.clearTimeout(peekTimer.current), [])
  useEffect(() => {
    if (!isMobile || !mobileOpen) return
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null
    const main = document.querySelector<HTMLElement>('[data-app-main]')
    if (main) main.inert = true
    drawer.current?.querySelector<HTMLElement>('button, a, input, [tabindex="0"]')?.focus()
    const key = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !event.defaultPrevented) setMobileOpen(false)
    }
    document.addEventListener('keydown', key)
    return () => {
      if (main) main.inert = false
      document.removeEventListener('keydown', key)
      if (previous?.isConnected) previous.focus()
    }
  }, [isMobile, mobileOpen])
  useEffect(() => {
    const close = () => setMobileOpen(false)
    window.addEventListener('popstate', close)
    window.addEventListener('close-app-sidebar', close)
    return () => { window.removeEventListener('popstate', close); window.removeEventListener('close-app-sidebar', close) }
  }, [])
  useEffect(() => {
    const showSidebar = () => { if (isMobile) setMobileOpen(true); else { restoredNavigation.current = true; setNavigationMode('fixed') } }
    window.addEventListener('open-app-sidebar', showSidebar)
    return () => window.removeEventListener('open-app-sidebar', showSidebar)
  }, [isMobile, setNavigationMode])
  const ownSidebarSettings = useRef<HTMLDivElement | null>(null)
  const registerSidebarSettings = useCallback((node: HTMLDivElement | null) => {
    const previous = ownSidebarSettings.current
    ownSidebarSettings.current = node
    const state = useProductNavigationToolbarStore.getState()
    if (node && shown) useProductNavigationToolbarStore.setState({ sidebarSettingsTarget: node })
    else if (state.sidebarSettingsTarget === previous) useProductNavigationToolbarStore.setState({ sidebarSettingsTarget: null })
  }, [shown])
  const userId = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const widthKey = `product_sidebar_width:${server || 'local'}:${userId || 'pending'}`
  const [sizing, setSizing] = useState(() => ({ key: widthKey, width: readWidth(widthKey) }))
  const width = sizing.key === widthKey ? sizing.width : readWidth(widthKey)
  useEffect(() => {
    useProductNavigationToolbarStore.setState({ sidebarWidth: isMobile ? 0 : open ? width : 0 })
    return () => useProductNavigationToolbarStore.setState({ sidebarWidth: 0 })
  }, [isMobile, open, width])
  const drag = usePointerDrag()
  useEffect(() => { drag.stop(); setSizing({ key: widthKey, width: readWidth(widthKey) }) }, [widthKey, drag.stop])
  const resize = (next: number) => {
    const value = clampWidth(next)
    setSizing({ key: widthKey, width: value })
    try { window.localStorage.setItem(widthKey, String(value)) } catch { /* Optional preference. */ }
  }
  const leadingTarget = useProductNavigationToolbarStore(state => state.leadingTarget)
  const toggle = <button type="button" data-navigation-toggle
    aria-label={open ? 'Close sidebar' : 'Open sidebar'} aria-expanded={open}
    title={open ? 'Close sidebar' : 'Open sidebar'}
    onClick={() => {
      if (isMobile) setMobileOpen(value => !value)
      else { restoredNavigation.current = true; setPeek(false); setNavigationMode(open ? 'auto-hide' : 'fixed') }
    }}
    className="inline-flex h-11 w-11 md:h-7 md:w-auto shrink-0 items-center justify-center gap-1.5 rounded-md px-1 text-muted-foreground hover:bg-secondary hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary">
    {open ? <PanelLeftClose className="h-4 w-4" aria-hidden="true" /> : <PanelLeftOpen className="h-4 w-4" aria-hidden="true" />}
  </button>
  // With the menu closed on a computer, its button sits in the top bar so one click also opens it.
  const leading = <div className="flex shrink-0 items-center gap-1">{toggle}</div>
  return <CompactProductNavigationContext.Provider value={true}>
      <div data-terminal-focus-chrome="header" data-app-sidebar data-product-navigation-mode={navigationMode}
        style={{ '--chat-sidebar-width': `${width}px` } as CSSProperties}
        className={`relative z-40 shrink-0 ${open ? 'w-0 md:w-[var(--chat-sidebar-width)]' : 'w-0'}`}>
        {isMobile ? mobileOpen && <div className="fixed inset-x-0 top-[var(--app-viewport-top,0px)] z-40 h-[var(--app-viewport-height,100dvh)]">
          <button type="button" aria-label="Close sidebar backdrop" onClick={() => setMobileOpen(false)} className="absolute inset-0 bg-black/50" />
          <div ref={drawer} role="dialog" aria-modal="true" aria-label="Your work" className="relative h-full w-[min(320px,calc(100vw-3rem))] border-r border-border bg-background text-foreground shadow-xl">
          <aside aria-label="Chat navigation" className="flex h-full w-full pb-[env(safe-area-inset-bottom)]">
            <Suspense fallback={<div className="w-full border-r border-border">{toggle}</div>}><WorkspaceChatsSidebar toggle={toggle} footer={<div ref={registerSidebarSettings} data-sidebar-setup-slot />} /></Suspense>
          </aside>
        </div></div> : <aside ref={peekPanel} aria-label="Chat navigation" onMouseEnter={peeking ? peekIn : undefined} onMouseLeave={peeking ? peekOut : undefined}
          className={`${shown ? 'flex' : 'hidden'} absolute inset-y-0 left-0 w-[var(--chat-sidebar-width)] bg-background ${peeking ? 'z-50 h-full border-r border-border shadow-2xl' : 'md:relative md:h-full'}`}>
          <Suspense fallback={<div className="w-full border-r border-border">{toggle}</div>}><WorkspaceChatsSidebar toggle={toggle} footer={<div ref={registerSidebarSettings} data-sidebar-setup-slot />} /></Suspense>
        </aside>}
        {/* The left edge opens the menu while it is closed (no rail). */}
        {!isMobile && !open && !peeking && <div aria-hidden="true" data-sidebar-peek-edge onMouseEnter={peekIn} className="fixed inset-y-0 left-0 z-50 w-2" />}
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
      </div>
      {/* One sidebar button: while the phone drawer is open, its own close button is the only one. */}
      {(isMobile ? !mobileOpen : !open) && (leadingTarget ? createPortal(leading, leadingTarget) : <div data-terminal-focus-chrome="header" className="absolute left-3 top-2 z-40 rounded-lg bg-background/95">{leading}</div>)}
  </CompactProductNavigationContext.Provider>
}


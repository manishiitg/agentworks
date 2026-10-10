import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import { PanelLeft, Pin, PinOff } from 'lucide-react'
import { usePersistentTab } from '../../hooks/usePersistentTab'

const NAVIGATION_IDLE_MS = 10 * 60 * 1000
const NAVIGATION_ACTIVITY_KEY = 'product_navigation_last_activity'
const NAVIGATION_HINT_DISMISSED_KEY = 'product_navigation_hint_dismissed'
const NAVIGATION_HINT_EXPIRES_KEY = 'product_navigation_hint_expires_at'
const NAVIGATION_HINT_DURATION_MS = 8_000

const ProductNavigationContext = createContext(false)
export const useProductNavigationSidebar = () => useContext(ProductNavigationContext)

/** Shared global navigation. Product controls keep their existing behavior. */
export function ProductTopBar({ children, sidebar = true }: { children: ReactNode; sidebar?: boolean }) {
  const [navigationMode, setNavigationMode] = usePersistentTab(
    'product_navigation_mode', 'fixed', ['fixed', 'auto-hide'] as const,
  )
  const autoHide = navigationMode === 'auto-hide'
  const [revealed, setRevealed] = useState(false)
  const hintExpiresAt = useRef<number | null>(null)
  const [navigationHintDismissed, setNavigationHintDismissed] = useState(() => {
    try {
      const expiresAt = Number(window.sessionStorage.getItem(NAVIGATION_HINT_EXPIRES_KEY))
      return window.sessionStorage.getItem(NAVIGATION_HINT_DISMISSED_KEY) === 'true' || (expiresAt > 0 && expiresAt <= Date.now())
    } catch { return false }
  })
  const rememberHintDismissed = useCallback((dismissed: boolean) => {
    if (!dismissed) hintExpiresAt.current = Date.now() + NAVIGATION_HINT_DURATION_MS
    setNavigationHintDismissed(dismissed)
    try {
      window.sessionStorage.setItem(NAVIGATION_HINT_DISMISSED_KEY, String(dismissed))
      if (!dismissed) window.sessionStorage.setItem(NAVIGATION_HINT_EXPIRES_KEY, String(hintExpiresAt.current))
    } catch { /* Storage is optional. */ }
  }, [])
  useEffect(() => {
    const dismissHint = () => rememberHintDismissed(true)
    window.addEventListener('quick-switcher-opened', dismissHint)
    return () => window.removeEventListener('quick-switcher-opened', dismissHint)
  }, [rememberHintDismissed])
  useEffect(() => {
    if (!sidebar || !autoHide || navigationHintDismissed) return
    if (hintExpiresAt.current === null) {
      let stored = 0
      try { stored = Number(window.sessionStorage.getItem(NAVIGATION_HINT_EXPIRES_KEY)) } catch { /* Storage is optional. */ }
      hintExpiresAt.current = stored > 0 ? stored : Date.now() + NAVIGATION_HINT_DURATION_MS
      try { window.sessionStorage.setItem(NAVIGATION_HINT_EXPIRES_KEY, String(hintExpiresAt.current)) } catch { /* Storage is optional. */ }
    }
    const dismissIfExpired = () => {
      if (Date.now() >= hintExpiresAt.current!) rememberHintDismissed(true)
    }
    const remaining = hintExpiresAt.current - Date.now()
    if (remaining <= 0) { rememberHintDismissed(true); return }
    const timer = window.setTimeout(() => rememberHintDismissed(true), remaining)
    window.addEventListener('focus', dismissIfExpired)
    document.addEventListener('visibilitychange', dismissIfExpired)
    return () => {
      window.clearTimeout(timer)
      window.removeEventListener('focus', dismissIfExpired)
      document.removeEventListener('visibilitychange', dismissIfExpired)
    }
  }, [sidebar, autoHide, revealed, navigationHintDismissed, rememberHintDismissed])
  // Session storage carries the deadline across product remounts and reloads.
  const lastActivity = useRef<number | null>(null)
  const idleTimer = useRef<number | undefined>(undefined)
  const expireNavigation = useRef(() => {})
  expireNavigation.current = () => {
    if (!autoHide || revealed) rememberHintDismissed(false)
    setRevealed(false)
    setNavigationMode('auto-hide')
  }
  const scheduleIdle = useCallback(() => {
    window.clearTimeout(idleTimer.current)
    const remaining = NAVIGATION_IDLE_MS - (Date.now() - (lastActivity.current ?? Date.now()))
    idleTimer.current = window.setTimeout(() => expireNavigation.current(), Math.max(0, remaining))
  }, [])
  const navigationActivity = useCallback(() => {
    lastActivity.current = Date.now()
    try { window.sessionStorage.setItem(NAVIGATION_ACTIVITY_KEY, String(lastActivity.current)) } catch { /* Storage is optional. */ }
    scheduleIdle()
  }, [scheduleIdle])
  useEffect(() => {
    if (!sidebar) return
    if (lastActivity.current === null) {
      let stored = 0
      try { stored = Number(window.sessionStorage.getItem(NAVIGATION_ACTIVITY_KEY)) } catch { /* Storage is optional. */ }
      lastActivity.current = stored > 0 && stored <= Date.now() ? stored : Date.now()
      try { window.sessionStorage.setItem(NAVIGATION_ACTIVITY_KEY, String(lastActivity.current)) } catch { /* Storage is optional. */ }
    }
    scheduleIdle()
    // Background tabs throttle timers; check the deadline immediately on return.
    const checkDeadline = () => {
      if (Date.now() - (lastActivity.current ?? Date.now()) >= NAVIGATION_IDLE_MS) expireNavigation.current()
    }
    document.addEventListener('visibilitychange', checkDeadline)
    window.addEventListener('focus', checkDeadline)
    return () => {
      window.clearTimeout(idleTimer.current)
      document.removeEventListener('visibilitychange', checkDeadline)
      window.removeEventListener('focus', checkDeadline)
    }
  }, [sidebar, scheduleIdle])
  return <ProductNavigationContext.Provider value={sidebar}>
    {/* Navigation flyouts sit above workspace toolbars (z-30), below dialogs (z-50). */}
    {sidebar ? <div data-terminal-focus-chrome="header" data-product-navigation-mode={navigationMode}
      onPointerEnter={() => { navigationActivity(); if (autoHide) setRevealed(true) }}
      onPointerLeave={() => setRevealed(false)}
      onPointerMove={navigationActivity} onPointerDown={navigationActivity} onWheel={navigationActivity}
      onFocusCapture={() => { navigationActivity(); if (autoHide) setRevealed(true) }} onKeyDownCapture={navigationActivity}
      onBlurCapture={event => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setRevealed(false) }}
      className={`relative z-40 shrink-0 ${autoHide ? 'w-0' : 'w-12'}`}>
      {autoHide && <button type="button" aria-label="Show navigation" title="Show navigation"
        onClick={() => { navigationActivity(); setNavigationMode('fixed') }}
        className="absolute inset-y-0 left-0 w-1.5 outline-none" />}
      {/* Use left rather than transform: fixed-position selector/monitor
          flyouts must keep the viewport as their containing block. The rail
          stays mounted so hiding it never stops the global activity monitor. */}
      <nav aria-label="Product navigation" data-product-navigation="sidebar"
        className={`${autoHide ? `absolute inset-y-0 ${revealed ? 'left-0 shadow-xl' : '-left-12'}` : 'relative h-full'} flex w-12 flex-col border-r border-border bg-background px-1.5 py-3`}>
        {children}
        <button type="button" aria-label="Keep navigation fixed" aria-pressed={!autoHide}
          title={autoHide ? 'Navigation: Auto-hide · click to keep fixed' : 'Navigation: Open · hides after 10 minutes without navigation activity · click to hide now'}
          onClick={() => {
            navigationActivity()
            setRevealed(false)
            setNavigationMode(autoHide ? 'fixed' : 'auto-hide')
            if (!autoHide) rememberHintDismissed(false)
          }}
          className="mt-2 grid h-7 w-full shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-secondary hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary">
          {autoHide ? <PinOff className="h-3.5 w-3.5" aria-hidden="true" /> : <Pin className="h-3.5 w-3.5" aria-hidden="true" />}
        </button>
      </nav>
    </div> : <div data-terminal-focus-chrome="header" className="shrink-0 border-b border-border bg-muted px-4 py-2">
      <div className="flex flex-wrap items-center justify-between gap-3 md:flex-nowrap">{children}</div>
    </div>}
    {sidebar && autoHide && !revealed && !navigationHintDismissed && <div data-terminal-focus-chrome="header" data-navigation-shortcut-hint
      className="fixed bottom-3 left-3 z-40 flex items-center gap-1 rounded-md border border-border bg-popover p-1 text-muted-foreground shadow-sm">
      <button type="button" aria-label="Reopen navigation" title="Reopen navigation · hides after 10 minutes without navigation activity"
        className="rounded p-1 hover:bg-secondary hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary"
        onClick={() => { navigationActivity(); setNavigationMode('fixed') }}><PanelLeft className="h-3.5 w-3.5" /></button>
      <button type="button" aria-label="Open quick navigation (Ctrl+K or Command+K)" title="Search products, projects, chats and panels"
        className="rounded px-1.5 py-1 text-[10px] hover:bg-secondary hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary"
        onClick={() => {
          rememberHintDismissed(true)
          window.dispatchEvent(new CustomEvent('open-quick-switcher'))
        }}><kbd>Ctrl+K / ⌘K</kbd></button>
    </div>}
  </ProductNavigationContext.Provider>
}

export function ProductTopBarMain({ children }: { children: ReactNode }) {
  const sidebar = useProductNavigationSidebar()
  return <div className={sidebar ? 'flex min-w-0 flex-col items-stretch gap-3' : 'flex min-w-0 items-center gap-3'}>{children}</div>
}

export function ProductTopBarActions({ children }: { children: ReactNode }) {
  const sidebar = useProductNavigationSidebar()
  return <div data-product-navigation-section="global-actions" className={sidebar ? 'mt-auto flex min-w-0 flex-col items-stretch gap-1 pt-5' : 'flex shrink-0 items-center gap-2'}>{children}</div>
}

export function ProductNavigationLabel({ children }: { children: ReactNode }) {
  return useProductNavigationSidebar() ? <span className="sr-only">{children}</span> : null
}

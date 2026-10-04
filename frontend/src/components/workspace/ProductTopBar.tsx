import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import { Pin, PinOff, X } from 'lucide-react'
import { usePersistentTab } from '../../hooks/usePersistentTab'

const ProductNavigationContext = createContext(false)
export const useProductNavigationSidebar = () => useContext(ProductNavigationContext)

/** Shared global navigation. Product controls keep their existing behavior. */
export function ProductTopBar({ children, sidebar = true }: { children: ReactNode; sidebar?: boolean }) {
  const [navigationMode, setNavigationMode] = usePersistentTab(
    'product_navigation_mode', 'fixed', ['fixed', 'auto-hide'] as const,
  )
  const autoHide = navigationMode === 'auto-hide'
  const [showNavigationHint, setShowNavigationHint] = useState(false)
  useEffect(() => {
    if (!showNavigationHint) return
    const timer = window.setTimeout(() => setShowNavigationHint(false), 8000)
    return () => window.clearTimeout(timer)
  }, [showNavigationHint])
  return <ProductNavigationContext.Provider value={sidebar}>
    {/* Navigation flyouts sit above workspace toolbars (z-30), below dialogs (z-50). */}
    {sidebar ? <div data-terminal-focus-chrome="header" data-product-navigation-mode={navigationMode}
      className={`group/navigation relative z-40 shrink-0 ${autoHide ? 'w-0' : 'w-12'}`}>
      {autoHide && <button type="button" aria-label="Show navigation" title="Show navigation"
        className="absolute inset-y-0 left-0 w-1.5 outline-none" />}
      {/* Use left rather than transform: fixed-position selector/monitor
          flyouts must keep the viewport as their containing block. The rail
          stays mounted so hiding it never stops the global activity monitor. */}
      <nav aria-label="Product navigation" data-product-navigation="sidebar"
        className={`${autoHide ? 'absolute inset-y-0 -left-12 group-hover/navigation:left-0 group-hover/navigation:shadow-xl group-has-[:focus-visible]/navigation:left-0 group-has-[:focus-visible]/navigation:shadow-xl' : 'relative h-full'} flex w-12 flex-col border-r border-border bg-background px-1.5 py-3`}>
        {children}
        <button type="button" aria-label="Keep navigation fixed" aria-pressed={!autoHide}
          title={autoHide ? 'Navigation: Auto-hide · click to keep fixed' : 'Navigation: Fixed · click to auto-hide'}
          onClick={() => {
            setNavigationMode(autoHide ? 'fixed' : 'auto-hide')
            setShowNavigationHint(!autoHide)
          }}
          className="mt-2 grid h-7 w-full shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-secondary hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary">
          {autoHide ? <PinOff className="h-3.5 w-3.5" aria-hidden="true" /> : <Pin className="h-3.5 w-3.5" aria-hidden="true" />}
        </button>
      </nav>
    </div> : <div data-terminal-focus-chrome="header" className="shrink-0 border-b border-border bg-muted px-4 py-2">
      <div className="flex flex-wrap items-center justify-between gap-3 md:flex-nowrap">{children}</div>
    </div>}
    {showNavigationHint && <div data-terminal-focus-chrome="header" role="status"
      className="fixed bottom-3 left-3 z-50 flex max-w-[calc(100vw-1.5rem)] items-start gap-3 rounded-lg border border-border bg-popover p-3 text-popover-foreground shadow-lg">
      <div className="text-xs leading-5">
        <p>Navigation hidden. Press <kbd className="font-semibold">Ctrl+K</kbd> (<kbd className="font-semibold">⌘K</kbd> on Mac) to switch running work, products, or menus.</p>
        <button type="button" className="font-medium text-primary hover:underline" onClick={() => {
          setShowNavigationHint(false)
          window.dispatchEvent(new CustomEvent('open-quick-switcher'))
        }}>Open quick navigation</button>
      </div>
      <button type="button" aria-label="Dismiss navigation hint" onClick={() => setShowNavigationHint(false)}
        className="rounded p-0.5 text-muted-foreground hover:bg-secondary hover:text-foreground"><X className="h-3.5 w-3.5" /></button>
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

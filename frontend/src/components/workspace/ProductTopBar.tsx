import { createContext, useContext, type ReactNode } from 'react'
import { Pin, PinOff } from 'lucide-react'
import { usePersistentTab } from '../../hooks/usePersistentTab'

const ProductNavigationContext = createContext(false)
export const useProductNavigationSidebar = () => useContext(ProductNavigationContext)

/** Shared global navigation. Product controls keep their existing behavior. */
export function ProductTopBar({ children, sidebar = true }: { children: ReactNode; sidebar?: boolean }) {
  const [navigationMode, setNavigationMode] = usePersistentTab(
    'product_navigation_mode', 'fixed', ['fixed', 'auto-hide'] as const,
  )
  const autoHide = navigationMode === 'auto-hide'
  return <ProductNavigationContext.Provider value={sidebar}>
    {/* Navigation flyouts sit above workspace toolbars (z-30), below dialogs (z-50). */}
    {sidebar ? <div data-terminal-focus-chrome="header" data-product-navigation-mode={navigationMode}
      className={`group/navigation relative z-40 shrink-0 ${autoHide ? 'w-1.5 bg-background' : 'w-12'}`}>
      {autoHide && <button type="button" aria-label="Show navigation" title="Show navigation"
        className="absolute inset-0 border-r border-border outline-none hover:bg-secondary focus-visible:bg-secondary" />}
      {/* Use left rather than transform: fixed-position selector/monitor
          flyouts must keep the viewport as their containing block. The rail
          stays mounted so hiding it never stops the global activity monitor. */}
      <nav aria-label="Product navigation" data-product-navigation="sidebar"
        className={`${autoHide ? 'absolute inset-y-0 -left-12 shadow-xl group-hover/navigation:left-0 group-has-[:focus-visible]/navigation:left-0' : 'relative h-full'} flex w-12 flex-col border-r border-border bg-background px-1.5 py-3`}>
        {children}
        <button type="button" aria-label="Keep navigation fixed" aria-pressed={!autoHide}
          title={autoHide ? 'Navigation: Auto-hide · click to keep fixed' : 'Navigation: Fixed · click to auto-hide'}
          onClick={() => setNavigationMode(autoHide ? 'fixed' : 'auto-hide')}
          className="mt-2 grid h-7 w-full shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-secondary hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary">
          {autoHide ? <PinOff className="h-3.5 w-3.5" aria-hidden="true" /> : <Pin className="h-3.5 w-3.5" aria-hidden="true" />}
        </button>
      </nav>
    </div> : <div data-terminal-focus-chrome="header" className="shrink-0 border-b border-border bg-muted px-4 py-2">
      <div className="flex flex-wrap items-center justify-between gap-3 md:flex-nowrap">{children}</div>
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

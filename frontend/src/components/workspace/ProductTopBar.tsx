import { createContext, useContext, type ReactNode } from 'react'

const ProductNavigationContext = createContext(false)
export const useProductNavigationSidebar = () => useContext(ProductNavigationContext)

/** Shared global navigation. Product controls keep their existing behavior. */
export function ProductTopBar({ children, sidebar = true }: { children: ReactNode; sidebar?: boolean }) {
  return <ProductNavigationContext.Provider value={sidebar}>
    {/* Navigation flyouts sit above workspace toolbars (z-30), below dialogs (z-50). */}
    {sidebar ? <nav aria-label="Product navigation" data-terminal-focus-chrome="header" data-product-navigation="sidebar"
      className="relative z-40 flex w-14 shrink-0 flex-col border-r border-border bg-background px-2 py-3">
      {children}
    </nav> : <div data-terminal-focus-chrome="header" className="shrink-0 border-b border-border bg-muted px-4 py-2">
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
  return <div className={sidebar ? 'mt-auto flex min-w-0 flex-col items-stretch gap-1 pt-5' : 'flex shrink-0 items-center gap-2'}>{children}</div>
}

export function ProductNavigationLabel({ children }: { children: ReactNode }) {
  return useProductNavigationSidebar() ? <span className="sr-only">{children}</span> : null
}

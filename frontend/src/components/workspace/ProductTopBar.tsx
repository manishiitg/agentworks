import { type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { useProductNavigationToolbarStore } from '../../stores/useProductNavigationToolbarStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { visibleProductSurfaceIDs } from '../../products/productSurfaceConfig'
import { ProductNavigationContext, CompactProductNavigationContext, useCompactProductNavigation, useProductSettingsMenu, useProductSetupTiles } from './productNavigationContexts'
export { useProductNavigationSidebar, useCompactProductNavigation } from './productNavigationContexts'

/** A product's header controls. The app shell owns the left menu and layout. */
export function ProductTopBar({ children }: { children: ReactNode }) {
  const target = useProductNavigationToolbarStore(state => state.target)
  const allowedProducts = useAuthStore(state => state.user?.allowed_products)
  const controls = <nav aria-label="Product navigation" data-product-navigation="topbar" data-terminal-focus-chrome="header" data-tour-products={visibleProductSurfaceIDs(allowedProducts).join(' ')}
    className="flex min-w-0 max-w-full flex-wrap items-center justify-end gap-1.5">{children}</nav>
  return <ProductNavigationContext.Provider value={false}>
    <CompactProductNavigationContext.Provider value={true}>
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

import type { MouseEvent } from 'react'
import { parseProductRoute, productRoutePath, type ProductRoute } from './productRoutes'

/** Navigate a top-level app page without replacing the document or its shell. */
export function openAppPage(path: '/home' | '/dashboards'): void {
  if (window.location.pathname + window.location.search === path) return
  window.history.pushState({}, '', path)
  window.dispatchEvent(new PopStateEvent('popstate'))
}

/** Resolve a product link through the existing account/project checks. */
export function openAppProduct(route: ProductRoute): void {
  const path = productRoutePath(route)
  if (window.location.pathname + window.location.search === path) return
  window.history.pushState({}, '', path)
  window.dispatchEvent(new PopStateEvent('popstate'))
}

/** Plain in-app link clicks use history; modified clicks remain ordinary links. */
export function followAppLink(event: MouseEvent<HTMLAnchorElement>): void {
  const link = event.currentTarget
  if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || link.hasAttribute('download') || (link.target && link.target !== '_self')) return
  const destination = new URL(link.href, window.location.href)
  if (destination.origin !== window.location.origin) return
  const path = destination.pathname + destination.search
  if (path === '/home' || path === '/dashboards') {
    event.preventDefault(); openAppPage(path); return
  }
  const route = parseProductRoute(path)
  if (!route) return
  event.preventDefault(); openAppProduct(route)
}

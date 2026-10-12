import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { deploymentDefaultProductSurface, enabledProductSurfaces, intersectAllowedProductSurfaces } from '../products/productSurfaceConfig'
import { waitForChatStoreHydration } from '../stores/useChatStore'
import { useAuthStore } from '../stores/useAuthStore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import { isLegacyAppPath, parseProductRoute, productRoutePath } from '../utils/productRoutes'
import { observeProductHistory, readProductNavigation } from '../utils/productUrlNavigation'
import { cancelPendingWorkflowNavigation } from '../utils/workflowNavigation'
import { ProductUnavailable, resolveProductRoute } from '../utils/resolveProductRoute'

type Status = 'loading' | 'ready' | 'disabled' | 'unavailable' | 'error'
export function ProductRouteBoundary({ children, account, allowedProducts }: { children: ReactNode; account?: string; allowedProducts?: string[] | null }) {
  const [location, setLocation] = useState(() => window.location.pathname + window.location.search)
  const [attempt, setAttempt] = useState(0)
  const permissionKey = allowedProducts == null ? 'unrestricted' : JSON.stringify([...allowedProducts].map(value => value.toLowerCase()).sort())
  const activeResolution = useRef<AbortController | null>(null)
  const [resolution, setResolution] = useState<{ location: string; account?: string; status: Status; attempt: number; permissionKey: string }>({ location: '', status: 'loading', attempt: 0, permissionKey: '' })
  const status = resolution.location === location && resolution.account === account && resolution.attempt === attempt && resolution.permissionKey === permissionKey ? resolution.status : 'loading'
  useLayoutEffect(() => {
    const onPop = () => {
      activeResolution.current?.abort()
      cancelPendingWorkflowNavigation()
      // Store-driven pushes need no rehydration. A later Back to the initial
      // path can have the same cached URL string, but is still a new intent.
      setAttempt(value => value + 1)
      setLocation(window.location.pathname + window.location.search)
    }
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])
  useEffect(() => {
    const controller = new AbortController()
    activeResolution.current = controller
    const finish = (status: Status) => { if (!controller.signal.aborted) setResolution({ location, account, status, attempt, permissionKey }) }
    const requested = window.location.pathname + window.location.search
    let route = parseProductRoute(requested)
    const enabled = enabledProductSurfaces()
    const allowed = intersectAllowedProductSurfaces(enabled, allowedProducts)
    if (route === undefined && isLegacyAppPath(requested) && allowed.length) {
      window.history.replaceState({}, '', '/')
      const preferred = deploymentDefaultProductSurface()
      route = { surface: allowed.includes(preferred) ? preferred : allowed[0] }
    }
    if (route === undefined) { finish('unavailable'); return () => controller.abort() }
    if (route && !enabled.includes(route.surface)) { finish('disabled'); return () => controller.abort() }
    if (!allowed.length || (route && !allowed.includes(route.surface))) { finish('unavailable'); return () => controller.abort() }
    if (!route) {
      const state = useProductSurfaceStore.getState()
      if (!allowed.includes(state.productSurface)) state.setProductSurface(allowed[0] ?? deploymentDefaultProductSurface())
      // Preserve CLI connection query arguments until App consumes them.
      if (!window.location.search) window.history.replaceState({}, '', productRoutePath(readProductNavigation()))
      finish('ready')
    } else {
      const destination = route
      finish('loading')
      void waitForChatStoreHydration().then(() => {
        if (controller.signal.aborted) return () => {}
        return resolveProductRoute(destination, controller.signal)
      }).then(activate => {
        if (controller.signal.aborted) return
        activate()
        window.history.replaceState({}, '', productRoutePath(destination))
        finish('ready')
      }).catch(error => {
        const code = error?.response?.status
        if (controller.signal.aborted) return
        if (code === 401) {
          // Re-check the session through the existing auth lifecycle. An expired
          // session mounts Login at this URL; a valid session gets Retry instead.
          void useAuthStore.getState().checkAuth({ background: true }).then(() => {
            if (useAuthStore.getState().isAuthenticated) finish('error')
          })
          return
        }
        finish(error instanceof ProductUnavailable || code === 403 || code === 404 ? 'unavailable' : 'error')
      })
    }
    return () => { controller.abort(); cancelPendingWorkflowNavigation() }
  }, [location, account, permissionKey, attempt])
  useEffect(() => status === 'ready' ? observeProductHistory() : undefined, [status, location, account])
  if (status === 'ready') return <>{children}</>
  const text = status === 'loading' ? 'Opening link…' : status === 'disabled' ? 'This product is not enabled on this server.' : status === 'error' ? 'Could not open this link. Please try again.' : 'This item is not available. You may not have access.'
  return <main className="grid min-h-screen place-items-center p-6"><div className="max-w-md space-y-4 text-center"><p role={status === 'loading' ? 'status' : 'alert'}>{text}</p>{status !== 'loading' && <div className="flex justify-center gap-4"><a className="underline" href="/">Home</a>{status === 'error' && <button className="underline" onClick={() => { activeResolution.current?.abort(); setAttempt(value => value + 1) }}>Retry</button>}</div>}</div></main>
}

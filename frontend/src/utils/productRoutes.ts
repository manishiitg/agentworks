import type { ProductSurface } from '../products/productSurfaceConfig'

export type ProductRoute = { surface: ProductSurface; id?: string; view?: string; folder?: string; overview?: boolean }
export const PRODUCT_PATHS: Record<ProductSurface, string> = {
  agentworks: '/goals', relays: '/relays', work: '/crew',
  knowledgebase: '/brain', 'mcp-gateway': '/vault', 'llm-gateway': '/llm-gateway', bullpen: '/agentbridge',
}
// The product was called Bullpen; its old address still opens it (and is shown as /agentbridge).
export const PRODUCT_ROUTE_ALIASES: Record<string, ProductSurface> = { bullpen: 'bullpen', agentworks: 'agentworks', work: 'work', knowledgebase: 'knowledgebase', 'mcp-gateway': 'mcp-gateway' }
const surfaces = Object.fromEntries(Object.entries(PRODUCT_PATHS).map(([id, path]) => [path.slice(1), id])) as Record<string, ProductSurface>
const segment = (value: string) => value.length > 0 && value.length <= 256 && !/[%\\/\x00-\x1f\x7f]/.test(value) && value !== '.' && value !== '..'

export function relativeBrainFolder(value: string): boolean {
  return value.length <= 2048 && !/[\\\x00-\x1f\x7f]/.test(value) && !value.startsWith('/') && !/^[a-z]:/i.test(value) && (value === '' || value.split('/').every(segment))
}

/** Check before URL normalization, including nested encodings used by redirects. */
export function safeRelativeAppPath(value: string | null): string | null {
  if (!value || value.length > 8192) return null
  let decoded = value.split(/[?#]/, 1)[0]
  for (let i = 0; i < 6; i++) {
    if (!decoded.startsWith('/') || decoded.startsWith('//') || /[\\\x00-\x20\x7f]/.test(decoded) || decoded.split('/').some(part => part === '.' || part === '..')) return null
    try {
      const next = decodeURIComponent(decoded)
      if (next === decoded) {
        const url = new URL(value, 'https://agentworks.invalid')
        return url.origin === 'https://agentworks.invalid' ? url.pathname + url.search : null
      }
      decoded = next
    } catch { return null }
  }
  return null
}

/** null is the legacy root; undefined is an unknown or malformed route. */
export function parseProductRoute(value: string): ProductRoute | null | undefined {
  const safe = safeRelativeAppPath(value)
  if (!safe) return undefined
  const url = new URL(safe, 'https://agentworks.invalid')
  if (url.pathname === '/') return null
  let parts: string[]
  try { parts = url.pathname.replace(/\/$/, '').slice(1).split('/').map(decodeURIComponent) } catch { return undefined }
  const overview = parts[0] === 'workflows'
  const surface = overview ? 'agentworks' : Object.hasOwn(surfaces, parts[0]) ? surfaces[parts[0]] : Object.hasOwn(PRODUCT_ROUTE_ALIASES, parts[0]) ? PRODUCT_ROUTE_ALIASES[parts[0]] : undefined
  if (!surface) return undefined
  let id: string | undefined
  let view = url.searchParams.get('view') || undefined
  const folder = url.searchParams.has('folder') ? url.searchParams.get('folder')! : undefined
  if (view && !/^[a-z][a-z0-9-]{0,63}$/.test(view)) return undefined
  if (folder !== undefined && (surface !== 'knowledgebase' || !relativeBrainFolder(folder))) return undefined
  if (parts.length > 1) {
    if (surface === 'bullpen' && parts.length === 2 && parts[1] === 'connect') view = 'setup'
    else {
      const project = surface === 'work'
      if (!overview && parts[0] !== 'agentworks' && surface !== 'relays' && !project) return undefined
      const offset = project ? 2 : 1
      if (project && parts[1] !== 'projects') return undefined
      id = parts[offset]
      if (!id || !segment(id) || parts.length > offset + 2) return undefined
      if (parts.length === offset + 2) {
        if (parts[offset + 1] !== 'dashboard') return undefined
        view = surface === 'agentworks' || surface === 'relays' ? 'report' : 'dashboard'
      }
    }
  }
  return { surface, ...(id ? { id } : {}), ...(view ? { view } : {}), ...(folder !== undefined ? { folder } : {}), ...(overview && !id ? { overview: true } : {}) }
}

export function productRoutePath(route: ProductRoute): string {
  let path = route.surface === 'agentworks' && (route.id || route.overview) ? '/workflows' : PRODUCT_PATHS[route.surface]
  const params = new URLSearchParams()
  if (route.id) {
    if (route.surface === 'work') path += '/projects'
    path += '/' + encodeURIComponent(route.id)
  }
  if (route.surface === 'bullpen' && route.view === 'setup') path += '/connect'
  else if (route.id && (route.view === 'dashboard' || route.view === 'report')) path += '/dashboard'
  else if (route.view) params.set('view', route.view)
  if (route.folder) params.set('folder', route.folder)
  return path + (params.size ? '?' + params.toString() : '')
}

export function productReturnPath(value: string | null): string | null {
  if (!value) return null
  if (safeRelativeAppPath(value) === '/home') return '/home'
  const route = parseProductRoute(value)
  return route ? productRoutePath(route) : route === null ? '/' : null
}

export function currentProductRoute(): ProductRoute | null | undefined {
  return typeof window === 'undefined' ? null : parseProductRoute(window.location.pathname + window.location.search)
}

/** Roots the old app actually served; only these keep the home fallback. */
export const LEGACY_APP_ROOTS = new Set(['settings', 'chat', 'users', 'providers', 'schedules'])

export function legacyAppRoot(value: string): string {
  const safe = safeRelativeAppPath(value)
  if (!safe) return ''
  try { return decodeURIComponent(new URL(safe, 'https://agentworks.invalid').pathname.slice(1).split('/')[0]) } catch { return '' }
}

/** Old app paths were ignored; retain that fallback only for safe unknown roots. */
export function isLegacyAppPath(value: string): boolean {
  const safe = safeRelativeAppPath(value)
  if (!safe) return false
  const root = decodeURIComponent(new URL(safe, 'https://agentworks.invalid').pathname.slice(1).split('/')[0])
  return segment(root) && !Object.hasOwn(surfaces, root) && !Object.hasOwn(PRODUCT_ROUTE_ALIASES, root) && !['workflows', 'login', 'auth', 'oauth', 'file', 'folder', 'report'].includes(root)
}

export function routeView<T extends string>(surface: ProductSurface, id: string | null | undefined, normalize: (value: unknown) => T | null, defaultView?: T): T | null {
  const route = currentProductRoute()
  return route?.surface === surface && (surface !== 'work' || (route.id ?? 'main') === (id ?? 'main')) ? normalize(route.view) ?? defaultView ?? null : null
}

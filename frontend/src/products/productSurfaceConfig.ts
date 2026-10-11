export const PRODUCT_SURFACES = ['agentworks', 'relays', 'video-studio', 'sparkquill', 'work', 'code', 'mcp-gateway', 'knowledgebase'] as const

export type ProductSurface = (typeof PRODUCT_SURFACES)[number]

export const PRODUCT_SURFACE_LABELS: Record<ProductSurface, string> = {
  agentworks: 'Goals', relays: 'Relays', 'video-studio': 'Video Studio',
  sparkquill: 'SparkQuill', work: 'Crew', code: 'Code', 'mcp-gateway': 'Vault', knowledgebase: 'Brain',
}

type ProductRuntimeConfig = {
  deploymentMode?: unknown
  localServerProducts?: unknown
  defaultProductSurface?: unknown
  enabledProductSurfaces?: unknown
  gatewaySso?: unknown
  gatewayUrl?: unknown
}

function runtimeConfig(): ProductRuntimeConfig | undefined {
  if (typeof window === 'undefined') return undefined
  return (window as Window & { __APP_RUNTIME_CONFIG__?: ProductRuntimeConfig }).__APP_RUNTIME_CONFIG__
}

export function isProductSurface(value: unknown): value is ProductSurface {
  return typeof value === 'string' && PRODUCT_SURFACES.includes(value as ProductSurface)
}

/**
 * Ordinary local apps exclude shared server products, including stale saved
 * selections. Source development can explicitly opt in; packaged local assets
 * remain restricted. Server defaults and dedicated allowlists are preserved.
 */
export function enabledProductSurfaces(): ProductSurface[] {
  const configured = runtimeConfig()?.enabledProductSurfaces
  if (isLocalProductInstallation()) {
    const local = Array.isArray(configured) ? configured.filter(isProductSurface).filter(surface => !SERVER_ONLY_SURFACES.includes(surface)) : []
    return local.length ? [...new Set(local)] : ['agentworks', 'work']
  }
  const defaults: ProductSurface[] = gatewayBaseUrl() ? ['agentworks', 'relays', 'work', 'mcp-gateway', 'knowledgebase'] : ['agentworks', 'relays', 'work', 'knowledgebase']
  if (!Array.isArray(configured)) return defaults

  const enabled = configured.filter(isProductSurface).filter(surface => surface !== 'mcp-gateway' || gatewayBaseUrl() !== null)
  return enabled.length > 0 ? [...new Set(enabled)] : defaults
}

export function deploymentDefaultProductSurface(): ProductSurface {
  const enabled = enabledProductSurfaces()
  const configuredDefault = runtimeConfig()?.defaultProductSurface
  return isProductSurface(configuredDefault) && enabled.includes(configuredDefault)
    ? configuredDefault
    : enabled[0]
}

export function isEnabledProductSurface(surface: ProductSurface): boolean {
  return enabledProductSurfaces().includes(surface)
}

export function isSingleProductDeployment(): boolean {
  return enabledProductSurfaces().length === 1
}

export function hasGatewaySSO(): boolean {
  if (isLocalProductInstallation()) return false
  return runtimeConfig()?.gatewaySso === true
}

/**
 * Base URL of the MCP Gateway deployment embedded as a product surface,
 * without a trailing slash. Null hides the switcher entry.
 */
export function gatewayBaseUrl(): string | null {
  if (isLocalProductInstallation()) return null
  const raw = runtimeConfig()?.gatewayUrl
  if (typeof raw !== 'string') return null
  const url = raw.trim().replace(/\/+$/, '')
  if (!/^https?:\/\/[^/\s]+/.test(url)) return null
  return url
}

const SERVER_ONLY_SURFACES: ProductSurface[] = ['code', 'relays', 'knowledgebase', 'mcp-gateway']

export function isLocalProductInstallation(): boolean {
  // Packaged local assets omit server entries. Source development uses the
  // local-full build profile and explicitly advertises the launcher opt-in.
  return import.meta.env.VITE_DEPLOYMENT_MODE === 'local' || (runtimeConfig()?.deploymentMode === 'local' && runtimeConfig()?.localServerProducts !== true)
}

/**
 * Admin URL of the MCP Gateway deployment embedded as a product surface.
 * The React console talks to the gateway API directly; the server-rendered
 * UI stays available at this URL as a standalone fallback.
 */
export function gatewayAdminUrl(): string | null {
  const base = gatewayBaseUrl()
  return base === null ? null : `${base}/admin/`
}

/**
 * Narrows a deployment-wide surface list to what one user is allowed to see.
 * `allowedProducts` is the logged-in user's `allowed_products` from
 * `/api/auth/me` -- null/undefined (unrestricted) passes `surfaces` through
 * unchanged; an array narrows to exactly those, and an EMPTY array is a
 * read-only account with nothing enabled, which sees no product at all. This
 * stays a pure function so `enabledProductSurfaces()` itself doesn't need to
 * know about auth state; callers intersect at the point they already read both.
 */
export function intersectAllowedProductSurfaces(
  surfaces: ProductSurface[],
  allowedProducts: string[] | null | undefined,
): ProductSurface[] {
  if (!allowedProducts) return surfaces
  const allowed = new Set(allowedProducts.map((p) => p.toLowerCase()))
  return surfaces.filter((surface) => allowed.has(surface.toLowerCase()))
}

/** Product switcher entries in stable UI order for the current user. */
export function visibleProductSurfaceIDs(allowedProducts?: string[] | null): ProductSurface[] {
  const enabled = new Set(enabledProductSurfaces())
  return intersectAllowedProductSurfaces(PRODUCT_SURFACES.filter(surface => enabled.has(surface)), allowedProducts)
}

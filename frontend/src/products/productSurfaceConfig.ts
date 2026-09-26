export const PRODUCT_SURFACES = ['agentworks', 'video-studio', 'dominion', 'sparkquill', 'work'] as const

export type ProductSurface = (typeof PRODUCT_SURFACES)[number]

type ProductRuntimeConfig = {
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
 * Returns the products intentionally exposed by this deployment.  Leaving the
 * runtime setting out is the ordinary AgentWorks localhost case, which ships
 * the automation surface and the built-in Work coding surface together.
 * Dedicated product shells can still replace this with their own allowlist.
 */
export function enabledProductSurfaces(): ProductSurface[] {
  const configured = runtimeConfig()?.enabledProductSurfaces
  if (!Array.isArray(configured)) return ['agentworks', 'work']

  const enabled = configured.filter(isProductSurface)
  return enabled.length > 0 ? [...new Set(enabled)] : ['agentworks', 'work']
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
  return runtimeConfig()?.gatewaySso === true
}

/**
 * Admin URL of the MCP Gateway deployment linked from the product switcher.
 * The gateway is a separate service with its own auth, so this is a plain
 * link-out, not a product surface: null hides the entry.
 */
export function gatewayAdminUrl(): string | null {
  const raw = runtimeConfig()?.gatewayUrl
  if (typeof raw !== 'string') return null
  const url = raw.trim().replace(/\/+$/, '')
  if (!/^https?:\/\/[^/\s]+/.test(url)) return null
  return `${url}/admin/`
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

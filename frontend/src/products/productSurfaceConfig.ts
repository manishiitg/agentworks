export const PRODUCT_SURFACES = ['agentworks', 'relays', 'video-studio', 'dominion', 'sparkquill', 'work', 'code'] as const

export type ProductSurface = (typeof PRODUCT_SURFACES)[number]

type ProductRuntimeConfig = {
  defaultProductSurface?: unknown
  enabledProductSurfaces?: unknown
  gatewaySso?: unknown
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
 * the automation, Relay, and built-in Crew surfaces together.
 * Dedicated product shells can still replace this with their own allowlist.
 */
export function enabledProductSurfaces(): ProductSurface[] {
  const configured = runtimeConfig()?.enabledProductSurfaces
  if (!Array.isArray(configured)) return ['agentworks', 'relays', 'work']

  const enabled = configured.filter(isProductSurface)
  return enabled.length > 0 ? [...new Set(enabled)] : ['agentworks', 'relays', 'work']
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
  // Relays use the existing workflow APIs and permissions. A user granted
  // AgentWorks workflow access can use the Relay view without a new ACL.
  return surfaces.filter((surface) => allowed.has(surface.toLowerCase()) || (surface === 'relays' && allowed.has('agentworks')))
}

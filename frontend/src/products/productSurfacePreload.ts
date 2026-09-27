import type { ProductSurface } from './productSurfaceConfig'

// React.lazy and the switcher share these imports so a warm module is reused
// when the user selects it. Loading code here does not mount a product or fetch
// its data.
export const loadVideoStudioSurface = () => import('./video-studio/VideoStudioSurface')
export const loadDominionSurface = () => import('./dominion/DominionSurface')
export const loadSparkQuillSurface = () => import('./sparkquill/SparkQuillSurface')
export const loadWorkSurface = () => import('./work/WorkSurface')
export const loadGatewaySurface = () => import('./mcp-gateway/GatewaySurface')

const loaders: Partial<Record<ProductSurface, () => Promise<unknown>>> = {
  'video-studio': loadVideoStudioSurface,
  dominion: loadDominionSurface,
  sparkquill: loadSparkQuillSurface,
  work: loadWorkSurface,
  'mcp-gateway': loadGatewaySurface,
}

const pending = new Map<ProductSurface, Promise<unknown>>()

export function preloadProductSurface(surface: ProductSurface): Promise<unknown> | null {
  const load = loaders[surface]
  if (!load) return null
  const existing = pending.get(surface)
  if (existing) return existing
  const promise = load().catch((error: unknown) => {
    pending.delete(surface)
    throw error
  })
  pending.set(surface, promise)
  return promise
}

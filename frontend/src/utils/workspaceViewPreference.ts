import type { ProductSurface } from '../products/productSurfaceConfig'
import { getWorkspaceScopedStorageKey } from '../stores/useWorkspaceConnectionStore'

export function workspaceViewPreferenceKey(product: ProductSurface, entity = 'main'): string {
  return getWorkspaceScopedStorageKey(`workspace-view:${product}:${encodeURIComponent(entity)}`)
}

export function readWorkspaceViewPreference<T extends string>(
  product: ProductSurface, entity: string | null | undefined,
  normalize: (value: unknown) => T | null,
  legacyRead?: () => T | null,
): T | null {
  if (!entity || typeof window === 'undefined') return null
  try {
    const saved = window.localStorage.getItem(workspaceViewPreferenceKey(product, entity))
    // Invalid/removed views use the product's current default, not a stale
    // legacy choice. Reading a default never overwrites a remembered choice.
    if (saved !== null) return normalize(saved)
    const legacy = legacyRead?.() ?? null
    if (legacy !== null) window.localStorage.setItem(workspaceViewPreferenceKey(product, entity), legacy)
    return legacy
  } catch { return null }
}

export function writeWorkspaceViewPreference(
  product: ProductSurface, entity: string | null | undefined, view: string | null,
): void {
  if (!entity || typeof window === 'undefined') return
  try {
    const key = workspaceViewPreferenceKey(product, entity)
    if (view === null) window.localStorage.removeItem(key)
    else window.localStorage.setItem(key, view)
  } catch { /* View selection still works when storage is unavailable. */ }
}

export function normalizeViewFrom<T extends string>(views: readonly T[]): (value: unknown) => T | null {
  return value => typeof value === 'string' && views.includes(value as T) ? value as T : null
}

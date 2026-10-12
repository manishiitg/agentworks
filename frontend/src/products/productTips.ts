// Display definitions come from product.yaml through /api/product-tips.
import type { ProductSurface } from './productSurfaceConfig'
import { featureUsed, type UsedFeature } from '../utils/featureUsage'

export interface TipContext {
  surface: ProductSurface
  codeTabs: number
  userId?: string
}

export interface ProductTip {
  id: string
  text: string
  skip_if_used?: UsedFeature
  when?: { code_tabs?: { min?: number; max?: number } }
}

export type ProductTipCatalog = Partial<Record<ProductSurface, ProductTip[]>>
const STATE_KEY = 'agentworks.working-tip'
const MINUTE = 60 * 1000
interface ProductHistory { lastShownAt?: number; shownCount?: number }
interface TipState { seen: string[]; products: Partial<Record<ProductSurface, ProductHistory>> }

function stateKey(context: TipContext): string {
  return `${STATE_KEY}:v2:${encodeURIComponent(context.userId || 'local')}`
}

function readState(context: TipContext): TipState {
  try {
    const stored = localStorage.getItem(stateKey(context))
    const raw = JSON.parse(stored || '{}')
    const seen = Array.isArray(raw?.seen) ? raw.seen.filter((id: unknown) => typeof id === 'string') : []
    const products = raw?.products && typeof raw.products === 'object' && !Array.isArray(raw.products) ? raw.products : {}
    // Preserve unknown previous viewer history only for local/anonymous use.
    if (!stored && !context.userId) {
      const legacy = JSON.parse(localStorage.getItem(STATE_KEY) || '{}')
      return { seen: Array.isArray(legacy?.seen) ? legacy.seen.filter((id: unknown) => typeof id === 'string') : [], products: { [context.surface]: { lastShownAt: legacy?.lastShownAt, shownCount: 0 } } }
    }
    return { seen, products }
  } catch { return { seen: [], products: {} } }
}

function history(context: TipContext, state: TipState): Required<ProductHistory> {
  const entry = state.products[context.surface]
  return {
    lastShownAt: typeof entry?.lastShownAt === 'number' && Number.isFinite(entry.lastShownAt) ? entry.lastShownAt : 0,
    shownCount: typeof entry?.shownCount === 'number' && Number.isFinite(entry.shownCount) ? Math.max(0, entry.shownCount) : 0,
  }
}

// New product: first two tips 30 minutes apart, then two at each slower tier.
export function workingTipIntervalMs(shownCount: number): number {
  return (shownCount < 2 ? 30 : shownCount < 4 ? 60 : shownCount < 6 ? 120 : 240) * MINUTE
}

export function workingTipDelayMs(context: TipContext, now = Date.now()): number {
  const previous = history(context, readState(context))
  return previous.lastShownAt ? Math.max(0, previous.lastShownAt + workingTipIntervalMs(previous.shownCount) - now) : 0
}

function isEligible(tip: ProductTip, context: TipContext, seen: string[]): boolean {
  const tabs = tip.when?.code_tabs
  return !seen.includes(tip.id) && !(tip.skip_if_used && featureUsed(tip.skip_if_used)) &&
    (tabs?.min === undefined || context.codeTabs >= tabs.min) &&
    (tabs?.max === undefined || context.codeTabs <= tabs.max)
}

// Whether an unseen eligible definition exists, ignoring the due check: after
// another chat claims this slot, the caller retries at the next due time.
export function hasUnseenEligibleTip(context: TipContext, tips: ProductTip[] = []): boolean {
  const seen = readState(context).seen
  return tips.some(tip => isEligible(tip, context, seen))
}

// The caller supplies only the current product's resolved YAML definitions.
export function claimWorkingTip(context: TipContext, now = Date.now(), tips: ProductTip[] = []): string | null {
  const state = readState(context)
  const previous = history(context, state)
  if (previous.lastShownAt && now - previous.lastShownAt < workingTipIntervalMs(previous.shownCount)) return null
  const pick = tips.find(tip => isEligible(tip, context, state.seen))
  if (!pick) return null
  try {
    state.seen.push(pick.id)
    state.products[context.surface] = { lastShownAt: now, shownCount: previous.shownCount + 1 }
    localStorage.setItem(stateKey(context), JSON.stringify(state))
    // The anonymous v2 state was migrated from the legacy key, so the first
    // successful anonymous write retires it. Signed-in writes never touch it:
    // that browser history belongs to no account.
    if (!context.userId) {
      try { localStorage.removeItem(STATE_KEY) } catch { /* legacy cleanup is best-effort */ }
    }
  } catch { return null }
  return pick.text
}

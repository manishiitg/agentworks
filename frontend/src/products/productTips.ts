// Tiny hints about product features, shown very rarely beside "Working…"
// (owner, 2026-10-07: very rare, non-intrusive, intelligent). One place to add,
// edit or remove a tip. A tip only names features that exist today.
import type { ProductSurface } from './productSurfaceConfig'
import { featureUsed, type UsedFeature } from '../utils/featureUsage'

export interface TipContext {
  surface: ProductSurface
  /** Chat tabs open in the current Code project (1 = only the main chat). */
  codeTabs: number
}

export interface ProductTip {
  id: string
  text: string
  /** Products where it applies; none = every product. */
  products?: ProductSurface[]
  /** Never shown to someone who already uses this feature. */
  skipIfUsed?: UsedFeature
  /** Only when it fits the moment. */
  when?: (context: TipContext) => boolean
}

export const PRODUCT_TIPS: ProductTip[] = [
  { id: 'quick-switcher', text: '⌘K / Ctrl+K searches products, projects, chats, panels and tabs. Try typing “slack”.', skipIfUsed: 'quick-switcher' },
  { id: 'code-new-tab', text: '⌥⇧T / Alt+Shift+T opens another chat tab, so two tasks run side by side.', products: ['code'], skipIfUsed: 'chat-tab-new', when: c => c.codeTabs <= 1 },
  { id: 'code-tab-keys', text: 'Switch chat tabs with ⌘1–5 in the desktop app, or ⌥1–5 in a browser.', products: ['code'], skipIfUsed: 'chat-tab-keys', when: c => c.codeTabs >= 2 },
  { id: 'code-ask-chat', text: 'Chats in a Code can ask each other, e.g. “ask Chat 2 to review this”.', products: ['code'], when: c => c.codeTabs >= 2 },
  { id: 'reminders', text: 'Ask for a reminder, e.g. “check the deploy in 15 minutes”.', products: ['code', 'work'] },
  { id: 'step-test-mode', text: 'Changed a step? Ask to run it in test mode: nothing is sent and the data is a copy.', products: ['agentworks'] },
  { id: 'toolbar-hide', text: 'The icon at the end of the toolbar hides it; ⌘K still opens any panel.', skipIfUsed: 'toolbar-hide' },
]

const STATE_KEY = 'agentworks.working-tip'
export const TIP_INTERVAL_MS = 24 * 60 * 60 * 1000

interface TipState { lastShownAt?: number; seen?: string[] }

function readState(): TipState {
  try { return JSON.parse(localStorage.getItem(STATE_KEY) || '{}') as TipState } catch { return {} }
}

/** Picks at most one tip a day: relevant here, for a feature not yet used, not
 * shown before. Records the pick. Returns null when nothing is due. */
export function claimWorkingTip(context: TipContext, now = Date.now(), tips = PRODUCT_TIPS): string | null {
  const state = readState()
  if (state.lastShownAt && now - state.lastShownAt < TIP_INTERVAL_MS) return null
  const seen = new Set(state.seen ?? [])
  const fitting = tips.filter(tip =>
    (!tip.products || tip.products.includes(context.surface)) &&
    !(tip.skipIfUsed && featureUsed(tip.skipIfUsed)) &&
    (!tip.when || tip.when(context)))
  const pick = fitting.find(tip => !seen.has(tip.id))
  if (!pick) return null
  try {
    localStorage.setItem(STATE_KEY, JSON.stringify({ lastShownAt: now, seen: [...seen, pick.id] }))
  } catch { return null }
  return pick.text
}

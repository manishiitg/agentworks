// @vitest-environment happy-dom
import { beforeEach, describe, expect, it } from 'vitest'
import { claimWorkingTip, TIP_INTERVAL_MS, type ProductTip } from './productTips'
import { markFeatureUsed } from '../utils/featureUsage'

// Owner, 2026-10-07: tips are very rare (one a day), fit the product and the
// moment, and never cover a feature the person already uses.
describe('claimWorkingTip', () => {
  beforeEach(() => localStorage.clear())
  const tips: ProductTip[] = [
    { id: 'panel', text: 'panel tip', skipIfUsed: 'panel-switcher' },
    { id: 'code-only', text: 'code tip', products: ['code'], when: c => c.codeTabs >= 2 },
    { id: 'any', text: 'any tip' },
  ]
  it('shows at most one tip a day, skips used features, and fits the product', () => {
    markFeatureUsed('panel-switcher')
    const now = 1_000_000
    expect(claimWorkingTip({ surface: 'code', codeTabs: 2 }, now, tips)).toBe('code tip')
    expect(claimWorkingTip({ surface: 'code', codeTabs: 2 }, now + 60_000, tips)).toBeNull()
    expect(claimWorkingTip({ surface: 'agentworks', codeTabs: 1 }, now + TIP_INTERVAL_MS, tips)).toBe('any tip')
    expect(claimWorkingTip({ surface: 'agentworks', codeTabs: 1 }, now + 2 * TIP_INTERVAL_MS, tips)).toBeNull()
  })
})

// @vitest-environment happy-dom
import { beforeEach, describe, expect, it } from 'vitest'
import { claimWorkingTip, type ProductTip } from './productTips'
import { markFeatureUsed } from '../utils/featureUsage'

// Owner, 2026-10-09: tips are occasional (one every four hours), fit the product and the
// moment, and never cover a feature the person already uses.
describe('claimWorkingTip', () => {
  beforeEach(() => localStorage.clear())
  const tips: ProductTip[] = [
    { id: 'panel', text: 'panel tip', skipIfUsed: 'panel-switcher' },
    { id: 'code-only', text: 'code tip', products: ['code'], when: c => c.codeTabs >= 2 },
    { id: 'any', text: 'any tip' },
  ]
  it('shows at most one tip every four hours, skips used features, and fits the product', () => {
    markFeatureUsed('panel-switcher')
    const now = 1_000_000
    const fourHours = 4 * 60 * 60 * 1000
    expect(claimWorkingTip({ surface: 'code', codeTabs: 2 }, now, tips)).toBe('code tip')
    expect(claimWorkingTip({ surface: 'code', codeTabs: 2 }, now + 60_000, tips)).toBeNull()
    expect(claimWorkingTip({ surface: 'agentworks', codeTabs: 1 }, now + fourHours - 1, tips)).toBeNull()
    expect(claimWorkingTip({ surface: 'agentworks', codeTabs: 1 }, now + fourHours, tips)).toBe('any tip')
    expect(claimWorkingTip({ surface: 'agentworks', codeTabs: 1 }, now + 2 * fourHours, tips)).toBeNull()
  })
})

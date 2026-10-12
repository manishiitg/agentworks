// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { claimWorkingTip, workingTipIntervalMs, type ProductTip } from './productTips'
import { markFeatureUsed } from '../utils/featureUsage'

vi.hoisted(() => {
  const values = new Map<string, string>()
  const storage = { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => { values.set(key, String(value)) }, removeItem: (key: string) => { values.delete(key) }, clear: () => values.clear(), key: (index: number) => [...values.keys()][index] ?? null, get length() { return values.size } }
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: storage })
  Object.defineProperty(window, 'localStorage', { configurable: true, value: storage })
})

// YAML supplies the current product's definitions; cadence slows with exposure.
describe('claimWorkingTip', () => {
  beforeEach(() => localStorage.clear())
  const tips: ProductTip[] = [
    { id: 'panel', text: 'panel tip', skip_if_used: 'panel-switcher' },
    { id: 'code-only', text: 'code tip', when: { code_tabs: { min: 2 } } },
    { id: 'any', text: 'any tip' },
  ]
  it('slows cadence with exposure and keeps account/product history separate without repeats', () => {
    markFeatureUsed('panel-switcher')
    const now = 1_000_000
    const minute = 60_000
    const code = { surface: 'code' as const, codeTabs: 2, userId: 'viewer-a' }
    expect(claimWorkingTip(code, now, tips)).toBe('code tip')
    expect(claimWorkingTip(code, now + 30 * minute - 1, tips)).toBeNull()
    expect(claimWorkingTip(code, now + 30 * minute, tips)).toBe('any tip')
    expect(workingTipIntervalMs(2)).toBe(60 * minute)
    const extra = Array.from({ length: 5 }, (_, index) => ({ id: `new-${index}`, text: `New ${index}` }))
    let time = now + 30 * minute
    for (let shown = 2; shown < 7; shown++) {
      const gap = workingTipIntervalMs(shown)
      expect(claimWorkingTip(code, time + gap - 1, extra)).toBeNull()
      time += gap
      expect(claimWorkingTip(code, time, extra)).toBe(`New ${shown - 2}`)
    }
    expect(workingTipIntervalMs(4)).toBe(120 * minute)
    expect(workingTipIntervalMs(6)).toBe(240 * minute)
    expect(claimWorkingTip(code, time + 240 * minute, [...tips, ...extra])).toBeNull()
    // Shared ids already seen in Code don't repeat in Crew, but a new product
    // gets its own initial cadence and product-specific definitions.
    expect(claimWorkingTip({ ...code, surface: 'work' }, now, [...tips, { id: 'crew', text: 'Crew tip' }])).toBe('Crew tip')
    expect(claimWorkingTip({ ...code, userId: 'viewer-b', codeTabs: 1 }, now, tips)).toBe('any tip')
  })
})

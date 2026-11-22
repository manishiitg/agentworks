import { expect, it } from 'vitest'
import { cacheReadShare, formatModelUsageCost } from './helpers'

// Real bug: run input excludes cache, so cache read / input showed shares
// above 100% and models with no rate card showed $0.0000.
it('cache share is against all input and unpriced models are not $0', () => {
  expect(cacheReadShare(1000, 9000, 0)).toBe(90)
  expect(cacheReadShare(0, 500, 0)).toBe(100)
  expect(cacheReadShare(0, 0, 0)).toBe(0)
  expect(formatModelUsageCost({ total_cost_usd: 0, unpriced: true })).toBe('Not priced')
  expect(formatModelUsageCost({ total_cost_usd: 0 })).not.toBe('Not priced')
})

import { describe, expect, it } from 'vitest'
import { inputTokens, totalTokens } from '../../../utils/costTokens'
import { resolveOverallSummary, type CombinedDailyCostSummaryEntry } from './helpers'

const daily = (patch: Partial<CombinedDailyCostSummaryEntry>): CombinedDailyCostSummaryEntry => ({
  date: '2026-10-13', workflowCost: 0, evaluationCost: 0, builderCost: 0, pulseCost: null,
  totalCost: 0, workflowTokens: 0, evaluationTokens: 0, builderTokens: 0, pulseTokens: null,
  totalTokens: 0, totalInputTokens: 0, totalOutputTokens: 0, llmDurationMS: 0, runCount: 0, ...patch,
})

describe('resolveOverallSummary', () => {
  it('sums the daily rows when the ledger total is priced but tokenless', () => {
    // Live case: header ledger $4.6959 with every token count 0, while the
    // daily rows carry Builder and run tokens.
    const scoped = { total: { total_cost_usd: 4.6959, completion_tokens: 0 }, by_scope: { builder: {} } }
    const rows = [
      daily({ totalCost: 4.6139, totalTokens: 7155250 + 313754, totalInputTokens: 7155250, totalOutputTokens: 313754, cacheReadTokens: 5000000, cacheWriteTokens: 100000 }),
      daily({ date: '2026-10-12', totalCost: 0.0819 + 0.062, totalTokens: 231000 + 8500, totalInputTokens: 231000, totalOutputTokens: 8500 }),
    ]
    const summary = resolveOverallSummary(scoped as never, { totalCost: 0, totalTokens: 0, totalInputTokens: 0, totalOutputTokens: 0, totalRuns: 1 }, null, rows, totalTokens, inputTokens)
    expect(summary?.totalTokens).toBe(7155250 + 313754 + 231000 + 8500)
    expect(summary?.totalInputTokens).toBe(7155250 + 231000)
    expect(summary?.totalOutputTokens).toBe(313754 + 8500)
    expect(summary?.totalCost).toBeCloseTo(4.6139 + 0.0819 + 0.062)
    expect(summary?.cacheReadTokens).toBe(5000000)
    expect(summary?.cacheWriteTokens).toBe(100000)
    // Fresh input as the header derives it: total - cached - writes.
    expect((summary?.totalInputTokens ?? 0) - (summary?.cacheReadTokens ?? 0) - (summary?.cacheWriteTokens ?? 0)).toBe(7155250 + 231000 - 5100000)
  })

  it('trusts the ledger-normalized total input (live numbers)', () => {
    // input_tokens from the API already includes cache reads exactly once
    // (normalized per provider); the header must not re-add cache.
    const rows = [daily({ totalCost: 4.7578, totalTokens: 53990000 + 322254, totalInputTokens: 53990000, totalOutputTokens: 322254, cacheReadTokens: 46710000, cacheWriteTokens: 0 })]
    const summary = resolveOverallSummary(null, null, null, rows, totalTokens, inputTokens)
    expect(summary?.totalInputTokens).toBe(53990000)
    expect(summary?.cacheReadTokens).toBe(46710000)
    expect((summary?.totalInputTokens ?? 0) - (summary?.cacheReadTokens ?? 0) - (summary?.cacheWriteTokens ?? 0)).toBe(7280000)
    expect(summary?.totalTokens).toBe(53990000 + 322254)
  })

  it('keeps the ledger total when it has tokens', () => {
    const scoped = { total: { total_cost_usd: 2, completion_tokens: 40, prompt_tokens: 60 }, by_scope: { builder: {} } }
    const summary = resolveOverallSummary(scoped as never, null, null, [daily({ totalTokens: 5, totalCost: 9 })], totalTokens, inputTokens)
    expect(summary).toMatchObject({ totalCost: 2, totalTokens: 100 })
  })
})

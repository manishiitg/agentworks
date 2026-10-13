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
      daily({ totalCost: 4.6139, totalTokens: 7155250 + 313754, totalInputTokens: 7155250, totalOutputTokens: 313754 }),
      daily({ date: '2026-10-12', totalCost: 0.0819 + 0.062, totalTokens: 231000 + 8500, totalInputTokens: 231000, totalOutputTokens: 8500 }),
    ]
    const summary = resolveOverallSummary(scoped as never, { totalCost: 0, totalTokens: 0, totalInputTokens: 0, totalOutputTokens: 0, totalRuns: 1 }, null, rows, totalTokens, inputTokens)
    expect(summary?.totalTokens).toBe(7155250 + 313754 + 231000 + 8500)
    expect(summary?.totalInputTokens).toBe(7155250 + 231000)
    expect(summary?.totalOutputTokens).toBe(313754 + 8500)
    expect(summary?.totalCost).toBeCloseTo(4.6139 + 0.0819 + 0.062)
  })

  it('keeps the ledger total when it has tokens', () => {
    const scoped = { total: { total_cost_usd: 2, completion_tokens: 40, prompt_tokens: 60 }, by_scope: { builder: {} } }
    const summary = resolveOverallSummary(scoped as never, null, null, [daily({ totalTokens: 5, totalCost: 9 })], totalTokens, inputTokens)
    expect(summary).toMatchObject({ totalCost: 2, totalTokens: 100 })
  })
})

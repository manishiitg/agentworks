import { describe, expect, it } from 'vitest'
import type { CostAggregate, CostSummary } from '../../../services/api-types'
import { buildModelCostRows, costAgentLabel } from './CostsModelSection'

const aggregate = (provider: string, cost: number, input = 10): CostAggregate => ({
  provider,
  prompt_tokens: input,
  completion_tokens: 5,
  reasoning_tokens: 0,
  cache_read_tokens: 0,
  cache_write_tokens: 0,
  total_cost_usd: cost,
  call_count: 1,
})

describe('cost model rows', () => {
  it('labels coding agents and sorts their effective models by cost', () => {
    const summary = {
      by_model: {
        'muse-spark-1.3-contributor': aggregate('muse-cli', 0.02),
        'claude-sonnet-5-5': aggregate('claude-code', 0.15),
      },
    } as Pick<CostSummary, 'by_model'>

    expect(buildModelCostRows(summary).map(row => [row.agentLabel, row.modelId])).toEqual([
      ['Claude Code', 'claude-sonnet-5-5'],
      ['Muse', 'muse-spark-1.3-contributor'],
    ])
  })

  it('keeps older model buckets visible when provider metadata is absent', () => {
    expect(costAgentLabel('', 'muse-spark-1.3-contributor')).toBe('Muse')
    expect(costAgentLabel('', 'legacy-model')).toBe('Unknown agent')
    expect(buildModelCostRows({ by_model: { 'legacy-model': aggregate('', 0, 12) } })).toHaveLength(1)
  })

  it('builds the provider/model split from one date bucket', () => {
    const dateBucket = {
      by_model: {
        'muse-spark-1.3-contributor': aggregate('muse-cli', 0.08, 40),
        'claude-sonnet-5-5': aggregate('claude-code', 0.12, 30),
      },
    } as Pick<CostSummary, 'by_model'>

    expect(buildModelCostRows(dateBucket).map(row => ({
      agent: row.agentLabel,
      model: row.modelId,
      provider: row.provider,
    }))).toEqual([
      { agent: 'Claude Code', model: 'claude-sonnet-5-5', provider: 'claude-code' },
      { agent: 'Muse', model: 'muse-spark-1.3-contributor', provider: 'muse-cli' },
    ])
  })
})

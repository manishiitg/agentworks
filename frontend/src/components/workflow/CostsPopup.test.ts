// @vitest-environment happy-dom
import { describe, expect, it, vi } from 'vitest'
import { act, createElement } from 'react'
import { createRoot } from 'react-dom/client'
import { agentApi } from '../../services/api'
import { useCostsData, type CostsData } from './costs/useCostsData'

import type { ModelTokenUsage, TokenUsageFile } from '../../services/api-types'
import {
  buildDailyStepCostsByDate,
  type DailyCostRun,
  type RunDailyCostInput,
} from '../../utils/dailyCostBreakdown'

const usage = (cost: number): ModelTokenUsage => ({
  provider: 'test-provider',
  input_tokens: 10,
  output_tokens: 5,
  input_tokens_m: '0.000M',
  output_tokens_m: '0.000M',
  cache_tokens: 0,
  cache_tokens_m: '0.000M',
  reasoning_tokens: 0,
  reasoning_tokens_m: '0.000M',
  llm_call_count: 1,
  total_cost_usd: cost,
})

const tokenFile = (
  cost: number,
  byStep: TokenUsageFile['by_step_and_model'] = {}
): TokenUsageFile => ({
  created_at: '2026-07-18T00:00:00Z',
  updated_at: '2026-07-18T00:01:00Z',
  by_model: { model: usage(cost) },
  by_step_and_model: byStep,
})

const run = (group: string): DailyCostRun => ({
  runFolder: `iteration-0/${group}`,
  tokenUsage: tokenFile(0),
  steps: {
    login: {
      step_id: 'login',
      type: 'scripted',
      title: 'Login',
      description: '',
      validations: [],
      executions: [],
    },
    scripted: {
      step_id: 'scripted',
      type: 'scripted',
      title: 'Scripted processing',
      description: '',
      validations: [],
      executions: [],
    },
  },
})

const daily = (group: string, cost: number): RunDailyCostInput => ({
  date: '2026-07-18',
  scope: 'execution',
  groupFolder: group,
  runFolder: `iteration-0/${group}`,
  tokenUsage: tokenFile(cost, {
    'execution_only:login': { model: usage(cost) },
  }),
})

describe('buildDailyStepCostsByDate', () => {
  it('keeps the same step separate by group and includes executed zero-cost steps', () => {
    const result = buildDailyStepCostsByDate(
      [run('customer-b'), run('mahima')],
      [daily('customer-b', 0.65), daily('mahima', 0.39)],
      []
    )

    const rows = result.get('2026-07-18') || []
    const loginRows = rows.filter(row => row.stepID === 'login')
    const scriptedRows = rows.filter(row => row.stepID === 'scripted')

    expect(loginRows).toHaveLength(2)
    expect(loginRows.map(row => row.groupLabel).sort()).toEqual(['customer-b', 'mahima'])
    expect(loginRows.reduce((sum, row) => sum + row.totalCost, 0)).toBeCloseTo(1.04)
    expect(scriptedRows).toHaveLength(2)
    expect(scriptedRows.every(row => row.totalCost === 0 && row.models.length === 0)).toBe(true)
  })

  it('labels historical step-less execution costs as unattributed orchestration', () => {
    const entry: RunDailyCostInput = {
      date: '2026-07-18',
      scope: 'execution',
      groupFolder: '__ungrouped__',
      runFolder: 'iteration-0',
      tokenUsage: tokenFile(1.25),
    }

    const rows = buildDailyStepCostsByDate([], [entry], []).get('2026-07-18') || []
    expect(rows).toHaveLength(1)
    expect(rows[0].stepID).toBe('unattributed-execution')
    expect(rows[0].stepTitle).toBe('Workflow Orchestrator (unattributed)')
    expect(rows[0].stepTitle).not.toContain('iteration-0')
    expect(rows[0].totalCost).toBeCloseTo(1.25)
  })

  it('reconciles partially attributed ledgers without changing the daily total', () => {
    const entry: RunDailyCostInput = {
      date: '2026-07-18',
      scope: 'execution',
      groupFolder: 'customer-b',
      runFolder: 'iteration-0/customer-b',
      tokenUsage: tokenFile(2, {
        'execution_only:login': { model: usage(1.25) },
      }),
    }

    const rows = buildDailyStepCostsByDate([], [entry], []).get('2026-07-18') || []
    expect(rows.find(row => row.stepID === 'login')?.totalCost).toBeCloseTo(1.25)
    expect(rows.find(row => row.stepID === 'unattributed-execution')?.totalCost).toBeCloseTo(0.75)
    expect(rows.reduce((sum, row) => sum + row.totalCost, 0)).toBeCloseTo(2)
  })
})


// PLAT-746-265: choosing one run must never label workspace totals as its cost.
it('scopes embedded costs to the selected run and never substitutes another run', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  const costs = vi.spyOn(agentApi, 'getCosts').mockResolvedValue({
    runs: [{ run_folder: 'selected', token_usage: tokenFile(1) }, { run_folder: 'unrelated', token_usage: tokenFile(99) }],
    scoped_costs: { total: { total_cost_usd: 500 } },
  } as Awaited<ReturnType<typeof agentApi.getCosts>>)
  const logs = vi.spyOn(agentApi, 'getExecutionLogs').mockResolvedValue({ steps: {} } as Awaited<ReturnType<typeof agentApi.getExecutionLogs>>)
  let data: CostsData | undefined
  function Probe({ folder }: { folder: string }) {
    data = useCostsData({ workspacePath: 'Workflow/child', selectedRunFolder: folder, runOnly: true })
    return null
  }
  const root = createRoot(document.createElement('div'))
  try {
    await act(async () => root.render(createElement(Probe, { folder: 'selected' })))
    expect(data!.runCosts.map(run => run.runFolder)).toEqual(['selected'])
    expect(data!.aggregateSummary?.totalCost).toBeCloseTo(1)
    expect(data!.scopedCosts).toBeNull()
    await act(async () => root.render(createElement(Probe, { folder: 'missing' })))
    expect(data!.runCosts).toEqual([])
    expect(data!.overallSummary).toBeNull()
  } finally { await act(async () => root.unmount()); costs.mockRestore(); logs.mockRestore() }
})

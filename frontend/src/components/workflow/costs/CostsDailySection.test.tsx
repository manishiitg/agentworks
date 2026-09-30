// @vitest-environment happy-dom
import React, { act, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { renderToStaticMarkup } from 'react-dom/server'
import { expect, it, vi } from 'vitest'
import type { CostAggregate, CostSummary } from '../../../services/api-types'
import { buildCostActivityBreakdown } from '../../../utils/costActivityBreakdown'
import CostsDailySection from './CostsDailySection'

it('keeps canonical input/output in daily, model, activity and execution details', () => {
  const usage: CostAggregate = { provider: 'muse-cli', input_tokens: 1000, prompt_tokens: 1000, completion_tokens: 25, reasoning_tokens: 0, cache_read_tokens: 800, cache_write_tokens: 0, call_count: 1, total_cost_usd: 0, unpriced_call_count: 1, missing_usage_call_count: 1 }
  const summary: CostSummary = { total: usage, by_model: { muse: usage }, by_date: { '2026-09-30': { ...usage, by_model: { muse: usage } } }, by_scope: { chat: { ...usage, by_execution: { chat: usage } } } }
  const categories = buildCostActivityBreakdown(summary)
  const html = renderToStaticMarkup(<CostsDailySection projectMode scopedCosts={summary} hasScopedActivity activityBreakdown={categories}
    combinedDailyCostSummaries={[{ date: '2026-09-30', workflowCost: 0, evaluationCost: 0, builderCost: 0, pulseCost: 0, totalCost: 0, workflowTokens: 0, evaluationTokens: 0, builderTokens: 1025, pulseTokens: 0, totalTokens: 1025, totalInputTokens: 1000, totalOutputTokens: 25, llmDurationMS: 0, runCount: 0 }]}
    dailyActivityBreakdown={new Map([['2026-09-30', categories]])} runDailyCostSummaries={[]} expandedDailyDates={new Set(['2026-09-30'])}
    setExpandedDailyDates={() => {}} costHistory={null} loadingOlder={false} loadOlderCosts={async () => {}} />)
  expect(html).toContain('1.0K input · 25 output')
  expect(html).toContain('Input tokens')
  expect(html).toContain('Output tokens')
  expect(html).toContain('Not priced')
  expect(html).not.toContain('1.8K')
  expect(html).not.toContain('>Calls<')
})


it('opens two dates together and collapses only the selected date', async () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  const dates = ['2026-09-30', '2026-09-29']
  const daily = dates.map(date => ({ date, workflowCost: 0, evaluationCost: 0, builderCost: 1, pulseCost: 0, totalCost: 1, workflowTokens: 0, evaluationTokens: 0, builderTokens: 100, pulseTokens: 0, totalTokens: 100, llmDurationMS: 0, runCount: 0 }))
  function Harness() {
    const [expandedDailyDates, setExpandedDailyDates] = useState<Set<string>>(new Set())
    return <CostsDailySection projectMode scopedCosts={null} hasScopedActivity={false} activityBreakdown={[]}
      combinedDailyCostSummaries={daily} dailyActivityBreakdown={new Map()} runDailyCostSummaries={[]}
      expandedDailyDates={expandedDailyDates} setExpandedDailyDates={setExpandedDailyDates}
      costHistory={null} loadingOlder={false} loadOlderCosts={async () => {}} />
  }
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<Harness />))
    const buttons = Array.from(host.querySelectorAll('button'))
    await act(async () => buttons[0].click())
    await act(async () => buttons[1].click())
    expect(buttons.map(button => button.getAttribute('aria-expanded'))).toEqual(['true', 'true'])
    expect(host.textContent?.match(/This older daily record/g)).toHaveLength(2)
    await act(async () => buttons[0].click())
    expect(buttons.map(button => button.getAttribute('aria-expanded'))).toEqual(['false', 'true'])
    expect(host.textContent?.match(/This older daily record/g)).toHaveLength(1)
  } finally {
    await act(async () => root.unmount())
    host.remove()
    vi.unstubAllGlobals()
  }
})

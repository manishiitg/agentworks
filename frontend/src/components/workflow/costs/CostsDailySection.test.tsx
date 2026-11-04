// @vitest-environment happy-dom
import React, { act, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { renderToStaticMarkup } from 'react-dom/server'
import { expect, it, vi } from 'vitest'
import type { CostAggregate, CostSummary } from '../../../services/api-types'
import { buildCostActivityBreakdown } from '../../../utils/costActivityBreakdown'
import CostsDailySection from './CostsDailySection'


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
    expect(host.textContent?.match(/Activity details unavailable/g)).toHaveLength(2)
    await act(async () => buttons[0].click())
    expect(buttons.map(button => button.getAttribute('aria-expanded'))).toEqual(['false', 'true'])
    expect(host.textContent?.match(/Activity details unavailable/g)).toHaveLength(1)
  } finally {
    await act(async () => root.unmount())
    host.remove()
    vi.unstubAllGlobals()
  }
})

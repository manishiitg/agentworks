// @vitest-environment happy-dom
import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import CostsHeader, { CostsOverview } from './CostsHeader'

const totals = { totalCost: 245.6234, totalTokens: 1025, totalInputTokens: 1000, totalOutputTokens: 25, cacheReadTokens: 800, cacheWriteTokens: 0, totalRuns: 1 }

describe('CostsHeader', () => {
  it('keeps only non-growing title/actions in the fixed header', () => {
    const html = renderToStaticMarkup(<CostsHeader loading={false} loadAllCosts={async () => {}} headerAction={<button data-testid="ask-ai">Ask AI</button>} />)
    expect(html).toContain('shrink-0')
    expect(html).toMatch(/^<div class="min-w-0 shrink-0">/)
    expect(html).not.toContain('Recorded tokens')
    expect(html).toContain('data-testid="ask-ai"')
  })

  it('keeps exact costs and token accounting in closed details below the readable total', () => {
    const html = renderToStaticMarkup(<CostsOverview overallSummary={totals} scopedCosts={null} aggregateSummary={null} phaseCostSummary={null} />)
    expect(html).toContain('Recorded tokens')
    expect(html).toContain('1k')
    expect(html.indexOf('Recorded tokens')).toBeLessThan(html.indexOf('Cost · USD'))
    expect(html).toContain('$245.62')
    expect(html).toContain('$245.6234')
    expect(html).toContain('Usage details')
    expect(html).toContain('Fresh input')
    expect(html).toContain('200')
    expect(html).toContain('80.0% cached')
    expect(html).not.toContain('<details open')
  })

  it('preserves the daily-sum token totals and incomplete pricing notice', () => {
    const html = renderToStaticMarkup(<CostsOverview
      scopedCosts={{ total: { total_cost_usd: 0, input_tokens: 0, unpriced_call_count: 1 }, by_scope: {}, by_date: {} } as never}
      overallSummary={{ ...totals, totalCost: 0, totalInputTokens: 7386250, totalOutputTokens: 322254, cacheReadTokens: 5000000, cacheWriteTokens: 100000 }}
      aggregateSummary={null} phaseCostSummary={null} />)
    expect(html).toContain('Not priced')
    for (const value of ['7.4M', '2.3M', '5M', '322k']) expect(html).toContain(value)
  })
})

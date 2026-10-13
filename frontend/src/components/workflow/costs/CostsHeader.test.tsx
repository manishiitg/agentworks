// @vitest-environment happy-dom
import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import CostsHeader from './CostsHeader'
import type { PhaseCostSummary } from './helpers'

function renderHeader() {
  return renderToStaticMarkup(
    <CostsHeader
      loading={false}
      loadAllCosts={() => Promise.resolve()}
      overallSummary={null}
      aggregateSummary={null}
      phaseCostSummary={null}
      headerAction={<button type="button" data-testid="ask-ai">Ask AI</button>}
    />,
  )
}

const phaseSummary: PhaseCostSummary = {
  totalCost: 261.8748,
  totalInputTokens: 0,
  totalOutputTokens: 0,
  totalTokens: 0,
  totalLLMCalls: 0,
  totalCacheReadTokens: 0,
  totalCacheWriteTokens: 0,
  totalReasoningTokens: 0,
  createdAt: null,
  updatedAt: null,
  phaseCosts: [],
  modelCosts: [],
}

function renderHeaderWithTotals() {
  return renderToStaticMarkup(
    <CostsHeader
      loading={false}
      loadAllCosts={() => Promise.resolve()}
      overallSummary={{ totalCost: 245.6234, totalTokens: 1548130000, totalInputTokens: 1500000000, totalOutputTokens: 48130000, cacheReadTokens: 0, cacheWriteTokens: 0, totalRuns: 10 }}
      aggregateSummary={null}
      phaseCostSummary={phaseSummary}
      headerAction={<button type="button" data-testid="ask-ai">Ask AI</button>}
    />,
  )
}

describe('CostsHeader', () => {
  it('keeps Ask AI left of refresh', () => {
    const html = renderHeader()
    const askIndex = html.indexOf('data-testid="ask-ai"')
    const refreshIndex = html.indexOf('aria-label="Refresh costs"')
    expect(askIndex).toBeGreaterThanOrEqual(0)
    expect(refreshIndex).toBeGreaterThanOrEqual(0)
    expect(askIndex).toBeLessThan(refreshIndex)
  })

  it('uses the larger shared title when there is no subtitle', () => {
    expect(renderHeader()).toContain('text-lg')
  })

  it('keeps the stats strip to neutral kit colors', () => {
    const html = renderHeaderWithTotals()
    expect(html).toContain('$245.6234')
    expect(html).toContain('Builder $261.8748')
    expect(html).toContain('1500.00M input')
    expect(html).toContain('48.13M output')
    expect(html).not.toContain('text-green-')
    expect(html).not.toContain('text-amber-')
  })

  it('shows the daily-sum token fields when the ledger total is tokenless', () => {
    const html = renderToStaticMarkup(
      <CostsHeader
        loading={false}
        loadAllCosts={() => Promise.resolve()}
        scopedCosts={{ total: { total_cost_usd: 4.6959, completion_tokens: 0, prompt_tokens: 0 }, by_scope: {}, by_date: {} } as never}
        overallSummary={{ totalCost: 4.7578, totalTokens: 7708754, totalInputTokens: 7386250, totalOutputTokens: 322254, cacheReadTokens: 5000000, cacheWriteTokens: 100000, totalRuns: 1 }}
        aggregateSummary={null}
        phaseCostSummary={null}
      />,
    )
    expect(html).toContain('Total input')
    expect(html).toContain('7.39M')
    expect(html).toContain('Fresh input')
    expect(html).toContain('2.29M')
    expect(html).toContain('Cached input')
    expect(html).toContain('5.00M')
    expect(html).toContain('322.3K')
  })

  it('shows each total once (no icon-duplicated $)', () => {
    const html = renderHeaderWithTotals()
    expect(html.split('$245.6234').length - 1).toBe(1)
    expect(html.split('$261.8748').length - 1).toBe(1)
  })
})

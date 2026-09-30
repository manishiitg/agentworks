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
      overallSummary={{ totalCost: 245.6234, totalTokens: 1548130000, totalInputTokens: 1500000000, totalOutputTokens: 48130000, totalRuns: 10 }}
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

  it('uses the standard title size', () => {
    expect(renderHeader()).not.toContain('text-lg')
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

  it('shows each total once (no icon-duplicated $)', () => {
    const html = renderHeaderWithTotals()
    expect(html.split('$245.6234').length - 1).toBe(1)
    expect(html.split('$261.8748').length - 1).toBe(1)
  })
})

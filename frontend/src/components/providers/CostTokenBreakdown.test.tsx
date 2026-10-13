// @vitest-environment happy-dom
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import CostTokenBreakdown from './CostTokenBreakdown'

describe('CostTokenBreakdown', () => {
  it('handles providers whose input excludes cache reads (live numbers)', () => {
    const html = renderToStaticMarkup(<CostTokenBreakdown compact usage={{ prompt_tokens: 7280000, cache_read_tokens: 46710000, completion_tokens: 322254 }} />)
    expect(html).toContain('53.99M')
    expect(html).toContain('7.28M')
    expect(html).toContain('46.71M')
    expect(html).toContain('86.5% cached')
    expect(html).not.toContain('641')
  })
  it('handles providers whose input includes cache reads', () => {
    const html = renderToStaticMarkup(<CostTokenBreakdown compact usage={{ input_tokens: 1000, cache_read_tokens: 400, completion_tokens: 50 }} />)
    expect(html).toContain('40.0% cached')
  })
})

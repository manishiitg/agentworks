// @vitest-environment happy-dom
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import CostTokenBreakdown from './CostTokenBreakdown'

describe('CostTokenBreakdown', () => {
  it('derives fresh input from the normalized total (live numbers)', () => {
    const html = renderToStaticMarkup(<CostTokenBreakdown compact usage={{ input_tokens: 53990000, cache_read_tokens: 46710000, completion_tokens: 322254 }} />)
    expect(html).toContain('54M')
    expect(html).toContain('7.3M')
    expect(html).toContain('46.7M')
    expect(html).toContain('86.5% cached')
    expect(html).not.toContain('641')
  })
  it('guards unnormalized run-file usage where cache exceeds input', () => {
    const html = renderToStaticMarkup(<CostTokenBreakdown compact usage={{ input_tokens: 100, cache_read_tokens: 400, completion_tokens: 50 }} />)
    expect(html).toContain('Fresh input')
    expect(html).toContain('80.0% cached')
  })

  it('handles providers whose input includes cache reads', () => {
    const html = renderToStaticMarkup(<CostTokenBreakdown compact usage={{ input_tokens: 1000, cache_read_tokens: 400, completion_tokens: 50 }} />)
    expect(html).toContain('40.0% cached')
  })
})

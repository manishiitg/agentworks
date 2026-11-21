import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { costHeading, isFocusedChat, PartsBlock } from './ChatContextSection'

// Real failure mode: subscription-equivalent spend shown as one plain dollar figure, as if the person was charged it.
describe('costHeading', () => {
  const base = { total_usd: 1.2, models: [], helper_usd: 0, unpriced_calls: 0, cache_percent: 0 }
  it('shows a provider figure as plain cost', () => {
    expect(costHeading({ ...base, basis: 'actual' })).toEqual({ label: 'Cost so far', value: '$1.20' })
  })
  it('labels subscription-equivalent spend as an estimate you are not charged', () => {
    const heading = costHeading({ ...base, basis: 'estimate' })
    expect(heading.value).toBe('about $1.20')
    expect(heading.note).toMatch(/not charged/)
  })
  it('says mixed spend is partly estimated', () => {
    expect(costHeading({ ...base, basis: 'mixed' }).label).toMatch(/partly estimated/)
  })
})

// Real failure mode: estimated parts shown as exact figures.
describe('PartsBlock', () => {
  it('labels the breakdown as an estimate and lists every part', () => {
    const html = renderToStaticMarkup(<PartsBlock parts={[{ key: 'tools', label: 'Tools', tokens: 2000 }, { key: 'messages', label: 'Conversation', tokens: 8000 }]} />)
    expect(html).toContain('(estimate)')
    expect(html).toContain('Tools')
    expect(html).toContain('Conversation')
    expect(html).toContain('about')
  })
})

// Real failure mode: every open chat's Context section stacked in one pane (ten or more at once).
describe('isFocusedChat', () => {
  it('shows only the chat the person is in', () => {
    expect(isFocusedChat('a', 'a')).toBe(true)
    expect(isFocusedChat('a', 'b')).toBe(false)
    expect(isFocusedChat('a', null)).toBe(false)
    expect(isFocusedChat('', '')).toBe(false)
  })
})

import { describe, expect, it } from 'vitest'
import { costHeading } from './ChatContextSection'

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

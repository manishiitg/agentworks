import { expect, it } from 'vitest'
import { thinkingSummary } from './thinkingSummary'

it('separates a provider bold thinking heading without treating ordinary prose as a heading', () => {
  expect(thinkingSummary('**Checking files**\n\nCompare the **changes**.')).toEqual({ title: 'Checking files', body: 'Compare the **changes**.' })
  expect(thinkingSummary('**Checking fi')).toEqual({ title: 'Checking fi', body: '' })
  expect(thinkingSummary('Compare **changes** first.')).toBeNull()
})

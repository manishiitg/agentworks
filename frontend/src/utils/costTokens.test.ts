import { expect, it } from 'vitest'
import type { CostAggregate } from '../services/api-types'
import { inputTokens } from './costTokens'

it('uses normalized input for mixed providers and supports older Muse reports', () => {
  const raw = { prompt_tokens: 100, cache_read_tokens: 80, cache_write_tokens: 10 } as CostAggregate
  expect(inputTokens({ ...raw, input_tokens: 120 })).toBe(120)
  expect(inputTokens({ ...raw, provider: 'muse-cli' })).toBe(100)
  expect(inputTokens({ ...raw, provider: 'anthropic' })).toBe(190)
  expect(inputTokens({ ...raw, input_tokens: 0 })).toBe(0)
})

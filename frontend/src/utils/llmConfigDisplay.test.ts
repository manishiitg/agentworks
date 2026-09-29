import { describe, expect, it } from 'vitest'
import { llmOptionMatchesRef, llmOptionsKey } from './llmConfigDisplay'

describe('LLM option matching', () => {
  it('keeps reasoning effort when provider and model are identical', () => {
    const high = {
      provider: 'claude-code',
      model: 'claude-sonnet-5-5',
      options: { reasoning_effort: 'high' },
    }
    const low = {
      provider: 'claude-code',
      model: 'claude-sonnet-5-5',
      options: { reasoning_effort: 'low' },
    }

    expect(llmOptionMatchesRef(high, {
      provider: 'claude-code',
      model_id: 'claude-sonnet-5-5',
      options: { reasoning_effort: 'high' },
    })).toBe(true)
    expect(llmOptionMatchesRef(low, {
      provider: 'claude-code',
      model_id: 'claude-sonnet-5-5',
      options: { reasoning_effort: 'high' },
    })).toBe(false)
  })

  it('compares option objects independently of property order', () => {
    expect(llmOptionsKey({ reasoning_effort: 'high', verbosity: 'low' }))
      .toBe(llmOptionsKey({ verbosity: 'low', reasoning_effort: 'high' }))
  })
})

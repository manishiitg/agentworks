import { describe, expect, it } from 'vitest'
import { liveUsageSummary } from './terminalUsage'

describe('liveUsageSummary', () => {
  it('shows context fill from the terminal status, and nothing when it is unknown', () => {
    expect(liveUsageSummary({ context_used_tokens: 129_200, context_window_tokens: 258_400 }).contextText).toBe('ctx 50%')
    expect(liveUsageSummary({ context_used_tokens: 235_000 }).contextText).toBe('ctx 235k')
    expect(liveUsageSummary({ status_extras: ['ctx 42%'] }).contextText).toBe('')
  })
})

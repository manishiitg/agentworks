import { describe, expect, it } from 'vitest'
import { withClosedQuestions } from './codingAgentQuestions'

const prompt = (promptId: string, state: 'pending' | 'answered' | 'interrupted') => ({ promptId, state })

describe('withClosedQuestions', () => {
  it('shows a pending prompt the server lost as interrupted, so it is never answerable again', () => {
    expect(withClosedQuestions(prompt('p1', 'pending'), new Set(['p1'])).state).toBe('interrupted')
  })

  it('leaves other prompts, already settled prompts and a missing set alone', () => {
    expect(withClosedQuestions(prompt('p2', 'pending'), new Set(['p1'])).state).toBe('pending')
    expect(withClosedQuestions(prompt('p1', 'answered'), new Set(['p1'])).state).toBe('answered')
    expect(withClosedQuestions(prompt('p1', 'pending'), undefined).state).toBe('pending')
  })
})

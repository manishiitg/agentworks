import { describe, expect, it } from 'vitest'
import { completionTitle } from './agentEventDisplayLabels'

describe('completionTitle', () => {
  it('hides internal agent labels and IDs', () => {
    expect(completionTitle('agent-math-solver', true, 'Step')).toBe('Completed')
  })

  it('keeps meaningful names for normal agent completion events', () => {
    expect(completionTitle('Daily latency collector', false, 'Sub-Agent')).toBe('Sub-Agent completed: Daily latency collector')
  })
})

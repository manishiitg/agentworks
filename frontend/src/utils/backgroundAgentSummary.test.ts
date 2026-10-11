import { describe, expect, it } from 'vitest'
import { backgroundAgentCompletionSummary } from './backgroundAgentSummary'

describe('backgroundAgentCompletionSummary', () => {
  it('removes the internal agent runtime name', () => {
    expect(backgroundAgentCompletionSummary(
      'Sub-agent Math Solver Probe completed: Agent math-solver completed: 2 items completed',
    )).toBe('Finished 2 tasks.')
    expect(backgroundAgentCompletionSummary(
      'Sub-agent Math Solver Probe completed: Agent math-solver completed: 2 item(s) completed',
    )).toBe('Finished 2 tasks.')
  })

  it('keeps useful agent-written result text', () => {
    expect(backgroundAgentCompletionSummary('Wrote the report and verified all required fields.'))
      .toBe('Wrote the report and verified all required fields.')
  })
})

import { describe, expect, it } from 'vitest'
import { humanReadableAgentResult } from './eventDisplayUtils'

describe('humanReadableAgentResult', () => {
  it('removes the Autopilot reviewer protocol marker but keeps findings', () => {
    expect(humanReadableAgentResult([
      '## Findings',
      '',
      '- The workflow wrote the expected artifact.',
      '',
      'PULSE_REVIEW_COMPLETE todo_id=pulse-review-bug-review-0724',
    ].join('\n'))).toBe([
      '## Findings',
      '',
      '- The workflow wrote the expected artifact.',
    ].join('\n'))
  })

  it('does not alter ordinary agent results', () => {
    const result = 'Completed the workflow.\n\nValidation passed.'
    expect(humanReadableAgentResult(result)).toBe(result)
  })
})

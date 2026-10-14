import { describe, expect, it } from 'vitest'
import { currentTurnStartedAt } from '../../utils/turnStart'
import { WORKING_WORDS, formatElapsed, resolveWorkingLabel } from './workingWords'

describe('working status', () => {
  it('shows the real status from the server instead of a playful word', () => {
    expect(resolveWorkingLabel('Waiting for background work…', 'Pondering')).toEqual({ text: 'Waiting for background work…', real: true })
    expect(resolveWorkingLabel('Running the audit step', 'Pondering').real).toBe(true)
    // A bare "busy" carries no information, so the friendly word is used.
    expect(resolveWorkingLabel('Working…', 'Pondering')).toEqual({ text: 'Pondering…', real: false })
    expect(resolveWorkingLabel(undefined, 'Pondering').real).toBe(false)
    expect(new Set(WORKING_WORDS).size).toBeGreaterThanOrEqual(30)
  })

  it('formats seconds, then minutes, from the server turn start', () => {
    expect(formatElapsed(17_000)).toBe('17s')
    expect(formatElapsed(65_000)).toBe('1m 05s')
    const events = [
      { type: 'user_message', timestamp: '2026-10-14T10:00:00Z' },
      { type: 'unified_completion', timestamp: '2026-10-14T10:00:30Z' },
      { type: 'user_message', timestamp: '2026-10-14T10:05:00Z' },
      { type: 'tool_call_start', timestamp: '2026-10-14T10:05:02Z' },
    ]
    expect(currentTurnStartedAt(events)).toBe(Date.parse('2026-10-14T10:05:00Z'))
    expect(currentTurnStartedAt([{ type: 'tool_call_start', timestamp: '2026-10-14T10:05:02Z' }])).toBeUndefined()
  })
})

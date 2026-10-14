import { describe, expect, it } from 'vitest'
import { currentTurnStartedAt } from '../../utils/turnStart'
import { WORKING_GLYPH_FRAMES, WORKING_GLYPH_STATIC, WORKING_WORDS, formatElapsed, nextWorkingWordIndex, resolveWorkingLabel } from './workingWords'

describe('working status', () => {
  it('shows the real status from the server instead of a playful word', () => {
    expect(resolveWorkingLabel('Waiting for background work…', 'Pondering')).toEqual({ text: 'Waiting for background work…', real: true })
    expect(resolveWorkingLabel('Running the audit step', 'Pondering').real).toBe(true)
    // A bare "busy" carries no information, so the friendly word is used.
    expect(resolveWorkingLabel('Working…', 'Pondering')).toEqual({ text: 'Pondering…', real: false })
    expect(resolveWorkingLabel(undefined, 'Pondering').real).toBe(false)
    expect(new Set(WORKING_WORDS).size).toBeGreaterThanOrEqual(190)
    // Nothing that could read as a real status or an error.
    expect(WORKING_WORDS.filter((word) => /fail|error|delet|send|deploy|cancel|stopp/i.test(word))).toEqual([])
    // A new word is never one of the last few shown.
    const recent = [0, 1, 2, 3, 4, 5, 6, 7]
    for (let n = 0; n < 50; n += 1) expect(recent).not.toContain(nextWorkingWordIndex(recent))
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
    // No proof of where the turn began (the previous turn's end is not loaded): show no time, not a wrong one.
    expect(currentTurnStartedAt([
      { type: 'user_message', timestamp: '2026-10-14T09:00:00Z' },
      { type: 'tool_call_start', timestamp: '2026-10-14T09:00:02Z' },
    ])).toBeUndefined()
  })

  it('uses only text-form star glyphs, never one with a colour-emoji form', () => {
    // U+2733 and U+2734 render as a green emoji square on Apple systems.
    for (const frame of [...WORKING_GLYPH_FRAMES, WORKING_GLYPH_STATIC]) {
      expect(frame).not.toMatch(/[\u2733\u2734]/)
      expect(frame.endsWith('\uFE0E')).toBe(true)
    }
  })
})

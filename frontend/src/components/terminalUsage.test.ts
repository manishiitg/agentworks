import { describe, expect, it } from 'vitest'
import { terminalUsageLines, usageWindowLabel } from './terminalUsage'

// Local-time instants so the assertions hold in any test-runner timezone.
const NOW = new Date(2026, 8, 27, 12, 0).getTime() // Sun 27 Sep 2026, 12:00 local
const at = (d: number, h: number, m: number) => new Date(2026, 8, d, h, m).toISOString()
// ICU may emit a narrow no-break space before AM/PM.
const texts = (meta: Record<string, unknown>) =>
  terminalUsageLines(meta, { now: NOW, locale: 'en-US' }).map(line => line.text.replace(/\s/g, ' '))

describe('terminalUsageLines', () => {
  it('formats Claude windows with browser-local reset times', () => {
    expect(texts({
      rate_limit_windows: [
        { name: 'five_hour', used_percent: 17.4, resets_at: at(27, 15, 30) },
        { name: 'seven_day', used_percent: 35, resets_at: new Date(2026, 9, 1, 10, 0).toISOString() },
      ],
      status_extras: ['5h 17% →3:30pm', '7d 35% →Thu'],
    })).toEqual(['5h 17% · resets 3:30 PM', '7d 35% · resets Thu 10:00 AM'])
  })

  it('shows weekday + time only when the reset is at least 24h away', () => {
    const [soon, far] = texts({
      rate_limit_windows: [
        { name: 'five_hour', used_percent: 5, resets_at: new Date(NOW + 23 * 3600_000).toISOString() },
        { name: 'seven_day', used_percent: 5, resets_at: new Date(2026, 9, 1, 10, 0).toISOString() },
      ],
    })
    expect(soon).toBe('5h 5% · resets 11:00 AM')
    expect(far).toBe('7d 5% · resets Thu 10:00 AM')
  })

  it('maps Codex window names', () => {
    expect(texts({
      rate_limit_windows: [
        { name: 'window_60m', used_percent: 3, resets_at: '0001-01-01T00:00:00Z' },
        { name: 'window_2880m', used_percent: 4 },
        { name: 'window_30m', used_percent: 1 },
        { name: 'primary', used_percent: 8 },
        { name: 'secondary', used_percent: 2, window_minutes: 10080 },
      ],
    })).toEqual(['1h 3%', '2d 4%', '30m 1%', 'primary 8%', '7d 2%'])
    expect(usageWindowLabel('seven_day_opus')).toBe('7d opus')
    expect(usageWindowLabel('something_new')).toBe('something_new')
  })

  it('omits the reset when resets_at is missing, empty or the zero time', () => {
    expect(texts({
      rate_limit_windows: [
        { name: 'five_hour', used_percent: 10 },
        { name: 'seven_day', used_percent: 20, resets_at: '' },
        { name: 'seven_day_opus', used_percent: 30, resets_at: '0001-01-01T00:00:00Z' },
        { name: 'five_hour', used_percent: 40, resets_at: 0 },
      ],
    })).toEqual(['5h 10%', '7d 20%', '7d opus 30%', '5h 40%'])
  })

  it('falls back to percentage status_extras and excludes ctx', () => {
    expect(texts({ status_extras: ['5h 17% →3:30pm', 'ctx 42%', '7d 35% →Thu', 'opus'] }))
      .toEqual(['5h 17% →3:30pm', '7d 35% →Thu'])
    expect(texts({ rate_limit_windows: [], status_extras: ['ctx 90%'] })).toEqual([])
    expect(terminalUsageLines(undefined)).toEqual([])
  })

  it('flags usage at or above 90%', () => {
    const lines = terminalUsageLines({
      rate_limit_windows: [
        { name: 'five_hour', used_percent: 89.9 },
        { name: 'seven_day', used_percent: 90 },
      ],
    }, { now: NOW })
    expect(lines.map(line => line.high)).toEqual([false, true])
    expect(terminalUsageLines({ status_extras: ['7d 96% →Fri', '5h 12%'] }).map(line => line.high)).toEqual([true, false])
  })
})

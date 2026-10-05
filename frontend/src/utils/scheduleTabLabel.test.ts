import { describe, expect, it } from 'vitest'
import { scheduleTabLabel } from './scheduleTabLabel'

describe('scheduleTabLabel', () => {
  it('falls back to "Schedule" only when there is no name', () => {
    expect(scheduleTabLabel(undefined)).toBe('Schedule')
    expect(scheduleTabLabel('')).toBe('Schedule')
    expect(scheduleTabLabel('   ')).toBe('Schedule')
  })

  it('keeps a short name intact', () => {
    expect(scheduleTabLabel('Daily execution')).toBe('Daily execution')
  })

  it('preserves schedule timing and weekdays in the title', () => {
    expect(scheduleTabLabel('Daily Execution x3 (10:00 / 15:00 / 20:00 IST)')).toBe('Daily Execution x3 (10:00 / 15:00 / 20:00 IST)')
    expect(scheduleTabLabel('Lead finding — US (Mon/Wed/Fri)')).toBe('Lead finding — US (Mon/Wed/Fri)')
  })

  it('leaves visual truncation to the tab component', () => {
    expect(scheduleTabLabel('  Weekly Strategy Discovery Proposer Pass  ')).toBe('Weekly Strategy Discovery Proposer Pass')
  })

  it('distinguishes two schedules that used to render identically', () => {
    const a = scheduleTabLabel('Daily Execution x3 (10:00 / 15:00 / 20:00 IST)')
    const b = scheduleTabLabel('Daily Measurement & Critqueue')
    expect(a).not.toBe(b)
  })

  it('distinguishes schedules with the same name but different times', () => {
    expect(scheduleTabLabel('News Briefing (7:30 AM IST)')).not.toBe(scheduleTabLabel('News Briefing (8:30 AM IST)'))
  })
})

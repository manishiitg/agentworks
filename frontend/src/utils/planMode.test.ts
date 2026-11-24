import { describe, expect, it } from 'vitest'
import { answersPlanCard, approvesPlan, isPlanToggleKey, planNeedsStartButton } from './planMode'

const key = (k: string, mods: Partial<{ shiftKey: boolean; ctrlKey: boolean; metaKey: boolean; altKey: boolean; isComposing: boolean }> = {}) =>
  ({ key: k, shiftKey: false, ctrlKey: false, metaKey: false, altKey: false, ...mods })

describe('Plan switch (#3699)', () => {
  it('toggles on Shift+Tab only', () => {
    expect(isPlanToggleKey(key('Tab', { shiftKey: true }))).toBe(true)
    expect(isPlanToggleKey(key('Tab'))).toBe(false)
    expect(isPlanToggleKey(key('Tab', { shiftKey: true, metaKey: true }))).toBe(false)
    expect(isPlanToggleKey(key('Tab', { shiftKey: true, isComposing: true }))).toBe(false)
    expect(isPlanToggleKey(key('Enter', { shiftKey: true }))).toBe(false)
  })
  it('offers Start for Codex and Cursor, and for Claude when its plan card never came', () => {
    expect(planNeedsStartButton('codex-cli')).toBe(true)
    expect(planNeedsStartButton('cursor-cli')).toBe(true)
    expect(planNeedsStartButton('claude-code')).toBe(true)
    expect(planNeedsStartButton(undefined)).toBe(false)
  })
  it('an approved Claude plan card turns Plan off; a rejection keeps it', () => {
    expect(approvesPlan([{ id: 'plan', selectedLabels: ['Approve'] }])).toBe(true)
    expect(approvesPlan([{ id: 'plan', selectedLabels: ['Reject'] }])).toBe(false)
    expect(approvesPlan([{ id: '0', selectedLabels: ['Approve'] }])).toBe(false)
  })

  it('treats any answer to the plan card as the card having come', () => {
    expect(answersPlanCard([{ id: 'plan' }])).toBe(true)
    expect(answersPlanCard([{ id: '0' }])).toBe(false)
  })
})

import { describe, expect, it } from 'vitest'
import { approvesPlan, isPlanToggleKey, planNeedsStartButton } from './planMode'

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
  it('offers Start the work for Codex and Cursor; Claude has its own approval card', () => {
    expect(planNeedsStartButton('codex-cli')).toBe(true)
    expect(planNeedsStartButton('cursor-cli')).toBe(true)
    expect(planNeedsStartButton('claude-code')).toBe(false)
    expect(planNeedsStartButton(undefined)).toBe(false)
  })
  it('an approved Claude plan card turns Plan off; a rejection keeps it', () => {
    expect(approvesPlan([{ id: 'plan', selectedLabels: ['Approve'] }])).toBe(true)
    expect(approvesPlan([{ id: 'plan', selectedLabels: ['Reject'] }])).toBe(false)
    expect(approvesPlan([{ id: '0', selectedLabels: ['Approve'] }])).toBe(false)
  })
})

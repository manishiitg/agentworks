import { describe, expect, it } from 'vitest'
import { liveTerminalControlKey } from './liveTerminalKeys'

// PLAT-746-938: screenshot-only Enter was swallowed by the empty-input CLI bridge.
describe('composer attachments own Enter before and after upload', () => {
  it('leaves Enter and arrows to the composer with attachments, retaining Escape and empty CLI navigation', () => {
    for (const key of ['Enter', 'ArrowUp', 'ArrowDown']) {
      expect(liveTerminalControlKey({ key }, '', true)).toBe(null)
      expect(liveTerminalControlKey({ key }, '  ', true)).toBe(null)
      expect(liveTerminalControlKey({ key }, 'caption')).toBe(null)
    }
    expect(liveTerminalControlKey({ key: 'Enter' }, '')).toBe('Enter')
    expect(liveTerminalControlKey({ key: 'Escape' }, '', true)).toBe('Escape')
    expect(liveTerminalControlKey({ key: 'Enter', shiftKey: true }, '', true)).toBe(null)
    expect(liveTerminalControlKey({ key: 'Enter', metaKey: true }, '', true)).toBe(null)
  })
})

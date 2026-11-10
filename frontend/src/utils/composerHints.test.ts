import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { nextComposerHint, recordComposerShortcut } from './composerHints'

beforeEach(() => {
  const values = new Map<string, string>()
  vi.stubGlobal('localStorage', { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => values.set(key, value) })
})
afterEach(() => vi.unstubAllGlobals())
it('does not teach a used shortcut when an unfamiliar hint is due', () => {
  localStorage.setItem('agentworks.composer-hints:person', JSON.stringify({ blurs: 19 }))
  recordComposerShortcut('person', 'queue')
  expect(nextComposerHint('person', [{ id: 'queue', text: 'queue hint' }, { id: 'newline', text: 'newline hint' }])).toBe('newline hint')
  expect(nextComposerHint('person', [{ id: 'queue', text: 'queue hint' }])).toBeNull()
})
it('skips shortcut hints already recorded by the app and limits repeat exposure', () => {
  localStorage.setItem('agentworks.composer-hints:person', JSON.stringify({ blurs: 19 }))
  localStorage.setItem('agentworks.features-used', JSON.stringify({ 'chat-tab-keys': 1 }))
  const hints = [{ id: 'tabs', text: 'tabs', feature: 'chat-tab-keys' }, { id: 'queue', text: 'queue' }]
  expect(nextComposerHint('person', hints)).toBe('queue')
  for (let i = 0; i < 40; i++) expect(nextComposerHint('person', hints)).toBeNull()
})

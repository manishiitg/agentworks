import { expect, it } from 'vitest'
import { backgroundActiveInMode } from './appearanceTheme'

// #4075: a mode-scoped shader or script background runs only in its own mode.
it('runs a background only in its mode', () => {
  expect(backgroundActiveInMode(undefined, 'light')).toBe(true)
  expect(backgroundActiveInMode('both', 'light')).toBe(true)
  expect(backgroundActiveInMode('dark', 'dark')).toBe(true)
  expect(backgroundActiveInMode('dark', 'light')).toBe(false)
  expect(backgroundActiveInMode('light', 'dark')).toBe(false)
})

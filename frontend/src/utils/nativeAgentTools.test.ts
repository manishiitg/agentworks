import { expect, it } from 'vitest'
import { nativeAgentToolsEnabled } from './nativeAgentTools'

it('keeps native agent tools on whatever an older manifest saved', () => {
  expect(nativeAgentToolsEnabled(undefined)).toBe(true)
  expect(nativeAgentToolsEnabled(true)).toBe(true)
  expect(nativeAgentToolsEnabled(false)).toBe(true)
})

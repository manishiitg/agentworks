import { expect, it } from 'vitest'
import { CODE_PRODUCT, CREW_PRODUCT } from './projectProduct'

// Native agent tools are on by default for Crew and Code (owner decision
// 2026-09-28/29). A Crew's owner can turn them off with its switch; a Code
// has no switch (2026-09-29): they are always on there.
it('only Crew shows the "Native agent tools" switch', () => {
  expect(CREW_PRODUCT.hasNativeAgentToolsSetting).toBe(true)
  expect(CODE_PRODUCT.hasNativeAgentToolsSetting).toBe(false)
})

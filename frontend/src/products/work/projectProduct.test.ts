import { expect, it } from 'vitest'
import { CODE_PRODUCT, CREW_PRODUCT } from './projectProduct'

// Native agent tools are always on in a Crew and a Code (owner decision
// 2026-09-29): people cannot turn them off, so neither shows the switch.
it('neither Crew nor Code shows a "Native agent tools" switch', () => {
  expect(CREW_PRODUCT.hasNativeAgentToolsSetting).toBe(false)
  expect(CODE_PRODUCT.hasNativeAgentToolsSetting).toBe(false)
})

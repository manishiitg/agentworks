import { expect, it } from 'vitest'
import { CODE_PRODUCT, CREW_PRODUCT } from './projectProduct'

// Owner decision 2026-09-28/29: native agent tools are on by default for
// Crew and Code, and each shows the switch its owner turns them off with.
it('Crew and Code both show the "Native agent tools" switch', () => {
  expect(CREW_PRODUCT.hasNativeAgentToolsSetting).toBe(true)
  expect(CODE_PRODUCT.hasNativeAgentToolsSetting).toBe(true)
})

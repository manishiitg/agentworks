import { describe, expect, it } from 'vitest'
import { CODE_PRODUCT, CREW_PRODUCT, projectProductForPath } from './projectProduct'

// Native agent tools are always on in a Crew and a Code (owner decision
// 2026-09-29): people cannot turn them off, so neither shows the switch.
it('neither Crew nor Code shows a "Native agent tools" switch', () => {
  expect(CREW_PRODUCT.hasNativeAgentToolsSetting).toBe(false)
  expect(CODE_PRODUCT.hasNativeAgentToolsSetting).toBe(false)
})

describe('projectProductForPath at the shared root', () => {
  it('knows a Crew at Crew/<folder>, in any spelling of the folder, and nothing else under Crew', () => {
    expect(projectProductForPath('Crew/sde-1a2b3c4d')).toBe(CREW_PRODUCT)
    expect(projectProductForPath('/Crew/sde-1a2b3c4d/db/reports')).toBe(CREW_PRODUCT)
    expect(projectProductForPath('Crew')).toBeNull()
    expect(projectProductForPath('Crew/')).toBeNull()
    expect(projectProductForPath('Crew/.migrating/x')).toBeNull()
    expect(projectProductForPath('Chats/Work/projects/x-1')).toBe(CREW_PRODUCT)
  })
})

import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

// "Use account" saved only the provider, so a Crew or Code kept launching the server account
// (Claude's login screen on excellence and Confida, 2026-09-30).
describe('Crew/Code account selection is saved', () => {
  it('passes the chosen account into the saved project config', () => {
    const surface = readFileSync('src/products/work/WorkSurface.tsx', 'utf8')
    const save = surface.slice(surface.indexOf('const updateLLMConfig'), surface.indexOf('const updateNativeAgentTools'))
    expect(save).toContain('connectionId: selection.connectionId')
  })

  it('writes and reads the account on builder_llm', () => {
    const sessions = readFileSync('src/products/work/workSessions.ts', 'utf8')
    expect(sessions).toContain('connection_id: selection.connectionId')
    expect(sessions).toContain('connectionId: builder.connection_id')
  })
})

import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import { workLLMConfigFromSelection, workLLMSelectionFromConfig } from './workSessions'

describe('Crew/Code account selection is saved', () => {
  it('keeps the chosen account through a save and a reload', () => {
    const config = workLLMConfigFromSelection({ connectionId: 'acct-1', provider: 'claude-code', modelId: 'claude-sonnet-5-5' })
    expect(config.builder_llm?.connection_id).toBe('acct-1')
    expect(workLLMSelectionFromConfig(config)?.connectionId).toBe('acct-1')
  })

  it('passes the account from "Use account" into the saved project config', () => {
    // "Use account" saved only the provider, so the Crew stayed on the server account.
    const surface = readFileSync('src/products/work/WorkSurface.tsx', 'utf8')
    const save = surface.slice(surface.indexOf('const updateLLMConfig'), surface.indexOf('const updateNativeAgentTools'))
    expect(save).toContain('connectionId: selection.connectionId')
  })
})

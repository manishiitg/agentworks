import { describe, expect, it } from 'vitest'
import { sharedAccountProvider } from './tokenLimits'

describe('sharedAccountProvider', () => {
  // A Claude Code chat showed the Codex account's limits: its tab still named global:codex-cli from before the switch.
  it('follows the current provider when the saved shared account names another one', () => {
    expect(sharedAccountProvider('global:codex-cli', 'claude-code')).toBe('claude-code')
    expect(sharedAccountProvider('global:claude-code', 'claude-code')).toBe('claude-code')
    expect(sharedAccountProvider('', 'claude-code')).toBe('claude-code')
    expect(sharedAccountProvider('personal-account-id', 'claude-code')).toBeNull()
  })
})

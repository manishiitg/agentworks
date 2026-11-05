import { describe, expect, it, vi } from 'vitest'

vi.mock('../services/api', () => ({
  getApiBaseUrl: () => 'http://localhost:8080',
  getAuthToken: () => null,
}))

import { secretsApi } from './secrets'

// All in Vault (PLAT-856): a project's secret is saved in its owner's Vault; there is
// no per-project store to list, reveal, share or promote from. This guards the
// client surface so the retired methods cannot creep back.
describe('secretsApi Vault-only contract', () => {
  it('exposes the Vault secret methods', () => {
    for (const method of [
      'storeWorkflowSecret',
      'deleteWorkflowSecret',
      'getGlobalSecrets',
      'saveGlobalSecret',
      'deleteGlobalSecret',
      'revealGlobalSecret',
      'encrypt',
      'decrypt',
    ] as const) {
      expect(typeof secretsApi[method], method).toBe('function')
    }
  })

  it('exposes no per-user secret methods', () => {
    const client = secretsApi as unknown as Record<string, unknown>
    expect(client.storeSecret).toBeUndefined()
    expect(client.deleteStoredSecret).toBeUndefined()
    expect(client.listStoredSecrets).toBeUndefined()
    expect(client.listWorkflowSecrets).toBeUndefined()
    expect(client.promoteWorkflowSecret).toBeUndefined()
    expect(client.shareWorkflowSecret).toBeUndefined()
  })
})

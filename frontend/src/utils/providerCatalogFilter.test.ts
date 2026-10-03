import { describe, it, expect } from 'vitest'
import { installedCodingProviders, nonDeprecatedProviders, readyCodingProviders } from './providerCatalogFilter'
import type { ProviderConnection, ProviderManifestEntry } from '../services/llm-config-api'

// Minimal fixture -- only the fields nonDeprecatedProviders reads.
const provider = (id: string, deprecated?: boolean): ProviderManifestEntry =>
  ({ id, deprecated } as ProviderManifestEntry)

describe('nonDeprecatedProviders', () => {
  // The regression this exists for: 2026-08-20, direct API transport
  // (openai/anthropic/vertex/bedrock/azure) deprecated in favor of MCP-routed
  // coding CLIs (docs/design/api_transport_vs_pi_tradeoff.md). The catalog
  // must not list them at all -- no marker, simply absent.
  it('drops deprecated providers entirely', () => {
    const result = nonDeprecatedProviders([
      provider('openai', true),
      provider('pi-cli', false),
    ])
    expect(result.map(p => p.id)).toEqual(['pi-cli'])
  })

  it('keeps a provider with no deprecated field at all', () => {
    const result = nonDeprecatedProviders([provider('codex-cli')])
    expect(result).toHaveLength(1)
  })

  it('returns everything when nothing is deprecated', () => {
    const result = nonDeprecatedProviders([provider('a'), provider('b')])
    expect(result).toHaveLength(2)
  })
})

describe('installedCodingProviders', () => {
  it('keeps installed CLIs needing sign-in and excludes absent, unknown, retired and API providers', () => {
    const cli = { integration_kind: 'coding_agent', runtime_available: true } as const
    const result = installedCodingProviders([
      { ...provider('claude-code'), ...cli, auth_configured: false, usable: false },
      { ...provider('codex-cli'), ...cli, runtime_available: false },
      { ...provider('cursor-cli'), ...cli, runtime_available: undefined },
      { ...provider('retired', true), ...cli },
      { ...provider('openai'), ...cli, integration_kind: 'api_model' },
    ])
    expect(result.map(entry => entry.id)).toEqual(['claude-code'])
  })
})

describe('readyCodingProviders', () => {
  const cli = (id: string, usable = true) => ({
    ...provider(id), integration_kind: 'coding_agent', runtime_available: true, usable,
  } as ProviderManifestEntry)
  const account = (overrides: Partial<ProviderConnection>): ProviderConnection => ({
    id: 'personal', provider: 'claude-code', scope: 'user', auth_method: 'api_key',
    display_name: 'Personal', ...overrides,
  })

  it('hides setup-only providers but keeps a ready personal account when the server is signed out', () => {
    const providers = [cli('claude-code', false), cli('codex-cli', false), cli('cursor-cli', false)]
    const accounts = [
      account({ scope: 'global', configured: false }),
      account({ configured: true }),
      account({ provider: 'codex-cli', configured: false }),
      account({ provider: 'cursor-cli', relation: 'admin_view' }),
    ]
    expect(readyCodingProviders(providers, accounts).map(p => p.id)).toEqual(['claude-code'])
  })

  it('honors scoped availability and the server restriction on personal accounts', () => {
    const accounts = [
      account({ scope: 'global', usable: false, personal_accounts_allowed: false }),
      account({ configured: true }),
      account({ provider: 'codex-cli', usable: false }),
    ]
    expect(readyCodingProviders([cli('claude-code'), cli('codex-cli', false)], accounts)).toEqual([])
  })

  it('keeps unknown personal authentication compatible but still requires an installed CLI', () => {
    const accounts = [account({}), account({ provider: 'codex-cli' })]
    expect(readyCodingProviders([
      cli('claude-code', false), { ...cli('codex-cli'), runtime_available: false },
    ], accounts).map(p => p.id)).toEqual(['claude-code'])
  })

  it('does not offer a manifest-ready server account whose scoped status needs setup', () => {
    expect(readyCodingProviders([cli('claude-code')], [account({ scope: 'global', configured: false })])).toEqual([])
  })
})

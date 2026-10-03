import type { ProviderConnection, ProviderManifestEntry } from '../services/llm-config-api'

// Extracted from LibraryTab.tsx so it can be tested without importing the
// component (which pulls in useLLMStore and the wider app graph).

// Deprecated providers (2026-08-20: direct API transport, see
// docs/design/api_transport_vs_pi_tradeoff.md) don't show in the provider
// catalog at all -- not listed with a deprecated marker, simply absent.
export function nonDeprecatedProviders(providers: ProviderManifestEntry[]): ProviderManifestEntry[] {
  return providers.filter(provider => !provider.deprecated)
}

// Installation is independent of authentication: an installed CLI that needs
// sign-in still belongs in provider management, while an absent CLI does not.
export function installedCodingProviders(providers: ProviderManifestEntry[]): ProviderManifestEntry[] {
  return providers.filter(provider => !provider.deprecated
    && provider.integration_kind === 'coding_agent'
    && provider.runtime_available === true)
}

// Scoped records override the installation's access; a personal account can
// also make a signed-out server CLI ready. Missing configured means unknown.
export function readyCodingProviders(providers: ProviderManifestEntry[], accounts: ProviderConnection[]): ProviderManifestEntry[] {
  return installedCodingProviders(providers).filter(provider => {
    const server = accounts.find(account => account.provider === provider.id && account.scope === 'global')
    if (provider.usable && server?.usable !== false && server?.configured !== false) return true
    if (server?.personal_accounts_allowed === false) return false
    return accounts.some(account => account.provider === provider.id
      && account.scope === 'user' && account.relation !== 'admin_view'
      && account.usable !== false && account.configured !== false)
  })
}

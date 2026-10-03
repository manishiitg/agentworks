import type { ProviderManifestEntry } from '../services/llm-config-api'

// Extracted from LibraryTab.tsx so it can be tested without importing the
// component (which pulls in useLLMStore and the wider app graph).

// Deprecated providers (2026-08-20: direct API transport, see
// docs/design/api_transport_vs_pi_tradeoff.md) don't show in the provider
// catalog at all -- not listed with a deprecated marker, simply absent.
export function nonDeprecatedProviders(providers: ProviderManifestEntry[]): ProviderManifestEntry[] {
  return providers.filter(provider => !provider.deprecated)
}

// Installation is independent of authentication: an installed CLI that needs
// sign-in still belongs in setup, while an absent CLI does not.
export function installedCodingProviders(providers: ProviderManifestEntry[]): ProviderManifestEntry[] {
  return providers.filter(provider => !provider.deprecated
    && provider.integration_kind === 'coding_agent'
    && provider.runtime_available === true)
}

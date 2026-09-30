import { describe, expect, it } from 'vitest'
import type { DelegationTierConfig } from '../services/api-types'
import type { ProviderManifestEntry } from '../services/llm-config-api'
import { resolveDelegationMainModel } from './workflowLLMTierDefaults'

const codexManifest: ProviderManifestEntry = {
  id: 'codex-cli',
  display_name: 'OpenAI Codex CLI',
  description: 'Codex',
  kind: 'local_cli',
  integration_kind: 'coding_agent',
  model_selection_mode: 'fixed_tier',
  auth_description: 'Local CLI',
  auth_configured: true,
  usable: true,
  requires_api_key: false,
  supports_dynamic_models: false,
  default_model_id: 'gpt-6.1-sol',
  default_tier_models: {
    builder: { provider: 'codex-cli', model_id: 'gpt-6.1-sol', options: { reasoning_effort: 'high' } },
    high: { provider: 'codex-cli', model_id: 'gpt-6.1-sol', options: { reasoning_effort: 'xhigh' } },
    medium: { provider: 'codex-cli', model_id: 'gpt-6.1-sol', options: { reasoning_effort: 'medium' } },
    low: { provider: 'codex-cli', model_id: 'gpt-6-luna', options: { reasoning_effort: 'medium' } },
    maintenance: { provider: 'codex-cli', model_id: 'gpt-6.1-sol', options: { reasoning_effort: 'high' } },
  },
  models: [],
  capabilities: [],
}

describe('resolveDelegationMainModel', () => {
  it('expands a simple Codex profile instead of retaining a stale chat model', () => {
    const config: DelegationTierConfig = {
      schema_version: 2,
      mode: 'provider_profile',
      provider: 'codex-cli',
    }

    expect(resolveDelegationMainModel(config, [codexManifest])).toEqual({
      provider: 'codex-cli',
      model_id: 'gpt-6.1-sol',
      options: { reasoning_effort: 'high' },
    })
  })

  it('preserves the advanced main-agent selection', () => {
    const config: DelegationTierConfig = {
      schema_version: 2,
      mode: 'explicit',
      main: { provider: 'openrouter', model_id: 'grok-1' },
    }

    expect(resolveDelegationMainModel(config, [codexManifest])).toEqual({
      provider: 'openrouter',
      model_id: 'grok-1',
    })
  })
})

import type { AgentLLMConfig, DelegationTierConfig, LLMProvider, PresetLLMConfig } from '../services/api-types'
import type {
  ProviderDefaultTierModels,
  ProviderManifestEntry,
  ProviderTierModelRef,
} from '../services/llm-config-api'
import type { LLMOption } from '../types/llm'

type WorkflowTierDefaults = {
  builder: AgentLLMConfig
  tier1: AgentLLMConfig
  tier2: AgentLLMConfig
  tier3: AgentLLMConfig
  maintenance: AgentLLMConfig
  pulse: AgentLLMConfig
  usesTierDefaults: boolean
}

const hasOptions = (options?: Record<string, unknown>) =>
  Boolean(options && Object.keys(options).length > 0)

function toAgentLLMConfig(option: LLMOption, modelID: string, provider = option.provider): AgentLLMConfig {
  const samePublishedModel = provider === option.provider && modelID === option.model
  return {
    ...(samePublishedModel && option.id ? { published_llm_id: option.id } : {}),
    provider: provider as LLMProvider,
    model_id: modelID,
    ...(samePublishedModel && hasOptions(option.options) ? { options: option.options } : {}),
  }
}

function toAgentLLMConfigFromRef(
  option: LLMOption,
  ref: ProviderTierModelRef,
): AgentLLMConfig {
  const config = toAgentLLMConfig(option, ref.model_id, ref.provider)
  if (hasOptions(ref.options)) {
    return { ...config, options: ref.options }
  }
  return config
}

function sameModelDefaults(option: LLMOption): WorkflowTierDefaults {
  const builder = toAgentLLMConfig(option, option.model)
  return {
    builder,
    tier1: builder,
    tier2: builder,
    tier3: builder,
    maintenance: builder,
    pulse: builder,
    usesTierDefaults: false,
  }
}

function findManifestDefaults(provider: string, providerManifest: ProviderManifestEntry[] = []) {
  return providerManifest.find(entry => entry.id === provider && !entry.deprecated)?.default_tier_models
}

function resolveManifestDefaults(defaults?: ProviderDefaultTierModels) {
  if (!defaults) return null
  const builder = defaults.builder ?? defaults.main ?? defaults.high
  const maintenance = defaults.maintenance ?? defaults.auto_improve ?? defaults.phase ?? defaults.high
  if (!builder || !defaults.high || !defaults.medium || !defaults.low || !maintenance) return null
  return {
    builder,
    high: defaults.high,
    medium: defaults.medium,
    low: defaults.low,
    maintenance,
    pulse: defaults.pulse ?? defaults.high,
  }
}

/** Resolve the live AgentWorks root-chat model from simple or advanced setup. */
export function resolveDelegationMainModel(
  config: DelegationTierConfig | null | undefined,
  providerManifest: ProviderManifestEntry[] = [],
): AgentLLMConfig | null {
  if (!config) return null

  if (config.mode === 'provider_profile' && config.provider) {
    const defaults = resolveManifestDefaults(findManifestDefaults(config.provider, providerManifest))
    if (!defaults) return null
    return {
      provider: defaults.builder.provider as LLMProvider,
      model_id: defaults.builder.model_id,
      ...(hasOptions(defaults.builder.options) ? { options: defaults.builder.options } : {}),
    }
  }

  if (!config.main?.provider || !config.main.model_id) return null
  return {
    provider: config.main.provider as LLMProvider,
    model_id: config.main.model_id,
    ...(hasOptions(config.main.options) ? { options: config.main.options } : {}),
  }
}

function getTierDefaults(option: LLMOption, providerManifest: ProviderManifestEntry[] = []) {
  return findManifestDefaults(option.provider, providerManifest)
}

export function getWorkflowLLMTierDefaults(
  option: LLMOption,
  providerManifest: ProviderManifestEntry[] = [],
): WorkflowTierDefaults {
  if (option.section === 'published_model') return sameModelDefaults(option)

  const defaults = resolveManifestDefaults(getTierDefaults(option, providerManifest))
  if (!defaults) return sameModelDefaults(option)

  return {
    builder: toAgentLLMConfigFromRef(option, defaults.builder),
    tier1: toAgentLLMConfigFromRef(option, defaults.high),
    tier2: toAgentLLMConfigFromRef(option, defaults.medium),
    tier3: toAgentLLMConfigFromRef(option, defaults.low),
    maintenance: toAgentLLMConfigFromRef(option, defaults.maintenance),
    pulse: toAgentLLMConfigFromRef(option, defaults.pulse),
    usesTierDefaults: true,
  }
}

export function getWorkflowProviderOptions(
  providerManifest: ProviderManifestEntry[] = [],
): LLMOption[] {
  const options: LLMOption[] = []

  providerManifest.forEach(entry => {
    if (entry.deprecated) return
    if (entry.integration_kind !== 'coding_agent') return
    const defaults = resolveManifestDefaults(entry.default_tier_models)
    if (!defaults) return

    const builder = defaults.builder
    options.push({
      provider: builder.provider,
      model: builder.model_id,
      label: entry.display_name,
      description: entry.usable
        ? entry.description
        : entry.setup_hint || entry.description,
      section: 'coding_agent',
      ...(hasOptions(builder.options) ? { options: builder.options } : {}),
    })
  })

  return options
}

function providerModelOptions(entry: ProviderManifestEntry): LLMOption[] {
  const options: LLMOption[] = []
  entry.models.forEach(model => {
    const base = {
      provider: entry.id,
      model: model.model_id,
      description: entry.description,
      section: entry.integration_kind === 'coding_agent' ? 'coding_agent' as const : 'published_model' as const,
    }
    const reasoningLevels = model.reasoning_effort_levels ?? []
    const thinkingLevels = model.thinking_levels ?? []
    if (reasoningLevels.length > 0) {
      reasoningLevels.forEach(level => options.push({
        ...base,
        label: `${model.model_name || model.model_id} · ${level}`,
        options: { reasoning_effort: level },
      }))
      return
    }
    if (thinkingLevels.length > 0) {
      thinkingLevels.forEach(level => options.push({
        ...base,
        label: `${model.model_name || model.model_id} · ${level}`,
        options: { thinking_level: level },
      }))
      return
    }
    options.push({ ...base, label: model.model_name || model.model_id })
  })
  return options
}

// Providers whose catalog is not a usable pick list. Pi routes to many
// upstream providers (Gemini, Z.AI, Kimi, DeepSeek, ...) and only the ones the
// user has keyed and published actually work, so the workflow model picker
// offers Pi's published entries only; the full catalog stays in the Pi
// drill-in where entries get published.
const PUBLISHED_ONLY_PROVIDERS = new Set<string>(['pi-cli'])

export function getWorkflowLLMOptions(
  availableLLMs: LLMOption[],
  providerManifest: ProviderManifestEntry[] = [],
): LLMOption[] {
  const options: LLMOption[] = []
  const seen = new Set<string>()
  const catalogModelIds = new Map<string, Set<string>>()

  providerManifest.forEach(entry => {
    if (entry.deprecated || entry.integration_kind === 'audio_provider') return
    catalogModelIds.set(entry.id, new Set(entry.models.map(model => model.model_id)))
    if (PUBLISHED_ONLY_PROVIDERS.has(entry.id)) return
    providerModelOptions(entry).forEach(option => {
      const key = `${option.provider}/${option.model}/${JSON.stringify(option.options ?? {})}`
      if (seen.has(key)) return
      seen.add(key)
      options.push(option)
    })
  })

  availableLLMs.forEach(option => {
    // A published entry whose "model" is just the provider id (e.g. Cursor
    // CLI / cursor-cli) is the old "let the CLI route" alias. Where the
    // catalog still lists that id (claude-code does) it dedupes below; where
    // the catalog moved on to a real default id (cursor-cli now uses "auto"),
    // the alias would show up as a second, differently named copy of the
    // default. Drop it: the catalog default already stands for it.
    if (option.model === option.provider && !catalogModelIds.get(option.provider)?.has(option.model)) return
    const key = `${option.provider}/${option.model}/${JSON.stringify(option.options ?? {})}`
    if (seen.has(key)) return
    seen.add(key)
    options.push({
      ...option,
      section: 'published_model',
    })
  })

  console.log('[WorkflowLLMOptions] Built workflow dropdown options', {
    providerManifestCount: providerManifest.length,
    publishedModelCount: availableLLMs.length,
    codingAgents: options
      .filter(option => option.section === 'coding_agent')
      .map(option => ({ provider: option.provider, model: option.model, label: option.label })),
    publishedModels: options
      .filter(option => option.section === 'published_model')
      .map(option => ({ provider: option.provider, model: option.model, label: option.label })),
    manifestCodingAgentProviders: providerManifest
      .filter(entry => entry.integration_kind === 'coding_agent' && !entry.deprecated)
      .map(entry => ({
        id: entry.id,
        displayName: entry.display_name,
        usable: entry.usable,
        hasDefaultTierModels: Boolean(entry.default_tier_models),
        builder: entry.default_tier_models?.builder,
      })),
  })

  return options
}

function workflowProviderDefaults(provider: string | undefined, providerManifest: ProviderManifestEntry[]): WorkflowTierDefaults | null {
  const option = provider ? getWorkflowProviderOptions(providerManifest).find(candidate => candidate.provider === provider) : undefined
  return option ? getWorkflowLLMTierDefaults(option, providerManifest) : null
}

/** The model a workflow's Builder chat is set to: its pinned Builder role, else its provider's Builder default. */
export function workflowBuilderLLM(config: PresetLLMConfig | null | undefined, providerManifest: ProviderManifestEntry[] = []): AgentLLMConfig | null {
  if (!config) return null
  if (config.mode === 'explicit') return config.builder_llm ?? null
  const builder = workflowProviderDefaults(config.provider, providerManifest)?.builder
  return builder ? { ...builder, connection_id: config.connection_id } : null
}

/**
 * The workflow setting with only the Builder chat moved to `builder`, as the Models
 * page's Builder row saves it: the other roles keep what they run on today.
 */
export function withWorkflowBuilderLLM(config: PresetLLMConfig, builder: AgentLLMConfig, providerManifest: ProviderManifestEntry[] = []): PresetLLMConfig {
  const defaults = config.mode === 'explicit' ? null : workflowProviderDefaults(config.provider, providerManifest)
  const keep = (current: AgentLLMConfig | undefined, fallback: AgentLLMConfig | undefined) =>
    (config.mode === 'explicit' ? current : undefined) ?? (fallback ? { ...fallback, connection_id: config.connection_id } : undefined)
  const tier1 = keep(config.tiered_config?.tier_1, defaults?.tier1) ?? builder
  return {
    ...config,
    schema_version: 2,
    mode: 'explicit',
    builder_llm: builder,
    background_llm: keep(config.background_llm, defaults?.pulse),
    tiered_config: {
      tier_1: tier1,
      tier_2: keep(config.tiered_config?.tier_2, defaults?.tier2) ?? builder,
      tier_3: keep(config.tiered_config?.tier_3, defaults?.tier3) ?? builder,
    },
  }
}

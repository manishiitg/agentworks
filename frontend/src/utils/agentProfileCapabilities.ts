import { getApiBaseUrl, getAuthToken } from '../services/api'
import type { ModelMetadata } from '../services/llm-config-api'

// Reads a capability declared on an agent profile's runtime.capabilities block
// (agentprofiles.RuntimeCapabilities in agent_go/pkg/agentprofiles/types.go).
// Deliberately generic over the profile id: a product opts into a shared
// capability (browser, secrets, voice, ...) by declaring it in its OWN
// product.yaml, so the frontend must not hardcode which product has which
// capability — that defeats the point of it being product.yaml-driven.

export type AgentProfileProviderOption = {
  id: string
  label?: string
  provider?: string
  model_id?: string
  default?: boolean
  options?: Record<string, unknown>
  /** Curates the composer's model list to exactly these ids; empty offers every catalog model for `provider`. */
  models?: string[]
  /** Offers supported reasoning-effort choices for this engine; empty offers none. */
  reasoning_efforts?: string[]
}

export type AgentProfileEngineGroup = {
  option: AgentProfileProviderOption
  models: Array<{ id: string; label: string }>
  reasoningLevels: Array<{ id: string; label: string }>
}

/** Shared profile-to-picker adapter used by product composers and settings. */
export function buildAgentProfileEngineGroups(
  options: AgentProfileProviderOption[],
  modelCatalog: ModelMetadata[],
): AgentProfileEngineGroup[] {
  const pseudo = new Set(['high', 'medium', 'low'])
  return options.map((option) => {
    const provider = (option.provider ?? '').trim()
    const catalogByID = new Map(modelCatalog.filter((model) => model.provider === provider).map((model) => [model.model_id, model]))
    const models = option.models && option.models.length > 0
      ? option.models.map((id) => ({ id, label: catalogByID.get(id)?.model_name || id }))
      : modelCatalog
          .filter((model) => model.provider === provider && model.model_id !== provider && !pseudo.has(model.model_id))
          .map((model) => ({ id: model.model_id, label: model.model_name || model.model_id }))
    const own = (option.model_id ?? '').trim()
    if (own && !models.some((model) => model.id === own)) models.unshift({ id: own, label: catalogByID.get(own)?.model_name || own })
    const reasoningLevels = (option.reasoning_efforts ?? []).map((id) => ({ id, label: id.charAt(0).toUpperCase() + id.slice(1) }))
    return { option, models, reasoningLevels }
  })
}

/** Restrict a profile's choices to the selected model's capabilities. */
export function modelReasoningLevels(option: AgentProfileProviderOption | undefined, model: ModelMetadata | undefined) {
  if (!option || option.provider === 'agy-cli' || model?.supports_reasoning_effort === false) return []
  const supported = model?.reasoning_effort_levels
  // Cursor's live IDs can encode effort themselves; only known configurable
  // selectors get a separate control. Auto and Composer offer none.
  if (option.provider === 'cursor-cli' && !supported?.length) return []
  return (option.reasoning_efforts || [])
    .filter(id => !supported?.length || supported.includes(id))
    .map(id => ({ id, label: id.charAt(0).toUpperCase() + id.slice(1) }))
}

type AgentProfileResponse = {
  resolved_features?: AgentProfileFeature[]
  tools?: Array<{ interaction?: AgentProfileInteractionBinding }>
  interactions?: AgentProfileInteractionBinding[]
  runtime?: {
    provider?: string
    model_id?: string
    transport?: string
    capabilities?: Record<string, unknown>
    provider_options?: AgentProfileProviderOption[]
  }
}

export type AgentProfileInteractionBinding = {
  kind: string
  render: string
  message?: string
}

export type AgentProfileFeature = {
  id: string
  dependencies?: string[]
  tools?: string[]
  skills?: string[]
  prompt_extension?: string
  ui_panels?: string[]
  capabilities?: Record<string, unknown>
  options?: Record<string, string>
}

export type AgentProfileRuntime = {
  provider: string
  model_id: string
}

const capabilityCache = new Map<string, Promise<boolean>>()
const profileCache = new Map<string, Promise<AgentProfileResponse>>()

function loadAgentProfile(profileId: string, version?: number): Promise<AgentProfileResponse> {
  const normalizedVersion = version && version > 0 ? version : undefined
  const cacheKey = `${profileId}::${normalizedVersion ?? 'latest'}`
  const cached = profileCache.get(cacheKey)
  if (cached) return cached

  const token = getAuthToken()
  const versionQuery = normalizedVersion ? `?version=${normalizedVersion}` : ''
  const promise = fetch(`${getApiBaseUrl()}/api/agent-profiles/${encodeURIComponent(profileId)}${versionQuery}`, {
    headers: token ? { Authorization: `Bearer ${token}` } : undefined,
  }).then((response) => {
    if (!response.ok) throw new Error(`Unable to load agent profile ${profileId} (${response.status})`)
    return response.json() as Promise<AgentProfileResponse>
  })

  profileCache.set(cacheKey, promise)
  return promise
}

export async function loadAgentProfileRuntime(
  profileId: string,
  version?: number,
): Promise<AgentProfileRuntime | null> {
  if (!profileId) return null
  try {
    const profile = await loadAgentProfile(profileId, version)
    const provider = profile.runtime?.provider?.trim() || ''
    const modelId = profile.runtime?.model_id?.trim() || ''
    return provider && modelId ? { provider, model_id: modelId } : null
  } catch {
    return null
  }
}

/**
 * Resolves whether `profileId` declared `capability` as anything other than
 * "disabled" (or absent). Mirrors agentprofiles.CapabilityRequirement: string
 * equality rather than a closed union, so an unrecognized future requirement
 * value (e.g. a new tier between preferred/optional) still gates as enabled
 * rather than silently hiding a capability the backend actually turned on.
 *
 * Cached per (profileId, capability) for the page session — this is read at
 * composer-mount time, and a profile's declared capabilities do not change
 * without a server restart.
 */
export function loadAgentProfileCapabilityEnabled(profileId: string, capability: string, version?: number): Promise<boolean> {
  if (!profileId) return Promise.resolve(false)
  const cacheKey = `${profileId}::${version ?? 'latest'}::${capability}`
  const cached = capabilityCache.get(cacheKey)
  if (cached) return cached

  const promise = loadAgentProfile(profileId, version)
    .then((profile) => {
      const value = profile.runtime?.capabilities?.[capability]
      const asStr = typeof value === 'string' ? value.trim() : ''
      return asStr !== '' && asStr !== 'disabled'
    })
    .catch(() => false)

  capabilityCache.set(cacheKey, promise)
  return promise
}

/**
 * The client-selectable (provider, model) bindings a profile declares in
 * product.yaml (runtime.provider_options). Empty when the profile declares
 * none: the composer then shows no model switcher, and the server picks the
 * profile's default binding. Same cache as the capability reads.
 */
export async function loadAgentProfileProviderOptions(profileId: string, version?: number): Promise<AgentProfileProviderOption[]> {
  if (!profileId) return []
  try {
    const profile = await loadAgentProfile(profileId, version)
    const options = profile.runtime?.provider_options
    if (!Array.isArray(options)) return []
    return options.filter((o): o is AgentProfileProviderOption => !!o && typeof o.id === 'string' && o.id.trim() !== '')
  } catch {
    return []
  }
}

/**
 * Returns the backend-resolved feature bundles for a product. Surfaces use the
 * declared ui_panels rather than maintaining another hardcoded feature list.
 */
export async function loadAgentProfileFeatures(profileId: string, version?: number): Promise<AgentProfileFeature[]> {
  if (!profileId) return []
  try {
    const profile = await loadAgentProfile(profileId, version)
    if (!Array.isArray(profile.resolved_features)) return []
    return profile.resolved_features.filter((feature): feature is AgentProfileFeature => (
      !!feature && typeof feature.id === 'string' && feature.id.trim() !== ''
    ))
  } catch {
    return []
  }
}

export async function loadAgentProfileUIPanels(profileId: string, version?: number): Promise<Set<string>> {
  const features = await loadAgentProfileFeatures(profileId, version)
  return new Set(features.flatMap(feature => Array.isArray(feature.ui_panels) ? feature.ui_panels : []))
}

/** Returns interaction kinds whose UI behavior is declared by the profile. */
export async function loadAgentProfileInteractionKinds(
  profileId: string,
  render: string,
  version?: number,
): Promise<Set<string>> {
  if (!profileId || !render) return new Set()
  try {
    const profile = await loadAgentProfile(profileId, version)
    return new Set([...(profile.tools || []).map(tool => tool?.interaction), ...(profile.interactions || [])]
      .filter((binding): binding is AgentProfileInteractionBinding => (
        !!binding && binding.render === render && typeof binding.kind === 'string' && binding.kind.trim() !== ''
      ))
      .map(binding => binding.kind.trim()))
  } catch {
    return new Set()
  }
}

/** Returns complete declarative interaction bindings for product-owned UI. */
export async function loadAgentProfileInteractions(profileId: string, version?: number): Promise<AgentProfileInteractionBinding[]> {
  if (!profileId) return []
  try {
    const profile = await loadAgentProfile(profileId, version)
    return [...(profile.tools || []).map(tool => tool?.interaction), ...(profile.interactions || [])]
      .filter((binding): binding is AgentProfileInteractionBinding => Boolean(
        binding && typeof binding.kind === 'string' && binding.kind.trim() && typeof binding.render === 'string' && binding.render.trim(),
      ))
  } catch {
    return []
  }
}

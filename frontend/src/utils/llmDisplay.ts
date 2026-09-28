import type { SavedLLM } from '../services/api-types'
import type { ModelMetadata } from '../services/llm-config-api'
import type { LLMOption } from '../types/llm'

export type ProviderType =
  | 'claude-code'
  | 'codex-cli'
  | 'cursor-cli'
  | 'agy-cli'
  | 'pi-cli'
  | 'muse-cli'

// 'api_model' is only the fallback bucket for an unrecognized provider id;
// every runnable provider is a coding-agent CLI.
export type LLMIntegrationKind = 'coding_agent' | 'api_model'

type ProviderDisplayInfo = {
  name: string
  authDescription: string
  colorClass: string
}

export type LLMIntegrationDisplayInfo = {
  label: string
  description: string
  toneClass: string
}

export const LLM_INTEGRATION_ORDER: LLMIntegrationKind[] = [
  'coding_agent',
  'api_model',
]

export const LLM_INTEGRATION_DISPLAY_INFO: Record<LLMIntegrationKind, LLMIntegrationDisplayInfo> = {
  coding_agent: {
    label: 'Coding Agents',
    description: 'Local agent runtimes',
    toneClass: 'text-amber-700 dark:text-amber-300',
  },
  api_model: {
    label: 'Other',
    description: 'Unrecognized provider',
    toneClass: 'text-blue-700 dark:text-blue-300',
  },
}

export const CODING_AGENT_PROVIDERS = new Set(['claude-code', 'codex-cli', 'cursor-cli', 'pi-cli', 'muse-cli', 'agy-cli'])

// Pi CLI routes to several different model backends via a `<backend>/<model>`
// model id. Mirrors agent_go/cmd/server/llm_provider_manifest.go's
// piModelGroup.
const PI_MODEL_GROUP_BY_PREFIX: Record<string, string> = {
  google: 'Gemini',
  'google-vertex': 'Google Vertex',
  anthropic: 'Anthropic',
  openai: 'OpenAI',
  openrouter: 'OpenRouter',
  bedrock: 'Amazon Bedrock',
  deepseek: 'DeepSeek',
  zai: 'Z.AI',
  'zai-coding-cn': 'Z.AI',
  minimax: 'MiniMax',
  'minimax-cn': 'MiniMax',
  'kimi-coding': 'Kimi',
  moonshotai: 'Kimi',
  'moonshotai-cn': 'Kimi',
  xai: 'xAI',
  nvidia: 'NVIDIA',
}

const PI_MODEL_GROUP_DISPLAY: Record<string, ProviderDisplayInfo> = {
  Gemini: { name: 'Gemini', authDescription: 'API Key', colorClass: 'text-purple-600 dark:text-purple-400' },
  OpenRouter: { name: 'OpenRouter', authDescription: 'API Key', colorClass: 'text-blue-600 dark:text-blue-400' },
  'Z.AI': { name: 'Z.AI', authDescription: 'API Key', colorClass: 'text-fuchsia-600 dark:text-fuchsia-400' },
  MiniMax: { name: 'MiniMax', authDescription: 'API Key', colorClass: 'text-cyan-600 dark:text-cyan-400' },
  Kimi: { name: 'Kimi', authDescription: 'API Key', colorClass: 'text-rose-600 dark:text-rose-400' },
  DeepSeek: { name: 'DeepSeek', authDescription: 'API Key', colorClass: 'text-indigo-600 dark:text-indigo-400' },
  xAI: { name: 'xAI', authDescription: 'API Key', colorClass: 'text-neutral-700 dark:text-neutral-300' },
}

/** Resolves a Pi CLI model id (e.g. "google/gemini-3.7-flash") to its display group ("Gemini"). Returns null when unresolvable. */
export function resolvePiModelGroup(modelId?: string): string | null {
  const prefix = (modelId || '').trim().split('/')[0]?.toLowerCase()
  if (!prefix) return null
  return PI_MODEL_GROUP_BY_PREFIX[prefix] || null
}

const PROVIDER_DISPLAY_INFO: Record<ProviderType, ProviderDisplayInfo> = {
  'claude-code': {
    name: 'Claude Code',
    authDescription: 'Local CLI (no API key)',
    colorClass: 'text-amber-600 dark:text-amber-400',
  },
  'codex-cli': {
    name: 'Codex CLI',
    authDescription: 'Local CLI (API key optional)',
    colorClass: 'text-emerald-600 dark:text-emerald-400',
  },
  'cursor-cli': {
    name: 'Cursor CLI',
    authDescription: 'Local CLI (API key optional)',
    colorClass: 'text-slate-600 dark:text-slate-300',
  },
  'agy-cli': {
    name: 'Antigravity CLI (Alpha)',
    authDescription: 'Local CLI (Google sign-in)',
    colorClass: 'text-zinc-600 dark:text-zinc-300',
  },
  'pi-cli': {
    name: 'Pi CLI',
    authDescription: 'Local CLI (Pi provider key)',
    colorClass: 'text-lime-700 dark:text-lime-300',
  },
  'muse-cli': {
    name: 'Muse',
    authDescription: 'Local CLI (Meta login or API key)',
    colorClass: 'text-orange-600 dark:text-orange-400',
  },
}

export const PROVIDER_ORDER: ProviderType[] = [
  'codex-cli',
  'cursor-cli',
  'pi-cli',
  'muse-cli',
  'agy-cli',
  'claude-code',
]

export function getProviderDisplayInfo(provider?: string, modelId?: string): ProviderDisplayInfo {
  if (!provider) {
    return {
      name: 'No LLM selected',
      authDescription: '',
      colorClass: 'text-gray-600 dark:text-gray-400',
    }
  }

  if (provider === 'pi-cli') {
    const group = resolvePiModelGroup(modelId)
    if (group && group in PI_MODEL_GROUP_DISPLAY) {
      return PI_MODEL_GROUP_DISPLAY[group]
    }
  }

  if (provider in PROVIDER_DISPLAY_INFO) {
    return PROVIDER_DISPLAY_INFO[provider as ProviderType]
  }

  return {
    name: provider,
    authDescription: 'API Key',
    colorClass: 'text-gray-600 dark:text-gray-400',
  }
}

export function getProviderIntegrationKind(provider?: string, modelId?: string): LLMIntegrationKind {
  const normalizedProvider = (provider || '').trim().toLowerCase()

  if (CODING_AGENT_PROVIDERS.has(normalizedProvider)) {
    return 'coding_agent'
  }
  return 'api_model'
}

export function getProviderIntegrationInfo(provider?: string, modelId?: string): LLMIntegrationDisplayInfo {
  return LLM_INTEGRATION_DISPLAY_INFO[getProviderIntegrationKind(provider, modelId)]
}

export function shouldShowLLMPricing(provider?: string, modelId?: string): boolean {
  return getProviderIntegrationKind(provider, modelId) !== 'coding_agent'
}

type ModelDisplayNameOptions = {
  provider?: string
  modelId?: string
  metadata?: ModelMetadata[]
  savedLLMs?: SavedLLM[]
  availableLLMs?: LLMOption[]
}

export function getModelDisplayName({
  provider,
  modelId,
  metadata = [],
  savedLLMs = [],
  availableLLMs = [],
}: ModelDisplayNameOptions): string {
  if (!modelId) return 'Unknown'

  const publishedLLM = savedLLMs.find(
    (llm) => llm.provider === provider && llm.model_id === modelId
  )
  if (publishedLLM?.name) return publishedLLM.name
  if (publishedLLM?.model_name) return publishedLLM.model_name

  const metadataMatch =
    metadata.find((item) => item.provider === provider && item.model_id === modelId) ||
    metadata.find((item) => item.model_id === modelId)
  if (metadataMatch?.model_name) return metadataMatch.model_name

  const availableLLM = availableLLMs.find(
    (llm) => llm.provider === provider && llm.model === modelId
  )
  if (availableLLM?.label) return availableLLM.label

  return modelId
}

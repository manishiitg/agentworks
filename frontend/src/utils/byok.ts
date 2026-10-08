// "Bring your own model key" (PLAT-717): a person's key for a model service,
// kept as a private Pi account. Pi is the engine; people pick a service, not
// a Pi provider id.
import type { ProviderConnection } from '../services/llm-config-api'
import { usePanelSwitcherStore } from '../stores/usePanelSwitcherStore'
import { useCommandDialogStore } from '../stores/useCommandDialogStore'

export interface ByokService {
  /** The Pi provider id the account stores as underlying_provider. */
  id: string
  label: string
  blurb: string
  freeTier: string
  keyUrl?: string
  keyLabel?: string
  keyHint?: string
  recommended?: boolean
  /** The person types a base URL (OpenAI-compatible) or a Pi provider id (other). */
  needs?: 'base_url' | 'pi_provider'
}

export const BYOK_SERVICES: ByokService[] = [
  { id: 'openrouter', label: 'OpenRouter', blurb: 'One key, hundreds of models, many free.', freeTier: 'Free models, rate-limited', keyUrl: 'https://openrouter.ai/keys', keyLabel: 'openrouter.ai/keys', keyHint: 'sk-or-v1-…', recommended: true },
  { id: 'nvidia', label: 'NVIDIA NIM', blurb: 'NVIDIA-hosted open models: GLM, Nemotron, Kimi…', freeTier: 'Free, 40 requests/min', keyUrl: 'https://build.nvidia.com/settings/api-keys', keyLabel: 'build.nvidia.com', keyHint: 'nvapi-…' },
  { id: 'groq', label: 'Groq', blurb: 'Very fast open models: GPT-OSS, Kimi, Qwen, Llama.', freeTier: 'Free tier with rate limits', keyUrl: 'https://console.groq.com/keys', keyLabel: 'console.groq.com/keys', keyHint: 'gsk_…' },
  { id: 'google', label: 'Google AI Studio', blurb: 'Gemini models with an AI Studio key.', freeTier: 'Free tier on Flash models, with limits', keyUrl: 'https://aistudio.google.com/apikey', keyLabel: 'aistudio.google.com/apikey', keyHint: 'AIza…' },
  { id: 'openai-compatible', label: 'Other OpenAI-compatible', blurb: 'Any endpoint that speaks the OpenAI API: base URL, key, model ids.', freeTier: 'Depends on the service', needs: 'base_url' },
  { id: 'pi-other', label: 'Other Pi provider', blurb: 'A provider Pi knows by id: zai, deepseek, mistral, xai, kimi-coding…', freeTier: 'Depends on the provider', needs: 'pi_provider' },
]

/** The service of a Pi account that is a model key account. */
export function byokServiceOf(record?: Pick<ProviderConnection, 'provider' | 'underlying_provider'> | null): ByokService | undefined {
  if (!record || record.provider !== 'pi-cli') return undefined
  return BYOK_SERVICES.find(service => service.id === record.underlying_provider && !service.needs?.startsWith('pi'))
}

/** "$0.30 / $1.20 per 1M" for a paid model; "" when the catalog has no price. */
export function formatPerMillion(input?: number, output?: number): string {
  if (!input && !output) return ''
  const money = (value?: number) => value === undefined ? '?' : value < 0.01 ? `$${value.toFixed(3)}` : `$${value.toFixed(2)}`
  return `${money(input)} / ${money(output)} per 1M`
}

export function formatContext(tokens?: number): string {
  if (!tokens) return ''
  if (tokens >= 1_000_000) return `${(tokens / 1_000_000).toFixed(tokens % 1_000_000 ? 1 : 0)}M`
  return `${Math.round(tokens / 1000)}K`
}

// Free models come and go and are often busy: a turn that fails with a rate
// limit or an unknown model gets a hint to pick another.
const FREE_MODEL_TROUBLE = /(\b429\b|rate.?limit|too many requests|no endpoints found|model .{0,40}(not found|does not exist|is not available|not a valid)|unknown model|temporarily unavailable|overloaded|capacity)/i

export function freeModelErrorHint(error?: string | null): string | null {
  if (!error || !FREE_MODEL_TROUBLE.test(error)) return null
  return 'This model is busy, rate-limited or gone (common for free models). Pick another model and send again.'
}

/** Opens the project's Models tab where it exists, else the Providers dialog. */
export function openModelPicker() {
  const entries = Object.values(usePanelSwitcherStore.getState().entries)
  const entry = entries.find(candidate => candidate?.panels.some(panel => panel.id === 'identity'))
  if (entry) { entry.open('identity', 'models'); return }
  useCommandDialogStore.getState().openDialog('models')
}

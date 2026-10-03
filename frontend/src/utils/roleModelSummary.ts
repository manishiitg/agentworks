import type { LLMOption } from '../types/llm'
import { formatLLMOptions } from './llmConfigDisplay'
import { getProviderDisplayInfo } from './llmDisplay'

type RoleValue = {
  provider?: string
  model_id?: string
  options?: Record<string, unknown>
}

/** "High", "Medium"...: the effort a model option stands for, or '' when it has none. */
export function effortLabel(options?: Record<string, unknown>): string {
  const summary = formatLLMOptions(options)
  if (!summary) return ''
  const value = summary.replace(/^reasoning\s+/i, '').replace(/^thinking\s+/i, '').replace(/_/g, ' ')
  return value.charAt(0).toUpperCase() + value.slice(1)
}

export function modelDisplayName(available: LLMOption[], value: RoleValue): string {
  const match = available.find(option => option.provider === value.provider && option.model === value.model_id)
  return match?.label?.split(' · ')[0]?.trim() || value.model_id || ''
}

/** "Codex · GPT-6.1 Sol · Medium": agent, model and effort of one role on one line. */
export function roleModelSummary(available: LLMOption[], value?: RoleValue | null): string {
  if (!value?.provider) return 'Not set'
  const parts = [getProviderDisplayInfo(value.provider, value.model_id).name, modelDisplayName(available, value), effortLabel(value.options)]
  return parts.filter(Boolean).join(' · ')
}

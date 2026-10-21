type LLMDisplayRef = {
  published_llm_id?: string
  provider?: string
  model_id?: string
  model?: string
  options?: Record<string, unknown>
}

type LLMOptionRef = {
  id?: string
  provider: string
  model: string
  options?: Record<string, unknown>
}

function normalizeOptionValue(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(normalizeOptionValue)
  if (value && typeof value === 'object') {
    return Object.fromEntries(
      Object.entries(value as Record<string, unknown>)
        .sort(([left], [right]) => left.localeCompare(right))
        .map(([key, nested]) => [key, normalizeOptionValue(nested)])
    )
  }
  return value
}

export function llmOptionsKey(options?: Record<string, unknown>): string {
  return JSON.stringify(normalizeOptionValue(options ?? {}))
}

export function llmOptionMatchesRef(option: LLMOptionRef, ref?: LLMDisplayRef | null): boolean {
  if (!ref?.provider || !(ref.model_id || ref.model)) return false
  if (ref.published_llm_id && option.id === ref.published_llm_id) return true
  return option.provider === ref.provider
    && option.model === (ref.model_id || ref.model)
    && llmOptionsKey(option.options) === llmOptionsKey(ref.options)
}

export function formatLLMOptions(options?: Record<string, unknown>): string {
  if (!options || Object.keys(options).length === 0) return ''
  const parts: string[] = []
  if (options.reasoning_effort) parts.push(`reasoning ${String(options.reasoning_effort)}`)
  if (options.thinking_level) parts.push(`thinking ${String(options.thinking_level)}`)
  if (options.thinking_budget) parts.push(`budget ${String(options.thinking_budget)}`)
  return parts.join(' / ')
}

import { useMemo } from 'react'
import type { LLMOption } from '../types/llm'
import { getProviderDisplayInfo } from '../utils/llmDisplay'
import { formatLLMOptions, llmOptionsKey } from '../utils/llmConfigDisplay'

type LLMRoleValue = {
  published_llm_id?: string
  provider?: string
  model_id?: string
  options?: Record<string, unknown>
}

type LLMRoleSelectorProps = {
  availableLLMs: LLMOption[]
  allowedProviderIds?: readonly string[]
  value: LLMRoleValue | null
  onLLMSelect: (option: LLMOption) => void
  disabled?: boolean
}

const selectClassName = 'h-9 min-w-0 w-full rounded-md border border-border bg-background px-2 text-xs text-foreground outline-none focus:border-primary disabled:cursor-not-allowed disabled:opacity-50'

function uniqueBy<T>(items: T[], keyFor: (item: T) => string): T[] {
  const seen = new Set<string>()
  return items.filter(item => {
    const key = keyFor(item)
    if (seen.has(key)) return false
    seen.add(key)
    return true
  })
}

function optionLabel(option: LLMOption): string {
  const summary = formatLLMOptions(option.options)
  if (!summary) return 'Default'
  const value = summary
    .replace(/^reasoning\s+/i, '')
    .replace(/^thinking\s+/i, '')
    .replace(/_/g, ' ')
  return value.charAt(0).toUpperCase() + value.slice(1)
}

function modelLabel(option: LLMOption): string {
  const label = option.label?.split(' · ')[0]?.trim()
  return label || option.model
}

export default function LLMRoleSelector({
  availableLLMs,
  allowedProviderIds,
  value,
  onLLMSelect,
  disabled = false,
}: LLMRoleSelectorProps) {
  const providerUnavailable = Boolean(allowedProviderIds && value?.provider && !allowedProviderIds.includes(value.provider))
  const options = useMemo(() => {
    if (!value?.provider || !value.model_id) return availableLLMs
    const exists = availableLLMs.some(option => option.provider === value.provider
      && option.model === value.model_id
      && llmOptionsKey(option.options) === llmOptionsKey(value.options))
    if (exists) return availableLLMs
    return [...availableLLMs, {
      ...(value.published_llm_id ? { id: value.published_llm_id } : {}),
      provider: value.provider,
      model: value.model_id,
      label: value.model_id,
      options: value.options,
      section: 'published_model' as const,
    }]
  }, [availableLLMs, value])

  const providers = useMemo(
    () => uniqueBy(options, option => option.provider),
    [options]
  )
  const selectedProvider = value?.provider && providers.some(option => option.provider === value.provider)
    ? value.provider
    : providers[0]?.provider ?? ''
  const providerOptions = options.filter(option => option.provider === selectedProvider)
  const models = uniqueBy(providerOptions, option => option.model)
  const selectedModel = value?.model_id && models.some(option => option.model === value.model_id)
    ? value.model_id
    : models[0]?.model ?? ''
  const variants = uniqueBy(
    providerOptions.filter(option => option.model === selectedModel),
    option => llmOptionsKey(option.options)
  )
  const requestedVariantKey = llmOptionsKey(value?.options)
  const selectedVariant = variants.find(option => llmOptionsKey(option.options) === requestedVariantKey) ?? variants[0]
  const selectedVariantKey = selectedVariant ? llmOptionsKey(selectedVariant.options) : ''

  const chooseFirst = (candidates: LLMOption[]) => {
    if (candidates[0]) onLLMSelect(candidates[0])
  }

  return (
    <div className="space-y-2">
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-[minmax(0,0.8fr)_minmax(0,1.2fr)]">
        <label className="min-w-0 space-y-1">
          <span className="text-[10px] font-medium uppercase text-muted-foreground">Agent</span>
          <select
            aria-label="Coding agent or provider"
            className={selectClassName}
            value={selectedProvider}
            disabled={disabled || providers.length === 0}
            onChange={event => chooseFirst(options.filter(option => option.provider === event.target.value))}
          >
            {providers.map(option => (
              <option key={option.provider} value={option.provider} disabled={Boolean(allowedProviderIds && !allowedProviderIds.includes(option.provider))}>
                {getProviderDisplayInfo(option.provider).name}{allowedProviderIds && !allowedProviderIds.includes(option.provider) ? ' (unavailable)' : ''}
              </option>
            ))}
          </select>
        </label>

        <label className="min-w-0 space-y-1">
          <span className="text-[10px] font-medium uppercase text-muted-foreground">Model</span>
          <select
            aria-label="Model"
            className={selectClassName}
            value={selectedModel}
            disabled={disabled || providerUnavailable || models.length === 0}
            onChange={event => chooseFirst(providerOptions.filter(option => option.model === event.target.value))}
          >
            {models.map(option => (
              <option key={option.model} value={option.model}>{modelLabel(option)}</option>
            ))}
          </select>
        </label>
      </div>

      {variants.length > 1 && (
        <div className="flex min-w-0 items-center gap-2">
          <span className="shrink-0 text-[10px] font-medium uppercase text-muted-foreground">Effort</span>
          <div className="flex min-w-0 flex-wrap gap-1" role="group" aria-label="Reasoning effort">
            {variants.map(option => {
              const key = llmOptionsKey(option.options)
              const selected = key === selectedVariantKey
              const label = optionLabel(option)
              return (
                <button
                  key={key}
                  type="button"
                  aria-label={`Set reasoning effort to ${label}`}
                  aria-pressed={selected}
                  disabled={disabled || providerUnavailable}
                  onClick={() => onLLMSelect(option)}
                  className={`h-7 rounded-md border px-2.5 text-xs font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${selected
                    ? 'border-primary bg-primary/15 text-primary'
                    : 'border-border bg-background text-muted-foreground hover:bg-secondary hover:text-foreground'
                  }`}
                >
                  {label}
                </button>
              )
            })}
          </div>
        </div>
      )}
    </div>
  )
}

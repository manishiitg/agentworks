import { Check, Box, Brain, Sparkles } from 'lucide-react'
import { cn } from '../../lib/utils'
import type { ModelMetadata } from '../../services/llm-config-api'
import { formatPerMillion } from '../../utils/byok'

interface TierModelSelectorProps {
  models: ModelMetadata[]
  selectedModelId: string
  onSelect: (modelId: string) => void
  className?: string
  disabled?: boolean
  /** Show the price per 1M tokens of paid models (a model key's catalog). */
  showPrices?: boolean
}

const formatContext = (ctx: number) => {
  if (ctx >= 1000000) return `${(ctx / 1000000).toFixed(1)}M`
  if (ctx >= 1000) return `${Math.round(ctx / 1000)}K`
  return `${ctx}`
}

function tierDescription(model: ModelMetadata): string | null {
  const id = model.model_id.toLowerCase()
  if (id === 'claude-code' || id === 'codex-cli' || id === 'cursor-cli' || id === 'pi-cli') {
    return 'Default — uses the CLI\'s own model routing'
  }
  if (id === 'auto') {
    return 'Automatically picks the best model'
  }
  return null
}

export function TierModelSelector({
  models,
  selectedModelId,
  onSelect,
  className,
  disabled = false,
  showPrices = false,
}: TierModelSelectorProps) {
  if (models.length === 0) {
    return (
      <div className="text-sm text-muted-foreground py-3 text-center">
        No models available for this provider.
      </div>
    )
  }

  return (
    <div className={cn('grid gap-2', models.length <= 3 ? 'grid-cols-1' : 'grid-cols-1 sm:grid-cols-2', className)}>
      {models.map((model) => {
        const isSelected = model.model_id === selectedModelId
        const desc = tierDescription(model)
        const hasReasoning = model.supports_reasoning_effort || model.supports_thinking_level || model.supports_thinking_budget

        return (
          <button
            key={model.model_id}
            type="button"
            disabled={disabled}
            aria-pressed={isSelected}
            onClick={() => onSelect(model.model_id)}
            className={cn(
              'relative flex flex-col items-start gap-1.5 rounded-lg border p-3 text-left transition-colors',
              'hover:bg-accent/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
              isSelected
                ? 'border-primary bg-primary/5 ring-1 ring-primary/30'
                : 'border-border bg-background',
              disabled && 'opacity-50 cursor-not-allowed'
            )}
          >
            {isSelected && (
              <div className="absolute top-2.5 right-2.5 flex h-5 w-5 items-center justify-center rounded-full bg-primary text-primary-foreground">
                <Check className="h-3 w-3" />
              </div>
            )}

            <div className="font-medium text-sm text-foreground pr-7">
              {model.model_name || model.model_id}
            </div>

            {desc && (
              <div className="text-xs text-muted-foreground leading-snug">
                {desc}
              </div>
            )}

            <div className="flex items-center gap-2.5 flex-wrap mt-0.5">
              {model.is_free && (
                <span className="inline-flex items-center text-[11px] font-medium text-emerald-700 bg-emerald-500/10 px-1.5 py-0.5 rounded dark:text-emerald-300">Free</span>
              )}
              {showPrices && !model.is_free && formatPerMillion(model.input_cost_per_1m, model.output_cost_per_1m) && (
                <span className="text-[11px] text-muted-foreground">{formatPerMillion(model.input_cost_per_1m, model.output_cost_per_1m)}</span>
              )}
              {model.context_window > 0 && (
                <span className="inline-flex items-center gap-1 text-[11px] text-muted-foreground bg-secondary/60 px-1.5 py-0.5 rounded">
                  <Box className="h-3 w-3" />
                  {formatContext(model.context_window)} ctx
                </span>
              )}
              {hasReasoning && (
                <span className="inline-flex items-center gap-1 text-[11px] text-muted-foreground bg-secondary/60 px-1.5 py-0.5 rounded">
                  <Brain className="h-3 w-3" />
                  Reasoning
                </span>
              )}
              {model.supports_thinking_budget && (
                <span className="inline-flex items-center gap-1 text-[11px] text-muted-foreground bg-secondary/60 px-1.5 py-0.5 rounded">
                  <Sparkles className="h-3 w-3" />
                  Thinking
                </span>
              )}
            </div>
          </button>
        )
      })}
    </div>
  )
}

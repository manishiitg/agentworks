import { LoadingIndicator } from './LoadingIndicator'
import { useEffect, useMemo, useState } from 'react'
import { Search, ChevronDown, ChevronRight } from 'lucide-react'
import { cn } from '../../lib/utils'
import type { DynamicModelEntry, DynamicModelsResponse } from '../../services/llm-config-api'
import { useLLMStore } from '../../stores'

interface DynamicModelSelectorProps {
  provider: string
  selectedModelId: string
  onSelect: (modelId: string) => void
  className?: string
  disabled?: boolean
  /** Restrict the picker to models whose `group` matches exactly. Used to
   * scope a single Pi CLI backend (e.g. "Gemini") to its own tab instead of
   * showing every backend's models grouped together. */
  groupFilter?: string
}

function isPrimaryPiGroup(group: string): boolean {
  const normalized = group.toLowerCase()
  return normalized.includes('recommended') ||
    normalized.includes('gemini') ||
    normalized.includes('google') ||
    normalized.includes('chinese') ||
    normalized.includes('z.ai') ||
    normalized.includes('kimi') ||
    normalized.includes('minimax') ||
    normalized.includes('deepseek') ||
    normalized.includes('openrouter')
}

export function DynamicModelSelector({
  provider,
  selectedModelId,
  onSelect,
  className,
  disabled = false,
  groupFilter,
}: DynamicModelSelectorProps) {
  const getProviderDynamicModels = useLLMStore(s => s.getProviderDynamicModels)
  const [data, setData] = useState<DynamicModelsResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [search, setSearch] = useState('')
  const [collapsedGroups, setCollapsedGroups] = useState<Set<string>>(new Set())
  const [customModel, setCustomModel] = useState('')
  const [showCustomInput, setShowCustomInput] = useState(false)
  const [showFullCatalog, setShowFullCatalog] = useState(false)
  const [loadingFull, setLoadingFull] = useState(false)
  const [freeOnly, setFreeOnly] = useState(false)

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError(null)
    setShowFullCatalog(false)
    const isPi = provider === 'pi-cli'
    getProviderDynamicModels(provider, isPi ? false : undefined).then(result => {
      if (cancelled) return
      if (result) {
        setData(result)
      } else {
        setError('Failed to load models')
      }
      setLoading(false)
    })
    return () => { cancelled = true }
  }, [provider, getProviderDynamicModels])

  useEffect(() => {
    if (!data?.models?.length) {
      setCollapsedGroups(new Set())
      return
    }
    if (groupFilter || provider !== 'pi-cli' || data.models.length <= 80) return

    const next = new Set<string>()
    for (const model of data.models) {
      const group = model.group || 'Other'
      if (!isPrimaryPiGroup(group)) next.add(group)
    }
    setCollapsedGroups(next)
  }, [data, provider, groupFilter])

  const groupScopedModels = useMemo(() => {
    if (!data?.models) return []
    if (!groupFilter) return data.models
    return data.models.filter(m => (m.group || 'Other') === groupFilter)
  }, [data, groupFilter])

  const hasFreeModels = useMemo(() => groupScopedModels.some(m => m.is_free), [groupScopedModels])

  // A group-scoped tab (e.g. one Pi CLI backend) has no meaningful
  // provider-wide default to fall back on -- the caller can't know a group's
  // real default model id without this catalog, so pick it here once data
  // arrives, but only if nothing is selected yet (never overrides a real
  // choice, including one restored from a saved config).
  useEffect(() => {
    if (!groupFilter || selectedModelId || groupScopedModels.length === 0) return
    const fallback = groupScopedModels.find(m => m.is_default) ?? groupScopedModels[0]
    if (fallback) onSelect(fallback.model_id)
  }, [groupFilter, selectedModelId, groupScopedModels, onSelect])

  const grouped = useMemo(() => {
    const base = freeOnly ? groupScopedModels.filter(m => m.is_free) : groupScopedModels
    const q = search.toLowerCase().trim()
    const filtered = q
      ? base.filter(m =>
          m.model_id.toLowerCase().includes(q) ||
          m.model_name.toLowerCase().includes(q) ||
          (m.group || '').toLowerCase().includes(q)
        )
      : base

    const groups = new Map<string, DynamicModelEntry[]>()
    for (const m of filtered) {
      const g = m.group || 'Other'
      if (!groups.has(g)) groups.set(g, [])
      groups.get(g)!.push(m)
    }
    return groups
  }, [groupScopedModels, search, freeOnly])

  const toggleGroup = (group: string) => {
    setCollapsedGroups(prev => {
      const next = new Set(prev)
      if (next.has(group)) next.delete(group)
      else next.add(group)
      return next
    })
  }

  const handleCustomSubmit = () => {
    const id = customModel.trim()
    if (id) {
      onSelect(id)
      setShowCustomInput(false)
      setCustomModel('')
    }
  }

  const handleLoadFullCatalog = async () => {
    setLoadingFull(true)
    setError(null)
    try {
      const result = await getProviderDynamicModels(provider, true)
      if (result) {
        setData(result)
        setShowFullCatalog(true)
      } else {
        setError('Failed to load full model catalog')
      }
    } catch {
      setError('Failed to load full model catalog')
    } finally {
      setLoadingFull(false)
    }
  }

  if (loading) {
    return (
      <div className={cn('flex items-center justify-center py-8 text-muted-foreground', className)}>
        <LoadingIndicator className="h-4 w-4 mr-2" />
        <span className="text-sm">Loading models from CLI...</span>
      </div>
    )
  }

  if (error || !data) {
    return (
      <div className={cn('text-sm text-destructive py-3', className)}>
        {error || 'No models available.'}
      </div>
    )
  }

  return (
    <div className={cn('space-y-2', className)}>
      {provider === 'pi-cli' && !showFullCatalog && (
        <div className="flex items-center justify-between px-3 py-2 bg-primary/5 rounded-md border border-primary/20">
          <span className="text-xs text-muted-foreground">
            Only showing curated models first.
          </span>
          <button
            type="button"
            disabled={disabled || loadingFull}
            onClick={handleLoadFullCatalog}
            className="text-xs font-semibold text-primary hover:underline flex items-center gap-1.5 disabled:opacity-50"
          >
            {loadingFull && <LoadingIndicator className="h-3 w-3" />}
            {groupFilter ? `Search full ${groupFilter} catalog` : 'Search full model catalog'}
          </button>
        </div>
      )}

      <div className="relative">
        <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground" />
        <input
          type="text"
          value={search}
          onChange={e => setSearch(e.target.value)}
          placeholder={`Search ${groupScopedModels.length} models...`}
          disabled={disabled}
          className="w-full pl-8 pr-3 py-2 text-sm bg-background border border-border rounded-md focus:outline-none focus:ring-1 focus:ring-primary disabled:opacity-50"
        />
      </div>

      {hasFreeModels && (
        <label className="flex items-center gap-2 text-xs text-muted-foreground select-none">
          <input
            type="checkbox"
            checked={freeOnly}
            onChange={e => setFreeOnly(e.target.checked)}
            disabled={disabled}
            className="h-3.5 w-3.5 rounded border-border"
          />
          Free only
        </label>
      )}

      <div className="max-h-64 overflow-y-auto border border-border rounded-md divide-y divide-border">
        {grouped.size === 0 && (
          <div className="px-3 py-4 text-sm text-muted-foreground text-center">
            No models match "{search}"
          </div>
        )}
        {Array.from(grouped.entries()).map(([group, models]) => {
          const isCollapsed = !groupFilter && collapsedGroups.has(group)
          return (
            <div key={group}>
              {!groupFilter && (
                <button
                  type="button"
                  onClick={() => toggleGroup(group)}
                  className="w-full flex items-center gap-1.5 px-3 py-1.5 text-xs font-semibold text-muted-foreground bg-muted/30 hover:bg-muted/50 transition-colors"
                >
                  {isCollapsed
                    ? <ChevronRight className="h-3 w-3" />
                    : <ChevronDown className="h-3 w-3" />
                  }
                  {group}
                  <span className="ml-auto text-[10px] font-normal">{models.length}</span>
                </button>
              )}
              {!isCollapsed && models.map(model => {
                const isSelected = model.model_id === selectedModelId
                return (
                  <button
                    key={model.model_id}
                    type="button"
                    disabled={disabled}
                    onClick={() => onSelect(model.model_id)}
                    className={cn(
                      'w-full flex items-center justify-between px-3 py-2 text-sm text-left transition-colors',
                      'hover:bg-accent/50',
                      isSelected && 'bg-primary/5 text-primary font-medium',
                      disabled && 'opacity-50 cursor-not-allowed'
                    )}
                  >
                    <div className="flex items-center gap-2 min-w-0">
                      <span className="truncate">{model.model_name || model.model_id}</span>
                      {model.is_default && (
                        <span className="shrink-0 text-[10px] font-medium bg-primary/10 text-primary px-1.5 py-0.5 rounded">default</span>
                      )}
                      {model.is_free && (
                        <span className="shrink-0 text-[10px] font-medium bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 px-1.5 py-0.5 rounded">Free</span>
                      )}
                    </div>
                    {model.context_window ? (
                      <span className="shrink-0 text-[10px] text-muted-foreground ml-2">
                        {model.context_window >= 1000000
                          ? `${(model.context_window / 1000000).toFixed(1)}M`
                          : `${Math.round(model.context_window / 1000)}K`
                        }
                      </span>
                    ) : null}
                  </button>
                )
              })}
            </div>
          )
        })}
      </div>

      {selectedModelId && (
        <div className="text-xs text-muted-foreground">
          Selected: <code className="bg-secondary px-1 py-0.5 rounded">{selectedModelId}</code>
        </div>
      )}

      {data.supports_custom_model && (
        <div>
          {!showCustomInput ? (
            <button
              type="button"
              onClick={() => setShowCustomInput(true)}
              disabled={disabled}
              className="text-xs text-primary hover:underline disabled:opacity-50"
            >
              Use custom model ID...
            </button>
          ) : (
            <div className="flex gap-2">
              <input
                type="text"
                value={customModel}
                onChange={e => setCustomModel(e.target.value)}
                onKeyDown={e => e.key === 'Enter' && handleCustomSubmit()}
                placeholder={data.custom_model_hint || 'Enter model ID'}
                className="flex-1 px-2.5 py-1.5 text-sm bg-background border border-border rounded-md focus:outline-none focus:ring-1 focus:ring-primary"
                autoFocus
              />
              <button
                type="button"
                onClick={handleCustomSubmit}
                disabled={!customModel.trim()}
                className="px-3 py-1.5 text-sm font-medium bg-primary text-primary-foreground rounded-md hover:bg-primary/90 disabled:opacity-50"
              >
                Use
              </button>
              <button
                type="button"
                onClick={() => { setShowCustomInput(false); setCustomModel('') }}
                className="px-2 py-1.5 text-sm text-muted-foreground hover:text-foreground"
              >
                Cancel
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  )
}

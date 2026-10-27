import { useMemo, useState } from 'react'
import { ChevronDown, ChevronRight, Search } from 'lucide-react'
import { cn } from '../../lib/utils'
import type { DynamicModelEntry } from '../../services/llm-config-api'

interface CursorModelPickerProps {
  models: DynamicModelEntry[]
  selectedModelId: string
  onSelect: (modelId: string) => void
  className?: string
}

interface Family {
  name: string
  note?: string
  older?: boolean
  variants: DynamicModelEntry[]
}

// PLAT-872: a paid Cursor account lists hundreds of models. The server splits
// each into family + variant (and maker, older); this only lays them out:
// one row per family, its variants as chips, older versions folded away.
export function CursorModelPicker({ models, selectedModelId, onSelect, className }: CursorModelPickerProps) {
  const [search, setSearch] = useState('')
  const [showOlder, setShowOlder] = useState(false)
  const selectedFamily = models.find(model => model.model_id === selectedModelId)?.family
  const [openFamily, setOpenFamily] = useState<string | undefined>(selectedFamily)

  const { makers, olderCount } = useMemo(() => {
    const q = search.trim().toLowerCase()
    const families = new Map<string, Family>()
    for (const model of models) {
      const name = model.family || model.model_name || model.model_id
      if (q && !`${name} ${model.variant || ''} ${model.group || ''} ${model.model_id}`.toLowerCase().includes(q)) continue
      if (!families.has(name)) families.set(name, { name, note: model.family_note, older: model.older, variants: [] })
      families.get(name)!.variants.push(model)
    }
    const all = Array.from(families.values())
    const visible = all.filter(family => showOlder || q || !family.older || family.name === selectedFamily)
    const byMaker = new Map<string, Family[]>()
    for (const family of visible) {
      const maker = family.variants[0].group || 'Other'
      if (!byMaker.has(maker)) byMaker.set(maker, [])
      byMaker.get(maker)!.push(family)
    }
    return { makers: Array.from(byMaker.entries()), olderCount: all.length - visible.length }
  }, [models, search, showOlder, selectedFamily])

  return (
    <div className={cn('space-y-2', className)}>
      <div className="relative">
        <Search className="absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
        <input
          type="text"
          value={search}
          onChange={event => setSearch(event.target.value)}
          placeholder="Search models"
          className="w-full rounded-md border border-border bg-background py-2 pl-8 pr-3 text-sm focus:outline-none focus:ring-1 focus:ring-primary"
        />
      </div>
      <div className="max-h-96 overflow-y-auto rounded-md border border-border">
        {makers.length === 0 && (
          <p className="px-3 py-4 text-center text-sm text-muted-foreground">
            {search.trim() ? `No models match "${search}"` : 'Cursor listed no models for this account. Check the account in Providers, then refresh.'}
          </p>
        )}
        {makers.map(([maker, families]) => (
          <div key={maker}>
            <p className="bg-muted/30 px-3 py-1.5 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">{maker}</p>
            {families.map(family => {
              const single = family.variants.length === 1
              const selected = family.variants.some(model => model.model_id === selectedModelId)
              const open = !single && (openFamily === family.name || Boolean(search.trim()))
              return (
                <div key={family.name} className={cn('border-t border-border first:border-t-0', selected && 'bg-primary/5')}>
                  <button
                    type="button"
                    aria-expanded={single ? undefined : open}
                    onClick={() => single ? onSelect(family.variants[0].model_id) : setOpenFamily(open ? undefined : family.name)}
                    className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-accent/50"
                  >
                    {single
                      ? <span className="w-3.5" />
                      : open ? <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" /> : <ChevronRight className="h-3.5 w-3.5 text-muted-foreground" />}
                    <span className={cn('truncate', selected && 'font-medium text-primary')}>{family.name}</span>
                    {family.variants.some(model => model.is_default) && <span className="shrink-0 rounded bg-primary/10 px-1.5 py-0.5 text-[10px] font-medium text-primary">default</span>}
                    {family.note && <span className="ml-auto shrink-0 text-[10px] text-muted-foreground">{family.note}</span>}
                  </button>
                  {open && (
                    <div ref={element => element?.scrollIntoView({ block: 'nearest' })} role="group" aria-label={`${family.name} variants`} className="flex flex-wrap gap-1.5 px-3 pb-3 pl-8">
                      {family.variants.map(model => (
                        <button
                          key={model.model_id}
                          type="button"
                          aria-pressed={model.model_id === selectedModelId}
                          onClick={() => onSelect(model.model_id)}
                          className={cn('rounded-md border px-2 py-1 text-xs transition-colors',
                            model.model_id === selectedModelId ? 'border-primary bg-primary/10 text-primary' : 'border-border bg-background text-muted-foreground hover:bg-muted')}
                        >
                          {model.variant || model.model_name}
                        </button>
                      ))}
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        ))}
      </div>
      {olderCount > 0 && !search.trim() && (
        <button type="button" onClick={() => setShowOlder(value => !value)} className="text-xs font-medium text-primary hover:underline">
          {showOlder ? 'Hide older models' : `Show older models (${olderCount})`}
        </button>
      )}
    </div>
  )
}

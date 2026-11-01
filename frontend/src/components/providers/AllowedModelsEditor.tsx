import { useEffect, useState } from 'react'
import { llmConfigService, type DynamicModelEntry } from '../../services/llm-config-api'
import { hasModelLimit } from '../../utils/allowedModels'
import { Chip, Segmented } from './ChoiceChips'

// Not models: a program's "Auto" entry (its own id) and Codex's effort shortcuts. Effort is picked per chat.
const notAModel = (provider: string, id: string) => id === provider || ['high', 'medium', 'low'].includes(id)

/**
 * Which models may run on one provider account: "All models" (the default) or
 * "Only these" with a chip per model of the provider. The server enforces
 * the list; saving an empty selection under "Only these" is not possible.
 *
 * With `person`, it edits one person's override of a shared account's list
 * (PLAT-714): "Account default" saves null, "All models" saves ["*"].
 */
export default function AllowedModelsEditor({ provider, value, disabled = false, onSave, onCancel, person }: {
  provider: string
  value?: string[]
  disabled?: boolean
  onSave: (models: string[] | null) => void | Promise<void>
  onCancel: () => void
  /** Edit a person's override; accountText describes the account's own list. */
  person?: { name: string; accountText: string }
}) {
  const [catalog, setCatalog] = useState<DynamicModelEntry[]>([])
  const [loading, setLoading] = useState(true)
  const initialMode = person && !hasModelLimit(value) ? 'inherit' : value?.includes('*') ? 'all' : hasModelLimit(value) ? 'only' : 'all'
  const [mode, setMode] = useState<'inherit' | 'all' | 'only'>(initialMode)
  const limited = mode === 'only'
  const [selected, setSelected] = useState<string[]>((value ?? []).filter(id => id !== '*'))
  useEffect(() => {
    let cancelled = false
    setLoading(true)
    llmConfigService.getProviderModels(provider, true).then(response => {
      if (!cancelled) setCatalog(response.models ?? [])
    }).catch(() => { if (!cancelled) setCatalog([]) }).finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [provider])
  // A model on the saved list the catalog no longer shows stays listed, so saving never drops it silently.
  const known = new Set(catalog.map(model => model.model_id))
  const rows = [...catalog.filter(model => !notAModel(provider, model.model_id)), ...selected.filter(id => !known.has(id)).map(id => ({ model_id: id, model_name: id }))]
  const toggle = (id: string) => setSelected(current => current.includes(id) ? current.filter(item => item !== id) : [...current, id])
  const canSave = !disabled && (!limited || selected.length > 0)
  return (
    <form className="mt-3 w-full space-y-3 rounded-lg border border-gray-200 p-3 dark:border-gray-700" onSubmit={event => { event.preventDefault(); if (canSave) void onSave(mode === 'inherit' ? null : limited ? selected : person ? ['*'] : []) }}>
      <div className="space-y-2">
        <p className="text-xs font-medium text-gray-700 dark:text-gray-300">Models</p>
        <Segmented label={person ? `Models allowed for ${person.name}` : 'Models allowed on this account'} value={mode} disabled={disabled} onChange={setMode}
          options={[...(person ? [{ value: 'inherit' as const, label: `Account default (${person.accountText})` }] : []), { value: 'all' as const, label: 'All models' }, { value: 'only' as const, label: 'Only these' }]} />
      </div>
      {limited && (
        <fieldset className="flex max-h-56 flex-wrap gap-1.5 overflow-auto" aria-label="Allowed models">
          {loading && <p className="text-xs text-gray-500 dark:text-gray-400">Loading models…</p>}
          {!loading && rows.length === 0 && <p className="text-xs text-gray-500 dark:text-gray-400">No models listed for this provider.</p>}
          {rows.map(model => (
            <Chip key={model.model_id} on={selected.includes(model.model_id)} disabled={disabled} onClick={() => toggle(model.model_id)} title={model.model_id}>
              {model.model_name || model.model_id}
            </Chip>
          ))}
          {selected.length === 0 && !loading && <p className="w-full text-xs text-amber-700 dark:text-amber-300">Pick at least one model, or choose All models.</p>}
        </fieldset>
      )}
      <div className="flex gap-2">
        <button disabled={!canSave} type="submit" className="rounded-lg bg-violet-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-violet-500 disabled:opacity-50">{disabled ? 'Saving…' : 'Save models'}</button>
        <button disabled={disabled} type="button" onClick={onCancel} className="rounded-lg border border-gray-300 bg-white px-3 py-1.5 text-xs font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200">Cancel</button>
      </div>
    </form>
  )
}

import { useEffect, useState } from 'react'
import { llmConfigService, type DynamicModelEntry } from '../../services/llm-config-api'
import { hasModelLimit } from '../../utils/allowedModels'

/**
 * Which models may run on one provider account: "All models" (the default) or
 * "Only these" with a checkbox per model of the provider. The server enforces
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
  const rows = [...catalog, ...selected.filter(id => !known.has(id)).map(id => ({ model_id: id, model_name: id }))]
  const toggle = (id: string) => setSelected(current => current.includes(id) ? current.filter(item => item !== id) : [...current, id])
  const canSave = !disabled && (!limited || selected.length > 0)
  return (
    <form className="mt-3 w-full space-y-3 rounded-lg border border-gray-200 p-3 dark:border-gray-700" onSubmit={event => { event.preventDefault(); if (canSave) void onSave(mode === 'inherit' ? null : limited ? selected : person ? ['*'] : []) }}>
      <label className="block text-xs font-medium text-gray-700 dark:text-gray-300">Models
        <select aria-label={person ? `Models allowed for ${person.name}` : 'Models allowed on this account'} disabled={disabled} value={mode} onChange={event => setMode(event.target.value as 'inherit' | 'all' | 'only')}
          className="mt-1.5 block w-full rounded-lg border border-gray-300 bg-white px-3 py-2 text-sm text-gray-900 outline-none focus:border-violet-500 focus:ring-2 focus:ring-violet-500/20 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100">
          {person && <option value="inherit">Account default ({person.accountText})</option>}
          <option value="all">All models</option>
          <option value="only">Only these…</option>
        </select>
      </label>
      {limited && (
        <fieldset className="max-h-56 space-y-1 overflow-auto" aria-label="Allowed models">
          {loading && <p className="text-xs text-gray-500 dark:text-gray-400">Loading models…</p>}
          {!loading && rows.length === 0 && <p className="text-xs text-gray-500 dark:text-gray-400">No models listed for this provider.</p>}
          {rows.map(model => (
            <label key={model.model_id} className="flex items-center gap-2 text-xs text-gray-700 dark:text-gray-200">
              <input type="checkbox" disabled={disabled} checked={selected.includes(model.model_id)} onChange={() => toggle(model.model_id)} />
              <span className="min-w-0 break-words">{model.model_name || model.model_id}</span>
              {model.model_name && model.model_name !== model.model_id && <code className="text-[10px] text-gray-400">{model.model_id}</code>}
            </label>
          ))}
          {selected.length === 0 && !loading && <p className="text-xs text-amber-700 dark:text-amber-300">Pick at least one model, or choose All models.</p>}
        </fieldset>
      )}
      <div className="flex gap-2">
        <button disabled={!canSave} type="submit" className="rounded-lg bg-violet-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-violet-500 disabled:opacity-50">{disabled ? 'Saving…' : 'Save models'}</button>
        <button disabled={disabled} type="button" onClick={onCancel} className="rounded-lg border border-gray-300 bg-white px-3 py-1.5 text-xs font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200">Cancel</button>
      </div>
    </form>
  )
}

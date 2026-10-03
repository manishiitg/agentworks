import { useEffect, useState } from 'react'
import { llmConfigService, type DynamicModelEntry } from '../../services/llm-config-api'
import { hasModelLimit } from '../../utils/allowedModels'

/**
 * Which models may run on one provider account: "All models" (the default) or
 * "Only these" with a checkbox per model of the provider. The server enforces
 * the list; saving an empty selection under "Only these" is not possible.
 */
export default function AllowedModelsEditor({ provider, value, disabled = false, onSave, onCancel }: {
  provider: string
  value?: string[]
  disabled?: boolean
  onSave: (models: string[]) => void | Promise<void>
  onCancel: () => void
}) {
  const [catalog, setCatalog] = useState<DynamicModelEntry[]>([])
  const [loading, setLoading] = useState(true)
  const [limited, setLimited] = useState(hasModelLimit(value))
  const [selected, setSelected] = useState<string[]>(value ?? [])
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
    <form className="mt-3 w-full space-y-3 rounded-lg border border-gray-200 p-3 dark:border-gray-700" onSubmit={event => { event.preventDefault(); if (canSave) void onSave(limited ? selected : []) }}>
      <label className="block text-xs font-medium text-gray-700 dark:text-gray-300">Models
        <select aria-label="Models allowed on this account" disabled={disabled} value={limited ? 'only' : 'all'} onChange={event => setLimited(event.target.value === 'only')}
          className="mt-1.5 block w-full rounded-lg border border-gray-300 bg-white px-3 py-2 text-sm text-gray-900 outline-none focus:border-violet-500 focus:ring-2 focus:ring-violet-500/20 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100">
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

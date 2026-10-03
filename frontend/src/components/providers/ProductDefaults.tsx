import { useEffect, useState } from 'react'
import { Loader2, Lock } from 'lucide-react'
import type { ProductDefault } from '../../services/api-types'
import { llmConfigService, providerApiErrorText, type ProviderManifestEntry } from '../../services/llm-config-api'

export const PRODUCT_LABELS: { id: string; label: string }[] = [
  { id: 'agentworks', label: 'Workflows' },
  { id: 'work', label: 'Crews' },
  { id: 'code', label: 'Code' },
]

const modelName = (provider: ProviderManifestEntry | undefined, modelId: string) =>
  provider?.models.find(model => model.model_id === modelId)?.model_name || modelId

/** Each product's starting provider and model; admins change the ones the installation does not pin. */
export default function ProductDefaults({ providers, isAdmin }: { providers: ProviderManifestEntry[]; isAdmin: boolean }) {
  const [defaults, setDefaults] = useState<Record<string, ProductDefault> | null>(null)
  const [editing, setEditing] = useState<string | null>(null)
  const [draft, setDraft] = useState({ provider: '', model: '' })
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const load = () => llmConfigService.getProductDefaults()
    .then(value => setDefaults(value))
    .catch(() => setError('Could not load product defaults.'))
  useEffect(() => { void load() }, [])

  const byId = new Map(providers.map(provider => [provider.id, provider]))
  const startEdit = (product: string) => {
    const current = defaults?.[product]
    const provider = current?.provider || providers[0]?.id || ''
    setDraft({ provider, model: current?.model_id || byId.get(provider)?.default_model_id || '' })
    setEditing(product); setError(null)
  }
  const save = async (product: string, value: { provider: string; model: string } | null) => {
    setBusy(true); setError(null)
    try {
      await llmConfigService.setProductDefaults({ [product]: value })
      setEditing(null)
      await load()
    } catch (saveError) {
      setError(providerApiErrorText(saveError, 'Could not save the default.'))
    } finally { setBusy(false) }
  }
  const draftProvider = byId.get(draft.provider)
  const inputClass = 'rounded-md border border-gray-300 bg-white px-2 py-1.5 text-xs text-gray-900 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100'
  const buttonClass = 'rounded-lg border border-gray-300 bg-white px-2.5 py-1.5 text-xs font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:hover:bg-gray-700'

  return (
    <div className="mx-auto max-w-3xl">
      <h2 className="text-xl font-semibold text-gray-950 dark:text-white">Product defaults</h2>
      <p className="mt-1 text-sm leading-6 text-gray-600 dark:text-gray-300">The provider and model a new workflow, Crew or Code starts with, on the provider's admin-managed account. Changing a default does not change existing items.</p>
      {error && <p role="alert" className="mt-3 rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700 dark:bg-red-500/10 dark:text-red-300">{error}</p>}
      {!defaults && !error && <p className="mt-4 flex items-center gap-2 text-sm text-gray-500"><Loader2 className="h-4 w-4 animate-spin" /> Loading…</p>}
      {defaults && (
        <ul className="mt-4 divide-y divide-gray-200 rounded-xl border border-gray-200 dark:divide-gray-700 dark:border-gray-700">
          {PRODUCT_LABELS.map(product => {
            const value = defaults[product.id]
            const provider = value ? byId.get(value.provider) : undefined
            return (
              <li key={product.id} className="px-4 py-3">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <div className="min-w-0">
                    <div className="text-sm font-medium text-gray-900 dark:text-gray-100">{product.label}</div>
                    <div className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                      {value ? `${provider?.display_name || value.provider} · ${modelName(provider, value.model_id)}` : 'No default set'}
                      {value?.pinned && <span className="ml-2 inline-flex items-center gap-1"><Lock className="h-3 w-3" /> Set by the installation</span>}
                    </div>
                  </div>
                  {isAdmin && !value?.pinned && editing !== product.id && (
                    <div className="flex gap-2">
                      <button type="button" className={buttonClass} disabled={busy} onClick={() => startEdit(product.id)}>Change</button>
                      {value && <button type="button" className={buttonClass} disabled={busy} onClick={() => void save(product.id, null)}>Clear</button>}
                    </div>
                  )}
                </div>
                {editing === product.id && (
                  <form className="mt-3 flex flex-wrap items-end gap-2" onSubmit={event => { event.preventDefault(); void save(product.id, draft) }}>
                    <label className="text-xs text-gray-700 dark:text-gray-300">Provider
                      <select aria-label={`${product.label} default provider`} className={`mt-1 block ${inputClass}`} value={draft.provider} onChange={event => setDraft({ provider: event.target.value, model: byId.get(event.target.value)?.default_model_id || '' })}>
                        {providers.map(option => <option key={option.id} value={option.id}>{option.display_name}</option>)}
                      </select>
                    </label>
                    <label className="text-xs text-gray-700 dark:text-gray-300">Model
                      {draftProvider && draftProvider.models.length > 0 ? (
                        <select aria-label={`${product.label} default model`} className={`mt-1 block ${inputClass}`} value={draft.model} onChange={event => setDraft(current => ({ ...current, model: event.target.value }))}>
                          {!draftProvider.models.some(model => model.model_id === draft.model) && draft.model && <option value={draft.model}>{draft.model}</option>}
                          {draftProvider.models.map(model => <option key={model.model_id} value={model.model_id}>{model.model_name || model.model_id}</option>)}
                        </select>
                      ) : (
                        <input aria-label={`${product.label} default model`} required className={`mt-1 block ${inputClass}`} value={draft.model} onChange={event => setDraft(current => ({ ...current, model: event.target.value }))} />
                      )}
                    </label>
                    <button type="submit" disabled={busy || !draft.provider || !draft.model} className="rounded-lg bg-violet-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-violet-500 disabled:opacity-50">{busy ? 'Saving…' : 'Save'}</button>
                    <button type="button" className={buttonClass} disabled={busy} onClick={() => setEditing(null)}>Cancel</button>
                  </form>
                )}
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}

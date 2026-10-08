import { useEffect, useMemo, useState } from 'react'
import { CheckCircle2, CircleAlert, Loader2, Search, Star, Wrench } from 'lucide-react'
import { llmConfigService, providerApiErrorText, type ByokCheck, type ByokModel, type ByokRequest } from '../../services/llm-config-api'
import { formatContext, formatPerMillion } from '../../utils/byok'

const PAGE = 60

/**
 * The models of one key's service, from its live catalog: Free, price per 1M,
 * context, tool support and "Recommended for agents". The person stars their
 * picks (the account's models in chat; the first is the default) and can run
 * Try it, a one-request tool-call check, on any model.
 */
export default function ByokModelBrowser({ request, service, value, onChange, canTry = true, manualIds = false, onLoaded }: {
  request: ByokRequest
  /** The Pi provider id model ids start with. */
  service?: string
  value: string[]
  onChange: (picks: string[]) => void
  /** Try it needs the key: the account's owner or the person setting it up. */
  canTry?: boolean
  /** Let the person add model ids by hand (an endpoint without a model list). */
  manualIds?: boolean
  onLoaded?: (models: ByokModel[], defaultModel?: string) => void
}) {
  const [models, setModels] = useState<ByokModel[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [search, setSearch] = useState('')
  const [freeOnly, setFreeOnly] = useState(false)
  const [agentsOnly, setAgentsOnly] = useState(false)
  const [shown, setShown] = useState(PAGE)
  const [trying, setTrying] = useState<string | null>(null)
  const [results, setResults] = useState<Record<string, ByokCheck>>({})
  const [manual, setManual] = useState('')
  const requestKey = JSON.stringify(request)
  useEffect(() => {
    let cancelled = false
    setLoading(true); setError(null)
    llmConfigService.byokModels(request).then(result => {
      if (cancelled) return
      const list = result.models ?? []
      setModels(list)
      setFreeOnly(list.some(model => model.is_free) && list.some(model => !model.is_free))
      onLoaded?.(list, result.default_model)
    }).catch(loadError => {
      if (!cancelled) { setModels([]); setError(providerApiErrorText(loadError, 'Could not load the models.')) }
    }).finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
    // request is compared by content
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [requestKey])

  const prefix = service || request.service || (models[0]?.model_id.split('/')[0] ?? '')
  const rows = useMemo(() => {
    const known = new Set(models.map(model => model.model_id))
    const extra = value.filter(id => !known.has(id)).map(id => ({ model_id: id, model_name: id.slice(id.indexOf('/') + 1) }) as ByokModel)
    const q = search.trim().toLowerCase()
    const picked = new Set(value)
    return [...extra, ...models]
      .filter(model => picked.has(model.model_id) || ((!freeOnly || model.is_free) && (!agentsOnly || model.recommended)))
      .filter(model => !q || model.model_id.toLowerCase().includes(q) || model.model_name.toLowerCase().includes(q))
      .sort((a, b) => Number(picked.has(b.model_id)) - Number(picked.has(a.model_id)))
  }, [models, value, search, freeOnly, agentsOnly])

  const toggle = (id: string) => onChange(value.includes(id) ? value.filter(item => item !== id) : [...value, id])
  const tryModel = async (id: string) => {
    setTrying(id)
    try {
      const result = await llmConfigService.byokTryModel({ ...request, model: id })
      setResults(current => ({ ...current, [id]: result }))
    } catch (tryError) {
      setResults(current => ({ ...current, [id]: { state: 'error', detail: providerApiErrorText(tryError, 'The check failed.') } }))
    } finally { setTrying(null) }
  }
  const addManual = () => {
    const id = manual.trim()
    if (!id) return
    const full = !prefix || id.startsWith(`${prefix}/`) ? id : `${prefix}/${id}`
    if (!value.includes(full)) onChange([...value, full])
    setManual('')
  }
  const chip = (active: boolean) => `rounded-full border px-2.5 py-1 text-[11px] font-medium transition-colors ${active ? 'border-violet-400 bg-violet-50 text-violet-700 dark:border-violet-500/50 dark:bg-violet-500/10 dark:text-violet-300' : 'border-gray-300 text-gray-600 hover:bg-gray-50 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-800'}`
  const badge = 'rounded px-1.5 py-0.5 text-[10px] font-medium'

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-[12rem] flex-1">
          <Search className="absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-gray-400" />
          <input aria-label="Search models" value={search} onChange={event => { setSearch(event.target.value); setShown(PAGE) }} placeholder={loading ? 'Loading models…' : `Search ${models.length} models`}
            className="w-full rounded-lg border border-gray-300 bg-white py-1.5 pl-8 pr-3 text-xs text-gray-900 outline-none focus:border-violet-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100" />
        </div>
        {models.some(model => model.is_free) && <button type="button" aria-pressed={freeOnly} className={chip(freeOnly)} onClick={() => setFreeOnly(on => !on)}>Free</button>}
        {models.some(model => model.recommended) && <button type="button" aria-pressed={agentsOnly} className={chip(agentsOnly)} onClick={() => setAgentsOnly(on => !on)}>Recommended for agents</button>}
      </div>
      <p className="text-[11px] text-gray-500 dark:text-gray-400">Star the models you want in chat; the first is the default. <strong>Try it</strong> sends one short request with a tool to check the model can work as an agent.</p>
      {error && <p role="alert" className="rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700 dark:bg-red-500/10 dark:text-red-300">{error}</p>}
      {loading && <p className="flex items-center gap-2 py-3 text-xs text-gray-500"><Loader2 className="h-3.5 w-3.5 animate-spin" /> Loading the live catalog…</p>}
      {!loading && (
        <ul aria-label="Models" className="max-h-80 divide-y divide-gray-100 overflow-y-auto rounded-lg border border-gray-200 dark:divide-gray-800 dark:border-gray-700">
          {rows.length === 0 && <li className="px-3 py-4 text-center text-xs text-gray-500">{manualIds ? 'No models listed. Add model ids below.' : 'No models match.'}</li>}
          {rows.slice(0, shown).map(model => {
            const picked = value.includes(model.model_id)
            const result = results[model.model_id]
            const price = model.is_free ? '' : formatPerMillion(model.cost_input, model.cost_output)
            return (
              <li key={model.model_id} className="flex flex-wrap items-center gap-2 px-2.5 py-2">
                <button type="button" aria-pressed={picked} aria-label={`${picked ? 'Unpick' : 'Pick'} ${model.model_name || model.model_id}`} onClick={() => toggle(model.model_id)}
                  className={`rounded p-1 ${picked ? 'text-amber-500' : 'text-gray-300 hover:text-gray-500 dark:text-gray-600'}`}>
                  <Star className="h-4 w-4" fill={picked ? 'currentColor' : 'none'} />
                </button>
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-1.5">
                    <span className="truncate text-xs font-medium text-gray-900 dark:text-gray-100">{model.model_name || model.model_id}</span>
                    {model.is_free && <span className={`${badge} bg-emerald-50 text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-300`}>Free</span>}
                    {model.recommended && <span className={`${badge} bg-violet-50 text-violet-700 dark:bg-violet-500/10 dark:text-violet-300`} title="Free and supports tool calls (or known to work for agents)">Recommended for agents</span>}
                    {model.supports_tools && <span className={`${badge} inline-flex items-center gap-0.5 bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300`} title="The catalog says it supports tool calls"><Wrench className="h-2.5 w-2.5" />tools</span>}
                    {value[0] === model.model_id && <span className={`${badge} bg-amber-50 text-amber-700 dark:bg-amber-500/10 dark:text-amber-300`}>default</span>}
                  </div>
                  <p className="truncate text-[10px] text-gray-500 dark:text-gray-400">
                    <code>{model.model_id.slice(model.model_id.indexOf('/') + 1)}</code>
                    {model.context_window ? ` · ${formatContext(model.context_window)} context` : ''}
                    {price ? ` · ${price}` : ''}
                  </p>
                  {result && <p className={`mt-0.5 flex items-center gap-1 text-[11px] ${result.state === 'ok' ? 'text-emerald-700 dark:text-emerald-300' : 'text-amber-700 dark:text-amber-300'}`}>
                    {result.state === 'ok' ? <CheckCircle2 className="h-3 w-3" /> : <CircleAlert className="h-3 w-3" />}{result.detail || (result.state === 'ok' ? 'Works' : 'Failed')}
                  </p>}
                </div>
                {canTry && <button type="button" disabled={trying !== null} onClick={() => void tryModel(model.model_id)}
                  className="inline-flex items-center gap-1 rounded-md border border-gray-300 px-2 py-1 text-[11px] font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50 dark:border-gray-600 dark:text-gray-200 dark:hover:bg-gray-800">
                  {trying === model.model_id && <Loader2 className="h-3 w-3 animate-spin" />}Try it
                </button>}
              </li>
            )
          })}
          {rows.length > shown && <li className="px-3 py-2 text-center"><button type="button" className="text-xs text-violet-600 hover:underline dark:text-violet-400" onClick={() => setShown(count => count + PAGE)}>Show more ({rows.length - shown})</button></li>}
        </ul>
      )}
      {(manualIds || error) && (
        <div className="flex gap-2">
          <input aria-label="Model id" value={manual} onChange={event => setManual(event.target.value)} onKeyDown={event => { if (event.key === 'Enter') { event.preventDefault(); addManual() } }} placeholder="Add a model id, e.g. qwen3-coder"
            className="flex-1 rounded-lg border border-gray-300 bg-white px-3 py-1.5 text-xs text-gray-900 outline-none focus:border-violet-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100" />
          <button type="button" onClick={addManual} disabled={!manual.trim()} className="rounded-lg border border-gray-300 px-3 py-1.5 text-xs font-medium text-gray-700 disabled:opacity-50 dark:border-gray-600 dark:text-gray-200">Add</button>
        </div>
      )}
    </div>
  )
}

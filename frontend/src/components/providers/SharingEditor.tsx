import { useEffect, useMemo, useState } from 'react'
import { AlertTriangle } from 'lucide-react'
import {
  llmConfigService,
  type ProviderAccountSharing,
  type ProviderAvailableTo,
  type ProviderShareTargets,
} from '../../services/llm-config-api'

const EMPTY_TARGETS: ProviderShareTargets = { workflows: [], crews: [], users: [] }

// Share targets are the same for every account row on the page; load them
// once per page view and share the promise.
let targetsPromise: Promise<ProviderShareTargets> | null = null
export function loadShareTargets(): Promise<ProviderShareTargets> {
  if (!targetsPromise) {
    targetsPromise = llmConfigService.getProviderShareTargets().catch(error => {
      targetsPromise = null
      throw error
    })
  }
  return targetsPromise
}
/** Test hook: forget the cached share targets. */
export function resetShareTargetsCache() { targetsPromise = null }

export function useShareTargets(enabled = true) {
  const [targets, setTargets] = useState<ProviderShareTargets>(EMPTY_TARGETS)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    if (!enabled) return
    let cancelled = false
    loadShareTargets()
      .then(value => { if (!cancelled) setTargets(value) })
      .catch(() => { if (!cancelled) setError('Could not load workflows, Crews and people to share with.') })
    return () => { cancelled = true }
  }, [enabled])
  return { targets, error }
}

const plural = (count: number, one: string, many: string) => `${count} ${count === 1 ? one : many}`

export function sharingSummary(sharing?: ProviderAccountSharing): string {
  if (!sharing || sharing.mode !== 'shared') return 'Private'
  return `Shared with ${plural(sharing.workflows?.length ?? 0, 'workflow', 'workflows')}, ${plural(sharing.crews?.length ?? 0, 'Crew', 'Crews')}, ${plural(sharing.users?.length ?? 0, 'person', 'people')}`
}

export const SHARING_WARNING = 'Runs by others act as your account and are billed to it. Nobody else sees the key or the login files.'

type PickOption = { id: string; label: string; detail?: string }

export function PickList({ label, options, selected, onChange, disabled, emptyText }: {
  label: string
  options: PickOption[]
  selected: string[]
  onChange: (ids: string[]) => void
  disabled?: boolean
  emptyText: string
}) {
  const [filter, setFilter] = useState('')
  // Keep chosen IDs the caller can no longer see (another admin's pick, a
  // deleted workflow) so saving does not silently drop them.
  const all = useMemo(() => {
    const known = new Set(options.map(option => option.id))
    return [...options, ...selected.filter(id => !known.has(id)).map(id => ({ id, label: id, detail: 'Not visible to you' }))]
  }, [options, selected])
  const needle = filter.trim().toLowerCase()
  const shown = needle ? all.filter(option => `${option.label} ${option.detail ?? ''}`.toLowerCase().includes(needle)) : all
  const toggle = (id: string) => onChange(selected.includes(id) ? selected.filter(value => value !== id) : [...selected, id])
  return (
    <fieldset className="min-w-0" disabled={disabled}>
      <legend className="text-xs font-medium text-gray-700 dark:text-gray-300">{label}{selected.length > 0 ? ` (${selected.length})` : ''}</legend>
      {all.length > 6 && <input aria-label={`Filter ${label.toLowerCase()}`} placeholder="Filter" value={filter} onChange={event => setFilter(event.target.value)} className="mt-1 block w-full rounded-md border border-gray-300 bg-white px-2 py-1 text-xs text-gray-900 outline-none focus:border-violet-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100" />}
      <div className="mt-1 max-h-36 overflow-y-auto rounded-md border border-gray-200 dark:border-gray-700">
        {shown.length === 0 && <p className="px-2 py-1.5 text-xs text-gray-500 dark:text-gray-400">{all.length === 0 ? emptyText : 'No matches.'}</p>}
        {shown.map(option => (
          <label key={option.id} className="flex cursor-pointer items-start gap-2 px-2 py-1 text-xs text-gray-800 hover:bg-gray-50 dark:text-gray-200 dark:hover:bg-gray-800">
            <input type="checkbox" className="mt-0.5" checked={selected.includes(option.id)} onChange={() => toggle(option.id)} />
            <span className="min-w-0 break-words">{option.label}{option.detail && <span className="text-gray-500 dark:text-gray-400"> · {option.detail}</span>}</span>
          </label>
        ))}
      </div>
    </fieldset>
  )
}

const workflowOptions = (targets: ProviderShareTargets) => targets.workflows.map(item => ({ id: item.id, label: item.name }))
const crewOptions = (targets: ProviderShareTargets) => targets.crews.map(item => ({ id: item.id, label: item.name, detail: item.owner }))
const userOptions = (targets: ProviderShareTargets) => targets.users.map(item => ({ id: item.id, label: item.name, detail: item.email }))

/** "Who can use it" for a user account: private, or shared with workflows, Crews and people. */
export function SharingFields({ value, onChange, disabled }: {
  value: ProviderAccountSharing
  onChange: (value: ProviderAccountSharing) => void
  disabled?: boolean
}) {
  const shared = value.mode === 'shared'
  const { targets, error } = useShareTargets(shared)
  return (
    <fieldset className="space-y-3" disabled={disabled}>
      <legend className="text-xs font-medium text-gray-700 dark:text-gray-300">Who can use it</legend>
      <div className="flex flex-wrap gap-4 text-sm text-gray-800 dark:text-gray-200">
        <label className="inline-flex items-center gap-2"><input type="radio" name="account-sharing" checked={!shared} onChange={() => onChange({ mode: 'private' })} /> Private</label>
        <label className="inline-flex items-center gap-2"><input type="radio" name="account-sharing" checked={shared} onChange={() => onChange({ mode: 'shared', workflows: value.workflows ?? [], crews: value.crews ?? [], users: value.users ?? [] })} /> Shared with workflows, Crews and people</label>
      </div>
      {!shared && <p className="text-xs text-gray-500 dark:text-gray-400">Only your own runs use this account.</p>}
      {shared && <>
        <p role="note" className="flex items-start gap-2 rounded-lg bg-amber-50 px-3 py-2 text-xs leading-5 text-amber-800 dark:bg-amber-500/10 dark:text-amber-200">
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />{SHARING_WARNING}
        </p>
        {error && <p className="text-xs text-red-600 dark:text-red-400">{error}</p>}
        <div className="grid gap-3 sm:grid-cols-3">
          <PickList label="Workflows" options={workflowOptions(targets)} selected={value.workflows ?? []} onChange={workflows => onChange({ ...value, workflows })} emptyText="No workflows." />
          <PickList label="Crews" options={crewOptions(targets)} selected={value.crews ?? []} onChange={crews => onChange({ ...value, crews })} emptyText="No Crews." />
          <PickList label="People" options={userOptions(targets)} selected={value.users ?? []} onChange={users => onChange({ ...value, users })} emptyText="No other people." />
        </div>
      </>}
    </fieldset>
  )
}

const PRODUCTS = [
  { id: 'agentworks', label: 'Workflows' },
  { id: 'work', label: 'Crews' },
  { id: 'code', label: 'Code' },
]

type AvailabilityChoice = 'all' | 'admins' | 'chosen'

/** Admin editor for who may use a server account. */
export function AvailabilityFields({ value, onChange, disabled }: {
  value: ProviderAvailableTo
  onChange: (value: ProviderAvailableTo) => void
  disabled?: boolean
}) {
  const choice: AvailabilityChoice = value === 'all' ? 'all' : value === 'admins' ? 'admins' : 'chosen'
  const object = typeof value === 'object' ? value : {}
  const { targets, error } = useShareTargets(choice === 'chosen')
  const setObject = (next: { admins?: boolean; products?: string[]; users?: string[] }) => onChange(next)
  return (
    <fieldset className="space-y-3" disabled={disabled}>
      <legend className="text-xs font-medium text-gray-700 dark:text-gray-300">Who can use it</legend>
      <div className="flex flex-wrap gap-4 text-sm text-gray-800 dark:text-gray-200">
        <label className="inline-flex items-center gap-2"><input type="radio" name="server-availability" checked={choice === 'all'} onChange={() => onChange('all')} /> Everyone</label>
        <label className="inline-flex items-center gap-2"><input type="radio" name="server-availability" checked={choice === 'admins'} onChange={() => onChange('admins')} /> Admins only</label>
        <label className="inline-flex items-center gap-2"><input type="radio" name="server-availability" checked={choice === 'chosen'} onChange={() => setObject({ admins: true, products: [], users: [] })} /> Chosen products and people</label>
      </div>
      {choice === 'chosen' && <>
        {error && <p className="text-xs text-red-600 dark:text-red-400">{error}</p>}
        <div className="flex flex-wrap gap-4 text-xs text-gray-800 dark:text-gray-200">
          <label className="inline-flex items-center gap-2"><input type="checkbox" checked={object.admins === true} onChange={event => setObject({ ...object, admins: event.target.checked })} /> Admins</label>
          {PRODUCTS.map(product => (
            <label key={product.id} className="inline-flex items-center gap-2">
              <input type="checkbox" checked={(object.products ?? []).includes(product.id)} onChange={event => setObject({ ...object, products: event.target.checked ? [...(object.products ?? []), product.id] : (object.products ?? []).filter(id => id !== product.id) })} />
              Everyone in {product.label}
            </label>
          ))}
        </div>
        <PickList label="People" options={targets.users.map(item => ({ id: item.email || item.id, label: item.name, detail: item.email }))} selected={(object.users ?? []).map(user => targets.users.find(item => item.id === user)?.email || user)} onChange={users => setObject({ ...object, users })} emptyText="No other people." />
      </>}
    </fieldset>
  )
}

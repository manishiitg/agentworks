import { useEffect, useMemo, useState } from 'react'
import { AlertTriangle } from 'lucide-react'
import { Chip, Segmented } from './ChoiceChips'
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

type PickOption = { id: string; label: string; detail?: string; searchText?: string }

export function PickList({ label, options, selected, onChange, disabled, emptyText, searchable = false, selectedFirst = false }: {
  label: string
  options: PickOption[]
  selected: string[]
  onChange: (ids: string[]) => void
  disabled?: boolean
  emptyText: string
  searchable?: boolean
  selectedFirst?: boolean
}) {
  const [filter, setFilter] = useState('')
  // Keep chosen IDs the caller can no longer see (another admin's pick, a
  // deleted workflow) so saving does not silently drop them.
  const all = useMemo(() => {
    const known = new Set(options.map(option => option.id))
    return [...options, ...selected.filter(id => !known.has(id)).map(id => ({ id, label: id, detail: 'Not visible to you' }))]
  }, [options, selected])
  const needle = filter.trim().toLowerCase()
  const filtered = needle ? all.filter(option => `${option.label} ${option.detail ?? ''} ${'searchText' in option ? option.searchText ?? '' : ''}`.toLowerCase().includes(needle)) : all
  const shown = selectedFirst ? [...filtered].sort((a, b) => Number(selected.includes(b.id)) - Number(selected.includes(a.id)) || a.label.localeCompare(b.label)) : filtered
  const toggle = (id: string) => onChange(selected.includes(id) ? selected.filter(value => value !== id) : [...selected, id])
  return (
    <fieldset className="min-w-0" disabled={disabled}>
      <legend className="text-xs font-medium text-gray-700 dark:text-gray-300">{label}{selected.length > 0 ? ` (${selected.length})` : ''}</legend>
      {(searchable || all.length > 6) && <input aria-label={`Filter ${label.toLowerCase()}`} placeholder={searchable ? 'Search people' : 'Filter'} value={filter} onChange={event => setFilter(event.target.value)} className="mt-1.5 block w-full rounded-lg border border-gray-300 bg-white px-3 py-1.5 text-xs text-gray-900 outline-none focus:border-violet-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100" />}
      <div className="mt-2 flex max-h-40 flex-wrap gap-1.5 overflow-y-auto">
        {shown.length === 0 && <p className="text-xs text-gray-500 dark:text-gray-400">{all.length === 0 ? emptyText : 'No matches.'}</p>}
        {shown.map(option => (
          <Chip key={option.id} on={selected.includes(option.id)} onClick={() => toggle(option.id)} disabled={disabled} title={option.detail}>
            {option.label}{option.detail && <span className="opacity-70"> · {option.detail}</span>}
          </Chip>
        ))}
      </div>
    </fieldset>
  )
}

const workflowOptions = (targets: ProviderShareTargets) => targets.workflows.map(item => ({ id: item.id, label: item.name }))
const crewOptions = (targets: ProviderShareTargets) => targets.crews.map(item => ({ id: item.id, label: item.name, detail: item.owner }))
// Sharing your own account never lists you: you always have it.
const userOptions = (targets: ProviderShareTargets) => targets.users.filter(item => !item.self).map(item => ({ id: item.id, label: item.name, detail: item.email }))

/** "Who can use it" for a user account: private, or shared with workflows, Crews and people. */
/** A member may share their own account with this many named colleagues (PLAT-715). */
export const MEMBER_SHARE_USER_LIMIT = 10

export function SharingFields({ value, onChange, disabled, peopleOnly = false }: {
  value: ProviderAccountSharing
  onChange: (value: ProviderAccountSharing) => void
  disabled?: boolean
  /** A member's account: named colleagues only, at most 10; workflows and Crews are for admins. */
  peopleOnly?: boolean
}) {
  const shared = value.mode === 'shared'
  const overLimit = peopleOnly && (value.users?.length ?? 0) > MEMBER_SHARE_USER_LIMIT
  const { targets, error } = useShareTargets(shared)
  return (
    <fieldset className="space-y-3" disabled={disabled}>
      <legend className="mb-2 text-xs font-medium text-gray-700 dark:text-gray-300">Who can use it</legend>
      <Segmented label="Who can use it" value={shared ? 'shared' : 'private'} disabled={disabled}
        options={[{ value: 'private', label: 'Only me' }, { value: 'shared', label: peopleOnly ? 'Chosen colleagues' : 'Chosen workflows, Crews and people' }]}
        onChange={next => onChange(next === 'private' ? { mode: 'private' } : peopleOnly ? { mode: 'shared', users: value.users ?? [] } : { mode: 'shared', workflows: value.workflows ?? [], crews: value.crews ?? [], users: value.users ?? [] })} />
      {!shared && <p className="text-xs text-gray-500 dark:text-gray-400">Only your own runs use this account.</p>}
      {shared && <>
        <p role="note" className="flex items-start gap-2 rounded-lg bg-amber-50 px-3 py-2 text-xs leading-5 text-amber-800 dark:bg-amber-500/10 dark:text-amber-200">
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />{SHARING_WARNING}
        </p>
        {error && <p className="text-xs text-red-600 dark:text-red-400">{error}</p>}
        {peopleOnly ? <>
          <PickList label="People" options={userOptions(targets)} selected={value.users ?? []} onChange={users => onChange({ mode: 'shared', users })} emptyText="No other people." />
          <p className={`text-xs ${overLimit ? 'text-red-600 dark:text-red-400' : 'text-gray-500 dark:text-gray-400'}`}>
            {overLimit ? `Pick at most ${MEMBER_SHARE_USER_LIMIT} people.` : `Up to ${MEMBER_SHARE_USER_LIMIT} people. Only an admin can share an account with workflows or Crews.`}
          </p>
        </> : <div className="grid gap-3 sm:grid-cols-3">
          <PickList label="Workflows" options={workflowOptions(targets)} selected={value.workflows ?? []} onChange={workflows => onChange({ ...value, workflows })} emptyText="No workflows." />
          <PickList label="Crews" options={crewOptions(targets)} selected={value.crews ?? []} onChange={crews => onChange({ ...value, crews })} emptyText="No Crews." />
          <PickList label="People" options={userOptions(targets)} selected={value.users ?? []} onChange={users => onChange({ ...value, users })} emptyText="No other people." />
        </div>}
      </>}
    </fieldset>
  )
}

type AvailabilityChoice = 'all' | 'admins' | 'chosen'

/** Admin editor for who may use a server account. */
export function AvailabilityFields({ value, onChange, disabled }: {
  value: ProviderAvailableTo
  onChange: (value: ProviderAvailableTo) => void
  disabled?: boolean
}) {
  const choice: AvailabilityChoice = value === 'all' ? 'all' : value === 'admins' ? 'admins' : 'chosen'
  const object = typeof value === 'object' ? value : { users: [] }
  const { targets, error } = useShareTargets(choice === 'chosen')
  const people = [...new Map(targets.users.map(person => [person.id, person])).values()]
  const selected = object.users.map(id => people.find(person => [person.id, person.email, person.name].some(identity => identity?.toLowerCase() === id.toLowerCase()))?.id || id)
  return (
    <fieldset className="space-y-3" disabled={disabled}>
      <legend className="mb-2 text-sm font-medium text-gray-900 dark:text-gray-100">Who can use this account?</legend>
      <Segmented label="Who can use this account" value={choice} disabled={disabled}
        options={[{ value: 'all', label: 'Everyone' }, { value: 'admins', label: 'Only admins' }, { value: 'chosen', label: 'Chosen people' }]}
        onChange={next => onChange(next === 'chosen' ? { users: object.users } : next)} />
      {choice === 'chosen' && <>
        {error && <p className="text-xs text-red-600 dark:text-red-400">{error}</p>}
        <PickList label="People" options={people.map(person => ({ id: person.id, label: person.name || person.email || person.id, searchText: person.email }))} selected={selected} onChange={users => onChange({ users })} searchable selectedFirst emptyText="No people available." />
        <p className="text-xs text-gray-500 dark:text-gray-400">Admins always have access.</p>
      </>}
    </fieldset>
  )
}

import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Users, Plus, ShieldCheck, UserRound, LogIn, Loader2, X, Share2, Lock, MoreHorizontal } from 'lucide-react'
import GuidedProviderTerminal from './GuidedProviderTerminal'
import ConfirmationDialog from '../ui/ConfirmationDialog'
import ProviderAccountCostsSection from './AccountCosts'
import { useCanReviewCode } from '../../hooks/useCanReviewCode'
import { AvailabilityFields, SharingFields, sharingSummary } from './SharingEditor'
import {
  llmConfigService,
  providerApiErrorText,
  type ProviderAccountRelation,
  type ProviderAccountSharing,
  type ProviderAccountStatus,
  type ProviderAvailableTo,
  type ProviderConnection,
  type ProviderSetupSession,
} from '../../services/llm-config-api'

// Providers with a browser login the server can sign out (the CLI's own logout).
const SIGN_OUT_PROVIDERS = new Set(['claude-code', 'codex-cli', 'cursor-cli', 'muse-cli'])

/** One line for an account's status; never a credential. */
export function accountStatusText(status?: ProviderAccountStatus | 'loading'): string {
  if (!status) return ''
  if (status === 'loading') return 'Checking status…'
  switch (status.state) {
    case 'signed_in': return `Signed in${status.identity ? ` as ${status.identity}` : ''}${status.verified ? ' (checked)' : ''}`
    case 'signed_out': return 'Signed out'
    case 'key_rejected': return `Login rejected${status.detail ? `: ${status.detail}` : ''}`
    default: return status.detail ? `Status unknown: ${status.detail}` : 'Status unknown'
  }
}

/** Whose account a row is, for action labels. */
export function accountScopeLabel(record: ProviderConnection): string {
  const relation = accountRelation(record)
  if (relation === 'server') return 'server account (shared)'
  if (relation === 'own') return 'your account'
  return `${record.owner_name || 'another person'}'s account`
}

/** Subtitle for someone else's account the viewer sees but may not use. */
export function otherAccountDetail(record: ProviderConnection, summary: string): string {
  const owner = record.owner_name || record.owner_user_id || 'unknown'
  if (record.sharing?.mode !== 'shared') return `Owner: ${owner} · Private (only ${owner} can use it)`
  return `Owner: ${owner} · ${summary}`
}

/** How the caller reaches an account; an older server sends no relation. */
export const accountRelation = (record: ProviderConnection): ProviderAccountRelation =>
  record.relation ?? (record.scope === 'global' ? 'server' : 'own')

/** Whether the caller may select the account where the list was requested. */
export const accountUsable = (record: ProviderConnection) =>
  record.usable !== false && accountRelation(record) !== 'admin_view'

/** Whether the account is set up (signed in or has a key). Unknown counts as set up. */
export const accountConfigured = (record: ProviderConnection) => record.configured !== false

export const ACCOUNT_GROUPS: { label: string; relations: ProviderAccountRelation[] }[] = [
  { label: 'Server account', relations: ['server'] },
  { label: 'Your accounts', relations: ['own'] },
  { label: 'Shared with you', relations: ['shared_with_you'] },
  { label: 'Shared with this workflow', relations: ['shared_with_workflow'] },
  { label: 'Shared with this Crew', relations: ['shared_with_crew'] },
]

/** Short label for a selectable account in a picker. */
export function accountOptionLabel(record: ProviderConnection): string {
  switch (accountRelation(record)) {
    case 'server': return record.display_name
    case 'own': return `${record.display_name} (${record.sharing?.mode === 'shared' ? 'yours, shared' : 'private'})`
    default: return `${record.display_name}${record.owner_name ? ` (${record.owner_name})` : ''}`
  }
}

export const NO_LONGER_AVAILABLE = 'No longer available here'

export default function ProviderAccounts({ provider, providerLabel, selectedId, onSelect, disabled = false, addRequest = 0, formOnly = false, selectionOnly = false, workspacePath, product }: {
  provider: string
  /** Display name of the provider, e.g. "Claude Code". */
  providerLabel?: string
  selectedId?: string
  onSelect?: (id: string) => void
  disabled?: boolean
  addRequest?: number
  formOnly?: boolean
  selectionOnly?: boolean
  /** Workflow, Crew or Code path the selection is for. */
  workspacePath?: string | null
  product?: string
}) {
  const manage = !onSelect && !formOnly && !selectionOnly
  const canReview = useCanReviewCode()
  const [connections, setConnections] = useState<ProviderConnection[]>([])
  const [editingId, setEditingId] = useState<string | null>(null)
  const [sharingId, setSharingId] = useState<string | null>(null)
  const [sharingDraft, setSharingDraft] = useState<ProviderAccountSharing>({ mode: 'private' })
  const [availabilityDraft, setAvailabilityDraft] = useState<ProviderAvailableTo | null>(null)
  // Signing in the shared login changes the account everyone allowed uses:
  // confirm, and point to Add my account for a private one.
  const [confirmSharedLogin, setConfirmSharedLogin] = useState<ProviderConnection | null>(null)
  const [authMethod, setAuthMethod] = useState('api_key')
  const [session, setSession] = useState<ProviderSetupSession | null>(null)
  const [sessionRowId, setSessionRowId] = useState<string | null>(null)
  const [usageText, setUsageText] = useState<{ rowId: string; text: string } | null>(null)
  const [statuses, setStatuses] = useState<Record<string, ProviderAccountStatus | 'loading'>>({})
  const [adding, setAdding] = useState(false)
  const [name, setName] = useState('')
  const [credential, setCredential] = useState('')
  const [newSharing, setNewSharing] = useState<ProviderAccountSharing>({ mode: 'private' })
  const [underlyingProvider, setUnderlyingProvider] = useState('google')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    let cancelled = false
    setError(null)
    const refresh = () => {
      void llmConfigService.getProviderConnections(workspacePath || product ? { workspacePath, product } : undefined).then(records => {
        if (!cancelled) setConnections(records.filter(record => record.provider === provider))
      }).catch(() => { if (!cancelled) setError('Could not load accounts.') })
    }
    refresh(); window.addEventListener('provider-connections-changed', refresh)
    return () => { cancelled = true; window.removeEventListener('provider-connections-changed', refresh) }
  }, [provider, workspacePath, product])
  // Each row shows its status; the cheap check runs on load, the real one on Refresh.
  useEffect(() => {
    if (!manage) return
    let cancelled = false
    for (const record of connections) {
      if (record.usable === false && !record.can_manage) continue
      void llmConfigService.getProviderAccountStatus(record.id, false, workspacePath).then(status => {
        if (!cancelled) setStatuses(current => ({ ...current, [record.id]: status }))
      }).catch(() => undefined)
    }
    return () => { cancelled = true }
  }, [connections, manage, workspacePath])
  const refreshStatus = async (record: ProviderConnection) => {
    setStatuses(current => ({ ...current, [record.id]: 'loading' }))
    try {
      const status = await llmConfigService.getProviderAccountStatus(record.id, true, workspacePath)
      setStatuses(current => ({ ...current, [record.id]: status }))
    } catch (statusError) {
      setStatuses(current => { const next = { ...current }; delete next[record.id]; return next })
      setError(providerApiErrorText(statusError, 'Could not check status.'))
    }
  }
  const signOut = async (record: ProviderConnection) => {
    const server = accountRelation(record) === 'server'
    const message = server
      ? `Every run that uses the ${providerLabel || provider} server account will stop working until someone signs in again.`
      : `Sign out ${record.display_name}? Runs that use it stop working until it signs in again. The account stays.`
    if (!window.confirm(message)) return
    setBusy(true); setError(null)
    try { await llmConfigService.signOutProviderAccount(record.id); changed(); await refreshStatus(record) }
    catch (signOutError) { setError(providerApiErrorText(signOutError, 'Could not sign out.')) }
    finally { setBusy(false) }
  }
  const resetForm = () => { setName(''); setCredential(''); setAuthMethod('api_key'); setNewSharing({ mode: 'private' }); setError(null) }
  useEffect(() => {
    if (!addRequest) return
    setAdding(true)
    setEditingId(null)
    resetForm()
  }, [addRequest])
  const server = connections.find(record => accountRelation(record) === 'server')
  const personalAllowed = server?.personal_accounts_allowed !== false
  const changed = () => window.dispatchEvent(new Event('provider-connections-changed'))
  const save = async () => {
    if (disabled || !personalAllowed) return
    setBusy(true); setError(null)
    try {
      let record: ProviderConnection
      if (editingId) {
        await llmConfigService.updateProviderConnection(editingId, { display_name: name, ...(credential ? { credential } : {}) })
        record = { ...connections.find(item => item.id === editingId)!, display_name: name }
      } else {
        record = await llmConfigService.addProviderConnection({
          provider, display_name: name,
          ...(authMethod === 'cli_login' ? { auth_method: 'cli_login' } : { credential }),
          ...(provider === 'pi-cli' ? { underlying_provider: underlyingProvider } : {}),
          ...(newSharing.mode === 'shared' ? { sharing: newSharing } : {}),
        })
        record = { relation: 'own', kind: 'user', can_manage: true, ...record }
      }
      setConnections(current => editingId ? current.map(item => item.id === editingId ? record : item) : [...current, record])
      setEditingId(null); resetForm(); setAdding(false)
      changed()
      onSelect?.(record.id)
      if (record.auth_method === 'cli_login' && !editingId) await login(record)
    } catch (saveError) { setError(providerApiErrorText(saveError, 'Could not save account. Check the provider and administrator policy.')) }
    finally { setBusy(false) }
  }
  const runSetup = async (record: ProviderConnection, action: 'authenticate' | 'usage' | 'inspect') => {
    setBusy(true); setError(null)
    try {
      if (action === 'usage') {
        const result = await llmConfigService.checkProviderUsage(provider, record.id)
        if (result.session) { setSession(result.session); setSessionRowId(record.id) }
        else setUsageText({ rowId: record.id, text: result.usage_output || 'No usage output.' })
      } else {
        let started: ProviderSetupSession
        try {
          started = await llmConfigService.startProviderSetup(provider, action, 100, 24, undefined, false, record.id)
        } catch (startError) {
          // One sign-in or terminal per account at a time: offer to end the running one.
          if ((startError as { response?: { status?: number } })?.response?.status !== 409 || !window.confirm(`A ${providerLabel || provider} terminal is already open for this account. End it and start a new one?`)) throw startError
          started = await llmConfigService.startProviderSetup(provider, action, 100, 24, undefined, true, record.id)
        }
        setSession(started)
        setSessionRowId(record.id)
      }
    } catch (setupError) { setError(providerApiErrorText(setupError, action === 'usage' ? 'Could not check usage.' : action === 'inspect' ? 'Could not open the terminal.' : 'Could not start account login.')) }
    finally { setBusy(false) }
  }
  const login = (record: ProviderConnection) => runSetup(record, 'authenticate')
  const remove = async (record: ProviderConnection) => {
    if (!window.confirm(`Remove ${record.display_name}? Workflows, Crews and Codes that use it will stop working until they pick another account.`)) return
    setBusy(true); setError(null)
    try { await llmConfigService.deleteProviderConnection(record.id); setConnections(current => current.filter(item => item.id !== record.id)); changed() }
    catch (removeError) { setError(providerApiErrorText(removeError, 'Could not remove account.')) }
    finally { setBusy(false) }
  }
  const saveSharing = async (record: ProviderConnection) => {
    setBusy(true); setError(null)
    try {
      const sharing: ProviderAccountSharing = sharingDraft.mode === 'shared' ? sharingDraft : { mode: 'private' }
      await llmConfigService.updateProviderConnection(record.id, { sharing })
      setConnections(current => current.map(item => item.id === record.id ? { ...item, sharing } : item))
      setSharingId(null); changed()
    } catch (saveError) { setError(providerApiErrorText(saveError, 'Could not save sharing.')) }
    finally { setBusy(false) }
  }
  const saveAvailability = async (value: ProviderAvailableTo | null) => {
    setBusy(true); setError(null)
    try {
      await llmConfigService.setServerAccountAvailability(provider, value)
      setAvailabilityDraft(null); changed()
    } catch (saveError) {
      const status = (saveError as { response?: { status?: number } })?.response?.status
      setError(status === 409 ? 'The installation sets who can use this account, so it cannot be changed here.' : providerApiErrorText(saveError, 'Could not save who can use it.'))
    } finally { setBusy(false) }
  }
  const inputClass = 'mt-1.5 block w-full rounded-lg border border-gray-300 bg-white px-3 py-2 text-sm text-gray-900 outline-none focus:border-violet-500 focus:ring-2 focus:ring-violet-500/20 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100'
  const secondaryButtonClass = 'inline-flex items-center justify-center gap-1.5 rounded-lg border border-gray-300 bg-white px-3 py-2 text-xs font-medium text-gray-700 transition-colors hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:hover:bg-gray-700'
  const badgeClass = 'rounded-md px-1.5 py-0.5 text-[10px] font-medium'
  const cancel = () => { setAdding(false); setEditingId(null); resetForm() }
  const openAdd = () => { setAdding(true); setEditingId(null); resetForm() }

  // Per-account actions. The server enforces who may run each one.
  // One status line: a dot and a few words, checked automatically on load.
  const needsSignIn = (record: ProviderConnection) => {
    const status = statuses[record.id]
    if (status && status !== 'loading') return status.state === 'signed_out' || status.state === 'key_rejected'
    return record.configured === false
  }
  const statusLine = (record: ProviderConnection) => {
    const status = statuses[record.id]
    const text = status ? accountStatusText(status) : record.configured === false ? 'Not signed in' : ''
    if (!text) return null
    const ok = status && status !== 'loading' && status.state === 'signed_in'
    return (
      <p className="mt-0.5 flex items-center gap-1.5 text-xs text-gray-600 dark:text-gray-300" aria-label={`Status of ${record.display_name}`}>
        <span className={`h-1.5 w-1.5 rounded-full ${ok ? 'bg-emerald-500' : needsSignIn(record) ? 'bg-amber-500' : 'bg-gray-400'}`} />{text}
      </p>
    )
  }
  const canSignOut = (record: ProviderConnection) => record.can_manage === true && SIGN_OUT_PROVIDERS.has(provider) && (accountRelation(record) === 'server' || record.auth_method === 'cli_login')
  // The terminal is where a person signs in by hand and checks usage (/usage); there is no
  // separate usage check.
  const terminalItem = (record: ProviderConnection) => record.can_manage ? [{ label: 'Terminal (sign in, check usage)', onSelect: () => void runSetup(record, 'inspect') }] : []
  const checkItem = (record: ProviderConnection) => statuses[record.id] !== undefined ? [{ label: 'Check sign-in again', onSelect: () => void refreshStatus(record) }] : []
  const terminalFor = (record: ProviderConnection) => (session && sessionRowId === record.id && (
    <div className="mt-3 w-full"><GuidedProviderTerminal session={session} onFinished={value => { setSession(value); changed() }} onClose={() => { setSession(null); setSessionRowId(null) }} /></div>
  )) || (usageText && usageText.rowId === record.id && (
    <div className="mt-3 w-full">
      <pre aria-label={`Usage output for ${record.display_name}`} className="max-h-64 overflow-auto whitespace-pre-wrap rounded-lg bg-gray-50 p-3 text-xs text-gray-800 dark:bg-gray-900 dark:text-gray-200">{usageText.text}</pre>
      <button type="button" className={`${secondaryButtonClass} mt-2`} onClick={() => setUsageText(null)}>Close</button>
    </div>
  ))
  const sharingEditor = (record: ProviderConnection) => sharingId === record.id && (
    <form className="mt-3 w-full space-y-3 rounded-lg border border-gray-200 p-3 dark:border-gray-700" onSubmit={event => { event.preventDefault(); void saveSharing(record) }}>
      <SharingFields value={sharingDraft} onChange={setSharingDraft} disabled={busy} />
      <div className="flex gap-2">
        <button disabled={busy} type="submit" className="rounded-lg bg-violet-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-violet-500 disabled:opacity-50">{busy ? 'Saving…' : 'Save sharing'}</button>
        <button disabled={busy} type="button" className={secondaryButtonClass} onClick={() => setSharingId(null)}>Cancel</button>
      </div>
    </form>
  )

  const userRow = (record: ProviderConnection) => {
    const relation = accountRelation(record)
    const own = relation === 'own'
    const canManage = own || record.can_manage === true
    let detail: string
    if (own) detail = sharingSummary(record.sharing)
    else if (relation === 'admin_view') detail = otherAccountDetail(record, sharingSummary(record.sharing))
    else detail = `Shared by ${record.owner_name || 'another person'}`
    return (
      <li key={record.id} className="flex flex-wrap items-center justify-between gap-3 py-3 first:pt-0 last:pb-0">
        <div className="flex min-w-0 items-center gap-3">
          <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400">
            {own ? <UserRound className="h-4 w-4" /> : <Share2 className="h-4 w-4" />}
          </div>
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <span className="break-words text-sm font-medium text-gray-900 dark:text-gray-100">{record.display_name}</span>
              {own && <span className={`${badgeClass} bg-violet-50 text-violet-700 dark:bg-violet-500/10 dark:text-violet-300`}>{record.sharing?.mode === 'shared' ? 'Shared' : 'Private'}</span>}
              {record.configured === false && <span className={`${badgeClass} bg-amber-50 text-amber-700 dark:bg-amber-500/10 dark:text-amber-300`} title="Not signed in and no key yet: use Sign in to set it up">Not set up</span>}
            </div>
            <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{detail}</p>
            {relation === 'shared_with_you' && <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">You cannot see its credential.</p>}
            {statusLine(record)}
          </div>
        </div>
        {!disabled && (
          <div className="flex flex-wrap items-center gap-1">
            {canManage && record.auth_method === 'cli_login' && personalAllowed && needsSignIn(record) && <button disabled={busy} type="button" className={secondaryButtonClass} onClick={() => void login(record)}><LogIn className="h-3.5 w-3.5" /> Sign in</button>}
            <ActionsMenu label={`More for ${record.display_name}`} disabled={busy} items={[
              ...terminalItem(record),
              ...checkItem(record),
              ...(canManage && record.auth_method === 'cli_login' && personalAllowed && !needsSignIn(record) ? [{ label: 'Sign in again', onSelect: () => void login(record) }] : []),
              ...(personalAllowed && canSignOut(record) ? [{ label: 'Sign out', onSelect: () => void signOut(record) }] : []),
              ...(canManage && manage ? [{ label: 'Who can use it', onSelect: () => { setSharingId(sharingId === record.id ? null : record.id); setSharingDraft(record.sharing ?? { mode: 'private' }) } }] : []),
              ...(own && personalAllowed ? [{ label: 'Rename or change key', onSelect: () => { setEditingId(record.id); setAdding(true); setName(record.display_name); setCredential(''); setAuthMethod(record.auth_method === 'cli_login' ? 'cli_login' : 'api_key'); setUnderlyingProvider(record.underlying_provider || 'google') } }] : []),
              ...(canManage ? [{ label: 'Remove', danger: true, onSelect: () => void remove(record) }] : []),
            ]} />
          </div>
        )}
        {sharingEditor(record)}
        {terminalFor(record)}
      </li>
    )
  }

  const serverBlock = (record: ProviderConnection) => {
    const availability = record.availability
    const choice = availabilityDraft === null ? null : availabilityDraft === 'all' ? 'all' : availabilityDraft === 'admins' ? 'admins' : 'chosen'
    return (
      <div className="rounded-lg border border-gray-200 p-3 dark:border-gray-700">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="flex min-w-0 items-start gap-3">
            <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400"><ShieldCheck className="h-4 w-4" /></div>
            <div className="min-w-0">
              <span className="text-sm font-medium text-gray-900 dark:text-gray-100">Shared account</span>
              {statusLine(record) || (record.identity && <p className="mt-0.5 text-xs text-gray-600 dark:text-gray-300">Signed in as {record.identity}</p>)}
              <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                {record.usable === false ? 'Not available to you' : `Used by ${availability?.text?.toLowerCase() || 'everyone'}`}
                {availability?.pinned && <span className="ml-1 inline-flex items-center gap-1"><Lock className="h-3 w-3" /> set by the installation</span>}
              </p>
            </div>
          </div>
          {!disabled && record.can_manage && (
            <div className="flex flex-wrap items-center gap-1">
              {needsSignIn(record) && <button disabled={busy} type="button" className={secondaryButtonClass} onClick={() => setConfirmSharedLogin(record)}><LogIn className="h-3.5 w-3.5" /> Sign in</button>}
              <ActionsMenu label="More for the shared account" disabled={busy} items={[
                ...terminalItem(record),
                ...checkItem(record),
                ...(!needsSignIn(record) ? [{ label: 'Sign in with another login', onSelect: () => setConfirmSharedLogin(record) }] : []),
                ...(canSignOut(record) ? [{ label: 'Sign out', onSelect: () => void signOut(record) }] : []),
                ...(record.availability_editable ? [{ label: 'Who can use it', onSelect: () => setAvailabilityDraft(availability?.available_to ?? 'all') }] : []),
              ]} />
            </div>
          )}
        </div>
        {choice !== null && record.availability_editable && (
          <div className="mt-3 space-y-3 border-t border-gray-200 pt-3 dark:border-gray-700">
            <label className="block text-xs font-medium text-gray-700 dark:text-gray-300">Who can use the shared account
              <select aria-label="Who can use the shared account" disabled={busy} value={choice} className={inputClass}
                onChange={event => {
                  const value = event.target.value
                  // Everyone and Only admins save at once; Specific people opens the pickers.
                  if (value === 'all' || value === 'admins') void saveAvailability(value)
                  else setAvailabilityDraft({ admins: true, products: [], users: [] })
                }}>
                <option value="all">Everyone</option>
                <option value="admins">Only admins</option>
                <option value="chosen">Specific people or products…</option>
              </select>
            </label>
            {choice === 'chosen' && availabilityDraft !== null && (
              <form className="space-y-3" onSubmit={event => { event.preventDefault(); void saveAvailability(availabilityDraft) }}>
                <AvailabilityFields value={availabilityDraft} onChange={setAvailabilityDraft} disabled={busy} />
                <button disabled={busy} type="submit" className="rounded-lg bg-violet-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-violet-500 disabled:opacity-50">{busy ? 'Saving…' : 'Save'}</button>
              </form>
            )}
            <div className="flex flex-wrap gap-2">
              {availability?.source === 'admin' && <button disabled={busy} type="button" className={secondaryButtonClass} onClick={() => void saveAvailability(null)}>Reset to installation policy</button>}
              <button disabled={busy} type="button" className={secondaryButtonClass} onClick={() => setAvailabilityDraft(null)}>Done</button>
            </div>
          </div>
        )}
        {terminalFor(record)}
      </div>
    )
  }

  const group = (title: string, records: ProviderConnection[], extra?: ReactNode) => (records.length > 0 || extra) && (
    <div className="mt-5">
      <div className="mb-2 flex items-center justify-between gap-2">
        <h4 className="text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{title}</h4>
        {extra}
      </div>
      {records.length > 0 && <ul className="divide-y divide-gray-200 dark:divide-gray-700">{records.map(userRow)}</ul>}
    </div>
  )

  // Picker: selectable accounts, grouped, plus a selected account that is no
  // longer available here so saving does not silently change it.
  const selectable = connections.filter(accountUsable)
  const selectedValue = selectedId || `global:${provider}`
  const selectedRecord = connections.find(record => record.id === selectedValue)
  const selectedMissing = !selectable.some(record => record.id === selectedValue)
  const own = connections.filter(record => accountRelation(record) === 'own')

  const addForm = adding && (
    <form className={formOnly ? 'space-y-4' : 'mt-4 space-y-4 border-t border-gray-200 pt-4 dark:border-gray-700'} onSubmit={event => { event.preventDefault(); void save() }}>
      <div className="flex items-center justify-between">
        <h4 className="text-sm font-medium text-gray-900 dark:text-gray-100">{editingId ? 'Edit your account' : 'Add your account'}</h4>
        <button disabled={busy} type="button" aria-label="Cancel account form" className="rounded-lg p-1 text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800" onClick={cancel}><X className="h-4 w-4" /></button>
      </div>
      <label className="block text-xs font-medium text-gray-700 dark:text-gray-300">Account name<input required maxLength={120} placeholder="e.g. Personal account" value={name} onChange={event => setName(event.target.value)} className={inputClass} /></label>
      {!editingId && ['claude-code', 'codex-cli', 'cursor-cli', 'muse-cli'].includes(provider) && <label className="block text-xs font-medium text-gray-700 dark:text-gray-300">Authentication<select value={authMethod} onChange={event => { setAuthMethod(event.target.value); setCredential('') }} className={inputClass}><option value="api_key">API key</option><option value="cli_login">Browser login</option></select></label>}
      {authMethod !== 'cli_login' && <label className="block text-xs font-medium text-gray-700 dark:text-gray-300">{provider === 'claude-code' ? 'Claude Code OAuth token' : 'API key'}<input required={!editingId} type="password" placeholder={editingId ? 'Leave blank to keep current credential' : 'Paste your credential'} autoComplete="new-password" value={credential} onChange={event => setCredential(event.target.value)} className={inputClass} /></label>}
      {provider === 'claude-code' && authMethod !== 'cli_login' && <p className="text-xs leading-5 text-gray-500 dark:text-gray-400">Generate a token with <code className="rounded bg-gray-100 px-1 py-0.5 dark:bg-gray-800">claude setup-token</code> for the account you want to add.</p>}
      {provider === 'pi-cli' && <label className="block text-xs font-medium text-gray-700 dark:text-gray-300">Pi provider ID<input required value={underlyingProvider} onChange={event => setUnderlyingProvider(event.target.value)} className={inputClass} /></label>}
      {!editingId && manage && <SharingFields value={newSharing} onChange={setNewSharing} disabled={busy} />}
      <p className="text-xs leading-5 text-gray-500 dark:text-gray-400">Credentials are encrypted. Nobody else sees them, even when the account is shared.</p>
      <div className="flex items-center gap-2">
        <button disabled={busy || disabled || !personalAllowed} type="submit" className="inline-flex items-center gap-2 rounded-lg bg-violet-600 px-3 py-2 text-sm font-medium text-white transition-colors hover:bg-violet-500 disabled:cursor-not-allowed disabled:opacity-50">{busy && <Loader2 className="h-4 w-4 animate-spin" />}{busy ? 'Saving…' : editingId ? 'Update account' : authMethod === 'cli_login' ? 'Save and sign in' : 'Save account'}</button>
        <button disabled={busy} type="button" className={secondaryButtonClass} onClick={cancel}>Cancel</button>
      </div>
    </form>
  )
  const addButton = !disabled && personalAllowed && !adding && (
    <button disabled={busy} type="button" className={secondaryButtonClass} onClick={openAdd}><Plus className="h-3.5 w-3.5" /> Add my account</button>
  )
  const errorLine = error && <p role="alert" className="mt-3 rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700 dark:bg-red-500/10 dark:text-red-300">{error}</p>

  if (manage) {
    return (
      <>
        <section className="mb-5 rounded-xl border border-gray-200 p-4 dark:border-gray-700">
          <div className="flex items-center gap-2">
            <Users className="h-4 w-4 text-violet-600 dark:text-violet-300" />
            <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">Provider accounts</h3>
          </div>
          <p className="mt-2 text-xs leading-5 text-gray-500 dark:text-gray-400">Which {providerLabel || 'provider'} login or key a workflow, Crew or Code runs as.</p>
          {group('Your accounts', own, addButton)}
          {!personalAllowed && <p className="mt-2 text-xs text-gray-500 dark:text-gray-400">The installation does not allow personal accounts for this provider.</p>}
          {personalAllowed && own.length === 0 && !adding && <p className="text-xs text-gray-500 dark:text-gray-400">You have no accounts yet. <strong>Add my account</strong> signs in your own {providerLabel || 'provider'} login: private to you unless you share it.</p>}
          {addForm}
          {server && <div className="mt-5">
            <div className="mb-2 text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">Shared account</div>
            {serverBlock(server)}
          </div>}
          {group('Shared with you', connections.filter(record => accountRelation(record) === 'shared_with_you'))}
          {group('Other people\'s accounts', connections.filter(record => accountRelation(record) === 'admin_view'))}
          {errorLine}
          <ConfirmationDialog
            isOpen={confirmSharedLogin !== null}
            onClose={() => setConfirmSharedLogin(null)}
            onConfirm={() => { const record = confirmSharedLogin; setConfirmSharedLogin(null); if (record) void login(record) }}
            title={`Sign in the shared ${providerLabel || 'provider'} login?`}
            message={`This is the shared account, not yours. Everyone it is available to will run on the login you sign in with, and on its plan. To add a login only you use, cancel and choose "Add my account" under Your accounts.`}
            confirmText="Sign in shared login"
            type="warning"
          />
        </section>
        {/* Cost is not part of setting up an account: collapsed, one click away. */}
        {canReview && <details className="mb-5 rounded-xl border border-gray-200 p-4 dark:border-gray-700">
          <summary className="cursor-pointer text-sm font-semibold text-gray-900 dark:text-gray-100">Cost by account</summary>
          <div className="mt-3"><ProviderAccountCostsSection provider={provider} /></div>
        </details>}
      </>
    )
  }

  return (
    <section className={selectionOnly ? '' : formOnly ? 'p-3' : 'mb-5 rounded-xl border border-gray-200 p-4 dark:border-gray-700'}>
      {!formOnly && !selectionOnly && <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="flex items-center gap-2">
            <Users className="h-4 w-4 text-violet-600 dark:text-violet-300" />
            <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">Provider accounts</h3>
          </div>
          <p className="mt-2 text-xs leading-5 text-gray-500 dark:text-gray-400">Use the server account or one of your own.</p>
        </div>
        {addButton}
      </div>}

      {onSelect && !formOnly && (
        <label className={`${selectionOnly ? '' : 'mt-4'} block text-xs font-medium text-gray-700 dark:text-gray-300`}>
          Account to use
          <select aria-label="Provider account" disabled={disabled || busy} value={selectedValue} onChange={event => onSelect(event.target.value)} className={inputClass}>
            {selectedMissing && <option value={selectedValue}>{selectedRecord ? `${selectedRecord.display_name} (${NO_LONGER_AVAILABLE.toLowerCase()})` : `Selected account: ${NO_LONGER_AVAILABLE.toLowerCase()}`}</option>}
            {ACCOUNT_GROUPS.map(groupSpec => {
              const records = selectable.filter(record => groupSpec.relations.includes(accountRelation(record)))
              if (records.length === 0) return null
              return <optgroup key={groupSpec.label} label={groupSpec.label}>
                {records.map(record => <option key={record.id} value={record.id}>{accountOptionLabel(record)}</option>)}
              </optgroup>
            })}
          </select>
        </label>
      )}

      {!formOnly && !selectionOnly && <ul className="mt-4 divide-y divide-gray-200 dark:divide-gray-700">{own.map(userRow)}</ul>}
      {addForm}
      {errorLine}
    </section>
  )
}

/** A small "more" menu: the account's less common actions, so each row shows at most one button. */
function ActionsMenu({ label, items, disabled }: {
  label: string
  items: { label: string; onSelect: () => void; danger?: boolean }[]
  disabled?: boolean
}) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const close = (event: MouseEvent) => { if (!ref.current?.contains(event.target as Node)) setOpen(false) }
    document.addEventListener('mousedown', close)
    return () => document.removeEventListener('mousedown', close)
  }, [open])
  if (items.length === 0) return null
  return (
    <div ref={ref} className="relative">
      <button type="button" disabled={disabled} aria-label={label} aria-expanded={open} onClick={() => setOpen(value => !value)} className="rounded-lg p-2 text-gray-500 hover:bg-gray-100 disabled:opacity-50 dark:text-gray-400 dark:hover:bg-gray-800">
        <MoreHorizontal className="h-4 w-4" />
      </button>
      {open && (
        <div role="menu" className="absolute right-0 z-20 mt-1 min-w-52 overflow-hidden rounded-lg border border-gray-200 bg-white py-1 shadow-lg dark:border-gray-700 dark:bg-gray-900">
          {items.map(item => (
            <button key={item.label} role="menuitem" type="button" onClick={() => { setOpen(false); item.onSelect() }}
              className={`block w-full px-3 py-2 text-left text-xs hover:bg-gray-50 dark:hover:bg-gray-800 ${item.danger ? 'text-red-600 dark:text-red-400' : 'text-gray-700 dark:text-gray-200'}`}>
              {item.label}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}


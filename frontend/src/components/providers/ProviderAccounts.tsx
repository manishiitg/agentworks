import { useEffect, useState, type ReactNode } from 'react'
import { Users, Plus, ShieldCheck, UserRound, Pencil, Trash2, LogIn, Loader2, X, Gauge, Share2, Lock } from 'lucide-react'
import GuidedProviderTerminal from './GuidedProviderTerminal'
import ProviderAccountCostsSection from './AccountCosts'
import { AvailabilityFields, SharingFields, sharingSummary } from './SharingEditor'
import {
  llmConfigService,
  providerApiErrorText,
  type ProviderAccountRelation,
  type ProviderAccountSharing,
  type ProviderAvailableTo,
  type ProviderConnection,
  type ProviderSetupSession,
} from '../../services/llm-config-api'

// Providers whose CLI has a usage command the guided terminal can run.
const USAGE_PROVIDERS = new Set(['claude-code', 'codex-cli', 'muse-cli'])

/** How the caller reaches an account; an older server sends no relation. */
export const accountRelation = (record: ProviderConnection): ProviderAccountRelation =>
  record.relation ?? (record.scope === 'global' ? 'server' : 'own')

/** Whether the caller may select the account where the list was requested. */
export const accountUsable = (record: ProviderConnection) =>
  record.usable !== false && accountRelation(record) !== 'admin_view'

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
  const [connections, setConnections] = useState<ProviderConnection[]>([])
  const [editingId, setEditingId] = useState<string | null>(null)
  const [sharingId, setSharingId] = useState<string | null>(null)
  const [sharingDraft, setSharingDraft] = useState<ProviderAccountSharing>({ mode: 'private' })
  const [availabilityDraft, setAvailabilityDraft] = useState<ProviderAvailableTo | null>(null)
  const [authMethod, setAuthMethod] = useState('api_key')
  const [session, setSession] = useState<ProviderSetupSession | null>(null)
  const [sessionRowId, setSessionRowId] = useState<string | null>(null)
  const [usageText, setUsageText] = useState<{ rowId: string; text: string } | null>(null)
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
  const runSetup = async (record: ProviderConnection, action: 'authenticate' | 'usage') => {
    setBusy(true); setError(null)
    try {
      if (action === 'usage') {
        const result = await llmConfigService.checkProviderUsage(provider, record.id)
        if (result.session) { setSession(result.session); setSessionRowId(record.id) }
        else setUsageText({ rowId: record.id, text: result.usage_output || 'No usage output.' })
      } else {
        setSession(await llmConfigService.startProviderSetup(provider, action, 100, 24, undefined, false, record.id))
        setSessionRowId(record.id)
      }
    } catch (setupError) { setError(providerApiErrorText(setupError, action === 'usage' ? 'Could not check usage.' : 'Could not start account login.')) }
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
  const iconButtonClass = 'rounded-lg p-2 text-gray-500 hover:bg-gray-100 disabled:opacity-50 dark:text-gray-400 dark:hover:bg-gray-800'
  const badgeClass = 'rounded-md px-1.5 py-0.5 text-[10px] font-medium'
  const cancel = () => { setAdding(false); setEditingId(null); resetForm() }
  const openAdd = () => { setAdding(true); setEditingId(null); resetForm() }

  const usageButton = (record: ProviderConnection) => record.can_view_usage && USAGE_PROVIDERS.has(provider) && (
    <button disabled={busy} type="button" className={secondaryButtonClass} aria-label={`Usage for ${record.display_name}`} onClick={() => void runSetup(record, 'usage')}><Gauge className="h-3.5 w-3.5" /> Usage</button>
  )
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
    else if (relation === 'admin_view') detail = `Owner: ${record.owner_name || record.owner_user_id || 'unknown'} · ${sharingSummary(record.sharing)}`
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
              {record.native_tools_off && <span className={`${badgeClass} bg-amber-50 text-amber-700 dark:bg-amber-500/10 dark:text-amber-300`} title="Your runs on this account use AgentWorks tools only">Native tools off</span>}
            </div>
            <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{detail}</p>
            {relation === 'shared_with_you' && <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">Your runs on it use native tools off. You cannot see its credential.</p>}
          </div>
        </div>
        {!disabled && (
          <div className="flex flex-wrap items-center gap-1">
            {usageButton(record)}
            {canManage && own && record.auth_method === 'cli_login' && personalAllowed && <button disabled={busy} type="button" className={secondaryButtonClass} onClick={() => void login(record)}><LogIn className="h-3.5 w-3.5" /> Sign in</button>}
            {canManage && manage && <button disabled={busy} type="button" className={secondaryButtonClass} aria-label={`Sharing for ${record.display_name}`} onClick={() => { setSharingId(sharingId === record.id ? null : record.id); setSharingDraft(record.sharing ?? { mode: 'private' }) }}><Share2 className="h-3.5 w-3.5" /> Sharing</button>}
            {own && personalAllowed && <button disabled={busy} type="button" aria-label={`Edit ${record.display_name}`} title="Edit account" className={iconButtonClass} onClick={() => { setEditingId(record.id); setAdding(true); setName(record.display_name); setCredential(''); setAuthMethod(record.auth_method === 'cli_login' ? 'cli_login' : 'api_key'); setUnderlyingProvider(record.underlying_provider || 'google') }}><Pencil className="h-3.5 w-3.5" /></button>}
            {canManage && <button disabled={busy} type="button" aria-label={`Remove ${record.display_name}`} title="Remove account" className="rounded-lg p-2 text-gray-500 hover:bg-red-50 hover:text-red-600 disabled:opacity-50 dark:text-gray-400 dark:hover:bg-red-500/10 dark:hover:text-red-400" onClick={() => void remove(record)}><Trash2 className="h-3.5 w-3.5" /></button>}
          </div>
        )}
        {sharingEditor(record)}
        {terminalFor(record)}
      </li>
    )
  }

  const serverBlock = (record: ProviderConnection) => {
    const availability = record.availability
    return (
      <div className="rounded-lg border border-gray-200 p-3 dark:border-gray-700">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="flex min-w-0 items-start gap-3">
            <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400"><ShieldCheck className="h-4 w-4" /></div>
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-sm font-medium text-gray-900 dark:text-gray-100">{record.display_name || 'Server account'}</span>
                {record.kind && record.kind !== 'user' && <span className={`${badgeClass} bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300`}>{record.kind === 'admin' ? 'Admin-configured' : 'Installed'}</span>}
                {record.usable === false && <span className={`${badgeClass} bg-amber-50 text-amber-700 dark:bg-amber-500/10 dark:text-amber-300`}>Not available to you</span>}
              </div>
              {record.source && <p className="mt-0.5 break-words text-xs text-gray-500 dark:text-gray-400">{record.source}</p>}
              {availability && <p className="mt-0.5 text-xs text-gray-600 dark:text-gray-300">Available to: {availability.text}</p>}
              {availability?.pinned && <p className="mt-0.5 inline-flex items-center gap-1 text-xs text-gray-500 dark:text-gray-400"><Lock className="h-3 w-3" /> Set by the installation</p>}
            </div>
          </div>
          {!disabled && (
            <div className="flex flex-wrap items-center gap-1">
              {usageButton(record)}
              {record.availability_editable && availabilityDraft === null && <button disabled={busy} type="button" className={secondaryButtonClass} onClick={() => setAvailabilityDraft(availability?.available_to ?? 'all')}>Edit who can use it</button>}
            </div>
          )}
        </div>
        {availabilityDraft !== null && record.availability_editable && (
          <form className="mt-3 space-y-3 border-t border-gray-200 pt-3 dark:border-gray-700" onSubmit={event => { event.preventDefault(); void saveAvailability(availabilityDraft) }}>
            <AvailabilityFields value={availabilityDraft} onChange={setAvailabilityDraft} disabled={busy} />
            <div className="flex flex-wrap gap-2">
              <button disabled={busy} type="submit" className="rounded-lg bg-violet-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-violet-500 disabled:opacity-50">{busy ? 'Saving…' : 'Save'}</button>
              {availability?.source === 'admin' && <button disabled={busy} type="button" className={secondaryButtonClass} onClick={() => void saveAvailability(null)}>Reset to installation policy</button>}
              <button disabled={busy} type="button" className={secondaryButtonClass} onClick={() => setAvailabilityDraft(null)}>Cancel</button>
            </div>
          </form>
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
    <button disabled={busy} type="button" className={secondaryButtonClass} onClick={openAdd}><Plus className="h-3.5 w-3.5" /> Add account</button>
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
          {server && <div className="mt-4">{serverBlock(server)}</div>}
          {group('Your accounts', own, addButton)}
          {!personalAllowed && <p className="mt-2 text-xs text-gray-500 dark:text-gray-400">The installation does not allow personal accounts for this provider.</p>}
          {personalAllowed && own.length === 0 && !adding && <p className="text-xs text-gray-500 dark:text-gray-400">You have no accounts for this provider.</p>}
          {addForm}
          {group('Shared with you', connections.filter(record => accountRelation(record) === 'shared_with_you'))}
          {group('Other people\'s accounts', connections.filter(record => accountRelation(record) === 'admin_view'))}
          {errorLine}
        </section>
        <ProviderAccountCostsSection provider={provider} />
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
                {records.map(record => <option key={record.id} value={record.id}>{accountOptionLabel(record)}{record.native_tools_off ? ' · native tools off' : ''}</option>)}
              </optgroup>
            })}
          </select>
        </label>
      )}
      {onSelect && !formOnly && selectedRecord?.native_tools_off && !selectedMissing && <p className="mt-1 text-[11px] text-muted-foreground">Native tools off: runs on someone else's account use AgentWorks tools only.</p>}

      {!formOnly && !selectionOnly && <ul className="mt-4 divide-y divide-gray-200 dark:divide-gray-700">{own.map(userRow)}</ul>}
      {addForm}
      {errorLine}
    </section>
  )
}

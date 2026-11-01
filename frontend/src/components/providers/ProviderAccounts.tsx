import { Suspense, lazy, useEffect, useRef, useState, type ReactNode } from 'react'
import { Users, Plus, ShieldCheck, UserRound, LogIn, Loader2, X, Share2, Lock, MoreHorizontal } from 'lucide-react'
import GuidedProviderTerminal from './GuidedProviderTerminal'
import ConfirmationDialog from '../ui/ConfirmationDialog'
import { ADMIN_MANAGED_ACCOUNT_LABEL } from '../../utils/providerAccountLabels'
import { AvailabilityFields, SharingFields, sharingSummary } from './SharingEditor'
import AllowedModelsEditor from './AllowedModelsEditor'
// Loaded on first use: the key setup is rare and kept the eager bundle over budget (PLAT-717).
const ByokSetup = lazy(() => import('./ByokSetup'))
const ByokModelBrowser = lazy(() => import('./ByokModelBrowser'))
import { byokServiceOf } from '../../utils/byok'
import { allowedModelsSummary, allowedModelsText } from '../../utils/allowedModels'
import { parseTokenAmount } from '../../utils/tokenLimits'
import {
  llmConfigService,
  providerApiErrorText,
  type MacAppLending,
  type ProviderAccountRelation,
  type ProviderAccountSharing,
  type ProviderAccountStatus,
  type ProviderAvailableTo,
  type ProviderConnection,
  type ProviderSetupSession,
} from '../../services/llm-config-api'
import { formatTokens } from '../../utils/formatTokens'
import { useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { desktopRequest, type DesktopConnection } from '../../services/desktopConnections'

// Providers with a browser login the server can sign out (the CLI's own logout).
const SIGN_OUT_PROVIDERS = new Set(['claude-code', 'codex-cli', 'cursor-cli', 'muse-cli'])

/** One line for an account's status; never a credential. */
export function accountStatusText(status?: ProviderAccountStatus | 'loading', keyAccount = false): string {
  if (!status) return ''
  if (status === 'loading') return keyAccount ? 'Testing the key…' : 'Checking status…'
  if (keyAccount) {
    switch (status.state) {
      case 'signed_in': return status.verified ? `Key works${status.identity ? ` · ${status.identity}` : ''}${status.detail ? ` · ${status.detail}` : ''}` : `${status.identity || 'Key saved'} · not tested yet`
      case 'key_rejected': return `Key rejected${status.detail ? `: ${status.detail}` : ''}`
      default: return status.detail ? `Could not test: ${status.detail}` : 'Status unknown'
    }
  }
  switch (status.state) {
    case 'signed_in': return `Signed in${status.identity ? ` as ${status.identity}` : ''}${status.verified ? ' (checked)' : ''}`
    case 'signed_out': return status.detail || 'Signed out'
    case 'key_rejected': return `Login rejected${status.detail ? `: ${status.detail}` : ''}`
    default: return status.detail ? `Status unknown: ${status.detail}` : 'Status unknown'
  }
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
  { label: ADMIN_MANAGED_ACCOUNT_LABEL, relations: ['server'] },
  { label: 'Your accounts', relations: ['own'] },
  { label: 'Shared with you', relations: ['shared_with_you'] },
  { label: 'Shared with this workflow', relations: ['shared_with_workflow'] },
  { label: 'Shared with this Crew', relations: ['shared_with_crew'] },
]

/** Short label for a selectable account in a picker. */
export function accountOptionLabel(record: ProviderConnection): string {
  switch (accountRelation(record)) {
    case 'server': return ADMIN_MANAGED_ACCOUNT_LABEL
    case 'own': return `${record.display_name} (${record.sharing?.mode === 'shared' ? 'yours, shared' : 'private'})`
    default: return `${record.display_name}${record.owner_name ? ` (${record.owner_name})` : ''}`
  }
}

export const NO_LONGER_AVAILABLE = 'No longer available here'

type ServerAccountState = 'ready' | 'sign_in' | 'different_login' | 'missing' | 'unavailable' | 'loading'
/** Where one of your accounts on this Mac stands on a server, with the login each side reports (an email, usually). */
type ServerAccount = { state: ServerAccountState; mac_login?: string; server_login?: string }

export default function ProviderAccounts({ provider, providerLabel, selectedId, onSelect, disabled = false, addRequest = 0, addService, formOnly = false, selectionOnly = false, workspacePath, product }: {
  provider: string
  /** Display name of the provider, e.g. "Claude Code". */
  providerLabel?: string
  selectedId?: string
  onSelect?: (id: string) => void
  disabled?: boolean
  addRequest?: number
  /** Pi: the model key service the add flow opens on (e.g. openrouter). */
  addService?: string
  formOnly?: boolean
  selectionOnly?: boolean
  /** Workflow, Crew or Code path the selection is for. */
  workspacePath?: string | null
  product?: string
}) {
  const manage = !onSelect && !formOnly && !selectionOnly
  const [connections, setConnections] = useState<ProviderConnection[]>([])
  // The server account row is manageable only by admins; members share their own accounts only with named colleagues (PLAT-715).
  const isAdmin = connections.some(record => accountRelation(record) === 'server' && record.can_manage === true)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [sharingId, setSharingId] = useState<string | null>(null)
  const [modelsId, setModelsId] = useState<string | null>(null)
  // A Pi model key account's picks being edited in the model browser (PLAT-717).
  const [keyModelsDraft, setKeyModelsDraft] = useState<{ id: string; picks: string[] } | null>(null)
  const [sharingDraft, setSharingDraft] = useState<ProviderAccountSharing>({ mode: 'private' })
  const [availabilityDraft, setAvailabilityDraft] = useState<ProviderAvailableTo | null>(null)
  // The server account's default per-person token limits being edited (PLAT-693).
  const [limitsDraft, setLimitsDraft] = useState<{ daily: string; weekly: string } | null>(null)
  // Signing in the shared login changes the account everyone allowed uses:
  // confirm, and point to Add my account for a private one.
  const [confirmSharedLogin, setConfirmSharedLogin] = useState<ProviderConnection | null>(null)
  // These CLIs sign in with the person's own browser login in a terminal, as an admin does for the server account; that is what
  // most people want, so it is the default. An API key stays one choice away.
  const defaultAuthMethod = ['claude-code', 'codex-cli', 'cursor-cli', 'muse-cli'].includes(provider) ? 'cli_login' : 'api_key'
  const [authMethod, setAuthMethod] = useState(defaultAuthMethod)
  const [session, setSession] = useState<ProviderSetupSession | null>(null)
  const [sessionRowId, setSessionRowId] = useState<string | null>(null)
  const [statuses, setStatuses] = useState<Record<string, ProviderAccountStatus | 'loading'>>({})
  const [adding, setAdding] = useState(false)
  const [name, setName] = useState('')
  const [credential, setCredential] = useState('')
  const [newSharing, setNewSharing] = useState<ProviderAccountSharing>({ mode: 'private' })
  const [underlyingProvider, setUnderlyingProvider] = useState('google')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  // The result of "Also use on <server>", shown under the account it was for.
  const [notice, setNotice] = useState<{ id: string; text: string; signInUrl?: string } | null>(null)
  // Connected servers, when this page shows the Mac app's own accounts: each own account can also be added there.
  const onThisMac = window.electronAPI?.getDesktopConnection?.() === 'local'
  const servers = useConnectedWorkStore(state => state.connections).filter(connection => connection.id !== 'local')
  useEffect(() => { if (onThisMac && manage) void useConnectedWorkStore.getState().refresh() }, [onThisMac, manage])
  // Where each of your accounts stands on each connected server: ready, needs sign-in, or not there yet.
  const [onServers, setOnServers] = useState<Record<string, Record<string, ServerAccount> | 'unavailable'>>({})
  const serverIds = servers.map(server => server.id).join(',')
  const loadOnServer = (serverId: string) => desktopRequest<{ accounts: Record<string, ServerAccount> }>(`/${encodeURIComponent(serverId)}/provider-accounts?provider=${encodeURIComponent(provider)}`)
    .then(result => setOnServers(current => ({ ...current, [serverId]: result.accounts })))
    .catch(() => setOnServers(current => ({ ...current, [serverId]: 'unavailable' })))
  useEffect(() => {
    if (!onThisMac || !manage) return
    for (const id of serverIds.split(',').filter(Boolean)) void loadOnServer(id)
  }, [onThisMac, manage, serverIds, provider, connections.length])
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
      ? `Every run that uses the ${providerLabel || provider} admin-managed account will stop working until someone signs in again.`
      : `Sign out ${record.display_name}? Runs that use it stop working until it signs in again. The account stays.`
    if (!window.confirm(message)) return
    setBusy(true); setError(null)
    try { await llmConfigService.signOutProviderAccount(record.id); changed(); await refreshStatus(record) }
    catch (signOutError) { setError(providerApiErrorText(signOutError, 'Could not sign out.')) }
    finally { setBusy(false) }
  }
  const resetForm = () => { setName(''); setCredential(''); setAuthMethod(defaultAuthMethod); setNewSharing({ mode: 'private' }); setError(null) }
  useEffect(() => {
    if (!addRequest) return
    setAdding(true)
    setEditingId(null)
    resetForm()
  }, [addRequest])
  const server = connections.find(record => accountRelation(record) === 'server')
  const personalAllowed = server?.personal_accounts_allowed !== false
  const changed = () => window.dispatchEvent(new Event('provider-connections-changed'))
  const [macApp, setMacApp] = useState<MacAppLending | null>(null)
  useEffect(() => {
    if (onThisMac || !manage || !server?.can_manage) return
    let cancelled = false
    let timer: ReturnType<typeof setTimeout>
    const load = () => Promise.resolve().then(() => llmConfigService.getMacAppLending(provider)).then(state => {
      if (cancelled) return
      setMacApp(state)
      if (state.signing_in && !state.signed_in_as) timer = setTimeout(() => void load(), 3000)
    }).catch(() => undefined)
    void load()
    return () => { cancelled = true; clearTimeout(timer) }
  }, [provider, onThisMac, manage, server?.can_manage, macApp?.signing_in?.url])
  const [callbackUrl, setCallbackUrl] = useState('')
  const finishMacApp = async () => {
    setBusy(true); setError(null)
    try { setMacApp(await llmConfigService.setMacAppLending(provider, 'finish', callbackUrl.trim())); setCallbackUrl('') }
    catch (finishError) { setError(providerApiErrorText(finishError, 'Could not finish the sign-in.')) }
    finally { setBusy(false) }
  }
  const changeMacApp = async (action: 'sign_in' | 'sign_out') => {
    setBusy(true); setError(null)
    try { setMacApp(await llmConfigService.setMacAppLending(provider, action)) }
    catch (macError) { setError(providerApiErrorText(macError, action === 'sign_in' ? 'Could not start the sign-in.' : 'Could not turn it off.')) }
    finally { setBusy(false) }
  }
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
  const runSetup = async (record: ProviderConnection, action: 'authenticate' | 'inspect') => {
    setBusy(true); setError(null)
    try {
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
    } catch (setupError) { setError(providerApiErrorText(setupError, action === 'inspect' ? 'Could not open the terminal.' : 'Could not start account login.')) }
    finally { setBusy(false) }
  }
  const login = (record: ProviderConnection) => runSetup(record, 'authenticate')
  // Reached from the Mac app's "Use it on your servers too" (?sign_in=<account>): start that account's sign-in at once.
  const signInRequested = useRef(false)
  useEffect(() => {
    if (signInRequested.current || disabled) return
    const params = new URLSearchParams(window.location.search)
    const record = connections.find(item => item.id === params.get('sign_in') && item.can_manage && item.auth_method === 'cli_login')
    if (!record) return
    signInRequested.current = true
    params.delete('sign_in'); params.delete('provider')
    window.history.replaceState(window.history.state, '', `${window.location.pathname}${params.toString() ? `?${params}` : ''}`)
    void login(record)
  }, [connections, disabled])
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
  const saveModels = async (record: ProviderConnection, models: string[]) => {
    setBusy(true); setError(null)
    try {
      await llmConfigService.setAccountAllowedModels(record.id, models)
      setConnections(current => current.map(item => item.id === record.id ? { ...item, allowed_models: models.length > 0 ? models : undefined, ...(accountRelation(item) === 'server' ? { default_allowed_models: models } : {}) } : item))
      setModelsId(null); changed()
    } catch (saveError) { setError(providerApiErrorText(saveError, 'Could not save the allowed models.')) }
    finally { setBusy(false) }
  }
  // A server account's own list; allowed_models there is the viewer's effective one (PLAT-714).
  const accountModels = (record: ProviderConnection) => accountRelation(record) === 'server' && record.default_allowed_models !== undefined ? (record.default_allowed_models ?? undefined) : record.allowed_models
  const modelsEditor = (record: ProviderConnection) => modelsId === record.id && (
    <AllowedModelsEditor provider={provider} value={accountModels(record)} disabled={busy} onSave={models => saveModels(record, models ?? [])} onCancel={() => setModelsId(null)} />
  )
  const keyModelsEditor = (record: ProviderConnection) => keyModelsDraft?.id === record.id && (
    <div className="mt-3 w-full space-y-2 rounded-lg border border-gray-200 p-3 dark:border-gray-700">
      <Suspense fallback={null}><ByokModelBrowser request={{ connection_id: record.id, workspace_path: workspacePath }} service={record.underlying_provider} value={keyModelsDraft.picks} onChange={picks => setKeyModelsDraft({ id: record.id, picks })} manualIds={record.underlying_provider === 'openai-compatible'} /></Suspense>
      <div className="flex gap-2">
        <button type="button" disabled={busy || keyModelsDraft.picks.length === 0} className="rounded-lg bg-violet-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-violet-500 disabled:opacity-50" onClick={() => void saveModels(record, keyModelsDraft.picks).then(() => setKeyModelsDraft(null))}>{busy ? 'Saving…' : 'Save models'}</button>
        <button type="button" disabled={busy} className={secondaryButtonClass} onClick={() => setKeyModelsDraft(null)}>Cancel</button>
      </div>
    </div>
  )
  const modelsItem = (record: ProviderConnection) => record.can_manage && manage ? [{ label: 'Models', onSelect: () => {
    if (byokServiceOf(record)) setKeyModelsDraft(keyModelsDraft?.id === record.id ? null : { id: record.id, picks: record.allowed_models ?? [] })
    else setModelsId(modelsId === record.id ? null : record.id)
  } }] : []
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
  const saveTokenLimits = async (draft: { daily: string; weekly: string }) => {
    const daily = parseTokenAmount(draft.daily)
    const weekly = parseTokenAmount(draft.weekly)
    if (daily === null || weekly === null) { setError('Use a number like 500k or 5M, or leave it empty for no limit.'); return }
    setBusy(true); setError(null)
    try {
      await llmConfigService.setServerAccountTokenLimits(provider, { daily, weekly })
      setConnections(current => current.map(item => item.id === `global:${provider}` ? { ...item, token_limits: daily || weekly ? { daily: daily || undefined, weekly: weekly || undefined } : undefined } : item))
      setLimitsDraft(null); changed()
    } catch (saveError) { setError(providerApiErrorText(saveError, 'Could not save the limits.')) }
    finally { setBusy(false) }
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
    const text = status ? accountStatusText(status, Boolean(byokServiceOf(record))) : record.configured === false ? 'Not signed in' : ''
    if (!text) return null
    const ok = status && status !== 'loading' && status.state === 'signed_in'
    return (
      <p className="mt-0.5 flex items-center gap-1.5 text-xs text-gray-600 dark:text-gray-300" aria-label={`Status of ${record.display_name}`}>
        <span className={`h-1.5 w-1.5 rounded-full ${ok ? 'bg-emerald-500' : needsSignIn(record) ? 'bg-amber-500' : 'bg-gray-400'}`} />{text}
      </p>
    )
  }
  // A key goes from this Mac's backend straight to your private account on the server; a browser login is signed in there.
  // A server login can be another person than the one signed in on that server's website: name it everywhere.
  const signedInAs = (server: DesktopConnection) => server.user?.email || server.user?.username || ''
  const addToServer = async (record: ProviderConnection, server: DesktopConnection, signInAgain = false) => {
    const who = signedInAs(server) ? ` for ${signedInAs(server)}` : ''
    setBusy(true); setError(null); setNotice(null)
    try {
      const result = await desktopRequest<{ status: 'copied' | 'sign_in' | 'exists'; server_url: string }>(`/${encodeURIComponent(server.id)}/provider-accounts`, 'POST', { account_id: record.id })
      if (result.status === 'sign_in' || signInAgain) {
        setNotice({ id: record.id, text: signInAgain ? `Sign in on ${server.name} with the same login as this Mac.` : `Added to ${server.name} as a private account${who}. Sign in to it on the page that opened.`, signInUrl: result.server_url })
        window.electronAPI?.openExternal?.(result.server_url)
      } else setNotice({ id: record.id, text: result.status === 'exists' ? `Already on ${server.name} as a private account${who}.` : `Added to ${server.name} as a private account${who}. It is ready to use there.` })
    } catch (addError) { setError(addError instanceof Error ? addError.message : 'Could not add the account.') }
    finally { setBusy(false); void loadOnServer(server.id) }
  }
  // "On your servers": one line under each of your accounts on this Mac, with what to do on each server.
  const serverRow = (record: ProviderConnection) => onThisMac && manage && record.can_manage && !record.via_connection && servers.length > 0 && (
    <div className="mt-2 w-full pl-11 text-xs">
      <div className="flex flex-wrap items-center gap-2">
      <span className="text-gray-500 dark:text-gray-400">Servers:</span>
      {servers.map(server => {
        const entry = onServers[server.id]
        const account: ServerAccount = entry === 'unavailable' ? { state: 'unavailable' } : entry?.[record.id] ?? { state: 'loading' }
        const state = account.state
        const title = signedInAs(server) ? `Your login there: ${signedInAs(server)}` : undefined
        return (
          <span key={server.id} title={title} className="inline-flex items-center gap-1.5 rounded-md border border-gray-200 px-2 py-1 dark:border-gray-700">
            <span className="font-medium text-gray-800 dark:text-gray-200">{server.name}</span>
            {state === 'ready' && <span className="text-emerald-600 dark:text-emerald-400">{account.server_login && account.mac_login ? `✓ Same login · ${account.server_login}` : '✓ Ready'}</span>}
            {state === 'sign_in' && <button type="button" disabled={busy} className="font-medium text-amber-600 hover:underline disabled:opacity-50 dark:text-amber-400" onClick={() => void addToServer(record, server)}>Sign in needed</button>}
            {state === 'different_login' && <>
              <span className="text-amber-600 dark:text-amber-400">Different login: {account.server_login}</span>
              <button type="button" disabled={busy} className="font-medium text-violet-600 hover:underline disabled:opacity-50 dark:text-violet-400" onClick={() => void addToServer(record, server, true)}>Sign in again</button>
            </>}
            {state === 'missing' && <button type="button" disabled={busy} className="font-medium text-violet-600 hover:underline disabled:opacity-50 dark:text-violet-400" onClick={() => void addToServer(record, server)}>Add</button>}
            {state === 'loading' && <span className="text-gray-400">…</span>}
            {state === 'unavailable' && <span className="text-gray-400">Status unknown</span>}
          </span>
        )
      })}
      </div>
    </div>
  )
  const canSignOut = (record: ProviderConnection) => record.can_manage === true && SIGN_OUT_PROVIDERS.has(provider) && (accountRelation(record) === 'server' || record.auth_method === 'cli_login')
  // The terminal is where a person signs in by hand and checks usage (/usage); there is no
  // separate usage check.
  const terminalItem = (record: ProviderConnection) => record.can_manage ? [{ label: 'Terminal (check usage)', onSelect: () => void runSetup(record, 'inspect') }] : []
  const checkItem = (record: ProviderConnection) => statuses[record.id] !== undefined || byokServiceOf(record) ? [{ label: byokServiceOf(record) ? 'Test key again' : 'Check it works', onSelect: () => void refreshStatus(record) }] : []
  const terminalFor = (record: ProviderConnection) => (session && sessionRowId === record.id && (
    <div className="mt-3 w-full"><GuidedProviderTerminal session={session} onFinished={value => { setSession(value); changed() }} onClose={() => { setSession(null); setSessionRowId(null) }} /></div>
  ))
  const sharingEditor = (record: ProviderConnection) => sharingId === record.id && (
    <form className="mt-3 w-full space-y-3 rounded-lg border border-gray-200 p-3 dark:border-gray-700" onSubmit={event => { event.preventDefault(); void saveSharing(record) }}>
      <SharingFields value={sharingDraft} onChange={setSharingDraft} disabled={busy} peopleOnly={!isAdmin} />
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
    const keyService = byokServiceOf(record)
    if (own) detail = `${keyService ? `${keyService.label} · ` : ''}${sharingSummary(record.sharing)}`
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
            </div>
            <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{detail}</p>
            {manage && <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">Models: {keyService && record.allowed_models?.length ? allowedModelsText(record.allowed_models.map(id => id.slice(id.indexOf('/') + 1))) : allowedModelsSummary(record.allowed_models)}</p>}
            {relation === 'shared_with_you' && <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">You cannot see its credential.</p>}
            {statusLine(record)}
          </div>
        </div>
        {!disabled && (
          <div className="flex flex-wrap items-center gap-1">
            {canManage && record.auth_method === 'cli_login' && personalAllowed && needsSignIn(record) && <button disabled={busy} type="button" className={secondaryButtonClass} onClick={() => void login(record)}><LogIn className="h-3.5 w-3.5" /> Sign in</button>}
            <ActionsMenu label={`More for ${record.display_name}`} disabled={busy} items={[
              ...terminalItem(record),
              ...(onThisMac ? [] : checkItem(record)),
              ...(canManage && record.auth_method === 'cli_login' && personalAllowed && !needsSignIn(record) ? [{ label: 'Sign in again', onSelect: () => void login(record) }] : []),
              ...(personalAllowed && canSignOut(record) ? [{ label: 'Sign out', onSelect: () => void signOut(record) }] : []),
              ...(canManage && manage && !onThisMac ? [{ label: 'Who can use it', onSelect: () => { setSharingId(sharingId === record.id ? null : record.id); setSharingDraft(record.sharing ?? { mode: 'private' }) } }] : []),
              ...(own ? modelsItem({ ...record, can_manage: true }) : []),
              ...(own && personalAllowed ? [{ label: keyService ? 'Rename or replace key' : 'Rename or change key', onSelect: () => { setEditingId(record.id); setAdding(true); setName(record.display_name); setCredential(''); setAuthMethod(record.auth_method === 'cli_login' ? 'cli_login' : 'api_key'); setUnderlyingProvider(record.underlying_provider || 'google') } }] : []),
              ...(canManage ? [{ label: 'Remove', danger: true, onSelect: () => void remove(record) }] : []),
            ]} />
          </div>
        )}
        {serverRow(record)}
        {noticeFor(record)}
        {sharingEditor(record)}
        {modelsEditor(record)}
        {keyModelsEditor(record)}
        {terminalFor(record)}
      </li>
    )
  }

  // "Use from the Mac app": the server's proxy holds its own login for the program, and everyone the account is
  // available to gets it on their Mac as "<server> · <program> (company)". The sign-in is the switch.
  const macAppRow = (record: ProviderConnection) => !onThisMac && record.can_manage && macApp?.available && (
    <div className="mt-3 border-t border-gray-100 pt-3 pl-11 text-xs dark:border-gray-800">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <p className="font-medium text-gray-800 dark:text-gray-200">Use from the Mac app</p>
          <p className="mt-0.5 text-gray-500 dark:text-gray-400">{macApp.signed_in_as
            ? <>On · {macApp.signed_in_as}{provider === 'cursor-cli' && <> · shares this account's Cursor privacy settings</>}</>
            : <>Off</>}</p>
        </div>
        {macApp.signed_in_as
          ? <button type="button" disabled={busy} className={secondaryButtonClass} onClick={() => void changeMacApp('sign_out')}>Turn off</button>
          : !macApp.signing_in && <button type="button" disabled={busy} className={secondaryButtonClass} onClick={() => void changeMacApp('sign_in')}>Turn on</button>}
      </div>
      {macApp.signing_in && !macApp.signed_in_as && <div className="mt-2 rounded-lg border border-violet-200 bg-violet-50 p-3 text-violet-900 dark:border-violet-500/30 dark:bg-violet-500/10 dark:text-violet-100">
        {macApp.signing_in.needs_callback
          ? <>
            <p>1. <a className="font-medium underline" href={macApp.signing_in.url} target="_blank" rel="noreferrer">Open the sign-in</a> and approve with the company's {providerLabel || provider} account.</p>
            <p className="mt-1">2. Your browser then shows a page that cannot load (an address starting with http://localhost). Copy that whole address and paste it here.</p>
            <form className="mt-2 flex gap-2" onSubmit={event => { event.preventDefault(); void finishMacApp() }}>
              <input aria-label="Address after approving" value={callbackUrl} onChange={event => setCallbackUrl(event.target.value)} placeholder="http://localhost:54545/callback?code=…" className={`${inputClass} flex-1`} />
              <button type="submit" disabled={busy || !callbackUrl.trim()} className={secondaryButtonClass}>Finish</button>
            </form>
          </>
          : <p>Open <a className="font-medium underline" href={macApp.signing_in.url} target="_blank" rel="noreferrer">{macApp.signing_in.url}</a>{macApp.signing_in.code && <> and enter <strong className="font-mono">{macApp.signing_in.code}</strong></>}, then approve with the company's {providerLabel || provider} account. This updates by itself.</p>}
      </div>}
    </div>
  )
  const serverBlock = (record: ProviderConnection) => {
    const availability = record.availability
    return (
      <div className={onThisMac ? '' : 'rounded-lg border border-gray-200 p-3 dark:border-gray-700'}>
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="flex min-w-0 items-start gap-3">
            <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400"><ShieldCheck className="h-4 w-4" /></div>
            <div className="min-w-0">
              <span className="text-sm font-medium text-gray-900 dark:text-gray-100">{onThisMac ? `${providerLabel || provider} on this Mac` : ADMIN_MANAGED_ACCOUNT_LABEL}</span>
              {statusLine(record) || (record.identity && <p className="mt-0.5 text-xs text-gray-600 dark:text-gray-300">Signed in as {record.identity}</p>)}
              {/* On a personal Mac nobody else uses it: who may use it and per-person limits only matter on a server. */}
              {!onThisMac && <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                {record.usable === false ? 'Not available to you' : `Used by ${availability?.text?.toLowerCase() || 'everyone'}`}
                {availability?.pinned && <span className="ml-1 inline-flex items-center gap-1"><Lock className="h-3 w-3" /> set by the installation</span>}
              </p>}
              {manage && !(onThisMac && !accountModels(record)?.length) && <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400" title="Models anyone may run on this account; a different list for one person is set in Access → Users. A turn on another model runs on the first allowed one.">
                Models: {allowedModelsText(accountModels(record))}
                {record.can_manage && !disabled && !onThisMac && <button type="button" disabled={busy} className="ml-1.5 text-violet-600 hover:underline disabled:opacity-50 dark:text-violet-400" onClick={() => setModelsId(modelsId === record.id ? null : record.id)}>Edit</button>}
              </p>}
              {manage && !onThisMac && <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400" title="Tokens (input + output) each person may use on this account; overrides per person are in Access → Users. Day and week are UTC; weeks start Monday.">
                Limit per person: {record.token_limits?.daily || record.token_limits?.weekly
                  ? [record.token_limits?.daily ? `${formatTokens(record.token_limits.daily)} a day` : '', record.token_limits?.weekly ? `${formatTokens(record.token_limits.weekly)} a week` : ''].filter(Boolean).join(' · ')
                  : 'none'}
              </p>}
            </div>
          </div>
          {!disabled && record.can_manage && (
            <div className="flex flex-wrap items-center gap-1">
              {needsSignIn(record) && <button disabled={busy} type="button" className={secondaryButtonClass} onClick={() => setConfirmSharedLogin(record)}><LogIn className="h-3.5 w-3.5" /> Sign in</button>}
              <ActionsMenu label={onThisMac ? `More for ${providerLabel || provider} on this Mac` : 'More for the admin-managed account'} disabled={busy} items={[
                ...terminalItem(record),
                ...(onThisMac ? [] : checkItem(record)),
                ...(!needsSignIn(record) ? [{ label: 'Sign in with another login', onSelect: () => setConfirmSharedLogin(record) }] : []),
                ...(canSignOut(record) ? [{ label: 'Sign out', onSelect: () => void signOut(record) }] : []),
                ...(record.availability_editable && !onThisMac ? [{ label: 'Who can use it', onSelect: () => setAvailabilityDraft(availability?.available_to ?? 'all') }] : []),
                ...modelsItem(record),
                ...(manage && !onThisMac ? [{ label: 'Limits', onSelect: () => setLimitsDraft(limitsDraft ? null : { daily: record.token_limits?.daily ? String(record.token_limits.daily) : '', weekly: record.token_limits?.weekly ? String(record.token_limits.weekly) : '' }) }] : []),
              ]} />
            </div>
          )}
        </div>
        {serverRow(record)}
        {macAppRow(record)}
        {noticeFor(record)}
        {availabilityDraft !== null && record.availability_editable && (
          <form className="mt-3 space-y-3 border-t border-gray-200 pt-3 dark:border-gray-700" onSubmit={event => { event.preventDefault(); void saveAvailability(availabilityDraft) }} onKeyDown={event => { if (event.key === 'Escape') setAvailabilityDraft(null) }}>
            <AvailabilityFields value={availabilityDraft} onChange={setAvailabilityDraft} disabled={busy} />
            <button disabled={busy} type="submit" className="rounded-lg bg-violet-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-violet-500 disabled:opacity-50">{busy ? 'Saving…' : 'Save'}</button>
          </form>
        )}
        {limitsDraft !== null && record.can_manage && (
          <form className="mt-3 space-y-2 border-t border-gray-200 pt-3 dark:border-gray-700" onSubmit={event => { event.preventDefault(); void saveTokenLimits(limitsDraft) }}>
            <p className="text-xs text-gray-600 dark:text-gray-300">Tokens each person may use on this account (UTC; weeks start Monday). Empty is unlimited. Set a different limit for one person in Access → Users.</p>
            <div className="flex flex-wrap gap-3">
              {(['daily', 'weekly'] as const).map(field => (
                <label key={field} className="text-xs font-medium text-gray-700 dark:text-gray-300">{field === 'daily' ? 'Daily' : 'Weekly'}
                  <input aria-label={`${field === 'daily' ? 'Daily' : 'Weekly'} token limit per person`} disabled={busy} value={limitsDraft[field]} placeholder="Unlimited"
                    onChange={event => setLimitsDraft({ ...limitsDraft, [field]: event.target.value })} className={`${inputClass} w-32`} />
                </label>
              ))}
            </div>
            <div className="flex gap-2">
              <button disabled={busy} type="submit" className="rounded-lg bg-violet-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-violet-500 disabled:opacity-50">{busy ? 'Saving…' : 'Save limits'}</button>
              <button disabled={busy} type="button" className={secondaryButtonClass} onClick={() => setLimitsDraft(null)}>Cancel</button>
            </div>
          </form>
        )}
        {modelsEditor(record)}
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
  const selectable = connections.filter(record => accountUsable(record) && (!selectionOnly || accountConfigured(record)))
  const selectedValue = selectedId || `global:${provider}`
  const selectedRecord = connections.find(record => record.id === selectedValue)
  const selectedMissing = !selectable.some(record => record.id === selectedValue)
  const selectedUnavailableText = selectedRecord?.configured === false ? "needs setup" : NO_LONGER_AVAILABLE.toLowerCase()
  const own = connections.filter(record => accountRelation(record) === 'own' && !record.via_connection)

  // Pi: adding an account is the guided "Use your own model key" flow.
  const keySetup = adding && !editingId && provider === 'pi-cli' && (
    <div className={formOnly ? '' : 'mt-4 border-t border-gray-200 pt-4 dark:border-gray-700'}>
      <Suspense fallback={null}><ByokSetup initialService={addService} disabled={disabled || !personalAllowed} onCancel={cancel} onSaved={record => {
        setConnections(current => [...current, record]); setAdding(false); resetForm(); changed(); onSelect?.(record.id)
      }} /></Suspense>
    </div>
  )
  const addForm = keySetup || adding && (
    <form className={formOnly ? 'space-y-4' : 'mt-4 space-y-4 border-t border-gray-200 pt-4 dark:border-gray-700'} onSubmit={event => { event.preventDefault(); void save() }}>
      <div className="flex items-center justify-between">
        <h4 className="text-sm font-medium text-gray-900 dark:text-gray-100">{editingId ? 'Edit your account' : 'Add your account'}</h4>
        <button disabled={busy} type="button" aria-label="Cancel account form" className="rounded-lg p-1 text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800" onClick={cancel}><X className="h-4 w-4" /></button>
      </div>
      <label className="block text-xs font-medium text-gray-700 dark:text-gray-300">Account name<input required maxLength={120} placeholder="e.g. Personal account" value={name} onChange={event => setName(event.target.value)} className={inputClass} /></label>
      {!editingId && ['claude-code', 'codex-cli', 'cursor-cli', 'muse-cli'].includes(provider) && <label className="block text-xs font-medium text-gray-700 dark:text-gray-300">Authentication<select value={authMethod} onChange={event => { setAuthMethod(event.target.value); setCredential('') }} className={inputClass}><option value="cli_login">Browser login (sign in with your own account)</option><option value="api_key">API key</option></select></label>}
      {authMethod !== 'cli_login' && <label className="block text-xs font-medium text-gray-700 dark:text-gray-300">{provider === 'claude-code' ? 'Claude Code OAuth token' : 'API key'}<input required={!editingId} type="password" placeholder={editingId ? 'Leave blank to keep current credential' : 'Paste your credential'} autoComplete="new-password" value={credential} onChange={event => setCredential(event.target.value)} className={inputClass} /></label>}
      {provider === 'claude-code' && authMethod !== 'cli_login' && <p className="text-xs leading-5 text-gray-500 dark:text-gray-400">Generate a token with <code className="rounded bg-gray-100 px-1 py-0.5 dark:bg-gray-800">claude setup-token</code> for the account you want to add.</p>}
      {provider === 'pi-cli' && !editingId && <label className="block text-xs font-medium text-gray-700 dark:text-gray-300">Pi provider ID<input required value={underlyingProvider} onChange={event => setUnderlyingProvider(event.target.value)} className={inputClass} /></label>}
      {!editingId && manage && <SharingFields value={newSharing} onChange={setNewSharing} disabled={busy} peopleOnly={!isAdmin} />}
      <p className="text-xs leading-5 text-gray-500 dark:text-gray-400">Credentials are encrypted. Nobody else sees them, even when the account is shared.</p>
      <div className="flex items-center gap-2">
        <button disabled={busy || disabled || !personalAllowed} type="submit" className="inline-flex items-center gap-2 rounded-lg bg-violet-600 px-3 py-2 text-sm font-medium text-white transition-colors hover:bg-violet-500 disabled:cursor-not-allowed disabled:opacity-50">{busy && <Loader2 className="h-4 w-4 animate-spin" />}{busy ? 'Saving…' : editingId ? 'Update account' : authMethod === 'cli_login' ? 'Save and sign in' : 'Save account'}</button>
        <button disabled={busy} type="button" className={secondaryButtonClass} onClick={cancel}>Cancel</button>
      </div>
    </form>
  )
  const addButton = !disabled && personalAllowed && !adding && (
    <button disabled={busy} type="button" className={onThisMac ? 'inline-flex items-center gap-1.5 text-sm font-medium text-violet-600 hover:underline disabled:opacity-50 dark:text-violet-400' : secondaryButtonClass} onClick={openAdd}><Plus className="h-3.5 w-3.5" /> {provider === 'pi-cli' ? 'Add a model key' : onThisMac ? 'Add another account' : 'Add my account'}</button>
  )
  const noticeFor = (record: ProviderConnection) => notice?.id === record.id && (
    <p role="status" className="mt-3 flex w-full flex-wrap items-center gap-2 rounded-lg bg-emerald-50 px-3 py-2 text-xs text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-300">
      <span>{notice.text}</span>
      {notice.signInUrl && <button type="button" className="font-medium underline" onClick={() => window.electronAPI?.openExternal?.(notice.signInUrl!)}>Open sign-in</button>}
    </p>
  )
  // A connected server's company login, used from this Mac: added and removed by the backend as the server allows.
  // The program runs here; the server applies its own limits and records the usage.
  const companyRows = connections.filter(record => record.via_connection)
  const companySection = companyRows.length > 0 && <div className="mt-5">
    <div className="mb-2 text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">From your company's servers</div>
    <ul className="divide-y divide-gray-200 dark:divide-gray-700">
      {companyRows.map(record => {
        const server = servers.find(item => item.id === record.via_connection)?.name || 'your server'
        return <li key={record.id} className="flex items-center gap-3 py-3 first:pt-0 last:pb-0">
          <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400"><Users className="h-4 w-4" /></div>
          <div className="min-w-0">
            <p className="text-sm font-medium text-gray-900 dark:text-gray-100">{record.display_name}</p>
            <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">Runs on this Mac with {server}'s login. {server}'s limits apply and the usage counts there.</p>
            <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">Models: {allowedModelsText(record.allowed_models)}</p>
          </div>
        </li>
      })}
    </ul>
  </div>
  const errorLine = error && <p role="alert" className="mt-3 rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700 dark:bg-red-500/10 dark:text-red-300">{error}</p>

  if (manage) {
    return (
      <>
        <section className={onThisMac ? 'mb-5' : 'mb-5 rounded-xl border border-gray-200 p-4 dark:border-gray-700'}>
          {server && <div>
            {!onThisMac && <div className="mb-2 text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{ADMIN_MANAGED_ACCOUNT_LABEL}</div>}
            {serverBlock(server)}
          </div>}
          {own.length > 0 ? group('Your accounts', own, addButton) : personalAllowed && !adding && <div className="mt-4">{addButton}</div>}
          {provider === 'pi-cli' && own.length === 0 && !adding && <p className="mt-2 text-xs text-gray-500 dark:text-gray-400">OpenRouter, NVIDIA NIM, Groq, Google AI Studio… many have free models.</p>}
          {addForm}
          {companySection}
          {group('Shared with you', connections.filter(record => accountRelation(record) === 'shared_with_you'))}
          {group('Other people\'s accounts', connections.filter(record => accountRelation(record) === 'admin_view'))}
          {errorLine}
          <ConfirmationDialog
            isOpen={confirmSharedLogin !== null}
            onClose={() => setConfirmSharedLogin(null)}
            onConfirm={() => { const record = confirmSharedLogin; setConfirmSharedLogin(null); if (record) void login(record) }}
            title={`Sign in the admin-managed ${providerLabel || 'provider'} account?`}
            message={`Signing in changes the login and plan used by every run allowed to use this account. To add a login only you use, cancel and choose "Add my account" under Your accounts.`}
            confirmText="Sign in account"
            type="warning"
          />
        </section>
      </>
    )
  }

  return (
    <section className={selectionOnly ? '' : formOnly ? 'p-3' : 'mb-5 rounded-xl border border-gray-200 p-4 dark:border-gray-700'}>
      {!formOnly && !selectionOnly && <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="flex items-center gap-2">
            <Users className="h-4 w-4 text-violet-600 dark:text-violet-300" />
            <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">Accounts</h3>
          </div>
          <p className="mt-2 text-xs leading-5 text-gray-500 dark:text-gray-400">Use the admin-managed account or one of your own.</p>
        </div>
        {addButton}
      </div>}

      {onSelect && !formOnly && (
        <label className={selectionOnly ? 'block min-w-0 space-y-1' : 'mt-4 block text-xs font-medium text-gray-700 dark:text-gray-300'}>
          {selectionOnly ? <span className="text-[10px] font-medium uppercase text-muted-foreground">Account to use</span> : 'Account to use'}
          <select aria-label="Provider account" disabled={disabled || busy} value={selectedValue} onChange={event => onSelect(event.target.value)} className={selectionOnly ? 'h-9 w-full min-w-0 rounded-md border border-border bg-background px-2 text-xs text-foreground outline-none focus:border-primary disabled:cursor-not-allowed disabled:opacity-50' : inputClass}>
            {selectedMissing && <option value={selectedValue} disabled>{selectedRecord ? `${selectedRecord.display_name} (${selectedUnavailableText})` : `Selected account: ${selectedUnavailableText}`}</option>}
            {ACCOUNT_GROUPS.map(groupSpec => {
              const records = selectable.filter(record => groupSpec.relations.includes(accountRelation(record)))
              if (records.length === 0) return null
              return <optgroup key={groupSpec.label} label={provider === 'pi-cli' && groupSpec.label === 'Your accounts' ? 'Your keys' : groupSpec.label}>
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


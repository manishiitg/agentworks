import { useEffect, useState } from 'react'
import { Users, Plus, ShieldCheck, UserRound, Pencil, Trash2, LogIn, Loader2, X } from 'lucide-react'
import GuidedProviderTerminal from './GuidedProviderTerminal'
import { llmConfigService, type ProviderSetupSession, type ProviderConnection } from '../../services/llm-config-api'

export default function ProviderAccounts({ provider, selectedId, onSelect, disabled = false, addRequest = 0, formOnly = false, selectionOnly = false }: {
  provider: string
  selectedId?: string
  onSelect?: (id: string) => void
  disabled?: boolean
  addRequest?: number
  formOnly?: boolean
  selectionOnly?: boolean
}) {
  const [connections, setConnections] = useState<ProviderConnection[]>([])
  const [editingId, setEditingId] = useState<string | null>(null)
  const [authMethod, setAuthMethod] = useState('api_key')
  const [session, setSession] = useState<ProviderSetupSession | null>(null)
  const [adding, setAdding] = useState(false)
  const [name, setName] = useState('')
  const [credential, setCredential] = useState('')
  const [underlyingProvider, setUnderlyingProvider] = useState('google')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    let cancelled = false
    setError(null)
    const refresh=()=>{void llmConfigService.getProviderConnections().then(records => {
      if (!cancelled) setConnections(records.filter(record => record.provider === provider))
    }).catch(() => { if (!cancelled) setError('Could not load accounts.') })}
    refresh();window.addEventListener('provider-connections-changed',refresh)
    return () => { cancelled = true;window.removeEventListener('provider-connections-changed',refresh) }
  }, [provider])
  useEffect(() => {
    if (!addRequest) return
    setAdding(true)
    setEditingId(null)
    setName('')
    setCredential('')
    setAuthMethod('api_key')
    setError(null)
  }, [addRequest])
  const personalAllowed=connections.find(record=>record.scope==='global')?.personal_accounts_allowed!==false
 const save = async () => {
    if (disabled || !personalAllowed) return
    setBusy(true); setError(null)
    try {
      const record = editingId ? await (async()=>{await llmConfigService.updateProviderConnection(editingId,{display_name:name,...(credential ? {credential}: {})});return {...connections.find(item=>item.id===editingId)!,display_name:name}})() : await llmConfigService.addProviderConnection({ provider, display_name: name, ...(authMethod === 'cli_login' ? { auth_method: 'cli_login' } : { credential }), ...(provider === 'pi-cli' ? { underlying_provider: underlyingProvider } : {}) })
      setConnections(current => editingId ? current.map(item=>item.id===editingId ? record : item) : [...current, record]); setEditingId(null); setCredential(''); setName(''); setAdding(false)
      window.dispatchEvent(new Event('provider-connections-changed'))
      onSelect?.(record.id)
      if (record.auth_method === "cli_login") await login(record)
    } catch { setError('Could not save account. Check the provider and administrator policy.') }
    finally { setBusy(false) }
  }
  const login = async (record: ProviderConnection) => {
    setBusy(true);setError(null)
    try { setSession(await llmConfigService.startProviderSetup(provider,'authenticate',100,24,undefined,false,record.id)) }
    catch { setError('Could not start account login.') }
    finally {setBusy(false)}
  }
  const remove = async (record: ProviderConnection) => {
    setBusy(true);setError(null)
    try {await llmConfigService.deleteProviderConnection(record.id);setConnections(current=>current.filter(item=>item.id!==record.id));window.dispatchEvent(new Event('provider-connections-changed'))}
    catch {setError('Could not remove account.')}
    finally {setBusy(false)}
  }
  const inputClass = 'mt-1.5 block w-full rounded-lg border border-gray-300 bg-white px-3 py-2 text-sm text-gray-900 outline-none focus:border-violet-500 focus:ring-2 focus:ring-violet-500/20 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100'
  const secondaryButtonClass = 'inline-flex items-center justify-center gap-1.5 rounded-lg border border-gray-300 bg-white px-3 py-2 text-xs font-medium text-gray-700 transition-colors hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:hover:bg-gray-700'
  const cancel = () => { setAdding(false); setEditingId(null); setName(''); setCredential(''); setError(null) }

  return (
    <section className={selectionOnly ? "" : formOnly ? "p-3" : "mb-5 rounded-xl border border-gray-200 p-4 dark:border-gray-700"}>
      {!formOnly && !selectionOnly && <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="flex items-center gap-2">
            <Users className="h-4 w-4 text-violet-600 dark:text-violet-300" />
            <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">Provider accounts</h3>
          </div>
          <p className="mt-2 text-xs leading-5 text-gray-500 dark:text-gray-400">Use the shared server account or add a private account for your workflows.</p>
        </div>
        {!disabled && personalAllowed && !adding && (
          <button disabled={busy} type="button" className={secondaryButtonClass} onClick={() => { setAdding(true); setEditingId(null); setName(''); setCredential(''); setAuthMethod('api_key'); setError(null) }}>
            <Plus className="h-3.5 w-3.5" /> Add account
          </button>
        )}
      </div>}

      {onSelect && !formOnly && (
        <label className={`${selectionOnly ? '' : 'mt-4'} block text-xs font-medium text-gray-700 dark:text-gray-300`}>
          Account to use
          <select aria-label="Provider account" disabled={disabled || busy} value={selectedId || `global:${provider}`} onChange={event => onSelect(event.target.value)} className={inputClass}>
            {selectedId && !connections.some(record => record.id === selectedId) && <option value={selectedId}>Selected account unavailable</option>}
            {connections.map(record => <option key={record.id} value={record.id}>{record.display_name}{record.scope === 'global' ? ' (shared)' : ' (private)'}</option>)}
          </select>
        </label>
      )}

      {!formOnly && !selectionOnly && <ul className="mt-4 divide-y divide-gray-200 dark:divide-gray-700">
        {connections.filter(record => !onSelect || record.scope === 'user').map(record => (
          <li key={record.id} className="flex flex-wrap items-center justify-between gap-3 py-3 first:pt-0 last:pb-0">
            <div className="flex min-w-0 items-center gap-3">
              <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400">
                {record.scope === 'global' ? <ShieldCheck className="h-4 w-4" /> : <UserRound className="h-4 w-4" />}
              </div>
              <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="break-words text-sm font-medium text-gray-900 dark:text-gray-100">{record.display_name}</span>
                  <span className={`rounded-md px-1.5 py-0.5 text-[10px] font-medium ${record.scope === 'global' ? 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-400' : 'bg-violet-50 text-violet-700 dark:bg-violet-500/10 dark:text-violet-300'}`}>{record.scope === 'global' ? 'Shared' : 'Private'}</span>
                </div>
                <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{record.scope === 'global' ? 'Managed on this server' : 'Only available to you'}</p>
              </div>
            </div>
            {!disabled && personalAllowed && record.scope === 'user' && (
              <div className="flex items-center gap-1">
                {record.auth_method === 'cli_login' && <button disabled={busy} type="button" className={secondaryButtonClass} onClick={() => void login(record)}><LogIn className="h-3.5 w-3.5" /> Sign in</button>}
                <button disabled={busy} type="button" aria-label={`Edit ${record.display_name}`} title="Edit account" className="rounded-lg p-2 text-gray-500 hover:bg-gray-100 disabled:opacity-50 dark:text-gray-400 dark:hover:bg-gray-800" onClick={() => { setEditingId(record.id); setAdding(true); setName(record.display_name); setCredential(''); setAuthMethod(record.auth_method === 'cli_login' ? 'cli_login' : 'api_key'); setUnderlyingProvider(record.underlying_provider || 'google') }}><Pencil className="h-3.5 w-3.5" /></button>
                <button disabled={busy} type="button" aria-label={`Remove ${record.display_name}`} title="Remove account" className="rounded-lg p-2 text-gray-500 hover:bg-red-50 hover:text-red-600 disabled:opacity-50 dark:text-gray-400 dark:hover:bg-red-500/10 dark:hover:text-red-400" onClick={() => void remove(record)}><Trash2 className="h-3.5 w-3.5" /></button>
              </div>
            )}
          </li>
        ))}
      </ul>}

      {session && <div className="mt-4"><GuidedProviderTerminal session={session} onFinished={value => { setSession(value); window.dispatchEvent(new Event('provider-connections-changed')) }} onClose={() => setSession(null)} /></div>}
      {adding && (
        <form className={formOnly ? "space-y-4" : "mt-4 space-y-4 border-t border-gray-200 pt-4 dark:border-gray-700"} onSubmit={event => { event.preventDefault(); void save() }}>
          <div className="flex items-center justify-between">
            <h4 className="text-sm font-medium text-gray-900 dark:text-gray-100">{editingId ? 'Edit private account' : 'Add a private account'}</h4>
            <button disabled={busy} type="button" aria-label="Cancel account form" className="rounded-lg p-1 text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800" onClick={cancel}><X className="h-4 w-4" /></button>
          </div>
          <label className="block text-xs font-medium text-gray-700 dark:text-gray-300">Account name<input required maxLength={120} placeholder="e.g. Personal account" value={name} onChange={event => setName(event.target.value)} className={inputClass} /></label>
          {!editingId && ['claude-code', 'codex-cli', 'cursor-cli', 'muse-cli'].includes(provider) && <label className="block text-xs font-medium text-gray-700 dark:text-gray-300">Authentication<select value={authMethod} onChange={event => { setAuthMethod(event.target.value); setCredential('') }} className={inputClass}><option value="api_key">API key</option><option value="cli_login">Browser login</option></select></label>}
          {authMethod !== 'cli_login' && <label className="block text-xs font-medium text-gray-700 dark:text-gray-300">{provider === 'claude-code' ? 'Claude Code OAuth token' : 'API key'}<input required={!editingId} type="password" placeholder={editingId ? 'Leave blank to keep current credential' : 'Paste your credential'} autoComplete="new-password" value={credential} onChange={event => setCredential(event.target.value)} className={inputClass} /></label>}
          {provider === 'claude-code' && authMethod !== 'cli_login' && <p className="text-xs leading-5 text-gray-500 dark:text-gray-400">Generate a token with <code className="rounded bg-gray-100 px-1 py-0.5 dark:bg-gray-800">claude setup-token</code> for the account you want to add.</p>}
          {provider === 'pi-cli' && <label className="block text-xs font-medium text-gray-700 dark:text-gray-300">Pi provider ID<input required value={underlyingProvider} onChange={event => setUnderlyingProvider(event.target.value)} className={inputClass} /></label>}
          <p className="text-xs leading-5 text-gray-500 dark:text-gray-400">Credentials are encrypted and private to your user.</p>
          <div className="flex items-center gap-2">
            <button disabled={busy || disabled || !personalAllowed} type="submit" className="inline-flex items-center gap-2 rounded-lg bg-violet-600 px-3 py-2 text-sm font-medium text-white transition-colors hover:bg-violet-500 disabled:cursor-not-allowed disabled:opacity-50">{busy && <Loader2 className="h-4 w-4 animate-spin" />}{busy ? 'Saving…' : editingId ? 'Update account' : authMethod === 'cli_login' ? 'Save and sign in' : 'Save account'}</button>
            <button disabled={busy} type="button" className={secondaryButtonClass} onClick={cancel}>Cancel</button>
          </div>
        </form>
      )}
      {error && <p role="alert" className="mt-3 rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700 dark:bg-red-500/10 dark:text-red-300">{error}</p>}
    </section>
  )
}

// Add your own AI account to a connected server from the Mac app, and see the ones you have there (#3807).
// Everything goes to the server's own routes through the Mac's connection (serverAccounts.ts): the server checks the
// key, who you are there, whether it allows your own accounts and who you may share with. This page only shows it.
import { useEffect, useMemo, useState } from 'react'
import { Loader2, UserRound, X } from 'lucide-react'
import GuidedProviderTerminal from './GuidedProviderTerminal'
import { Segmented } from './ChoiceChips'
import { SharingFields, sharingSummary } from './SharingEditor'
import { providerApiErrorText, type ProviderAccountSharing, type ProviderConnection, type ProviderSetupSession, type ProviderShareTargets } from '../../services/llm-config-api'
import { serverAccounts } from '../../services/serverAccounts'
import type { DesktopConnection } from '../../services/desktopConnections'

const BROWSER_SIGN_IN = new Set(['claude-code', 'codex-cli', 'cursor-cli', 'muse-cli', 'agy-cli'])
// What to paste for each program, and where to get it.
const KEY_HELP: Record<string, { label: string; hint: string }> = {
  'claude-code': { label: 'Claude token', hint: 'On your Mac run "claude setup-token" and paste what it prints. Uses your Claude plan.' },
  'cursor-cli': { label: 'Cursor API key', hint: 'cursor.com → Settings → API keys. Billed to that Cursor account.' },
  'codex-cli': { label: 'OpenAI API key', hint: 'platform.openai.com → API keys. Billed per use.' },
  'agy-cli': { label: 'Gemini API key', hint: 'aistudio.google.com → Get API key.' },
}
const inputClass = 'mt-1.5 block w-full rounded-lg border border-border bg-background px-3 py-2 text-sm text-foreground outline-none focus:border-primary'
const linkClass = 'font-medium text-primary hover:underline disabled:opacity-50'

// One loader per server for the "who can use it" pickers: the same function each render, cached per page view.
const targetLoaders = new Map<string, () => Promise<ProviderShareTargets>>()
function targetsFor(serverId: string) {
  let load = targetLoaders.get(serverId)
  if (!load) {
    let cached: Promise<ProviderShareTargets> | null = null
    load = () => (cached ??= serverAccounts(serverId).shareTargets().catch(error => { cached = null; throw error }))
    targetLoaders.set(serverId, load)
  }
  return load
}

/** What the server says about this program for you: your accounts there, whether you're an admin, whether your own accounts are allowed. */
type ServerProgramState = { accounts: ProviderConnection[]; admin: boolean; ownAllowed: boolean } | 'loading' | 'unavailable'

function useServerProgram(serverId: string | null, provider: string, version: number): ServerProgramState {
  const [state, setState] = useState<ServerProgramState>('loading')
  useEffect(() => {
    if (!serverId) return
    let cancelled = false
    setState('loading')
    serverAccounts(serverId).list(provider).then(records => {
      if (cancelled) return
      const company = records.find(record => record.relation === 'server' || record.scope === 'global')
      setState({
        // Logins signed in once on the server are listed with this Mac's accounts already ("Signed in once on your server").
        accounts: records.filter(record => record.relation === 'own' && record.auth_method !== 'server_login'),
        admin: company?.can_manage === true,
        ownAllowed: company?.personal_accounts_allowed !== false,
      })
    }).catch(() => { if (!cancelled) setState('unavailable') })
    return () => { cancelled = true }
  }, [serverId, provider, version])
  return state
}

function SignInWindow({ server, session, onDone }: { server: DesktopConnection; session: ProviderSetupSession; onDone: () => void }) {
  const api = serverAccounts(server.id)
  return <div className="mt-3 w-full">
    <GuidedProviderTerminal session={session} remote={{ streamUrl: api.signInStreamUrl, cancel: api.cancelSignIn }} onFinished={onDone} onClose={onDone} />
  </div>
}

async function startSignIn(server: DesktopConnection, provider: string, accountId: string): Promise<ProviderSetupSession> {
  const api = serverAccounts(server.id)
  try { return await api.startSignIn(provider, accountId) } catch (error) {
    // One sign-in per account at a time: offer to end the one still open.
    if ((error as { response?: { status?: number } })?.response?.status !== 409 || !window.confirm('A sign-in is already open for this account. End it and start a new one?')) throw error
    return api.startSignIn(provider, accountId, true)
  }
}

/** Signs one of your accounts on a server in, in a window inside the app (the website is not needed). */
export function ServerSignIn({ server, provider, accountId, onDone }: { server: DesktopConnection; provider: string; accountId: string; onDone: () => void }) {
  const [session, setSession] = useState<ProviderSetupSession | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    let cancelled = false
    startSignIn(server, provider, accountId).then(next => { if (!cancelled) setSession(next) })
      .catch(() => { if (!cancelled) setError(`Could not start the sign-in on ${server.name}. Try again.`) })
    return () => { cancelled = true }
  }, [server, provider, accountId])
  if (error) return <p role="alert" className="mt-2 w-full text-xs text-destructive">{error}</p>
  return session ? <SignInWindow server={server} session={session} onDone={onDone} /> : <p className="mt-2 w-full text-xs text-muted-foreground">Starting the sign-in on {server.name}…</p>
}

/** The Add account form when "Where" is a server. */
export function AddOnServerForm({ server, provider, providerLabel, macCopy, onSaved, onCancel }: {
  server: DesktopConnection
  provider: string
  providerLabel: string
  /** Copy (or for Codex, move) this Mac's own login there, when the Mac has one. */
  macCopy?: { label: string; note: string; run: () => Promise<void> }
  onSaved: () => void
  onCancel: () => void
}) {
  const keyHelp = KEY_HELP[provider] ?? { label: 'API key', hint: '' }
  const program = useServerProgram(server.id, provider, 0)
  const [how, setHow] = useState<'key' | 'browser' | 'mac'>('key')
  const [name, setName] = useState('')
  const [credential, setCredential] = useState('')
  const [sharing, setSharing] = useState<ProviderAccountSharing>({ mode: 'private' })
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [signIn, setSignIn] = useState<ProviderSetupSession | null>(null)
  const signedInAs = server.user?.email || server.user?.username || ''
  const hows = useMemo(() => [
    { value: 'key' as const, label: provider === 'claude-code' ? 'Paste a token' : 'Paste a key' },
    ...(macCopy ? [{ value: 'mac' as const, label: macCopy.label }] : []),
    ...(BROWSER_SIGN_IN.has(provider) ? [{ value: 'browser' as const, label: 'Sign in with the browser' }] : []),
  ], [provider, macCopy])
  const ready = program !== 'loading' && program !== 'unavailable'
  const save = async () => {
    setBusy(true); setError(null)
    try {
      if (how === 'mac' && macCopy) { await macCopy.run(); onSaved(); return }
      const record = await serverAccounts(server.id).add({
        provider, display_name: name.trim() || `${providerLabel} (my ${how === 'key' ? 'key' : 'login'})`,
        ...(how === 'browser' ? { auth_method: 'cli_login' } : { credential }),
        ...(sharing.mode === 'shared' ? { sharing } : {}),
      })
      if (how === 'browser') setSignIn(await startSignIn(server, provider, record.id))
      else onSaved()
    } catch (saveError) { setError(providerApiErrorText(saveError, `Could not add the account on ${server.name}.`)) }
    finally { setBusy(false) }
  }
  if (signIn) return <div className="space-y-2">
    <p className="text-sm font-medium text-foreground">Sign in {providerLabel} on {server.name}</p>
    <p className="text-xs text-muted-foreground">Open the link the window shows, sign in with the account you want, and paste the code back here if it asks for one.</p>
    <SignInWindow server={server} session={signIn} onDone={() => { setSignIn(null); onSaved() }} />
  </div>
  return (
    <form className="space-y-4" onSubmit={event => { event.preventDefault(); void save() }}>
      {signedInAs && <p className="text-xs text-muted-foreground">You're signed in to {server.name} as {signedInAs}.</p>}
      {program === 'unavailable' && <p role="alert" className="rounded-lg bg-destructive/10 px-3 py-2 text-xs text-destructive">Can't reach {server.name} right now. Your other connections still work. Try again in a moment, or add it to this Mac for now.</p>}
      {ready && !program.ownAllowed && <p role="alert" className="rounded-lg bg-destructive/10 px-3 py-2 text-xs text-destructive">{server.name} doesn't allow your own {providerLabel} accounts right now; an admin there turned them off. Use {server.name}'s company account, or ask an admin there to allow your own accounts.</p>}
      <div className="space-y-1.5"><p className="text-xs font-medium text-foreground">How</p><Segmented label="How" value={how} options={hows} onChange={value => { setHow(value); setError(null) }} disabled={busy} /></div>
      {how !== 'mac' && <label className="block text-xs font-medium text-foreground">Account name<input maxLength={120} placeholder={`e.g. My ${providerLabel}`} value={name} onChange={event => setName(event.target.value)} className={inputClass} /></label>}
      {how === 'key' && <label className="block text-xs font-medium text-foreground">{keyHelp.label}<input required type="password" autoComplete="new-password" placeholder="Paste it here" value={credential} onChange={event => setCredential(event.target.value)} className={inputClass} />
        {keyHelp.hint && <span className="mt-1 block font-normal text-muted-foreground">{keyHelp.hint}</span>}</label>}
      {how === 'browser' && <p className="text-xs text-muted-foreground">After you save, a sign-in window opens here. You don't need to open {server.name}'s website.</p>}
      {how === 'mac' && macCopy && <p className="rounded-lg bg-warning/10 px-3 py-2 text-xs text-foreground">{macCopy.note}</p>}
      {how !== 'mac' && ready && <SharingFields value={sharing} onChange={setSharing} disabled={busy} peopleOnly={!program.admin} loadTargets={targetsFor(server.id)} place={server.name} />}
      {how !== 'mac' && <p className="text-xs text-muted-foreground">The key or login is kept encrypted on {server.name}. People you share it with can run with it but never see it.</p>}
      {error && <p role="alert" className="rounded-lg bg-destructive/10 px-3 py-2 text-xs text-destructive">{error}</p>}
      <div className="flex items-center gap-2">
        <button disabled={busy || !ready || !program.ownAllowed} type="submit" className="inline-flex items-center gap-2 rounded-lg bg-primary px-3 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:cursor-not-allowed disabled:opacity-50">{busy && <Loader2 className="h-4 w-4 animate-spin" />}{busy ? 'Saving…' : how === 'browser' ? 'Save and sign in' : how === 'mac' ? 'Add' : 'Save'}</button>
        <button disabled={busy} type="button" onClick={onCancel} className="rounded-lg border border-border px-3 py-2 text-xs font-medium text-foreground hover:bg-muted">Cancel</button>
      </div>
    </form>
  )
}

/** "Your accounts on <server>": your own accounts kept there, with sign in again, who can use it and remove. */
export function YourAccountsOnServer({ server, provider, version, onChanged }: {
  server: DesktopConnection
  provider: string
  /** Bumped after a change, to read the list again. */
  version: number
  onChanged: () => void
}) {
  const program = useServerProgram(server.id, provider, version)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [signIn, setSignIn] = useState<{ id: string; session: ProviderSetupSession } | null>(null)
  const [sharingFor, setSharingFor] = useState<{ id: string; draft: ProviderAccountSharing } | null>(null)
  if (program === 'loading' || program === 'unavailable' || program.accounts.length === 0) return null
  const run = async (action: () => Promise<void>, fallback: string) => {
    setBusy(true); setError(null)
    try { await action() } catch (actionError) { setError(providerApiErrorText(actionError, fallback)) } finally { setBusy(false) }
  }
  const api = serverAccounts(server.id)
  return <div className="mt-5">
    <div className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">Your accounts on {server.name}</div>
    <ul className="divide-y divide-border">
      {program.accounts.map(record => <li key={record.id} className="flex flex-wrap items-start gap-3 py-3 first:pt-0 last:pb-0">
        <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-muted text-muted-foreground"><UserRound className="h-4 w-4" /></div>
        <div className="min-w-0 flex-1">
          <p className="text-sm font-medium text-foreground">{record.display_name}</p>
          <p className="mt-0.5 text-xs text-muted-foreground">{record.configured === false ? 'Not signed in yet' : record.auth_method === 'cli_login' ? 'Your own login' : 'Your key'} · Who can use it: {record.sharing?.mode === 'shared' ? sharingSummary(record.sharing) : 'Only me'}</p>
          <p className="mt-0.5 text-xs text-muted-foreground">Kept on {server.name}. Your runs there use it when you pick it in a project's Models.</p>
          <div className="mt-1 flex flex-wrap gap-3 text-xs">
            {record.auth_method === 'cli_login' && <button type="button" disabled={busy} className={linkClass} onClick={() => void run(async () => setSignIn({ id: record.id, session: await startSignIn(server, provider, record.id) }), 'Could not start the sign-in.')}>{record.configured === false ? 'Sign in' : 'Sign in again'}</button>}
            <button type="button" disabled={busy} className={linkClass} onClick={() => setSharingFor({ id: record.id, draft: record.sharing ?? { mode: 'private' } })}>Who can use it</button>
            <button type="button" disabled={busy} className="font-medium text-destructive hover:underline disabled:opacity-50" onClick={() => { if (window.confirm(`Remove ${record.display_name} from ${server.name}? Projects there that use it stop until they pick another account.`)) void run(async () => { await api.remove(record.id); onChanged() }, 'Could not remove the account.') }}>Remove</button>
          </div>
          {sharingFor?.id === record.id && <form className="mt-3 space-y-3 rounded-lg border border-border p-3" onSubmit={event => { event.preventDefault(); void run(async () => { await api.setSharing(record.id, sharingFor.draft); setSharingFor(null); onChanged() }, 'Could not save who can use it.') }}>
            <div className="flex justify-end"><button type="button" aria-label="Close" className="text-muted-foreground" onClick={() => setSharingFor(null)}><X className="h-4 w-4" /></button></div>
            <SharingFields value={sharingFor.draft} onChange={draft => setSharingFor({ id: record.id, draft })} disabled={busy} peopleOnly={!program.admin} loadTargets={targetsFor(server.id)} place={server.name} />
            <button disabled={busy} type="submit" className="rounded-lg bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50">Save</button>
          </form>}
          {signIn?.id === record.id && <SignInWindow server={server} session={signIn.session} onDone={() => { setSignIn(null); onChanged() }} />}
        </div>
      </li>)}
    </ul>
    {error && <p role="alert" className="mt-2 rounded-lg bg-destructive/10 px-3 py-2 text-xs text-destructive">{error}</p>}
  </div>
}

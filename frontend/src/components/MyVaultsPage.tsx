import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { Check, KeyRound, Loader2, Plug, Plus, RefreshCw, Trash2, Users, Vault } from 'lucide-react'
import { WorkspaceBackButton } from './workspace/WorkspaceBackButton'
import { Button } from './ui/Button'
import { Input } from './ui/Input'
import { Badge } from './ui/badge'
import { SecretField } from './ui/SecretField'
import { SettingsCount, SettingsEmpty } from './ui/SettingsCard'
import ConfirmationDialog from './ui/ConfirmationDialog'
import { agentApi } from '../services/api'
import { useChatStore } from '../stores/useChatStore'

// "My vaults" (PLAT-507). A vault is a shared set of apps (MCP connections) and secrets. The people in it can use what is
// inside; only its owners change it. The server checks ownership on every action, so this screen only decides what to
// show. A secret's value is typed here and nowhere else.

interface VaultConnection {
  id: string
  label: string
  provider: string
  status: string
  /** Why the last sign-in did not finish, e.g. it timed out or the app's tools could not be loaded. */
  sign_in_error?: string
}

interface VaultView {
  group: { ID: string; Name: string; Description?: string; Owners?: string[] }
  role: 'owner' | 'member'
  members: string[]
  connector_ids: string[]
  connections?: VaultConnection[]
  secret_names: string[]
}

interface VaultApp {
  name: string
  oauth: boolean
  /** Sign-in cannot be set up automatically on this server (PLAT-708); the server lists these last. */
  needs_admin_setup?: boolean
}

const needsAdminSetupHint = "Sign-in can't be set up automatically here; an admin can register an OAuth app for it, or use an API key as a Vault secret"

/** The apps Vault can connect (its catalog), loaded once per page and shared by every vault card. */
let vaultAppsRequest: Promise<VaultApp[]> | null = null
function loadVaultApps(): Promise<VaultApp[]> {
  if (!vaultAppsRequest) vaultAppsRequest = agentApi.myVaultsOp({ operation: 'apps' })
    .then(text => (JSON.parse(text) as { apps?: VaultApp[] }).apps ?? [])
    .catch(cause => { vaultAppsRequest = null; throw cause })
  return vaultAppsRequest
}

/** A searchable list of the apps Vault can connect. Only catalog names can be picked: the server accepts nothing else. */
function AppPicker({ value, onChange }: { value: string; onChange: (name: string) => void }) {
  const [apps, setApps] = useState<VaultApp[] | null>(null)
  const [failed, setFailed] = useState('')
  const [query, setQuery] = useState('')
  useEffect(() => {
    let live = true
    loadVaultApps().then(list => { if (live) setApps(list) }).catch(cause => { if (live) { setApps([]); setFailed(errorText(cause)) } })
    return () => { live = false }
  }, [])
  const shown = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return (apps ?? []).filter(app => !needle || app.name.toLowerCase().includes(needle))
  }, [apps, query])
  return (
    <div className="w-full space-y-1.5">
      <Input autoFocus value={query} onChange={event => setQuery(event.target.value)} placeholder="Search apps, for example Notion" aria-label="Search apps to connect" className="h-8" />
      {apps === null ? (
        <div className="flex items-center gap-2 text-muted-foreground"><Loader2 className="h-3.5 w-3.5 animate-spin" />Loading apps…</div>
      ) : failed ? (
        <p className="text-destructive">Could not load the apps: {failed}</p>
      ) : (
        <ul role="listbox" aria-label="Apps Vault can connect" className="max-h-48 overflow-y-auto rounded-md border border-border">
          {shown.length === 0 && <li className="px-3 py-2 text-muted-foreground">No app matches “{query.trim()}”.</li>}
          {shown.map(app => {
            const selected = app.name === value
            return (
              <li key={app.name} role="option" aria-selected={selected}>
                <button type="button" onClick={() => onChange(app.name)} className={`flex w-full items-center justify-between gap-2 px-3 py-1.5 text-left hover:bg-muted ${selected ? 'bg-muted font-medium text-foreground' : 'text-foreground'}`}>
                  <span className="flex items-center gap-2">{selected ? <Check className="h-3.5 w-3.5 text-primary" /> : <span className="w-3.5" />}{app.name}</span>
                  {app.needs_admin_setup
                    ? <Badge variant="outline" title={needsAdminSetupHint} className="border-warning/30 bg-warning/10 text-warning">Needs admin setup</Badge>
                    : app.oauth && <Badge variant="outline">Sign-in</Badge>}
                </button>
              </li>
            )
          })}
        </ul>
      )}
      <p className="text-muted-foreground">Google apps, GitHub and Slack are not here: connect them through their own Integrations. For any other app, save its API key as a secret below.</p>
    </div>
  )
}

type Pending =
  | { kind: 'connection'; id: string; name: string }
  | { kind: 'secret'; name: string }
  | { kind: 'vault' }
  | null

function errorText(cause: unknown): string {
  const data = (cause as { response?: { data?: { error?: string } | string } })?.response?.data
  if (typeof data === 'string' && data) return data
  if (data && typeof data === 'object' && data.error) return data.error
  return cause instanceof Error ? cause.message : 'Something went wrong'
}

// The sign-in link for a connection: only the reply's auth_url. Any other URL in it (a callback, an endpoint) is not
// a sign-in page, and linking it sent people to a provider page with no app (PLAT-708).
function signInUrl(text: string): string {
  const match = text.replace(/\\u0026/g, '&').match(/"auth_url"\s*:\s*"(https?:\/\/[^"\\]+)"/)
  return match ? match[1] : ''
}

// What to show after adding an app: its sign-in link, or the server's words when there is none to give.
// connectionId and until let the card re-read the vault while the link is open; a link lives five minutes.
interface SignInNote { url: string; text: string; connectionId: string; wasSignedIn: boolean; until: number }

function signInNote(text: string, wasSignedIn = false): SignInNote {
  const url = signInUrl(text)
  const connectionId = url ? (text.match(/\bConnection (c-[a-f0-9]{8,32})\b/)?.[1] ?? '') : ''
  return { url, text: url ? '' : text, connectionId, wasSignedIn, until: Date.now() + 5 * 60 * 1000 }
}

const noSignIn: SignInNote = { url: '', text: '', connectionId: '', wasSignedIn: false, until: 0 }

function plural(count: number, one: string, many: string): string {
  return `${count} ${count === 1 ? one : many}`
}

/** One part of a vault: a title, what it is for in plain words, its rows, and (for owners) an add action. */
function Part({ icon, title, count, help, action, children }: { icon: ReactNode; title: string; count: string; help: string; action?: ReactNode; children: ReactNode }) {
  return (
    <div className="space-y-2 py-4 first:pt-0">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          {icon}
          <h3 className="text-sm font-semibold text-foreground">{title}</h3>
          <SettingsCount>{count}</SettingsCount>
        </div>
        {action}
      </div>
      <p className="text-muted-foreground">{help}</p>
      {children}
    </div>
  )
}

function Row({ children, actions }: { children: ReactNode; actions?: ReactNode }) {
  return (
    <li className="flex min-h-9 flex-wrap items-center justify-between gap-2 rounded-md border border-border bg-muted/30 px-3 py-1.5">
      <div className="flex min-w-0 items-center gap-2">{children}</div>
      {actions && <div className="flex shrink-0 items-center gap-1">{actions}</div>}
    </li>
  )
}

function VaultCard({ vault, onChanged }: { vault: VaultView; onChanged: () => void }) {
  const addToast = useChatStore(state => state.addToast)
  const owner = vault.role === 'owner'
  const vaultId = vault.group.ID
  const owners = vault.group.Owners ?? []
  const connections: VaultConnection[] = vault.connections ?? vault.connector_ids.map(id => ({ id, label: id, provider: '', status: '' }))
  const [open, setOpen] = useState<'person' | 'connection' | 'secret' | null>(null)
  const [email, setEmail] = useState('')
  const [provider, setProvider] = useState('')
  const [label, setLabel] = useState('')
  const [secretName, setSecretName] = useState('')
  const [secretValue, setSecretValue] = useState('')
  const [replacing, setReplacing] = useState(false)
  const [busy, setBusy] = useState(false)
  const [signIn, setSignIn] = useState<SignInNote>(noSignIn)
  const [pending, setPending] = useState<Pending>(null)

  const run = useCallback(async (args: Record<string, unknown>, done: string): Promise<string | null> => {
    setBusy(true)
    try {
      const result = await agentApi.myVaultsOp({ vault_id: vaultId, ...args })
      addToast(done, 'success')
      onChanged()
      return result
    } catch (cause) {
      addToast(errorText(cause), 'error')
      return null
    } finally {
      setBusy(false)
    }
  }, [vaultId, addToast, onChanged])

  // While a sign-in link is open, re-read the vault so the app turns "Signed in" (or shows why not) without a reload.
  // "Sign in again" starts from an app that is already signed in, so only a new sign-in going wrong ends that wait.
  const pendingStatus = connections.find(connection => connection.id === signIn.connectionId)
  const pendingDone = !!pendingStatus && ((!signIn.wasSignedIn && pendingStatus.status === 'active') || !!pendingStatus.sign_in_error)
  useEffect(() => {
    if (!signIn.url || !signIn.connectionId || pendingDone || Date.now() > signIn.until) return
    // Each refresh re-renders the card, which schedules the next one.
    const timer = window.setTimeout(onChanged, 4000)
    return () => window.clearTimeout(timer)
  }, [signIn, pendingDone, onChanged])

  const addPerson = async () => {
    if (!email.trim()) return
    if (await run({ operation: 'add_member', email: email.trim() }, `${email.trim()} can now use this vault`) !== null) { setEmail(''); setOpen(null) }
  }

  const addConnection = async () => {
    if (!provider.trim()) return
    const result = await run({ operation: 'connect', provider: provider.trim(), label: label.trim() }, 'App added. Sign in to finish.')
    // A sign-in that could not be set up changes which apps need an admin; reload the list next time.
    vaultAppsRequest = null
    if (result !== null) { setProvider(''); setLabel(''); setOpen(null); setSignIn(signInNote(result)) }
  }

  const saveSecret = async () => {
    if (!secretName.trim() || !secretValue) return
    setBusy(true)
    try {
      await agentApi.setMyVaultSecret(vaultId, secretName.trim(), secretValue, replacing)
      addToast(replacing ? 'Secret value replaced' : 'Secret saved', 'success')
      setSecretName(''); setSecretValue(''); setReplacing(false); setOpen(null)
      onChanged()
    } catch (cause) {
      addToast(errorText(cause), 'error')
    } finally {
      setBusy(false)
    }
  }

  const confirmPending = async () => {
    const target = pending
    setPending(null)
    if (!target) return
    if (target.kind === 'connection') await run({ operation: 'remove_connection', connection_id: target.id }, `${target.name} removed`)
    if (target.kind === 'secret') await run({ operation: 'remove_secret', name: target.name }, 'Secret deleted')
    if (target.kind === 'vault') await run({ operation: 'delete' }, 'Vault deleted')
  }

  const empty = connections.length === 0 && vault.secret_names.length === 0
  const pendingText = pending?.kind === 'connection'
    ? { title: 'Remove this app?', message: `${pending.name} and its sign-in are removed from this vault. Everyone loses access to it. This cannot be undone.`, confirm: 'Remove app' }
    : pending?.kind === 'secret'
      ? { title: 'Delete this secret?', message: `${pending.name} and its value are deleted. Anything that uses it stops working. This cannot be undone.`, confirm: 'Delete secret' }
      : { title: 'Delete this vault?', message: `${vault.group.Name} is deleted for everyone in it. This cannot be undone.`, confirm: 'Delete vault' }

  return (
    <section aria-label={`Vault ${vault.group.Name}`} className="rounded-lg border border-border p-4 text-xs">
      <header className="flex flex-wrap items-start justify-between gap-2 pb-4">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <Vault className="h-4 w-4 text-primary" />
            <h2 className="text-sm font-semibold text-foreground">{vault.group.Name}</h2>
            <Badge variant={owner ? 'default' : 'secondary'}>{owner ? 'You own this' : 'You are a member'}</Badge>
            {busy && <Loader2 className="h-3.5 w-3.5 animate-spin text-muted-foreground" />}
          </div>
          {vault.group.Description && <p className="mt-1 text-muted-foreground">{vault.group.Description}</p>}
        </div>
        <p className="text-muted-foreground">
          {plural(vault.members.length, 'person', 'people')} · {plural(connections.length, 'app', 'apps')} · {plural(vault.secret_names.length, 'secret', 'secrets')}
        </p>
      </header>
      {!owner && (
        <p className="mb-2 rounded-md border border-border bg-muted/40 px-3 py-2 text-muted-foreground">You can use everything in this vault. Only its owners can change it or add people.</p>
      )}

      <div className="divide-y divide-border">
        <Part
          icon={<Users className="h-4 w-4 text-primary" />}
          title="People"
          count={plural(vault.members.length, 'person', 'people')}
          help="Everyone here can use this vault's apps and secrets. Owners can also change the vault and add people."
          action={owner && open !== 'person' && <Button variant="outline" size="sm" onClick={() => setOpen('person')}><Plus className="mr-1 h-3.5 w-3.5" />Add person</Button>}
        >
          <ul className="space-y-1.5">
            {vault.members.map(member => {
              const isOwner = owners.includes(member)
              return (
                <Row
                  key={member}
                  actions={owner && (
                    <>
                      <Button variant="ghost" size="sm" disabled={busy} onClick={() => void run({ operation: isOwner ? 'remove_owner' : 'add_owner', email: member }, isOwner ? `${member} is now a member` : `${member} is now an owner`)}>
                        {isOwner ? 'Make member' : 'Make owner'}
                      </Button>
                      {!isOwner && <Button variant="ghost" size="icon" className="h-7 w-7" title="Remove from this vault" aria-label={`Remove ${member}`} disabled={busy} onClick={() => void run({ operation: 'remove_member', email: member }, `${member} removed`)}><Trash2 className="h-3.5 w-3.5" /></Button>}
                    </>
                  )}
                >
                  <span aria-hidden className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-muted text-[11px] font-semibold uppercase text-muted-foreground">{member.charAt(0)}</span>
                  <span className="truncate text-foreground">{member}</span>
                  <Badge variant="outline">{isOwner ? 'Owner' : 'Member'}</Badge>
                </Row>
              )
            })}
          </ul>
          {owner && open === 'person' && (
            <form className="flex flex-wrap items-center gap-2" onSubmit={event => { event.preventDefault(); void addPerson() }}>
              <Input autoFocus value={email} onChange={event => setEmail(event.target.value)} placeholder="their email address" aria-label="Email of the person to add" className="h-8 min-w-0 flex-1" />
              <Button type="submit" size="sm" disabled={busy || !email.trim()}>Add</Button>
              <Button type="button" variant="ghost" size="sm" onClick={() => { setOpen(null); setEmail('') }}>Cancel</Button>
            </form>
          )}
        </Part>

        <Part
          icon={<Plug className="h-4 w-4 text-primary" />}
          title="Apps"
          count={plural(connections.length, 'app', 'apps')}
          help="Connected apps, such as Notion, that everyone in this vault can use."
          action={owner && open !== 'connection' && <Button variant="outline" size="sm" onClick={() => setOpen('connection')}><Plus className="mr-1 h-3.5 w-3.5" />Add app</Button>}
        >
          {connections.length === 0 ? <SettingsEmpty>No apps yet. Ask an agent to move one in from a Crew, Code or workflow, or add one here.</SettingsEmpty> : (
            <ul className="space-y-1.5">
              {connections.map(connection => {
                const name = connection.label || connection.provider || connection.id
                const needsSignIn = connection.status === 'authentication_required'
                const signInFailed = needsSignIn && !!connection.sign_in_error
                return (
                  <Row
                    key={connection.id}
                    actions={owner && (
                      <>
                        <Button variant="ghost" size="sm" disabled={busy} onClick={async () => { const r = await run({ operation: 'sign_in', connection_id: connection.id }, 'Sign-in link ready'); if (r) setSignIn(signInNote(r, connection.status === 'active')) }}>{needsSignIn ? 'Sign in' : 'Sign in again'}</Button>
                        <Button variant="ghost" size="icon" className="h-7 w-7" title="Refresh this app's tools" aria-label={`Refresh ${name}`} disabled={busy} onClick={() => void run({ operation: 'sync', connection_id: connection.id }, `${name} refreshed`)}><RefreshCw className="h-3.5 w-3.5" /></Button>
                        <Button variant="ghost" size="icon" className="h-7 w-7" title="Remove this app" aria-label={`Remove ${name}`} disabled={busy} onClick={() => setPending({ kind: 'connection', id: connection.id, name })}><Trash2 className="h-3.5 w-3.5" /></Button>
                      </>
                    )}
                  >
                    <Plug aria-hidden className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                    <span className="truncate font-medium text-foreground">{name}</span>
                    {connection.provider && connection.provider.toLowerCase() !== name.toLowerCase() && <span className="truncate text-muted-foreground">{connection.provider}</span>}
                    {connection.status && <Badge variant={needsSignIn ? 'outline' : 'secondary'} className={needsSignIn ? 'border-warning/30 bg-warning/10 text-warning' : ''} title={connection.sign_in_error}>{signInFailed ? 'Sign-in failed' : needsSignIn ? 'Needs sign-in' : connection.status === 'active' ? 'Signed in' : connection.status}</Badge>}
                    {signInFailed && <span className="min-w-0 truncate text-xs text-warning" title={connection.sign_in_error}>{connection.sign_in_error}</span>}
                  </Row>
                )
              })}
            </ul>
          )}
          {signIn.url && !pendingDone && <p className="rounded-md border border-info/30 bg-info/10 px-3 py-2 text-info">Finish connecting: <a className="underline" href={signIn.url} target="_blank" rel="noreferrer">open the sign-in page</a>. Come back here when you are done.</p>}
          {signIn.text && <p className="rounded-md border border-warning/30 bg-warning/10 px-3 py-2 text-warning">{signIn.text}</p>}
          {owner && open === 'connection' && (
            <form className="flex flex-wrap items-center gap-2" onSubmit={event => { event.preventDefault(); void addConnection() }}>
              <AppPicker value={provider} onChange={setProvider} />
              <Input value={label} onChange={event => setLabel(event.target.value)} placeholder="Your name for it (optional)" aria-label="Name for this connection" className="h-8 min-w-0 flex-1" />
              <Button type="submit" size="sm" disabled={busy || !provider.trim()}>{provider ? `Add ${provider}` : 'Pick an app'}</Button>
              <Button type="button" variant="ghost" size="sm" onClick={() => { setOpen(null); setProvider(''); setLabel('') }}>Cancel</Button>
            </form>
          )}
        </Part>

        <Part
          icon={<KeyRound className="h-4 w-4 text-primary" />}
          title="Secrets"
          count={plural(vault.secret_names.length, 'secret', 'secrets')}
          help="Passwords and keys. People in this vault can use them but can never read them."
          action={owner && open !== 'secret' && <Button variant="outline" size="sm" onClick={() => { setReplacing(false); setSecretName(''); setOpen('secret') }}><Plus className="mr-1 h-3.5 w-3.5" />Add secret</Button>}
        >
          {vault.secret_names.length === 0 ? <SettingsEmpty>No secrets yet.</SettingsEmpty> : (
            <ul className="space-y-1.5">
              {vault.secret_names.map(name => (
                <Row
                  key={name}
                  actions={owner && (
                    <>
                      <Button variant="ghost" size="sm" disabled={busy} onClick={() => { setReplacing(true); setSecretName(name); setSecretValue(''); setOpen('secret') }}>Replace value</Button>
                      <Button variant="ghost" size="icon" className="h-7 w-7" title="Delete this secret" aria-label={`Delete ${name}`} disabled={busy} onClick={() => setPending({ kind: 'secret', name })}><Trash2 className="h-3.5 w-3.5" /></Button>
                    </>
                  )}
                >
                  <KeyRound aria-hidden className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                  <code className="truncate text-foreground">{name}</code>
                  <span className="text-muted-foreground">value hidden</span>
                </Row>
              ))}
            </ul>
          )}
          {owner && open === 'secret' && (
            <form className="space-y-2" onSubmit={event => { event.preventDefault(); void saveSecret() }} autoComplete="off">
              <Input autoFocus={!replacing} value={secretName} readOnly={replacing} onChange={event => setSecretName(event.target.value)} placeholder="Name, for example GITHUB_TOKEN" aria-label="Secret name" className="h-8" />
              <SecretField label={replacing ? 'New value' : 'Value'} value={secretValue} onChange={setSecretValue} placeholder="Shown only while you type. It is never shown again." />
              <div className="flex items-center gap-2">
                <Button type="submit" size="sm" disabled={busy || !secretName.trim() || !secretValue}>{replacing ? 'Replace value' : 'Save secret'}</Button>
                <Button type="button" variant="ghost" size="sm" onClick={() => { setOpen(null); setSecretName(''); setSecretValue(''); setReplacing(false) }}>Cancel</Button>
              </div>
            </form>
          )}
        </Part>
      </div>

      {owner && (
        <footer className="mt-4 flex flex-wrap items-center justify-between gap-2 border-t border-border pt-4">
          <p className="text-muted-foreground">{empty ? 'Delete this vault when you no longer need it.' : 'To delete this vault, first remove its apps and secrets.'}</p>
          <Button variant="outline" size="sm" className="border-destructive/40 text-destructive hover:bg-destructive/10" disabled={busy || !empty} onClick={() => setPending({ kind: 'vault' })}>Delete vault</Button>
        </footer>
      )}

      <ConfirmationDialog
        isOpen={pending !== null}
        onClose={() => setPending(null)}
        onConfirm={() => void confirmPending()}
        title={pendingText.title}
        message={pendingText.message}
        confirmText={pendingText.confirm}
        type="danger"
        requireText={pending?.kind === 'vault' ? vault.group.Name : undefined}
      />
    </section>
  )
}

export default function MyVaultsPage() {
  const addToast = useChatStore(state => state.addToast)
  const [vaults, setVaults] = useState<VaultView[] | null>(null)
  const [maxOwned, setMaxOwned] = useState(5)
  const [creating, setCreating] = useState(false)
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [saving, setSaving] = useState(false)

  const refresh = useCallback(async () => {
    try {
      const parsed = JSON.parse(await agentApi.myVaultsOp({ operation: 'list' })) as { vaults?: VaultView[]; max_owned?: number }
      setVaults(parsed.vaults ?? [])
      if (parsed.max_owned) setMaxOwned(parsed.max_owned)
    } catch (cause) {
      setVaults([])
      addToast(errorText(cause), 'error')
    }
  }, [addToast])

  useEffect(() => { void refresh() }, [refresh])

  const create = async () => {
    if (!name.trim()) return
    setSaving(true)
    try {
      await agentApi.myVaultsOp({ operation: 'create', name: name.trim(), description: description.trim() })
      setName(''); setDescription(''); setCreating(false)
      await refresh()
    } catch (cause) {
      addToast(errorText(cause), 'error')
    } finally {
      setSaving(false)
    }
  }

  const owned = (vaults ?? []).filter(vault => vault.role === 'owner').length
  return (
    <section aria-label="My vaults" className="flex h-full min-h-0 flex-col bg-background">
      <header className="shrink-0 border-b border-border px-4 sm:px-6">
        <div className="flex flex-wrap items-center gap-3 py-3">
          <WorkspaceBackButton />
          <span aria-hidden="true" className="h-4 w-px bg-border" />
          <Vault className="h-4 w-4 text-primary" />
          <h1 className="text-sm font-semibold text-foreground">My vaults</h1>
          <SettingsCount>{owned} of {maxOwned} owned</SettingsCount>
        </div>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto w-full max-w-3xl space-y-4 p-4 text-xs sm:p-6">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <p className="max-w-xl text-muted-foreground">
              A vault shares apps (like Notion) and secrets (passwords and keys) with the people you choose. Everyone in it can use what is inside. Only its owners can change it.
            </p>
            {!creating && <Button size="sm" disabled={owned >= maxOwned} title={owned >= maxOwned ? `You can own up to ${maxOwned} vaults` : undefined} onClick={() => setCreating(true)}><Plus className="mr-1 h-3.5 w-3.5" />New vault</Button>}
          </div>
          {creating && (
            <form className="space-y-2 rounded-lg border border-border p-4" onSubmit={event => { event.preventDefault(); void create() }}>
              <h2 className="text-sm font-semibold text-foreground">New vault</h2>
              <Input autoFocus value={name} onChange={event => setName(event.target.value)} placeholder="Name, for example Team tools" aria-label="Vault name" className="h-8" />
              <Input value={description} onChange={event => setDescription(event.target.value)} placeholder="What is it for? (optional)" aria-label="What the vault is for" className="h-8" />
              <div className="flex items-center gap-2">
                <Button type="submit" size="sm" disabled={saving || !name.trim()}>Create vault</Button>
                <Button type="button" variant="ghost" size="sm" onClick={() => { setCreating(false); setName(''); setDescription('') }}>Cancel</Button>
              </div>
            </form>
          )}
          {vaults === null && <div className="flex items-center gap-2 text-muted-foreground"><Loader2 className="h-3.5 w-3.5 animate-spin" />Loading your vaults…</div>}
          {vaults?.length === 0 && !creating && <SettingsEmpty>You have no vaults yet. Create one to share apps and secrets with other people.</SettingsEmpty>}
          {vaults?.map(vault => <VaultCard key={vault.group.ID} vault={vault} onChanged={() => void refresh()} />)}
        </div>
      </div>
    </section>
  )
}

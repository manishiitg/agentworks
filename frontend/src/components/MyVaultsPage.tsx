import { useCallback, useEffect, useState } from 'react'
import { Loader2, Vault } from 'lucide-react'
import { WorkspaceBackButton } from './workspace/WorkspaceBackButton'
import { Button } from './ui/Button'
import { Input } from './ui/Input'
import { agentApi } from '../services/api'
import { useChatStore } from '../stores/useChatStore'

// "My vaults" (PLAT-507): a vault is a bundle of MCP connections and secrets that its owner(s) manage and its members use.
// Only an owner can change a vault; a member can use what is in it and cannot re-share. The server checks ownership on
// every action; this screen only shows what the person may do. A secret's value is typed here and nowhere else.

interface VaultView {
  group: { ID: string; Name: string; Description?: string; Owners?: string[] }
  role: 'owner' | 'member'
  members: string[]
  connector_ids: string[]
  secret_names: string[]
}

function errorText(cause: unknown): string {
  const data = (cause as { response?: { data?: { error?: string } | string } })?.response?.data
  if (typeof data === 'string' && data) return data
  if (data && typeof data === 'object' && data.error) return data.error
  return cause instanceof Error ? cause.message : 'Something went wrong'
}

// The sign-in link for a connection, if the server's reply holds one.
function signInUrl(text: string): string {
  const match = text.replace(/\\u0026/g, '&').match(/https?:\/\/[^\s"\\]+/)
  return match ? match[0] : ''
}

function VaultCard({ vault, onChanged }: { vault: VaultView; onChanged: () => void }) {
  const addToast = useChatStore(state => state.addToast)
  const owner = vault.role === 'owner'
  const vaultId = vault.group.ID
  const [email, setEmail] = useState('')
  const [provider, setProvider] = useState('')
  const [label, setLabel] = useState('')
  const [secretName, setSecretName] = useState('')
  const [secretValue, setSecretValue] = useState('')
  const [replace, setReplace] = useState(false)
  const [busy, setBusy] = useState(false)
  const [signIn, setSignIn] = useState('')

  const run = useCallback(async (args: Record<string, unknown>, done: string): Promise<string> => {
    setBusy(true)
    try {
      const result = await agentApi.myVaultsOp({ vault_id: vaultId, ...args })
      addToast(done, 'success')
      onChanged()
      return result
    } catch (cause) {
      addToast(errorText(cause), 'error')
      return ''
    } finally {
      setBusy(false)
    }
  }, [vaultId, addToast, onChanged])

  const people = async (operation: string, target: string, done: string) => {
    if (!target.trim()) return
    const result = await run({ operation, email: target.trim() }, done)
    if (result) setEmail('')
  }

  const connect = async () => {
    if (!provider.trim()) return
    const result = await run({ operation: 'connect', provider: provider.trim(), label: label.trim() }, 'Connection added')
    if (result) {
      setProvider('')
      setLabel('')
      setSignIn(signInUrl(result))
    }
  }

  const saveSecret = async () => {
    if (!secretName.trim() || !secretValue) return
    setBusy(true)
    try {
      await agentApi.setMyVaultSecret(vaultId, secretName.trim(), secretValue, replace)
      addToast('Secret saved', 'success')
      setSecretName('')
      setSecretValue('')
      setReplace(false)
      onChanged()
    } catch (cause) {
      addToast(errorText(cause), 'error')
    } finally {
      setBusy(false)
    }
  }

  const owners = vault.group.Owners ?? []
  return (
    <section aria-label={`Vault ${vault.group.Name}`} className="rounded-lg border border-border bg-card p-4">
      <header className="flex flex-wrap items-center gap-2">
        <h2 className="text-sm font-semibold text-foreground">{vault.group.Name}</h2>
        <span className="rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">{owner ? 'You own this' : 'Member'}</span>
        {busy && <Loader2 className="h-3.5 w-3.5 animate-spin text-muted-foreground" />}
      </header>
      {vault.group.Description && <p className="mt-1 text-xs text-muted-foreground">{vault.group.Description}</p>}

      <div className="mt-3 grid gap-4 md:grid-cols-2">
        <div>
          <h3 className="text-xs font-semibold text-foreground">People</h3>
          <ul className="mt-1 space-y-1 text-xs">
            {vault.members.map(member => (
              <li key={member} className="flex items-center justify-between gap-2">
                <span className="truncate text-foreground">{member}{owners.includes(member) ? ' (owner)' : ''}</span>
                {owner && (
                  <span className="flex shrink-0 gap-1">
                    {owners.includes(member)
                      ? <Button variant="ghost" size="sm" disabled={busy} onClick={() => void people('remove_owner', member, 'Owner removed')}>Make member</Button>
                      : <Button variant="ghost" size="sm" disabled={busy} onClick={() => void people('add_owner', member, 'Owner added')}>Make owner</Button>}
                    {!owners.includes(member) && <Button variant="ghost" size="sm" disabled={busy} onClick={() => void people('remove_member', member, 'Removed')}>Remove</Button>}
                  </span>
                )}
              </li>
            ))}
          </ul>
          {owner && (
            <form className="mt-2 flex gap-2" onSubmit={event => { event.preventDefault(); void people('add_member', email, 'Added') }}>
              <Input value={email} onChange={event => setEmail(event.target.value)} placeholder="Add a person by email" aria-label="Email to add" />
              <Button type="submit" variant="outline" size="sm" disabled={busy || !email.trim()}>Add</Button>
            </form>
          )}
        </div>

        <div>
          <h3 className="text-xs font-semibold text-foreground">Connections</h3>
          {vault.connector_ids.length === 0 && <p className="mt-1 text-xs text-muted-foreground">None yet. Ask an agent to promote a connection, or connect one here.</p>}
          <ul className="mt-1 space-y-1 text-xs">
            {vault.connector_ids.map(id => (
              <li key={id} className="flex items-center justify-between gap-2">
                <code className="truncate text-foreground">{id}</code>
                {owner && (
                  <span className="flex shrink-0 gap-1">
                    <Button variant="ghost" size="sm" disabled={busy} onClick={async () => { const r = await run({ operation: 'sign_in', connection_id: id }, 'Sign-in ready'); if (r) setSignIn(signInUrl(r)) }}>Sign in</Button>
                    <Button variant="ghost" size="sm" disabled={busy} onClick={() => void run({ operation: 'sync', connection_id: id }, 'Synced')}>Sync</Button>
                    <Button variant="ghost" size="sm" disabled={busy} onClick={() => { if (window.confirm('Remove this connection and its sign-in from the vault?')) void run({ operation: 'remove_connection', connection_id: id }, 'Connection removed') }}>Remove</Button>
                  </span>
                )}
              </li>
            ))}
          </ul>
          {signIn && <p className="mt-2 text-xs"><a className="text-primary underline" href={signIn} target="_blank" rel="noreferrer">Open the sign-in page</a></p>}
          {owner && (
            <form className="mt-2 flex flex-wrap gap-2" onSubmit={event => { event.preventDefault(); void connect() }}>
              <Input value={provider} onChange={event => setProvider(event.target.value)} placeholder="Server (e.g. Notion)" aria-label="Server to connect" className="min-w-0 flex-1" />
              <Input value={label} onChange={event => setLabel(event.target.value)} placeholder="Label (optional)" aria-label="Connection label" className="min-w-0 flex-1" />
              <Button type="submit" variant="outline" size="sm" disabled={busy || !provider.trim()}>Connect</Button>
            </form>
          )}
        </div>
      </div>

      <div className="mt-4">
        <h3 className="text-xs font-semibold text-foreground">Secrets</h3>
        {vault.secret_names.length === 0 && <p className="mt-1 text-xs text-muted-foreground">None yet. Members can use a secret but never read its value.</p>}
        <ul className="mt-1 space-y-1 text-xs">
          {vault.secret_names.map(name => (
            <li key={name} className="flex items-center justify-between gap-2">
              <code className="truncate text-foreground">{name}</code>
              {owner && <Button variant="ghost" size="sm" disabled={busy} onClick={() => { if (window.confirm(`Delete the secret ${name} and its value?`)) void run({ operation: 'remove_secret', name }, 'Secret removed') }}>Remove</Button>}
            </li>
          ))}
        </ul>
        {owner && (
          <form className="mt-2 flex flex-wrap items-center gap-2" onSubmit={event => { event.preventDefault(); void saveSecret() }} autoComplete="off">
            <Input value={secretName} onChange={event => setSecretName(event.target.value)} placeholder="NAME" aria-label="Secret name" className="min-w-0 flex-1" />
            <Input type="password" value={secretValue} onChange={event => setSecretValue(event.target.value)} placeholder="Value (never shown again)" aria-label="Secret value" className="min-w-0 flex-1" autoComplete="new-password" />
            <label className="flex items-center gap-1 text-xs text-muted-foreground"><input type="checkbox" checked={replace} onChange={event => setReplace(event.target.checked)} />Replace</label>
            <Button type="submit" variant="outline" size="sm" disabled={busy || !secretName.trim() || !secretValue}>Save secret</Button>
          </form>
        )}
      </div>

      {owner && (
        <footer className="mt-4 border-t border-border pt-3">
          <Button variant="ghost" size="sm" disabled={busy || vault.connector_ids.length > 0 || vault.secret_names.length > 0}
            title={vault.connector_ids.length > 0 || vault.secret_names.length > 0 ? 'Remove its connections and secrets first' : 'Delete this vault'}
            onClick={() => { if (window.confirm(`Delete the vault ${vault.group.Name}?`)) void run({ operation: 'delete' }, 'Vault deleted') }}>
            Delete vault
          </Button>
        </footer>
      )}
    </section>
  )
}

export default function MyVaultsPage() {
  const addToast = useChatStore(state => state.addToast)
  const [vaults, setVaults] = useState<VaultView[] | null>(null)
  const [maxOwned, setMaxOwned] = useState(5)
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [creating, setCreating] = useState(false)

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
    setCreating(true)
    try {
      await agentApi.myVaultsOp({ operation: 'create', name: name.trim(), description: description.trim() })
      setName('')
      setDescription('')
      await refresh()
    } catch (cause) {
      addToast(errorText(cause), 'error')
    } finally {
      setCreating(false)
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
          <span className="text-xs text-muted-foreground">{owned} of {maxOwned} owned</span>
        </div>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto w-full max-w-4xl space-y-4 p-4 sm:p-6">
          <p className="text-xs text-muted-foreground">
            A vault is a bundle of MCP connections and secrets you can share with other people. Only its owners add, remove or change what is in it and who may use it; members use it and cannot re-share.
          </p>
          <form className="flex flex-wrap gap-2 rounded-lg border border-border bg-card p-3" onSubmit={event => { event.preventDefault(); void create() }}>
            <Input value={name} onChange={event => setName(event.target.value)} placeholder="New vault name" aria-label="Vault name" className="min-w-0 flex-1" />
            <Input value={description} onChange={event => setDescription(event.target.value)} placeholder="What is it for? (optional)" aria-label="Vault description" className="min-w-0 flex-[2]" />
            <Button type="submit" size="sm" disabled={creating || !name.trim() || owned >= maxOwned}>Create vault</Button>
          </form>
          {vaults === null && <div className="flex items-center gap-2 text-xs text-muted-foreground"><Loader2 className="h-3.5 w-3.5 animate-spin" />Loading…</div>}
          {vaults?.length === 0 && <p className="text-xs text-muted-foreground">You have no vaults yet.</p>}
          {vaults?.map(vault => <VaultCard key={vault.group.ID} vault={vault} onChanged={() => void refresh()} />)}
        </div>
      </div>
    </section>
  )
}

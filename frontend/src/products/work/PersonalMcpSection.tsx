import { useCallback, useEffect, useState } from 'react'
import { KeyRound, Loader2, Plug, Trash2, User } from 'lucide-react'
import { SettingsCard } from '../../components/ui/SettingsCard'
import { Button } from '../../components/ui/Button'
import { Input } from '../../components/ui/Input'
import { Checkbox } from '../../components/ui/checkbox'
import { personalMcpApi, type PersonalMcpCatalogServer, type PersonalMcpServer } from '../../api/personalMcp'

const errorText = (cause: unknown, fallback: string) => {
  const response = (cause as { response?: { data?: { error?: string } } })?.response
  return response?.data?.error || (cause instanceof Error ? cause.message : fallback)
}

/**
 * Your own MCP servers and secrets in a Code (docs/design/code_private_mcp.md).
 * They are the signed-in person's alone: used only in their own chats, in the
 * Codes where they switch them on, never seen by anyone else in the Code.
 */
export function PersonalMcpSection({ projectId }: { projectId: string }) {
  const [servers, setServers] = useState<PersonalMcpServer[]>([])
  const [secrets, setSecrets] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [name, setName] = useState('')
  const [url, setUrl] = useState('')
  const [header, setHeader] = useState('')
  const [headerSecret, setHeaderSecret] = useState('')
  const [secretName, setSecretName] = useState('')
  const [secretValue, setSecretValue] = useState('')
  const [catalog, setCatalog] = useState<PersonalMcpCatalogServer[]>([])
  const [catalogPick, setCatalogPick] = useState('')
  // A sign-in that needs the person's own OAuth app (Google, GitHub).
  const [clientPrompt, setClientPrompt] = useState<{ server: string; message?: string; redirectUri?: string } | null>(null)
  const [clientId, setClientId] = useState('')
  const [clientSecret, setClientSecret] = useState('')

  const refresh = useCallback(async () => {
    try {
      const result = await personalMcpApi.list(projectId)
      setServers(result.servers)
      setSecrets(result.secrets)
      setError(null)
    } catch (cause) {
      setError(errorText(cause, 'Could not load your MCP servers.'))
    } finally {
      setLoading(false)
    }
  }, [projectId])

  useEffect(() => { void refresh() }, [refresh])
  useEffect(() => {
    personalMcpApi.catalog().then(setCatalog).catch(() => setCatalog([]))
  }, [])
  // After signing in in another window, show the new state on return.
  useEffect(() => {
    const onFocus = () => { void refresh() }
    window.addEventListener('focus', onFocus)
    return () => window.removeEventListener('focus', onFocus)
  }, [refresh])

  const run = async (key: string, action: () => Promise<unknown>) => {
    setBusy(key)
    setError(null)
    try {
      await action()
      await refresh()
    } catch (cause) {
      setError(errorText(cause, 'That did not work.'))
    } finally {
      setBusy(null)
    }
  }

  const add = () => run('add', async () => {
    const headers = header.trim() && headerSecret ? { [header.trim()]: { secret: headerSecret, format: header.trim().toLowerCase() === 'authorization' ? 'Bearer {}' : '{}' } } : undefined
    const added = await personalMcpApi.add({ name: name.trim(), url: url.trim(), headers })
    setName(''); setUrl(''); setHeader(''); setHeaderSecret('')
    if (added.oauth) await connect(added.name)
  })

  const addFromCatalog = () => run('catalog', async () => {
    const entry = catalog.find(item => item.catalog === catalogPick)
    if (!entry) return
    const added = await personalMcpApi.add({ name: entry.name, catalog: entry.catalog })
    setCatalogPick('')
    if (added.oauth) await connect(added.name)
  })

  const connect = async (server: string, client?: { clientId: string; clientSecret?: string }) => {
    const result = await personalMcpApi.connect(server, client)
    if (result.auth_url) {
      setClientPrompt(null); setClientId(''); setClientSecret('')
      window.open(result.auth_url, '_blank', 'noopener')
    } else if (result.status === 'needs_client_id') {
      if (clientPrompt?.server !== server) { setClientId(''); setClientSecret('') }
      setClientPrompt({ server, message: result.message, redirectUri: result.redirect_uri })
    } else if (result.message) setError(result.message)
  }

  const saveSecret = () => run('secret', async () => {
    await personalMcpApi.saveSecret(secretName.trim(), secretValue)
    setSecretName(''); setSecretValue('')
  })

  return (
    <div className="space-y-4" data-testid="personal-mcp-section">
      <SettingsCard
        icon={<User className="h-4 w-4 text-primary" />}
        title="Your servers"
        description="Your own MCP servers, with your own logins. They run only in your chats, in the Codes where you switch them on; nobody else in this Code sees or uses them. Changes apply from your next message."
      >
        {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
        {loading ? (
          <p className="text-sm text-muted-foreground"><Loader2 className="mr-2 inline h-4 w-4 animate-spin" />Loading…</p>
        ) : servers.length === 0 ? (
          <p className="text-sm text-muted-foreground">You have no servers yet.</p>
        ) : (
          <div className="space-y-2">
            {servers.map(server => (
              <div key={server.name} className="flex flex-wrap items-center gap-2 rounded-md border border-border p-2 text-sm">
                <label className="flex items-center gap-2" title="On in this Code">
                  <Checkbox
                    checked={server.enabled}
                    disabled={busy !== null}
                    onCheckedChange={() => { void run(`on:${server.name}`, () => personalMcpApi.setEnabled(server.name, projectId, !server.enabled)) }}
                    aria-label={`Use ${server.name} in this Code`}
                  />
                  <span className="font-medium text-foreground">{server.name}</span>
                </label>
                <span className="min-w-0 flex-1 truncate text-xs text-muted-foreground">{server.url}</span>
                {server.oauth && (server.connected
                  ? <span className="text-xs text-emerald-600">Signed in</span>
                  : <span className="text-xs text-amber-600">Needs sign-in</span>)}
                {server.oauth && (
                  <Button variant="outline" size="sm" disabled={busy !== null} onClick={() => { void run(`connect:${server.name}`, () => connect(server.name)) }}>
                    <Plug className="mr-1 h-3.5 w-3.5" />{server.connected ? 'Reconnect' : 'Sign in'}
                  </Button>
                )}
                <Button variant="ghost" size="icon" className="h-7 w-7 text-destructive" title={`Remove ${server.name}`} disabled={busy !== null} onClick={() => { void run(`rm:${server.name}`, () => personalMcpApi.remove(server.name)) }}>
                  <Trash2 className="h-3.5 w-3.5" />
                </Button>
              </div>
            ))}
          </div>
        )}
        {clientPrompt && (
          <div className="space-y-2 rounded-md border border-amber-500/40 bg-amber-500/5 p-3 text-sm" data-testid="personal-mcp-client-prompt">
            <p className="font-medium text-foreground">Sign in to {clientPrompt.server} with your own OAuth app</p>
            <p className="text-xs text-muted-foreground">
              This provider has no automatic app registration. Create an OAuth app (in Google Cloud or GitHub developer settings), then enter its client ID and secret. They are kept encrypted and used only for your sign-in.
            </p>
            {clientPrompt.redirectUri && (
              <p className="text-xs text-muted-foreground">Callback URL to register: <code className="break-all text-foreground">{clientPrompt.redirectUri}</code></p>
            )}
            <div className="grid gap-2 sm:grid-cols-2">
              <Input value={clientId} onChange={event => setClientId(event.target.value)} placeholder="Client ID" aria-label="OAuth client ID" />
              <Input type="password" autoComplete="off" value={clientSecret} onChange={event => setClientSecret(event.target.value)} placeholder="Client secret" aria-label="OAuth client secret" />
            </div>
            <div className="flex justify-end gap-2">
              <Button variant="ghost" size="sm" onClick={() => { setClientPrompt(null); setClientId(''); setClientSecret('') }}>Cancel</Button>
              <Button size="sm" disabled={busy !== null || !clientId.trim()} onClick={() => { void run(`client:${clientPrompt.server}`, () => connect(clientPrompt.server, { clientId: clientId.trim(), clientSecret: clientSecret.trim() })) }}>
                Sign in
              </Button>
            </div>
          </div>
        )}
        {catalog.length > 0 && (
          <div className="flex flex-col gap-2 border-t border-border pt-3 sm:flex-row">
            <select
              value={catalogPick}
              onChange={event => setCatalogPick(event.target.value)}
              className="h-9 min-w-0 flex-1 rounded-md border border-border bg-background px-2 text-sm"
              aria-label="Add from the catalog"
            >
              <option value="">Add a known server (Gmail, Drive, GitHub…)</option>
              {catalog.map(entry => (
                <option key={entry.catalog} value={entry.catalog} disabled={servers.some(server => server.name === entry.name)}>
                  {entry.catalog}{entry.description ? ` — ${entry.description}` : ''}
                </option>
              ))}
            </select>
            <Button disabled={busy !== null || !catalogPick} onClick={() => { void addFromCatalog() }}>
              {busy === 'catalog' ? <Loader2 className="mr-1 h-4 w-4 animate-spin" /> : null}Add as mine
            </Button>
          </div>
        )}
        <div className="grid gap-2 border-t border-border pt-3 sm:grid-cols-2">
          <Input value={name} onChange={event => setName(event.target.value)} placeholder="name (e.g. linear)" aria-label="Server name" />
          <Input value={url} onChange={event => setUrl(event.target.value)} placeholder="https://… MCP URL" aria-label="Server URL" />
          <Input value={header} onChange={event => setHeader(event.target.value)} placeholder="API key header (optional, e.g. Authorization)" aria-label="API key header" />
          <select
            value={headerSecret}
            onChange={event => setHeaderSecret(event.target.value)}
            className="h-9 rounded-md border border-border bg-background px-2 text-sm"
            aria-label="Secret for the header"
          >
            <option value="">{secrets.length ? 'Secret for the header…' : 'Add a secret below first'}</option>
            {secrets.map(secret => <option key={secret} value={secret}>{secret}</option>)}
          </select>
          <div className="sm:col-span-2 flex justify-end">
            <Button disabled={busy !== null || !name.trim() || !url.trim() || (!!header.trim() && !headerSecret)} onClick={() => { void add() }}>
              {busy === 'add' ? <Loader2 className="mr-1 h-4 w-4 animate-spin" /> : null}Add server
            </Button>
          </div>
        </div>
      </SettingsCard>

      <SettingsCard
        icon={<KeyRound className="h-4 w-4 text-primary" />}
        title="Your secrets"
        description="Values only your own servers can use, for their API keys. Nobody can read them back, including you."
      >
        {secrets.length > 0 && (
          <div className="flex flex-wrap gap-2">
            {secrets.map(secret => (
              <span key={secret} className="inline-flex items-center gap-1 rounded-md border border-border px-2 py-1 font-mono text-xs">
                {secret}
                <button type="button" className="text-destructive" aria-label={`Delete ${secret}`} disabled={busy !== null} onClick={() => { void run(`del:${secret}`, () => personalMcpApi.deleteSecret(secret)) }}>×</button>
              </span>
            ))}
          </div>
        )}
        <div className="flex flex-col gap-2 sm:flex-row">
          <Input value={secretName} onChange={event => setSecretName(event.target.value.toUpperCase())} placeholder="NAME_LIKE_THIS" aria-label="Secret name" />
          <Input type="password" value={secretValue} onChange={event => setSecretValue(event.target.value)} placeholder="value" aria-label="Secret value" />
          <Button disabled={busy !== null || !secretName.trim() || !secretValue} onClick={() => { void saveSecret() }}>Save</Button>
        </div>
      </SettingsCard>
    </div>
  )
}

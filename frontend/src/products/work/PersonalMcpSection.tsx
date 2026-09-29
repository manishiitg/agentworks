import { useCallback, useEffect, useMemo, useState } from 'react'
import { Loader2, MessageCircle, Plus, Search, Trash2 } from 'lucide-react'
import ConnectionIcon from '../../components/connectors/ConnectionIcon'
import { brandSlugFor } from '../../components/connectors/brandSlug'
import { DEVELOPER_FIRST_GROUP_ORDER, descriptionFor, groupFor } from '../../components/connectors/catalog'
import { ConnectorGroupSection } from '../../components/connectors/ConnectorGroupSection'
import { Button } from '../../components/ui/Button'
import { Input } from '../../components/ui/Input'
import { Checkbox } from '../../components/ui/checkbox'
import { personalMcpApi, type PersonalMcpCatalogServer, type PersonalMcpServer } from '../../api/personalMcp'

const errorText = (cause: unknown, fallback: string) => {
  const response = (cause as { response?: { data?: { error?: string } } })?.response
  return response?.data?.error || (cause instanceof Error ? cause.message : fallback)
}

/** One card: a catalog server (added or not) or a custom server of the person's. */
interface Card {
  key: string
  title: string
  description: string
  catalog?: string
  server?: PersonalMcpServer
}

/**
 * MCP servers in a Code (docs/design/code_private_mcp.md): the full catalog as
 * in a Crew, but every connection is the signed-in person's own. Connect adds
 * the server to their account and signs them in; nobody else in this Code sees
 * or uses it. Switch it on per Code. Header keys come from their own secrets
 * (Setup → Secrets).
 */
export function PersonalMcpSection({ projectId, onAsk }: { projectId: string; onAsk?: (message: string) => Promise<void> }) {
  const [servers, setServers] = useState<PersonalMcpServer[]>([])
  const [secrets, setSecrets] = useState<string[]>([])
  const [catalog, setCatalog] = useState<PersonalMcpCatalogServer[]>([])
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [query, setQuery] = useState('')
  // A sign-in that needs the person's own OAuth app (Google, GitHub).
  const [clientPrompt, setClientPrompt] = useState<{ server: string; message?: string; redirectUri?: string } | null>(null)
  const [clientId, setClientId] = useState('')
  const [clientSecret, setClientSecret] = useState('')
  const [showCustom, setShowCustom] = useState(false)
  const [name, setName] = useState('')
  const [url, setUrl] = useState('')
  const [header, setHeader] = useState('')
  const [headerSecret, setHeaderSecret] = useState('')

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

  const signIn = async (server: string, client?: { clientId: string; clientSecret?: string }) => {
    const result = await personalMcpApi.connect(server, client)
    if (result.auth_url) {
      setClientPrompt(null); setClientId(''); setClientSecret('')
      window.open(result.auth_url, '_blank', 'noopener')
    } else if (result.status === 'needs_client_id') {
      if (clientPrompt?.server !== server) { setClientId(''); setClientSecret('') }
      setClientPrompt({ server, message: result.message, redirectUri: result.redirect_uri })
    } else if (result.message) setError(result.message)
  }

  // Connect: add the catalog server as the person's own, switch it on in
  // this Code, then sign in when it needs one.
  const connect = (card: Card) => run(`connect:${card.key}`, async () => {
    const server = card.server
    if (!server && card.catalog) {
      const entry = catalog.find(item => item.catalog === card.catalog)
      const added = await personalMcpApi.add({ name: entry?.name ?? card.catalog.toLowerCase(), catalog: card.catalog })
      await personalMcpApi.setEnabled(added.name, projectId, true)
      if (added.oauth) await signIn(added.name)
      return
    }
    if (server?.oauth) await signIn(server.name)
  })

  const addCustom = () => run('custom', async () => {
    const headers = header.trim() && headerSecret ? { [header.trim()]: { secret: headerSecret, format: header.trim().toLowerCase() === 'authorization' ? 'Bearer {}' : '{}' } } : undefined
    const added = await personalMcpApi.add({ name: name.trim(), url: url.trim(), headers })
    await personalMcpApi.setEnabled(added.name, projectId, true)
    setName(''); setUrl(''); setHeader(''); setHeaderSecret(''); setShowCustom(false)
    if (added.oauth) await signIn(added.name)
  })

  const cards = useMemo<Card[]>(() => {
    const byCatalog = new Map(servers.filter(server => server.catalog).map(server => [server.catalog as string, server]))
    const fromCatalog = catalog.map(entry => ({
      key: `catalog:${entry.catalog}`,
      title: entry.catalog,
      description: descriptionFor(entry.catalog) !== 'Custom MCP server' ? descriptionFor(entry.catalog) : entry.description || 'MCP server',
      catalog: entry.catalog,
      server: byCatalog.get(entry.catalog),
    }))
    const custom = servers.filter(server => !server.catalog || !catalog.some(entry => entry.catalog === server.catalog)).map(server => ({
      key: `mine:${server.name}`,
      title: server.name,
      description: server.url,
      server,
    }))
    const q = query.trim().toLowerCase()
    return [...custom, ...fromCatalog].filter(card => !q || card.title.toLowerCase().includes(q) || card.description.toLowerCase().includes(q))
  }, [catalog, servers, query])

  const isConnected = (server?: PersonalMcpServer) => !!server && (!server.oauth || server.connected)
  const connected = cards.filter(card => card.server)
  const others = cards.filter(card => !card.server)

  const renderCard = (card: Card) => {
    const server = card.server
    const ready = isConnected(server)
    const dot = !server ? 'bg-gray-300 dark:bg-gray-600' : ready ? 'bg-green-500' : 'bg-amber-500'
    const dotTitle = !server ? 'Not connected' : ready ? 'Connected (your account)' : 'Needs sign-in'
    return (
      <div key={card.key} className="flex flex-col rounded-xl border border-gray-200 bg-white transition-colors hover:border-gray-300 dark:border-gray-800 dark:bg-gray-900/60 dark:hover:border-gray-700">
        <div className="flex items-start gap-3 p-3">
          <ConnectionIcon icon={brandSlugFor(card.title)} name={card.title} size="lg" />
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-1.5">
              <span className="truncate text-sm font-semibold text-gray-900 dark:text-gray-100">{card.title}</span>
              <span className={`h-2 w-2 shrink-0 rounded-full ${dot}`} title={dotTitle} aria-label={dotTitle} />
            </div>
            <p className="mt-0.5 line-clamp-2 text-xs leading-relaxed text-gray-500 dark:text-gray-400">{card.description}</p>
            {server && (
              <label className="mt-2 flex items-center gap-2 text-xs text-foreground" title="Use in this Code">
                <Checkbox
                  checked={server.enabled}
                  disabled={busy !== null}
                  onCheckedChange={() => { void run(`on:${server.name}`, () => personalMcpApi.setEnabled(server.name, projectId, !server.enabled)) }}
                  aria-label={`Use ${card.title} in this Code`}
                />
                Use in this Code
              </label>
            )}
          </div>
          <div className="flex shrink-0 flex-col items-end gap-1 self-center">
            {onAsk && (
              <Button variant="ghost" size="icon" className="h-7 w-7 text-muted-foreground hover:text-primary" title={`Ask the agent about ${card.title}`} aria-label={`Ask the agent about ${card.title}`}
                onClick={() => { void onAsk(server
                  ? `Help me with my own ${card.title} MCP connection in this Code: check whether it is signed in and switched on here, then ask what I want to do with it.`
                  : `Connect ${card.catalog ?? card.title} for me as my own MCP server in this Code, then give me the sign-in link.`) }}>
                <MessageCircle className="h-3.5 w-3.5" />
              </Button>
            )}
            {(!server || (server.oauth && !server.connected)) && (
              <Button size="sm" variant={server ? 'outline' : 'default'} disabled={busy !== null} onClick={() => { void connect(card) }}>
                {busy === `connect:${card.key}` ? <Loader2 className="mr-1 h-3.5 w-3.5 animate-spin" /> : null}{server ? 'Sign in' : 'Connect'}
              </Button>
            )}
            {server && (
              <Button variant="ghost" size="icon" className="h-7 w-7 text-destructive" title={`Disconnect ${card.title}`} aria-label={`Disconnect ${card.title}`} disabled={busy !== null}
                onClick={() => { void run(`rm:${server.name}`, () => personalMcpApi.remove(server.name)) }}>
                <Trash2 className="h-3.5 w-3.5" />
              </Button>
            )}
          </div>
        </div>
      </div>
    )
  }

  return (
    <div className="flex flex-col" data-testid="personal-mcp-section">
      <div className="relative">
        <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" />
        <input
          type="text"
          value={query}
          onChange={event => setQuery(event.target.value)}
          placeholder="Search connectors"
          aria-label="Search connectors"
          className="w-full rounded-lg border border-gray-300 bg-white py-2.5 pl-10 pr-3 text-sm text-gray-900 placeholder-gray-400 transition-colors focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-100"
        />
      </div>
      <div className="mt-3 flex flex-wrap items-center gap-3 rounded-lg border border-border bg-muted/40 p-3">
        <p className="min-w-0 flex-1 basis-48 text-xs leading-5 text-muted-foreground">
          Connections here are yours alone: you sign in with your own account, and they run only in your chats, in the Codes where you switch them on. Nobody else in this Code sees or uses them. Changes apply from your next message.
        </p>
        {onAsk && (
          <Button size="sm" onClick={() => { void onAsk(query.trim()
            ? `Connect ${JSON.stringify(query.trim())} for me as my own MCP server in this Code. Check the catalog first; if it is not there, find its official remote MCP URL, then give me the sign-in link.`
            : 'Help me connect one of my own MCP servers in this Code. Ask which app or service I want, then connect it and give me the sign-in link.') }}>
            <MessageCircle className="mr-1 h-3.5 w-3.5" />Ask the agent to connect
          </Button>
        )}
      </div>
      {error && <p role="alert" className="mt-3 text-xs text-destructive">{error}</p>}

      {clientPrompt && (
        <div className="mt-3 space-y-2 rounded-md border border-amber-500/40 bg-amber-500/5 p-3 text-sm" data-testid="personal-mcp-client-prompt">
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
            <Button size="sm" disabled={busy !== null || !clientId.trim()} onClick={() => { void run(`client:${clientPrompt.server}`, () => signIn(clientPrompt.server, { clientId: clientId.trim(), clientSecret: clientSecret.trim() })) }}>
              Sign in
            </Button>
          </div>
        </div>
      )}

      <div className="pt-5">
        {loading && (
          <div className="flex items-center gap-2 py-8 text-sm text-gray-500 dark:text-gray-400">
            <Loader2 className="h-4 w-4 animate-spin" /><span>Loading connectors...</span>
          </div>
        )}
        {!loading && connected.length > 0 && (
          <section className="mb-5">
            <h4 className="mb-3 border-b border-gray-200 pb-2 text-sm font-semibold text-gray-900 dark:border-gray-800 dark:text-gray-100">Your connections</h4>
            <div className="grid grid-cols-1 gap-2 md:grid-cols-2">{connected.map(renderCard)}</div>
          </section>
        )}
        {!loading && others.length > 0 && (
          <section>
            <h4 className="mb-3 border-b border-gray-200 pb-2 text-sm font-semibold text-gray-900 dark:border-gray-800 dark:text-gray-100">Others</h4>
            {DEVELOPER_FIRST_GROUP_ORDER.map(({ id, label }) => {
              const entries = others.filter(card => groupFor(card.title) === id)
              if (entries.length === 0) return null
              return <ConnectorGroupSection key={id} label={label} entries={entries} render={renderCard} expandAll={!!query.trim()} />
            })}
          </section>
        )}
        {!loading && cards.length === 0 && (
          <p className="py-8 text-center text-sm text-gray-500 dark:text-gray-400">No connectors match &quot;{query}&quot;.</p>
        )}
      </div>

      <div className="mt-4 border-t border-border pt-3">
        {!showCustom ? (
          <Button variant="outline" size="sm" onClick={() => setShowCustom(true)}><Plus className="mr-1 h-3.5 w-3.5" />Add a server that is not listed</Button>
        ) : (
          <div className="grid gap-2 sm:grid-cols-2">
            <Input value={name} onChange={event => setName(event.target.value)} placeholder="name (e.g. linear)" aria-label="Server name" />
            <Input value={url} onChange={event => setUrl(event.target.value)} placeholder="https://… MCP URL" aria-label="Server URL" />
            <Input value={header} onChange={event => setHeader(event.target.value)} placeholder="API key header (optional, e.g. Authorization)" aria-label="API key header" />
            <select
              value={headerSecret}
              onChange={event => setHeaderSecret(event.target.value)}
              className="h-9 rounded-md border border-border bg-background px-2 text-sm"
              aria-label="Secret for the header"
            >
              <option value="">{secrets.length ? 'Secret for the header…' : 'Add a secret in Setup → Secrets first'}</option>
              {secrets.map(secret => <option key={secret} value={secret}>{secret}</option>)}
            </select>
            <div className="flex justify-end gap-2 sm:col-span-2">
              <Button variant="ghost" size="sm" onClick={() => setShowCustom(false)}>Cancel</Button>
              <Button size="sm" disabled={busy !== null || !name.trim() || !url.trim() || (!!header.trim() && !headerSecret)} onClick={() => { void addCustom() }}>
                {busy === 'custom' ? <Loader2 className="mr-1 h-4 w-4 animate-spin" /> : null}Add server
              </Button>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

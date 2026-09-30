import { useCallback, useEffect, useMemo, useState } from 'react'
import { Check, Loader2, MessageCircle, Plus, Search, Trash2, UserRound } from 'lucide-react'
import ConnectionIcon from '../../components/connectors/ConnectionIcon'
import { brandSlugFor } from '../../components/connectors/brandSlug'
import { Button } from '../../components/ui/Button'
import { Input } from '../../components/ui/Input'
import { parseOAuthClientJson } from './oauthClientJson'
import { mcpCatalogApi, type McpCatalogServer } from '../../api/mcpCatalog'
import { placeMcpApi, type PlaceMcpCustomServer, type PlaceMcpServer } from '../../api/placeMcp'
import { secretsApi } from '../../api/secrets'
import { DEVELOPER_FIRST_GROUP_ORDER, GROUP_ORDER, descriptionFor, groupFor } from '../../components/connectors/catalog'
import { groupServiceLabel, providerGroupLabel, providerGroups } from './mcpGroups'

const errorText = (cause: unknown, fallback: string) => {
  const response = (cause as { response?: { data?: { error?: string } } })?.response
  return response?.data?.error || (cause instanceof Error ? cause.message : fallback)
}

/**
 * MCP connections with a person's own login in a Code, Crew or workflow
 * (docs/design/personal_mcp_attach.md): one screen for all three. Someone who
 * can add here connects, say, their Gmail; every chat and run there then uses
 * it like any other MCP server. It belongs to this place only.
 */
export function PlaceMcpSection({ workspacePath, placeNoun, canEdit, onAsk }: {
  workspacePath: string
  /** "Code", "Crew" or "workflow", for the wording. */
  placeNoun: string
  /** The viewer can add connections here. */
  canEdit: boolean
  /** Lets the person ask the agent to connect something (chat products). */
  onAsk?: (message: string) => Promise<void>
}) {
  const [servers, setServers] = useState<PlaceMcpServer[]>([])
  const [catalog, setCatalog] = useState<McpCatalogServer[]>([])
  // idle -> loading -> ready | failed. An empty list must say which: a load that failed or came
  // back empty used to show "Loading…" forever, which looks like there is nothing to connect.
  const [catalogState, setCatalogState] = useState<'idle' | 'loading' | 'ready' | 'failed'>('idle')
  const [secrets, setSecrets] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [message, setMessage] = useState<string | null>(null)
  const [query, setQuery] = useState('')
  // A sign-in group (Google Workspace): pick services, add them, sign in once.
  const [groupPicks, setGroupPicks] = useState<Record<string, string[]>>({})
  // A sign-in that needs the person's own OAuth app (Google, GitHub).
  const [clientPrompt, setClientPrompt] = useState<{ server: string; redirectUri?: string } | null>(null)
  const [clientId, setClientId] = useState('')
  const [clientSecret, setClientSecret] = useState('')
  // A server that is not in the catalog.
  const [showCustom, setShowCustom] = useState(false)
  const [customName, setCustomName] = useState('')
  const [customUrl, setCustomUrl] = useState('')
  const [customHeader, setCustomHeader] = useState('')
  const [customSecret, setCustomSecret] = useState('')
  const groups = useMemo(() => providerGroups(catalog), [catalog])

  const refresh = useCallback(async () => {
    try {
      setServers(await placeMcpApi.list(workspacePath))
      setError(null)
    } catch (cause) {
      setError(errorText(cause, 'Could not load connections.'))
    } finally {
      setLoading(false)
    }
  }, [workspacePath])

  useEffect(() => { void refresh() }, [refresh])
  const loadCatalog = useCallback(() => {
    setCatalogState('loading')
    void mcpCatalogApi.catalog()
      .then(list => { setCatalog(list.filter(entry => entry.sign_in)); setCatalogState('ready') })
      .catch(() => { setCatalog([]); setCatalogState('failed') })
  }, [])
  // The connectors people can add are listed on the page itself (no button to reveal them).
  useEffect(() => {
    if (canEdit && catalogState === 'idle') loadCatalog()
  }, [canEdit, catalogState, loadCatalog])
  // API-key servers name a secret of this project (Setup > Secrets).
  useEffect(() => {
    if (!showCustom) return
    void secretsApi.listWorkflowSecrets(workspacePath).then(list => setSecrets(list.map(item => item.name))).catch(() => setSecrets([]))
  }, [showCustom, workspacePath])
  // A sign-in finishes in another tab; pick up its result when the person
  // comes back.
  useEffect(() => {
    const onFocus = () => { void refresh() }
    window.addEventListener('focus', onFocus)
    return () => window.removeEventListener('focus', onFocus)
  }, [refresh])

  const mineByCatalog = useMemo(() => new Set(servers.filter(s => s.mine).map(s => s.catalog || s.name)), [servers])

  const signIn = async (name: string, client?: { clientId: string; clientSecret?: string }) => {
    const result = await placeMcpApi.connect(workspacePath, name, client)
    if (result.auth_url) {
      setClientPrompt(null); setClientId(''); setClientSecret('')
      window.open(result.auth_url, '_blank', 'noopener')
    } else if (result.status === 'needs_client_id') {
      if (clientPrompt?.server !== name) { setClientId(''); setClientSecret('') }
      setClientPrompt({ server: name, redirectUri: result.redirect_uri })
      setMessage(null)
    } else if (result.message) {
      setMessage(result.message)
    }
  }

  const run = async (key: string, action: () => Promise<unknown>, failure: string) => {
    setBusy(key)
    setError(null)
    setMessage(null)
    try {
      await action()
    } catch (cause) {
      setError(errorText(cause, failure))
    } finally {
      setBusy(null)
    }
  }

  const add = (entry: McpCatalogServer) => run(entry.catalog, async () => {
    const saved = await placeMcpApi.add(workspacePath, entry.catalog)
    await refresh()
    if (saved.oauth) await signIn(saved.name)
  }, 'Could not add the connection.')

  const addGroup = (group: string) => run(`group:${group}`, async () => {
    const picks = groupPicks[group] ?? []
    let first: string | null = null
    for (const entry of (groups.get(group) ?? []).filter(item => picks.includes(item.catalog))) {
      const saved = await placeMcpApi.add(workspacePath, entry.catalog)
      if (saved.oauth) first = first ?? saved.name
    }
    setGroupPicks(current => ({ ...current, [group]: [] }))
    await refresh()
    // One sign-in covers every service added here.
    if (first) await signIn(first)
  }, 'Could not add the connections.')

  const addCustom = (server: PlaceMcpCustomServer) => run('custom', async () => {
    const saved = await placeMcpApi.add(workspacePath, server)
    setCustomName(''); setCustomUrl(''); setCustomHeader(''); setCustomSecret(''); setShowCustom(false)
    await refresh()
    if (saved.oauth) await signIn(saved.name)
  }, 'Could not add the server.')

  const remove = (server: PlaceMcpServer) => run(`${server.owner}:${server.name}`, async () => {
    await placeMcpApi.remove(workspacePath, server.name, server.owner)
    await refresh()
  }, 'Could not remove the connection.')

  const togglePick = (group: string, catalogName: string) => setGroupPicks(current => {
    const list = current[group] ?? []
    return { ...current, [group]: list.includes(catalogName) ? list.filter(item => item !== catalogName) : [...list, catalogName] }
  })

  const customServer = (): PlaceMcpCustomServer => {
    const header = customHeader.trim()
    return {
      name: customName.trim(),
      url: customUrl.trim(),
      headers: header && customSecret ? { [header]: { secret: customSecret, format: header.toLowerCase() === 'authorization' ? 'Bearer {}' : '{}' } } : undefined,
    }
  }

  const q = query.trim().toLowerCase()
  const matches = (text: string) => !q || text.toLowerCase().includes(q)

  if (loading) return null
  if (servers.length === 0 && !canEdit) return null

  return (
    <div data-testid="place-mcp-section" className="flex flex-col gap-2">
      <div className="flex items-center justify-between gap-2">
        <div className="text-sm font-medium text-foreground">MCP connections</div>
        <div className="flex items-center gap-2">
          {canEdit && onAsk && (
            <Button size="sm" variant="ghost" onClick={() => {
              void onAsk('Help me connect one of my own MCP servers in this ' + placeNoun + '. Ask which app or service I want, then connect it and give me the sign-in link.')
            }}>
              <MessageCircle className="mr-1 h-3.5 w-3.5" />Ask the agent
            </Button>
          )}
        </div>
      </div>
      <p className="text-xs leading-5 text-muted-foreground">
        Each connection uses the login of the person who added it, and everyone who uses this {placeNoun} uses it as that person. It stays in this {placeNoun} only. Changes apply from the next message.
      </p>
      {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
      {message && <p className="text-xs text-amber-700 dark:text-amber-300">{message}</p>}

      {servers.length > 0 && <div className="mt-1 text-xs font-medium uppercase tracking-wide text-muted-foreground">Connected</div>}
      {servers.map(server => (
        <div key={`${server.owner}:${server.name}`} className={`flex items-center gap-3 rounded-md border border-border px-3 py-2 text-sm ${server.active ? '' : 'opacity-60'}`}>
          <ConnectionIcon icon={brandSlugFor(server.catalog || server.name)} name={server.catalog || server.name} size="sm" />
          <div className="min-w-0 flex-1">
            <div className="truncate font-medium">{server.catalog || server.name}</div>
            <div className="flex items-center gap-1 text-[11px] text-muted-foreground">
              <UserRound className="h-3 w-3" />
              {server.mine ? 'your login' : `${server.owner_name}'s login`}
              {!server.active && ' · paused: they can no longer edit here'}
              {server.active && !server.connected && ' · not signed in yet'}
            </div>
          </div>
          {server.active && server.connected && <span className="text-xs text-green-600 dark:text-green-400">Connected</span>}
          {server.mine && !server.connected && (
            <Button size="sm" disabled={busy !== null} onClick={() => { void run(server.name, () => signIn(server.name), 'Could not start sign-in.') }}>
              {busy === server.name ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : 'Sign in'}
            </Button>
          )}
          {(server.mine || canEdit) && (
            <Button size="icon" variant="ghost" className="h-7 w-7 text-muted-foreground hover:text-destructive" title="Remove from here (their login is deleted)" aria-label={`Remove ${server.catalog || server.name}`} disabled={busy !== null} onClick={() => { void remove(server) }}>
              <Trash2 className="h-3.5 w-3.5" />
            </Button>
          )}
        </div>
      ))}

      {clientPrompt && (
        <div className="space-y-2 rounded-md border border-amber-500/40 bg-amber-500/5 p-3 text-sm" data-testid="place-mcp-client-prompt">
          <p className="font-medium text-foreground">Sign in to {clientPrompt.server} with your own OAuth app</p>
          <p className="text-xs text-muted-foreground">
            This provider has no automatic app registration. Create an OAuth app (in Google Cloud or GitHub developer settings), then enter its client ID and secret. They are kept encrypted and used only for this sign-in.
          </p>
          {clientPrompt.redirectUri && (
            <p className="text-xs text-muted-foreground">Callback URL to register: <code className="break-all text-foreground">{clientPrompt.redirectUri}</code></p>
          )}
          <label className="inline-flex cursor-pointer items-center gap-1 text-xs font-medium text-primary hover:underline">
            Upload client_secret.json
            <input type="file" accept="application/json,.json" className="hidden" aria-label="Upload the client JSON" onChange={event => {
              const file = event.target.files?.[0]
              event.target.value = ''
              if (file) void file.text().then(text => {
                const parsed = parseOAuthClientJson(text)
                if (parsed) { setClientId(parsed.clientId); setClientSecret(parsed.clientSecret) } else setError('That file is not an OAuth client file (client_secret_….json).')
              })
            }} />
          </label>
          <div className="grid gap-2 sm:grid-cols-2">
            <Input value={clientId} onChange={event => setClientId(event.target.value)} placeholder="Client ID" aria-label="OAuth client ID" />
            <Input type="password" autoComplete="off" value={clientSecret} onChange={event => setClientSecret(event.target.value)} placeholder="Client secret" aria-label="OAuth client secret" />
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="ghost" size="sm" onClick={() => { setClientPrompt(null); setClientId(''); setClientSecret('') }}>Cancel</Button>
            <Button size="sm" disabled={busy !== null || !clientId.trim()} onClick={() => { void run(`client:${clientPrompt.server}`, () => signIn(clientPrompt.server, { clientId: clientId.trim(), clientSecret: clientSecret.trim() }), 'Could not start sign-in.') }}>
              Sign in
            </Button>
          </div>
        </div>
      )}

      {canEdit && (
        <div className="mt-2 flex flex-col gap-3">
          <div className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Available</div>
          <p data-testid="mcp-google-pointer" className="text-xs leading-5 text-muted-foreground">
            Google apps (Gmail, Drive, Calendar, Docs, Sheets, Slides) are connected in the Google apps tab, not here. For GitHub, add a personal access token as a secret named GITHUB_TOKEN in Setup → Secrets; the agent uses it with git and the GitHub API.
          </p>
          <div className="relative">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
            <Input value={query} onChange={event => setQuery(event.target.value)} placeholder="Search connectors" aria-label="Search connectors" className="pl-9" />
          </div>
          {catalogState === 'loading' && <span className="text-xs text-muted-foreground">Loading…</span>}
          {catalogState === 'failed' && (
            <div role="alert" className="flex items-center gap-2 text-xs text-destructive">
              Could not load the list of connectors.
              <Button variant="outline" size="sm" onClick={loadCatalog}>Retry</Button>
            </div>
          )}
          {catalogState === 'ready' && catalog.length === 0 && (
            <span className="text-xs text-muted-foreground">No connectors with sign-in are set up on this server. You can still add a server that is not listed below.</span>
          )}

          {[...groups.keys()].filter(group => matches(providerGroupLabel(group)) || (groups.get(group) ?? []).some(entry => matches(entry.catalog))).map(group => {
            const label = providerGroupLabel(group)
            const provider = label.split(' ')[0]
            const available = (groups.get(group) ?? []).filter(entry => !mineByCatalog.has(entry.catalog))
            const picks = groupPicks[group] ?? []
            if (available.length === 0) return null
            return (
              <section key={group} data-testid={`mcp-group-${group}`} className="rounded-md border border-border p-3">
                <div className="flex items-center gap-2">
                  <ConnectionIcon icon={brandSlugFor(group) ?? brandSlugFor(label)} name={label} size="sm" />
                  <div className="min-w-0 flex-1">
                    <div className="text-sm font-medium">{label}</div>
                    <div className="text-xs text-muted-foreground">One {provider} sign-in covers every service you pick.</div>
                  </div>
                </div>
                <div className="mt-2 flex flex-wrap gap-1.5">
                  {available.map(entry => {
                    const service = groupServiceLabel(entry.catalog, group)
                    const picked = picks.includes(entry.catalog)
                    return (
                      <button key={entry.catalog} type="button" aria-pressed={picked} aria-label={`Add ${service}`} disabled={busy !== null}
                        onClick={() => togglePick(group, entry.catalog)}
                        className={`inline-flex items-center gap-1.5 rounded-full border py-1 pl-1.5 pr-2.5 text-xs transition-colors disabled:opacity-50 ${picked
                          ? 'border-primary bg-primary/10 text-primary'
                          : 'border-border text-muted-foreground hover:border-foreground/30 hover:text-foreground'}`}>
                        <ConnectionIcon icon={brandSlugFor(entry.catalog) ?? brandSlugFor(group)} name={service} size="xs" />
                        {service}
                        {picked ? <Check className="h-3 w-3" /> : <Plus className="h-3 w-3 opacity-60" />}
                      </button>
                    )
                  })}
                </div>
                {picks.length > 0 && (
                  <Button size="sm" className="mt-2.5" disabled={busy !== null} onClick={() => { void addGroup(group) }}>
                    {busy === `group:${group}` ? <Loader2 className="mr-1 h-3.5 w-3.5 animate-spin" /> : null}
                    Connect {picks.length} {picks.length === 1 ? 'service' : 'services'}
                  </Button>
                )}
              </section>
            )
          })}

          {/* The same shelves the old platform browser had (Payments, Customers, ...),
              so a long list of servers stays scannable. */}
          {(placeNoun === 'Code' ? DEVELOPER_FIRST_GROUP_ORDER : GROUP_ORDER).map(shelf => {
            const entries = catalog.filter(entry => !mineByCatalog.has(entry.catalog) && !(entry.group && groups.has(entry.group)) && matches(entry.catalog) && groupFor(entry.catalog) === shelf.id)
            if (entries.length === 0) return null
            return (
              <section key={shelf.id} data-testid={`mcp-shelf-${shelf.id}`}>
                <div className="mb-1 text-xs font-medium uppercase tracking-wide text-muted-foreground">{shelf.label}</div>
                <div className="grid grid-cols-1 gap-1.5 sm:grid-cols-2">
                  {entries.map(entry => (
                    <button
                      key={entry.catalog}
                      type="button"
                      disabled={busy !== null}
                      onClick={() => { void add(entry) }}
                      className="flex items-center gap-2 rounded px-2 py-1.5 text-left text-sm hover:bg-muted"
                    >
                      <ConnectionIcon icon={brandSlugFor(entry.catalog)} name={entry.catalog} size="xs" />
                      <span className="min-w-0">
                        <span className="block truncate">{entry.catalog}</span>
                        {descriptionFor(entry.catalog) && <span className="block truncate text-xs text-muted-foreground">{descriptionFor(entry.catalog)}</span>}
                      </span>
                      {busy === entry.catalog
                        ? <Loader2 className="ml-auto h-3.5 w-3.5 shrink-0 animate-spin" />
                        : <span className="ml-auto inline-flex shrink-0 items-center gap-1 text-xs text-muted-foreground"><Plus className="h-3 w-3" />Connect</span>}
                    </button>
                  ))}
                </div>
              </section>
            )
          })}

          <div className="border-t border-border pt-3">
            {!showCustom ? (
              <Button variant="outline" size="sm" onClick={() => setShowCustom(true)}><Plus className="mr-1 h-3.5 w-3.5" />Add a server that is not listed</Button>
            ) : (
              <div className="grid gap-2 sm:grid-cols-2">
                <Input value={customName} onChange={event => setCustomName(event.target.value)} placeholder="name (e.g. linear)" aria-label="Server name" />
                <Input value={customUrl} onChange={event => setCustomUrl(event.target.value)} placeholder="https://… MCP URL" aria-label="Server URL" />
                <Input value={customHeader} onChange={event => setCustomHeader(event.target.value)} placeholder="API key header (optional, e.g. Authorization)" aria-label="API key header" />
                <select
                  value={customSecret}
                  onChange={event => setCustomSecret(event.target.value)}
                  className="h-9 rounded-md border border-border bg-background px-2 text-sm"
                  aria-label="Secret for the header"
                >
                  <option value="">{secrets.length ? 'Secret for the header…' : `Add a secret in this ${placeNoun}'s Secrets first`}</option>
                  {secrets.map(secret => <option key={secret} value={secret}>{secret}</option>)}
                </select>
                <div className="flex justify-end gap-2 sm:col-span-2">
                  <Button variant="ghost" size="sm" onClick={() => setShowCustom(false)}>Cancel</Button>
                  <Button size="sm" disabled={busy !== null || !customName.trim() || !customUrl.trim() || (!!customHeader.trim() && !customSecret)} onClick={() => { void addCustom(customServer()) }}>
                    {busy === 'custom' ? <Loader2 className="mr-1 h-4 w-4 animate-spin" /> : null}Add server
                  </Button>
                </div>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

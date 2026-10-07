import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { mcpCatalogApi, type McpCatalogServer } from '../../api/mcpCatalog'
import { placeMcpApi, type PlaceMcpCustomServer, type PlaceMcpServer } from '../../api/placeMcp'
import api from '../../services/api'
import { descriptionFor, groupFor } from '../connectors/catalog'
import { providerGroupLabel, providerGroups } from '../../products/work/mcpGroups'
import { McpOAuthClientForm, McpCustomServerForm, McpNamedConnectionForm } from './McpConnectionForms'
import type { McpConnectionRow, McpCatalogRow } from './McpConnectionsPanel'

const errorText = (cause: unknown, fallback: string) => (cause as { response?: { data?: { error?: string } } })?.response?.data?.error || (cause instanceof Error ? cause.message : fallback)
export function usePlaceMcpConnections({ workspacePath, placeNoun, canEdit, onAsk, chatSessionId }: {
  chatSessionId?: string
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
  const [clientPrompt, setClientPrompt] = useState<{ server: string; redirectUri?: string } | null>(null)
  const [showCustom, setShowCustom] = useState(false)
  const [namingProvider, setNamingProvider] = useState<string | null>(null)
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
      .then(list => { setCatalog(list); setCatalogState('ready') })
      .catch(() => { setCatalog([]); setCatalogState('failed') })
  }, [])
  // The connectors people can add are listed on the page itself (no button to reveal them).
  useEffect(() => {
    if (canEdit && catalogState === 'idle') loadCatalog()
  }, [canEdit, catalogState, loadCatalog])
  // API-key servers reference a secret in the caller's private store.
  useEffect(() => {
    if (!showCustom) return
    void api.get('/api/me/secrets').then(response => response.data.secrets || []).then(list => setSecrets(list.map((item: { name: string }) => item.name))).catch(() => setSecrets([]))
  }, [showCustom, workspacePath])
  // A sign-in finishes in another tab; pick up its result when the person
  // comes back.
  useEffect(() => {
    const onFocus = () => { void refresh() }
    window.addEventListener('focus', onFocus)
    return () => window.removeEventListener('focus', onFocus)
  }, [refresh])

  // A connection turning "connected" (sign-in finished in the other tab, or an API-key server
  // that connected at once) is also told to the chat, through the same function as Connect, so
  // the agent knows it can use it now (the API bridge resolves connections on every call). The first load only records what
  // was already connected, so opening the screen never sends anything.
  const connectedBefore = useRef<{ path: string; names: Set<string> } | null>(null)
  useEffect(() => {
    if (loading) return
    const now = new Set(servers.filter(item => item.mine && item.active && item.connected).map(item => item.name))
    const before = connectedBefore.current?.path === workspacePath ? connectedBefore.current.names : null
    connectedBefore.current = { path: workspacePath, names: now }
    if (!before || !onAsk) return
    // OAuth outcomes are delivered by the backend to this chat, even while
    // this panel is unmounted. Keep the focus fallback for other connections.
    const oauthNames = new Set(servers.filter(item => item.sign_in || catalog.some(provider => provider.catalog === item.catalog && provider.sign_in)).map(item => item.name))
    const added = [...now].filter(name => !before.has(name) && !(chatSessionId && oauthNames.has(name)))
    if (added.length > 0) {
      void onAsk(`${added.map(name => { const server = servers.find(item => item.name === name); return server?.label || server?.catalog || name }).join(', ')} ${added.length === 1 ? 'is' : 'are'} now connected in this ${placeNoun}. Check it now through the API bridge (its tools may not be in your direct tool list yet, that is fine) and tell me briefly what you can do with it.`)
    }
  }, [servers, loading, onAsk, placeNoun, workspacePath, catalog, chatSessionId])


  const signIn = async (name: string, client?: { clientId: string; clientSecret?: string }) => {
    const result = await placeMcpApi.connect(workspacePath, name, client, chatSessionId)
    // needs_client_id first: an authorize URL without a client only shows the provider's "app ID is invalid" page.
    if (result.status === 'needs_client_id') {
      setClientPrompt({ server: name, redirectUri: result.redirect_uri })
      setMessage(result.message ?? null)
    } else if (result.auth_url) {
      setClientPrompt(null)
      window.open(result.auth_url, '_blank', 'noopener')
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
      return true
    } catch (cause) {
      setError(errorText(cause, failure))
      return false
    } finally {
      setBusy(null)
    }
  }

  // Connect hands the request to the agent in this project's chat (the same path as "Ask the
  // agent"): it adds the connection with the person's login and sends back the sign-in link, so
  // the chat shows what happened and the person can adjust it in words.
  const add = (entry: McpCatalogServer, label: string) => run(entry.catalog, async () => {
    if (onAsk) {
      const setupSkill = placeNoun === 'workflow' ? 'the MCP management guidance in the system-tools skill' : 'the attached work-mcp skill'
      await onAsk(`Connect ${entry.catalog} to this ${placeNoun} as a new account named ${JSON.stringify(label)} with my login. Pass catalog=${JSON.stringify(entry.catalog)} and label=${JSON.stringify(label)} to the setup tool. Preserve my other accounts. First read ${setupSkill}, then use its setup tools. If sign-in is required, give me the link returned by the tool. Check the connection status before saying it is connected.`)
      setNamingProvider(null)
      return
    }
    const saved = await placeMcpApi.add(workspacePath, { catalog: entry.catalog, label })
    setNamingProvider(null)
    await refresh()
    if (saved.oauth) await signIn(saved.name)
  }, 'Could not add the connection.')

  const addGroup = (group: string, picks: string[]) => run(`group:${group}`, async () => {
    let first: string | null = null
    for (const entry of (groups.get(group) ?? []).filter(item => picks.includes(item.catalog))) {
      const saved = await placeMcpApi.add(workspacePath, entry.catalog)
      if (saved.oauth) first = first ?? saved.name
    }
    await refresh()
    if (first) await signIn(first)
  }, 'Could not add the connections.')

  const addCustom = (server: PlaceMcpCustomServer, key: { name: string; value: string } | null) => run('custom', async () => {
    if (key) await api.post('/api/me/secrets', key)
    const saved = await placeMcpApi.add(workspacePath, server)
    setShowCustom(false)
    await refresh()
    if (saved.oauth) await signIn(saved.name)
  }, 'Could not add the server.')

  const remove = (server: PlaceMcpServer) => run(`${server.owner}:${server.name}`, async () => {
    await placeMcpApi.remove(workspacePath, server.name, server.owner)
    await refresh()
  }, 'Could not remove the connection.')

  return {
    loading, refresh,
    notices: [
      ...(error ? [{ message: error, retry: () => void refresh() }] : []),
      ...(message ? [{ message, tone: 'info' as const }] : []),
      ...(catalogState === 'failed' ? [{ message: 'Could not load the list of connectors.', retry: loadCatalog }] : []),
    ],
    servers: servers.map(server => ({
      id: `private:${workspacePath}:${server.owner}:${server.name}`, name: server.label || server.catalog || server.name, source: `Connected by ${server.mine ? 'you' : server.owner_name || server.owner}${server.label && server.catalog ? ` · ${server.catalog}` : ''}`,
      status: !server.active ? 'Paused' : server.connected ? 'Connected' : 'Sign-in required', statusDot: server.active && server.connected ? 'bg-green-500' : 'bg-muted-foreground',
      loadTools: server.active && server.connected ? async () => {
        const result = await placeMcpApi.tools(server.name)
        if (result.status !== 'ok') throw new Error(result.error || 'Could not load tools.')
        return (result.tools ?? []).map(tool => ({ id: tool.name, name: tool.name, description: tool.description, rawSchema: tool.parameters ? { type: 'object', properties: tool.parameters, ...(tool.required ? { required: tool.required } : {}) } : undefined }))
      } : undefined,
      actions: [
        ...(server.mine && (!server.connected || server.sign_in) ? [{ label: server.connected ? 'Sign in again' : 'Sign in', disabled: busy !== null, run: () => run(server.name, () => signIn(server.name), 'Could not start sign-in.') }] : []),
        ...(server.mine || canEdit ? [{ label: 'Remove from this project', ariaLabel: `Remove ${server.label || server.catalog || server.name}`, destructive: true, disabled: busy !== null, run: () => remove(server) }] : []),
      ],
    })) satisfies McpConnectionRow[],
    catalog: canEdit ? catalog.map(entry => ({
      id: entry.catalog, name: entry.catalog, description: entry.description || descriptionFor(entry.catalog), category: groupFor(entry.catalog),
      connect: { label: 'Add connection', disabled: busy !== null, run: () => setNamingProvider(entry.catalog) },
      details: namingProvider === entry.catalog ? <McpNamedConnectionForm provider={entry.catalog} busy={busy !== null} cancel={() => setNamingProvider(null)} submit={label => add(entry, label)} /> : undefined,
      ...(entry.group && groups.has(entry.group) ? { batch: { id: entry.group, name: providerGroupLabel(entry.group), connect: (picks: string[]) => addGroup(entry.group!, picks) } } : {}),
    })) satisfies McpCatalogRow[] : [],
    addCustom: canEdit ? { label: 'Add custom server', disabled: busy !== null, run: () => onAsk ? onAsk('Help me connect one of my own MCP servers in this ' + placeNoun + '. First read the relevant MCP setup skill. Ask which app or service I want, then use its setup tools and give me any returned sign-in link. Verify the connection status.') : setShowCustom(true) } : undefined,
    help: <><p>A connection added here is used by everyone with access to this {placeNoun}, and only here. It acts as the account of the person who connected it. Use Vault for access shared across places.</p><p>Google apps are connected in the Google apps tab. For GitHub, add a personal access token as a secret named GITHUB_TOKEN in Integrations → Secrets.</p></>,
    dialogs: <>
      {clientPrompt && <McpOAuthClientForm key={clientPrompt.server} {...clientPrompt} busy={busy !== null} cancel={() => setClientPrompt(null)} reportError={setError} submit={client => { void run(`client:${clientPrompt.server}`, () => signIn(clientPrompt.server, client), 'Could not start sign-in.') }} />}
      {showCustom && <McpCustomServerForm secrets={secrets} busy={busy !== null} cancel={() => setShowCustom(false)} submit={addCustom} />}
    </>,
  }
}

import { useEffect, useMemo, useRef, useState } from 'react'
import { ChevronDown, KeyRound, Loader2, MoreHorizontal, PlugZap, Plus, RefreshCw, Search, Server, Trash2 } from 'lucide-react'
import { SettingsCard } from '../../components/ui/SettingsCard'
import { Button } from '../../components/ui/Button'
import { Input } from '../../components/ui/Input'
import IconPopover from '../../components/ui/IconPopover'
import ConfirmationDialog from '../../components/ui/ConfirmationDialog'
import ConnectionIcon from '../../components/connectors/ConnectionIcon'
import OAuthStatusBadge from '../../components/OAuthStatusBadge'
import { brandSlugFor } from '../../components/connectors/brandSlug'
import { GatewayToolCard } from './GatewayToolCard'
import { descriptionFor, statusIndicator } from '../../components/connectors/catalog'
import { useMCPStore } from '../../stores/useMCPStore'
import { agentApi } from '../../services/api'
import type { ToolDefinition, ToolDetail } from '../../stores/types'
import {
  createConnector,
  approveTool,
  deleteConnector,
  listCatalog,
  listConnectors,
  listTools,
  listToolVersions,
  syncConnector,
  setConnectorBearer,
  type GatewayConnector,
  type GatewayTool,
} from './gatewayAdminApi'
import { ConsoleEmpty, ConsoleError, ConsoleLoading, ConsoleStale } from './gatewayConsoleShared'
import {
  gatewayErrorMessage,
  mergeServerRows,
  plural,
  tableClass,
  tdClass,
  thClass,
  useAttempt,
  useGatewayLoader,
  type AgentWorksServer,
  type ServerRow,
} from './gatewayConsoleUtils'

function agentWorksServers(toolList: ToolDefinition[]): AgentWorksServer[] {
  const groups = new Map<string, ToolDefinition[]>()
  for (const tool of toolList) {
    if (!tool.server) continue
    const list = groups.get(tool.server) ?? []
    list.push(tool)
    groups.set(tool.server, list)
  }
  return [...groups.entries()].map(([name, entries]) => {
    const details = entries.flatMap((entry) => entry.tools ?? [])
    const names = [...new Set(entries.flatMap((entry) => entry.function_names ?? entry.tools?.map((tool) => tool.name) ?? []))]
    return {
      name,
      connection: entries[0]?.connection,
      status: entries[0]?.status,
      toolCount: Math.max(names.length, details.length),
      toolNames: names,
      tools: details,
    }
  })
}

function displayName(row: ServerRow): string {
  return row.agentworks?.name ?? row.catalogMatch?.Name ?? row.gateway[0]?.Label ?? row.name
}

function gatewayStatusDot(status: string): string {
  if (status === 'active') return 'bg-green-500'
  if (status === 'quarantined') return 'bg-red-500'
  return 'bg-gray-400'
}

type Sort = 'name' | 'tools'

function matchesQuery(row: ServerRow, q: string): boolean {
  if (!q) return true
  const name = displayName(row)
  return (
    name.toLowerCase().includes(q) ||
    descriptionFor(name).toLowerCase().includes(q) ||
    row.gateway.some((c) => c.UpstreamURL.toLowerCase().includes(q))
  )
}

export function GatewayServersPanel({ base, standalone = false, view = 'connected', onConnected, onAddCustom, revision }: {
  base: string
  standalone?: boolean
  view?: 'connected' | 'available'
  onConnected?: () => void
  onAddCustom?: () => Promise<void>
  revision?: string
}) {
  const [attempt, bump] = useAttempt()
  const { data, loading, error } = useGatewayLoader(async () => {
    const [connectors, catalog, tools] = await Promise.all([listConnectors(base), listCatalog(base), listTools(base)])
    return { connectors: connectors.connectors, providers: catalog.providers, tools: tools.tools }
  }, attempt)
  const toolList = useMCPStore((state) => state.toolList)
  const refreshTools = useMCPStore((state) => state.refreshTools)
  const agentWorksLoading = useMCPStore((state) => state.isLoadingTools)
  const agentWorksError = useMCPStore((state) => state.toolsError)

  useEffect(() => {
    if (!standalone) void refreshTools()
  }, [refreshTools, standalone])

  const [query, setQuery] = useState('')
  useEffect(() => { setQuery(''); setSort('name') }, [view])
  const [sort, setSort] = useState<Sort>('name')
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const [expandedAgentWorks, setExpandedAgentWorks] = useState<Set<string>>(new Set())
  const [addingKey, setAddingKey] = useState<string | null>(null)
  const [syncing, setSyncing] = useState<string | null>(null)
  const [approving, setApproving] = useState<string | null>(null)
  const [deleting, setDeleting] = useState<GatewayConnector | null>(null)
  const [deleteBusy, setDeleteBusy] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)
  const [credentialFor, setCredentialFor] = useState<string | null>(null)
  const [credentialValue, setCredentialValue] = useState('')
  const [credentialBusy, setCredentialBusy] = useState(false)
  const [chatRequestBusy, setChatRequestBusy] = useState(false)

  const lastRevision = useRef(revision)
  useEffect(() => {
    if (revision && revision !== lastRevision.current) {
      lastRevision.current = revision
      bump()
    }
  }, [revision, bump])

  async function requestCustomServer() {
    if (!onAddCustom) return
    setChatRequestBusy(true)
    setActionError(null)
    try { await onAddCustom() }
    catch (err) { setActionError(gatewayErrorMessage(err)) }
    finally { setChatRequestBusy(false) }
  }

  const rows = useMemo(() => {
    if (!data) return []
    return mergeServerRows(standalone ? [] : agentWorksServers(toolList), data.connectors, data.providers)
  }, [data, toolList, standalone])

  const toolsByConnector = useMemo(() => {
    const byId = new Map<string, GatewayTool[]>()
    for (const t of data?.tools ?? []) {
      const list = byId.get(t.ConnectorID) ?? []
      list.push(t)
      byId.set(t.ConnectorID, list)
    }
    for (const list of byId.values()) list.sort((a, b) => a.PublicName.localeCompare(b.PublicName))
    return byId
  }, [data])

  const visible = useMemo(() => {
    const q = query.trim().toLowerCase()
    const filtered = rows.filter((row) => matchesQuery(row, q))
    if (sort === 'tools') {
      const count = (row: ServerRow) =>
        (row.agentworks?.toolCount ?? 0) + row.gateway.reduce((n, c) => n + (toolsByConnector.get(c.ID)?.length ?? 0), 0)
      return [...filtered].sort((a, b) => count(b) - count(a))
    }
    return filtered
  }, [rows, query, sort, toolsByConnector])

  const connected = useMemo(
    () => visible.filter((r) => r.gateway.length > 0 || r.agentworks?.connection === 'connected'),
    [visible],
  )
  const available = useMemo(
    () => visible.filter((r) => r.gateway.length === 0 && r.agentworks?.connection !== 'connected'),
    [visible],
  )

  async function onAddToGateway(row: ServerRow) {
    if (!row.catalogMatch) return
    setAddingKey(row.key)
    setActionError(null)
    try {
      await createConnector(base, { Provider: row.catalogMatch.Name, Label: '', Slug: '', URL: '' })
      bump()
      if (row.catalogMatch.OAuth && !standalone) void refreshTools()
      onConnected?.()
    } catch (err: unknown) {
      setActionError(gatewayErrorMessage(err))
    } finally {
      setAddingKey(null)
    }
  }

  async function onSync(id: string) {
    setSyncing(id)
    setActionError(null)
    try {
      await syncConnector(base, id)
      bump()
    } catch (err: unknown) {
      setActionError(gatewayErrorMessage(err))
    } finally {
      setSyncing(null)
    }
  }

  async function onApprove(tool: GatewayTool) {
    setApproving(tool.PublicName)
    setActionError(null)
    try {
      await approveTool(base, tool)
      bump()
    } catch (err: unknown) {
      setActionError(gatewayErrorMessage(err))
    } finally {
      setApproving(null)
    }
  }

  async function onDelete() {
    if (!deleting) return
    setDeleteBusy(true)
    try {
      await deleteConnector(base, deleting.ID)
      setDeleting(null)
      bump()
    } catch (err: unknown) {
      setActionError(gatewayErrorMessage(err))
      setDeleting(null)
    } finally {
      setDeleteBusy(false)
    }
  }

  async function onRotateCredential(id: string) {
    setCredentialBusy(true)
    setActionError(null)
    try {
      await setConnectorBearer(base, id, credentialValue.trim())
      setCredentialValue('')
      setCredentialFor(null)
      bump()
    } catch (err: unknown) {
      setActionError(gatewayErrorMessage(err))
    } finally {
      setCredentialBusy(false)
    }
  }

  if (loading) return <ConsoleLoading label="Loading servers…" />
  if (!data) return <ConsoleError message={error ?? 'Failed to load.'} onRetry={bump} />

  return (
    <div className="space-y-4" data-testid="gateway-servers">
      {error && <ConsoleStale message={error} onRetry={bump} />}
      <div className="flex flex-wrap items-center gap-2">
        <span className="relative min-w-52 flex-1 sm:max-w-xs">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" aria-hidden />
          <Input
            aria-label="Search servers"
            placeholder="Search servers…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className="pl-8"
            data-testid="gateway-servers-search"
          />
        </span>
        <select
          aria-label="Sort servers"
          className="h-8 rounded-md border border-input bg-background px-2 text-xs shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
          value={sort}
          onChange={(e) => setSort(e.target.value as Sort)}
        >
          <option value="name">Sort A–Z</option>
          <option value="tools">Sort by tools</option>
        </select>
        <span className="text-xs text-muted-foreground">{plural(view === 'connected' ? connected.length : available.length, 'result')}</span>
      </div>

      {actionError && <ConsoleError message={actionError} onRetry={bump} />}

      {!standalone && agentWorksError && (
        <p className="text-xs text-destructive" role="alert">
          AgentWorks connections could not be refreshed: {agentWorksError}
          <Button variant="ghost" size="xs" onClick={() => void refreshTools()}>Retry</Button>
        </p>
      )}

      {view === 'connected' && <SettingsCard
        icon={<PlugZap className="h-4 w-4 text-primary" />}
        title="Connected"
        ariaLabel="Connected servers"
        count={plural(connected.length, 'server')}
      >
        {connected.length === 0 ? (
          <ConsoleEmpty>No connected servers match.</ConsoleEmpty>
        ) : (
          <div className="space-y-3">
            {connected.map((row) => {
              const name = displayName(row)
              const aw = row.agentworks ? statusIndicator(row.agentworks.connection, row.agentworks.status) : null
              const openAgentWorks = !!row.agentworks && expandedAgentWorks.has(row.key)
              return (
                <article key={row.key} aria-label={`${name} server`} className="min-w-0 rounded-md border border-border">
                  {(row.gateway.length !== 1 || row.agentworks) && <div className="flex items-start gap-2.5 px-3 pt-3">
                    <ConnectionIcon icon={brandSlugFor(name)} name={name} size="xs" />
                    <div className="min-w-0 flex-1">
                      <h4 className="break-words text-sm font-semibold text-foreground">{name}</h4>
                      {descriptionFor(name) !== 'Custom MCP server' && (
                        <p className="mt-0.5 text-muted-foreground">{descriptionFor(name)}</p>
                      )}
                    </div>
                  </div>}
                  {row.gateway.map((c) => {
                    const tools = toolsByConnector.get(c.ID) ?? []
                    const needsReview = tools.filter((tool) => tool.Status === 'quarantined').length
                    const approved = tools.filter((tool) => tool.Status === 'active').length
                    const open = expanded.has(c.ID)
                    return (
                      <div key={c.ID} className="min-w-0 space-y-2.5 p-3">
                        <div className="flex flex-wrap items-center justify-between gap-3">
                          <div className="flex min-w-52 flex-1 items-center gap-2.5">
                            {row.gateway.length === 1 && !row.agentworks && <ConnectionIcon icon={brandSlugFor(name)} name={name} size="xs" />}
                            <div className="min-w-0 space-y-1">
                              {row.gateway.length === 1 && !row.agentworks ? <h4 className="break-words text-sm font-semibold text-foreground">{name}</h4> : (
                                <p className="font-medium">CapLayer{row.gateway.length > 1 ? ` · ${c.Label || c.Provider}${c.InstanceSlug ? ` (${c.InstanceSlug})` : ''}` : ''}</p>
                              )}
                              <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-muted-foreground">
                                <span className="inline-flex items-center gap-1.5">
                                  <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${gatewayStatusDot(c.Status)}`} aria-hidden />
                                  {c.Status === 'active' ? 'Connected' : c.Status === 'disabled' ? 'Disabled' : c.Status === 'quarantined' ? 'Connection needs review' : c.Status}
                                </span>
                                <span aria-hidden>·</span>
                                <span>{plural(tools.length, 'tool')}</span>
                              </div>
                            </div>
                          </div>
                          <div className="ml-auto flex shrink-0 items-center gap-2">
                            <Button
                              variant="outline" size="sm"
                              onClick={() => setExpanded((prev) => {
                                const next = new Set(prev)
                                if (next.has(c.ID)) next.delete(c.ID)
                                else next.add(c.ID)
                                return next
                              })}
                              aria-expanded={open}
                              aria-label={`${open ? 'Hide' : 'Show'} ${plural(tools.length, 'tool')} on ${c.Label || c.Provider}`}
                            >
                              {open ? 'Hide tools' : 'View tools'}
                              <ChevronDown className={`transition-transform ${open ? 'rotate-180' : ''}`} aria-hidden />
                            </Button>
                            <IconPopover icon={syncing === c.ID ? <Loader2 className="h-4 w-4 animate-spin" /> : <MoreHorizontal className="h-4 w-4" />} label={`Actions for ${c.Label || c.Provider}`} panelClassName="w-52 !border-border !bg-popover !p-1">
                              {(close) => <div className="space-y-0.5" role="group" aria-label="Server actions">
                                <Button variant="ghost" size="sm" className="w-full justify-start" disabled={syncing === c.ID} onClick={() => { close(); void onSync(c.ID) }} aria-label={`Sync ${c.Label || c.Provider}`}>
                                  <RefreshCw />Refresh tool list
                                </Button>
                                <Button variant="ghost" size="sm" className="w-full justify-start" onClick={() => { close(); setCredentialFor(c.ID); setCredentialValue('') }}>
                                  <KeyRound />Connection settings
                                </Button>
                                <div className="my-1 border-t border-border" />
                                <Button variant="ghost" size="sm" className="w-full justify-start text-destructive hover:text-destructive" onClick={() => { close(); setDeleting(c) }}>
                                  <Trash2 />Disconnect server
                                </Button>
                              </div>}
                            </IconPopover>
                          </div>
                        </div>
                        {credentialFor === c.ID && <div className="space-y-2 rounded-md border border-border p-3">
                          <h5 className="font-semibold">Connection settings</h5>
                          {c.OAuthServer ? <>
                            <p className="text-muted-foreground">Sign in again to reconnect. Group permissions stay unchanged.</p>
                            <OAuthStatusBadge serverName={c.OAuthServer} requiresOAuth connection="available" connectLabel="Sign in again"
                              onAuthChange={valid => { if (valid) void onSync(c.ID) }} />
                            <Button variant="ghost" size="sm" onClick={() => setCredentialFor(null)}>Close</Button>
                          </> : <>
                          <p className="text-muted-foreground">Update this server’s connection token.</p>
                          <label className="block font-medium" htmlFor={`credential-${c.ID}`}>Server access token</label>
                          <Input id={`credential-${c.ID}`} type="password" autoComplete="off" aria-label={`New bearer token for ${c.Label || c.Provider}`} placeholder="Enter a replacement token" value={credentialValue} onChange={event => setCredentialValue(event.target.value)} />
                          <div className="flex items-center gap-2">
                            <Button size="sm" disabled={credentialBusy || !credentialValue.trim()} onClick={() => void onRotateCredential(c.ID)}>Update token</Button>
                            <Button variant="ghost" size="sm" onClick={() => { setCredentialFor(null); setCredentialValue('') }}>Cancel</Button>
                          </div>
                          </>}
                        </div>}
                        {open && (
                          <section className="min-w-0 space-y-3 border-t border-border pt-3" aria-label={`${name} Gateway tools`}>
                            <h5 className="font-semibold text-foreground">Tools</h5>
                            <div className="flex flex-wrap items-center gap-2 text-muted-foreground">
                              {needsReview > 0 && <span>{needsReview} need review</span>}
                              {approved > 0 && <span>{approved} approved</span>}
                            </div>
                            {needsReview > 0 && <p className="text-muted-foreground">Tools changed since connection. Review before approving. Group access is assigned separately.</p>}
                            <ToolList tools={tools} base={base} onApprove={onApprove} approving={approving} />
                          </section>
                        )}
                      </div>
                    )
                  })}
                  {!standalone && row.agentworks && aw && (
                    <div className={`space-y-2 p-3 ${row.gateway.length > 0 ? 'border-t border-border' : ''}`}>
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="font-medium">AgentWorks</span>
                        <span className="inline-flex items-center gap-1.5 text-muted-foreground" title={aw.title}>
                          <span className={`h-2 w-2 shrink-0 rounded-full ${aw.dot}`} aria-hidden />
                          {row.agentworks.connection === 'connected' ? 'Connected' : aw.title}
                        </span>
                        {row.agentworks.connection === 'connected' && <span className="text-muted-foreground">{row.agentworks.status === 'not_loaded' ? 'Tools not loaded' : plural(row.agentworks.toolCount, 'tool')}</span>}
                      </div>
                      <div className="flex flex-wrap items-center gap-2">
                        {row.agentworks.connection === 'connected' && (
                          <Button variant="outline" size="sm" aria-expanded={openAgentWorks} aria-label={`${openAgentWorks ? 'Hide' : 'Show'} AgentWorks tools on ${name}`} onClick={() => setExpandedAgentWorks((previous) => {
                            const next = new Set(previous)
                            if (next.has(row.key)) next.delete(row.key)
                            else next.add(row.key)
                            return next
                          })}>
                            <ChevronDown className={`transition-transform ${openAgentWorks ? 'rotate-180' : ''}`} aria-hidden />
                            {openAgentWorks ? 'Hide tools' : 'View tools'}
                          </Button>
                        )}
                        {row.gateway.length === 0 && (row.catalogMatch?.OAuth ? (
                          <OAuthStatusBadge serverName={row.catalogMatch.Name} requiresOAuth connection="available" reuseAuthentication connectLabel="Connect with OAuth"
                            readOnly={addingKey === row.key} onAuthChange={valid => { if (valid) void onAddToGateway(row) }} />
                        ) : row.catalogMatch ? (
                          <Button variant="outline" size="sm" disabled={addingKey === row.key} onClick={() => void onAddToGateway(row)} data-testid={`gateway-add-${row.key}`}>
                            {addingKey === row.key ? <Loader2 className="animate-spin" /> : <Plus />}Connect to gateway
                          </Button>
                        ) : <span className="text-muted-foreground">Use Add custom server in Available MCPs to connect it in chat.</span>)}
                      </div>
                      {openAgentWorks && <section className="min-w-0 border-t border-border pt-3" aria-label={`${name} AgentWorks tools`}>
                        <h5 className="mb-2 font-semibold text-foreground">AgentWorks tools</h5>
                        <AgentWorksToolList server={row.agentworks} />
                      </section>}
                    </div>
                  )}
                </article>
              )
            })}
          </div>
        )}
      </SettingsCard>}

      {view === 'available' && <SettingsCard
          icon={<Server className="h-4 w-4 text-primary" />}
          title="Available to add"
          ariaLabel="Available servers"
          count={plural(available.length, 'server')}
          description={undefined}
        >
          {!standalone && agentWorksLoading && <p className="mb-2 text-xs text-muted-foreground">Checking AgentWorks connections…</p>}
          {available.length === 0 ? <ConsoleEmpty>No servers available to add.</ConsoleEmpty> : (
          <div className="overflow-x-auto">
            <table className={tableClass}>
              <thead>
                <tr>
                  <th className={thClass}>Server</th>
                  <th className={thClass}>Gateway</th>
                </tr>
              </thead>
              <tbody>
                {available.map((row) => {
                  const name = displayName(row)
                  return (
                    <tr key={row.key}>
                      <td className={tdClass}>
                        <span className="flex items-center gap-2">
                          <ConnectionIcon icon={brandSlugFor(name)} name={name} size="xs" />
                          <span className="min-w-0">
                            <span className="block truncate font-medium text-foreground">{name}</span>
                            <span className="block max-w-96 truncate text-muted-foreground" title={descriptionFor(name)}>
                              {descriptionFor(name)}
                            </span>
                          </span>
                        </span>
                      </td>
                      <td className={tdClass}>
                        {row.catalogMatch?.OAuth ? (
                          <OAuthStatusBadge serverName={row.catalogMatch.Name} requiresOAuth connection="available" reuseAuthentication connectLabel="Connect with OAuth"
                            readOnly={addingKey === row.key} onAuthChange={valid => { if (valid) void onAddToGateway(row) }} />
                        ) : row.catalogMatch ? (
                          <Button
                            variant="outline"
                            size="xs"
                            disabled={addingKey === row.key}
                            onClick={() => void onAddToGateway(row)}
                            data-testid={`gateway-add-${row.key}`}
                          >
                            {addingKey === row.key ? <Loader2 className="animate-spin" /> : <Plus />}
                            Connect to gateway
                          </Button>
                        ) : (
                          <span className="text-muted-foreground">Not in catalog</span>
                        )}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
          )}
      </SettingsCard>}

      {view === 'available' && <div className="flex flex-wrap items-center justify-between gap-3 border-t border-border pt-4" aria-label="Add custom server">
        <Button variant="outline" size="sm" disabled={!onAddCustom || chatRequestBusy} onClick={() => void requestCustomServer()} data-testid="gateway-add-custom">
          {chatRequestBusy ? <Loader2 className="animate-spin" /> : <Plus />}Add custom server
        </Button>
      </div>}

      <ConfirmationDialog
        isOpen={deleting !== null}
        onClose={() => setDeleting(null)}
        onConfirm={() => void onDelete()}
        title="Disconnect server"
        message={`Disconnect "${deleting?.Label || deleting?.Provider}" from CapLayer? Its tools will become unavailable, and its group permissions will be removed. Reconnecting requires assigning permissions again.`}
        confirmText="Disconnect"
        loadingText="Disconnecting…"
        isLoading={deleteBusy}
      />
    </div>
  )
}

function toolJSON(encoded: string | null | undefined): string {
  if (!encoded) return 'Not provided.'
  try { return JSON.stringify(JSON.parse(atob(encoded)), null, 2) }
  catch { return 'Schema could not be displayed.' }
}

function ToolReviewRow({ tool, base, onApprove, approving }: {
  tool: GatewayTool
  base: string
  onApprove: (tool: GatewayTool) => Promise<void>
  approving: boolean
}) {
  const [open, setOpen] = useState(false)
  const [versions, setVersions] = useState<GatewayTool[] | null>(null)
  const [historyError, setHistoryError] = useState<string | null>(null)

  async function openReview() {
    setOpen(true)
    if (versions !== null) return
    try {
      const response = await listToolVersions(base, tool.PublicName)
      setVersions(response.versions)
    } catch (err: unknown) {
      setHistoryError(gatewayErrorMessage(err))
    }
  }

  return (
    <GatewayToolCard name={tool.UpstreamName} description={tool.Description} schema={tool.InputSchema}
      status={tool.Status === 'quarantined' ? 'Needs review' : tool.Status === 'active' ? 'Approved' : tool.Status === 'disabled' ? 'Disabled' : tool.Status}>
      <span className="flex flex-wrap items-center gap-2">
        <Button variant="ghost" size="xs" onClick={() => open ? setOpen(false) : void openReview()}>
          {open ? 'Hide details' : 'Review details'}
        </Button>
        {open && tool.Status === 'quarantined' && (
          <Button size="xs" disabled={approving} onClick={() => void onApprove(tool)}>
            {approving && <Loader2 className="animate-spin" />}Approve v{tool.Version}
          </Button>
        )}
      </span>
      {open && (
        <span className="block space-y-2 rounded-md border border-border p-2 text-xs">
          <span className="block font-medium">MCP tool name · v{tool.Version}</span>
          <span className="block break-all font-mono">{tool.PublicName}</span>
          {tool.Description && <><span className="block font-medium">Description</span><span className="block whitespace-pre-wrap">{tool.Description}</span></>}
          <span className="block font-medium">Current input schema</span>
          <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-all">{toolJSON(tool.InputSchema)}</pre>
          <span className="block font-medium">Output schema</span>
          <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-all">{toolJSON(tool.OutputSchema)}</pre>
          <span className="block font-medium">Annotations</span>
          <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-all">{toolJSON(tool.Annotations)}</pre>
          {historyError && <span className="text-destructive">{historyError}</span>}
          {versions?.slice(-1).map((previous) => (
            <span className="block" key={previous.Version}>
              <span className="block font-medium">Previous v{previous.Version}: {previous.Description}</span>
              <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-all">{toolJSON(previous.InputSchema)}</pre>
              <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-all">{toolJSON(previous.OutputSchema)}</pre>
            </span>
          ))}
        </span>
      )}
    </GatewayToolCard>
  )
}

function ToolList({ tools, base, onApprove, approving }: {
  tools: GatewayTool[]
  base: string
  onApprove: (tool: GatewayTool) => Promise<void>
  approving: string | null
}) {
  if (tools.length === 0) return <span className="text-muted-foreground">No tools discovered yet. Use Sync tools to check the server again.</span>
  return (
    <div className="flex min-w-0 flex-col gap-3">
      {tools.map((t) => (
        <ToolReviewRow key={t.PublicName} tool={t} base={base} onApprove={onApprove} approving={approving === t.PublicName} />
      ))}
    </div>
  )
}

function AgentWorksToolList({ server }: { server: AgentWorksServer }) {
  const [detail, setDetail] = useState<ToolDefinition | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [attempt, setAttempt] = useState(0)
  const refreshTools = useMCPStore((state) => state.refreshTools)

  useEffect(() => {
    // The summary endpoint intentionally does not discover idle servers. Fetch
    // detail only when someone expands a connected server's tool list.
    if (server.tools?.length) return
    let cancelled = false
    setLoading(true)
    setError(null)
    void agentApi.getToolDetail(server.name).then(
      (result: ToolDefinition) => {
        if (cancelled) return
        setDetail(result)
        setLoading(false)
        if (result.status === 'ok') void refreshTools()
      },
      (cause: unknown) => {
        if (cancelled) return
        setError(gatewayErrorMessage(cause))
        setLoading(false)
      },
    )
    return () => { cancelled = true }
    // A new discovery only occurs when the server changes or the user retries.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [server.name, attempt])

  const tools: ToolDetail[] = detail?.tools?.length ? detail.tools : server.tools ?? []
  const names = detail?.function_names?.length ? detail.function_names : server.toolNames ?? []
  const items: ToolDetail[] = tools.length > 0 ? tools : names.map((name) => ({ name, description: '', server: server.name }))
  const rawError = detail?.status === 'not_connected' || detail?.status === 'error'
    ? detail.error || 'Could not discover tools for this server.'
    : error
  const discoveryError = rawError && /authorization required|unauthorized|oauth/i.test(rawError)
    ? 'Authorization is required to load this server’s tools.'
    : rawError

  return (
    <div className="flex min-w-0 flex-col gap-3 whitespace-normal">
      {loading && <span className="inline-flex items-center gap-1 text-muted-foreground"><Loader2 className="h-3 w-3 animate-spin" />Loading tools…</span>}
      {items.map(tool => <GatewayToolCard key={tool.name} name={tool.name} description={tool.description}
        rawSchema={tool.parameters ? { type: 'object', properties: tool.parameters, ...(tool.required ? { required: tool.required } : {}) } : undefined} />)}
      {!loading && !discoveryError && items.length === 0 && <span className="text-muted-foreground">No tools discovered.</span>}
      {discoveryError && (
        <span className="text-destructive" role="alert">
          {discoveryError}
          <Button variant="ghost" size="xs" onClick={() => setAttempt((value) => value + 1)}>Retry</Button>
        </span>
      )}
    </div>
  )
}

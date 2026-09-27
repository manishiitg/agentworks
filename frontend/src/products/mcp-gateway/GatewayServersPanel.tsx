import { useMemo, useState } from 'react'
import { ChevronDown, Loader2, PlugZap, Plus, RefreshCw, Search, Server, Trash2 } from 'lucide-react'
import { SettingsCard, SettingsCount } from '../../components/ui/SettingsCard'
import { Button } from '../../components/ui/Button'
import { Input } from '../../components/ui/Input'
import { Textarea } from '../../components/ui/Textarea'
import ConfirmationDialog from '../../components/ui/ConfirmationDialog'
import ConnectionIcon from '../../components/connectors/ConnectionIcon'
import { brandSlugFor } from '../../components/connectors/brandSlug'
import { GROUP_ORDER, descriptionFor, groupFor, statusIndicator } from '../../components/connectors/catalog'
import { useMCPStore } from '../../stores/useMCPStore'
import type { ToolDefinition } from '../../stores/types'
import {
  createConnector,
  approveTool,
  deleteConnector,
  listCatalog,
  listConnectors,
  listTools,
  listToolVersions,
  syncConnector,
  type GatewayConnector,
  type GatewayTool,
} from './gatewayAdminApi'
import { ConsoleEmpty, ConsoleError, ConsoleLoading, ConsoleStale } from './gatewayConsoleShared'
import {
  codeClass,
  gatewayErrorMessage,
  mergeServerRows,
  parseMcpServersJson,
  parseToolArgs,
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
  return [...groups.entries()].map(([name, tools]) => ({
    name,
    connection: tools[0]?.connection,
    status: tools[0]?.status,
    toolCount: tools.reduce((n, t) => n + (t.function_names?.length ?? t.tools?.length ?? 0), 0),
  }))
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

function ToolArgs({ tool }: { tool: GatewayTool }) {
  const args = parseToolArgs(tool.InputSchema)
  if (args === null) return <span className="text-muted-foreground">parameters not documented</span>
  if (args.length === 0) return <span className="text-muted-foreground">takes no parameters</span>
  return (
    <span className="flex flex-wrap gap-1">
      {args.map((a) => (
        <span key={a.name} className={codeClass} title={a.description || `${a.name}: ${a.type}`}>
          {a.name}: {a.type}
          {a.required ? '*' : ''}
        </span>
      ))}
    </span>
  )
}

const JSON_PLACEHOLDER = `{
  "mcpServers": {
    "acme-notes": {
      "url": "https://acme.example.com/mcp"
    }
  }
}`

export function GatewayServersPanel({ base }: { base: string }) {
  const [attempt, bump] = useAttempt()
  const { data, loading, error } = useGatewayLoader(async () => {
    const [connectors, catalog, tools] = await Promise.all([listConnectors(base), listCatalog(base), listTools(base)])
    return { connectors: connectors.connectors, providers: catalog.providers, tools: tools.tools }
  }, attempt)
  const toolList = useMCPStore((state) => state.toolList)

  const [query, setQuery] = useState('')
  const [sort, setSort] = useState<Sort>('name')
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const [addingKey, setAddingKey] = useState<string | null>(null)
  const [syncing, setSyncing] = useState<string | null>(null)
  const [approving, setApproving] = useState<string | null>(null)
  const [deleting, setDeleting] = useState<GatewayConnector | null>(null)
  const [deleteBusy, setDeleteBusy] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)
  const [jsonText, setJsonText] = useState('')
  const [jsonBusy, setJsonBusy] = useState(false)
  const [jsonError, setJsonError] = useState<string | null>(null)
  const [jsonNotice, setJsonNotice] = useState<string | null>(null)
  const [customName, setCustomName] = useState('')
  const [customUrl, setCustomUrl] = useState('')
  const [customBusy, setCustomBusy] = useState(false)
  const [customError, setCustomError] = useState<string | null>(null)

  const rows = useMemo(() => {
    if (!data) return []
    return mergeServerRows(agentWorksServers(toolList), data.connectors, data.providers)
  }, [data, toolList])

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
        row.gateway.reduce((n, c) => n + (toolsByConnector.get(c.ID)?.length ?? 0), 0)
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

  const groupedConnected = useMemo(() => {
    const byGroup = new Map<string, ServerRow[]>()
    for (const row of connected) {
      const group = groupFor(displayName(row))
      const list = byGroup.get(group) ?? []
      list.push(row)
      byGroup.set(group, list)
    }
    return GROUP_ORDER.filter((g) => byGroup.has(g.id)).map((g) => ({ ...g, rows: byGroup.get(g.id) ?? [] }))
  }, [connected])

  async function onAddToGateway(row: ServerRow) {
    if (!row.catalogMatch) return
    setAddingKey(row.key)
    setActionError(null)
    try {
      await createConnector(base, { Provider: row.catalogMatch.Name, Label: '', Slug: '', URL: '' })
      bump()
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

  async function onAddCustom() {
    const name = customName.trim()
    const url = customUrl.trim()
    if (!name || !url) {
      setCustomError('Enter a name and a server URL.')
      return
    }
    if (!url.startsWith('https://') && !url.startsWith('http://127.0.0.1') && !url.startsWith('http://localhost')) {
      setCustomError('Upstream must be https (or loopback http).')
      return
    }
    setCustomBusy(true)
    setCustomError(null)
    setActionError(null)
    try {
      await createConnector(base, { Provider: name, Label: '', Slug: '', URL: url })
      setCustomName('')
      setCustomUrl('')
      bump()
    } catch (err: unknown) {
      setCustomError(gatewayErrorMessage(err))
    } finally {
      setCustomBusy(false)
    }
  }

  async function onAddJson() {
    let parsed: ReturnType<typeof parseMcpServersJson>
    try {
      parsed = parseMcpServersJson(jsonText)
    } catch (err: unknown) {
      setJsonError(gatewayErrorMessage(err))
      setJsonNotice(null)
      return
    }
    setJsonBusy(true)
    setJsonError(null)
    setJsonNotice(null)
    const added: string[] = []
    const failed: string[] = parsed.skipped.map((s) => `${s.name} (${s.reason})`)
    for (const server of parsed.servers) {
      try {
        await createConnector(base, { Provider: server.name, Label: '', Slug: '', URL: server.url })
        added.push(server.name)
      } catch (err: unknown) {
        failed.push(`${server.name} (${gatewayErrorMessage(err)})`)
      }
    }
    setJsonBusy(false)
    if (added.length > 0) {
      setJsonText('')
      bump()
    }
    const parts = []
    if (added.length > 0) parts.push(`Added ${plural(added.length, 'server')}: ${added.join(', ')}`)
    if (failed.length > 0) parts.push(`Skipped ${plural(failed.length, 'server')}: ${failed.join('; ')}`)
    const summary = parts.join('. ')
    if (added.length > 0) setJsonNotice(summary)
    else setJsonError(summary)
  }

  if (loading) return <ConsoleLoading label="Loading servers…" />
  if (!data) return <ConsoleError message={error ?? 'Failed to load.'} onRetry={bump} />

  const inGateway = rows.filter((r) => r.gateway.length > 0).length
  const stats: Array<[string, string]> = [
    [String(connected.length), 'connected'],
    [String(inGateway), 'in gateway'],
    [String(data.tools.filter((tool) => tool.Status === 'active').length), 'tools available'],
    [String(data.tools.filter((tool) => tool.Status === 'quarantined').length), 'awaiting review'],
    [String(data.providers.length), 'in catalog'],
  ]

  return (
    <div className="space-y-4" data-testid="gateway-servers">
      {error && <ConsoleStale message={error} onRetry={bump} />}
      <div className="flex flex-wrap items-center gap-x-6 gap-y-1 rounded-lg border border-border px-4 py-2.5" aria-label="Gateway overview">
        {stats.map(([value, label]) => (
          <p key={label} className="text-xs text-muted-foreground">
            <span className="mr-1.5 text-base font-semibold text-foreground">{value}</span>
            {label}
          </p>
        ))}
      </div>

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
        <span className="text-xs text-muted-foreground">{plural(visible.length, 'server')}</span>
      </div>

      {actionError && <ConsoleError message={actionError} onRetry={bump} />}

      <SettingsCard
        icon={<PlugZap className="h-4 w-4 text-primary" />}
        title="Connected"
        count={<SettingsCount>{plural(connected.length, 'server')}</SettingsCount>}
        description="Live right now: governed by the gateway, connected in AgentWorks, or both. Expand a gateway server to inspect its tools."
      >
        {connected.length === 0 ? (
          <ConsoleEmpty>No connected servers match.</ConsoleEmpty>
        ) : (
          <div className="space-y-4">
            {groupedConnected.map((group) => (
              <div key={group.id}>
                <h4 className="mb-1 text-xs font-semibold text-foreground">{group.label}</h4>
                <div className="overflow-x-auto">
                  <table className={tableClass}>
                    <thead>
                      <tr>
                        <th className={thClass}>Server</th>
                        <th className={thClass}>AgentWorks</th>
                        <th className={thClass}>Gateway</th>
                      </tr>
                    </thead>
                    <tbody>
                      {group.rows.map((row) => {
                        const name = displayName(row)
                        const aw = row.agentworks ? statusIndicator(row.agentworks.connection, row.agentworks.status) : null
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
                            <td className={`${tdClass} whitespace-nowrap`}>
                              {row.agentworks && aw ? (
                                <span className="inline-flex items-center gap-1.5" title={aw.title}>
                                  <span className={`h-2 w-2 shrink-0 rounded-full ${aw.dot}`} aria-hidden />
                                  {row.agentworks.connection === 'connected'
                                    ? plural(row.agentworks.toolCount, 'tool')
                                    : aw.title}
                                </span>
                              ) : (
                                <span className="text-muted-foreground">—</span>
                              )}
                            </td>
                            <td className={tdClass}>
                              {row.gateway.length > 0 ? (
                                <span className="flex flex-col gap-1.5">
                                  {row.gateway.map((c) => {
                                    const tools = toolsByConnector.get(c.ID) ?? []
                                    const open = expanded.has(c.ID)
                                    return (
                                      <span key={c.ID} className="flex flex-col gap-1">
                                        <span className="inline-flex flex-wrap items-center gap-1.5">
                                          <button
                                            onClick={() =>
                                              setExpanded((prev) => {
                                                const next = new Set(prev)
                                                if (next.has(c.ID)) next.delete(c.ID)
                                                else next.add(c.ID)
                                                return next
                                              })
                                            }
                                            aria-expanded={open}
                                            aria-label={`${open ? 'Hide' : 'Show'} ${plural(tools.length, 'tool')} on ${c.Label || c.Provider}`}
                                            className="inline-flex items-center gap-1.5 rounded hover:bg-muted/60"
                                          >
                                            <ChevronDown
                                              className={`h-3.5 w-3.5 text-muted-foreground transition-transform ${open ? 'rotate-180' : ''}`}
                                              aria-hidden
                                            />
                                            <span className={`h-2 w-2 shrink-0 rounded-full ${gatewayStatusDot(c.Status)}`} aria-hidden />
                                            <span className={codeClass}>
                                              {c.Label || c.Provider}
                                              {c.InstanceSlug ? `:${c.InstanceSlug}` : ''}
                                            </span>
                                            <span className="whitespace-nowrap text-muted-foreground">
                                              {plural(tools.length, 'tool')}
                                            </span>
                                          </button>
                                          <Button
                                            variant="ghost"
                                            size="xs"
                                            disabled={syncing === c.ID}
                                            onClick={() => void onSync(c.ID)}
                                            aria-label={`Sync ${c.Label || c.Provider}`}
                                          >
                                            {syncing === c.ID ? <Loader2 className="animate-spin" /> : <RefreshCw />}
                                          </Button>
                                          <Button
                                            variant="ghost"
                                            size="xs"
                                            onClick={() => setDeleting(c)}
                                            aria-label={`Delete ${c.Label || c.Provider}`}
                                          >
                                            <Trash2 />
                                          </Button>
                                        </span>
                                        {open && <ToolList tools={tools} base={base} onApprove={onApprove} approving={approving} />}
                                      </span>
                                    )
                                  })}
                                </span>
                              ) : row.catalogMatch ? (
                                <Button
                                  variant="outline"
                                  size="xs"
                                  disabled={addingKey === row.key}
                                  onClick={() => void onAddToGateway(row)}
                                  data-testid={`gateway-add-${row.key}`}
                                >
                                  {addingKey === row.key ? <Loader2 className="animate-spin" /> : <Plus />}
                                  Add to gateway
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
              </div>
            ))}
          </div>
        )}
      </SettingsCard>

      {available.length > 0 && (
        <SettingsCard
          icon={<Server className="h-4 w-4 text-primary" />}
          title="Available to add"
          count={<SettingsCount>{plural(available.length, 'server')}</SettingsCount>}
          description="Known servers that are not connected anywhere yet. Add one to start governing it."
        >
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
                        {row.catalogMatch ? (
                          <Button
                            variant="outline"
                            size="xs"
                            disabled={addingKey === row.key}
                            onClick={() => void onAddToGateway(row)}
                            data-testid={`gateway-add-${row.key}`}
                          >
                            {addingKey === row.key ? <Loader2 className="animate-spin" /> : <Plus />}
                            Add to gateway
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
        </SettingsCard>
      )}

      <SettingsCard
        title="Add custom servers"
        description='Connect any Streamable-HTTP ("url") MCP server by name and URL. Only https URLs (or loopback http) can join the gateway.'
      >
        <div className="flex flex-wrap items-center gap-2">
          <Input
            aria-label="Custom server name"
            placeholder="Name (e.g. acme-notes)"
            value={customName}
            onChange={(e) => setCustomName(e.target.value)}
            className="max-w-56"
            data-testid="gateway-add-name"
          />
          <Input
            aria-label="Custom server URL"
            placeholder="https://acme.example.com/mcp"
            value={customUrl}
            onChange={(e) => setCustomUrl(e.target.value)}
            className="min-w-64 flex-1 font-mono text-xs sm:max-w-md"
            data-testid="gateway-add-url"
          />
          <Button size="sm" disabled={customBusy || !customName.trim() || !customUrl.trim()} onClick={() => void onAddCustom()} data-testid="gateway-add-custom-submit">
            {customBusy && <Loader2 className="animate-spin" />}
            Connect server
          </Button>
        </div>
        {customError && (
          <p className="text-destructive" role="alert">
            {customError}
          </p>
        )}
        <details>
          <summary className="cursor-pointer text-xs font-medium text-muted-foreground hover:text-foreground">
            Advanced: import JSON
          </summary>
          <div className="mt-2 space-y-2">
            <Textarea
              aria-label="MCP servers JSON"
              placeholder={JSON_PLACEHOLDER}
              value={jsonText}
              onChange={(e) => setJsonText(e.target.value)}
              rows={6}
              className="font-mono text-xs"
              data-testid="gateway-add-json"
            />
            {jsonError && (
              <p className="text-destructive" role="alert">
                {jsonError}
              </p>
            )}
            {jsonNotice && <p className="text-muted-foreground" role="status">{jsonNotice}</p>}
            <div>
              <Button size="sm" disabled={jsonBusy || !jsonText.trim()} onClick={() => void onAddJson()} data-testid="gateway-add-submit">
                {jsonBusy && <Loader2 className="animate-spin" />}
                Connect servers
              </Button>
            </div>
          </div>
        </details>
      </SettingsCard>

      <ConfirmationDialog
        isOpen={deleting !== null}
        onClose={() => setDeleting(null)}
        onConfirm={() => void onDelete()}
        title="Delete connection"
        message={`Remove "${deleting?.Label || deleting?.Provider}"? Its tools disappear from the gateway and every grant on them stops applying. You can reconnect the server later.`}
        confirmText="Delete"
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
    <span className="flex flex-col gap-1 py-1">
      <span className="inline-flex flex-wrap items-center gap-1.5">
        <span className={codeClass}>{tool.PublicName}</span>
        <span className="text-muted-foreground">{tool.UpstreamName}</span>
        <span className="text-xs text-muted-foreground">v{tool.Version} · {tool.Status}</span>
      </span>
      {tool.Description && <span className="max-w-2xl text-muted-foreground">{tool.Description}</span>}
      <ToolArgs tool={tool} />
      <span className="flex items-center gap-2">
        <Button variant="ghost" size="xs" onClick={() => open ? setOpen(false) : void openReview()}>
          {open ? 'Hide details' : 'Review details'}
        </Button>
        {tool.Status === 'quarantined' && (
          <Button size="xs" disabled={approving} onClick={() => void onApprove(tool)}>
            {approving && <Loader2 className="animate-spin" />}Approve v{tool.Version}
          </Button>
        )}
      </span>
      {open && (
        <span className="block space-y-2 rounded-md border border-border p-2 text-xs">
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
    </span>
  )
}

function ToolList({ tools, base, onApprove, approving }: {
  tools: GatewayTool[]
  base: string
  onApprove: (tool: GatewayTool) => Promise<void>
  approving: string | null
}) {
  if (tools.length === 0) return <span className="ml-5 text-muted-foreground">No tools discovered yet.</span>
  return (
    <span className="ml-5 flex flex-col gap-1 border-l border-border pl-2">
      {tools.map((t) => (
        <ToolReviewRow key={t.PublicName} tool={t} base={base} onApprove={onApprove} approving={approving === t.PublicName} />
      ))}
    </span>
  )
}

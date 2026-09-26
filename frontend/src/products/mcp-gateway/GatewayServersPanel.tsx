import { useMemo, useState } from 'react'
import { Loader2, Plus, RefreshCw, Server, Trash2 } from 'lucide-react'
import { SettingsCard, SettingsCount } from '../../components/ui/SettingsCard'
import { Button } from '../../components/ui/Button'
import { Input } from '../../components/ui/Input'
import ConfirmationDialog from '../../components/ui/ConfirmationDialog'
import ConnectionIcon from '../../components/connectors/ConnectionIcon'
import { brandSlugFor } from '../../components/connectors/brandSlug'
import { GROUP_ORDER, descriptionFor, groupFor, statusIndicator } from '../../components/connectors/catalog'
import { useMCPStore } from '../../stores/useMCPStore'
import type { ToolDefinition } from '../../stores/types'
import {
  createConnector,
  deleteConnector,
  listCatalog,
  listConnectors,
  listTools,
  syncConnector,
  type GatewayConnector,
} from './gatewayAdminApi'
import { ConsoleEmpty, ConsoleError, ConsoleLoading } from './gatewayConsoleShared'
import {
  codeClass,
  gatewayErrorMessage,
  mergeServerRows,
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

export function GatewayServersPanel({ base }: { base: string }) {
  const [attempt, bump] = useAttempt()
  const { data, loading, error } = useGatewayLoader(async () => {
    const [connectors, catalog, tools] = await Promise.all([listConnectors(base), listCatalog(base), listTools(base)])
    return { connectors: connectors.connectors, providers: catalog.providers, tools: tools.tools }
  }, attempt)
  const toolList = useMCPStore((state) => state.toolList)

  const [addingKey, setAddingKey] = useState<string | null>(null)
  const [syncing, setSyncing] = useState<string | null>(null)
  const [deleting, setDeleting] = useState<GatewayConnector | null>(null)
  const [deleteBusy, setDeleteBusy] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)
  const [customName, setCustomName] = useState('')
  const [customUrl, setCustomUrl] = useState('')
  const [customLabel, setCustomLabel] = useState('')
  const [customSlug, setCustomSlug] = useState('')
  const [customBusy, setCustomBusy] = useState(false)
  const [customError, setCustomError] = useState<string | null>(null)

  const rows = useMemo(() => {
    if (!data) return []
    return mergeServerRows(agentWorksServers(toolList), data.connectors, data.providers)
  }, [data, toolList])

  const grouped = useMemo(() => {
    const byGroup = new Map<string, ServerRow[]>()
    for (const row of rows) {
      const group = groupFor(displayName(row))
      const list = byGroup.get(group) ?? []
      list.push(row)
      byGroup.set(group, list)
    }
    return GROUP_ORDER.filter((g) => byGroup.has(g.id)).map((g) => ({ ...g, rows: byGroup.get(g.id) ?? [] }))
  }, [rows])

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
    if (!customName.trim()) {
      setCustomError('Name the custom provider.')
      return
    }
    if (!customUrl.trim()) {
      setCustomError('Enter the upstream MCP URL.')
      return
    }
    setCustomBusy(true)
    setCustomError(null)
    try {
      await createConnector(base, { Provider: customName.trim(), Label: customLabel, Slug: customSlug, URL: customUrl.trim() })
      setCustomName('')
      setCustomUrl('')
      setCustomLabel('')
      setCustomSlug('')
      bump()
    } catch (err: unknown) {
      setCustomError(gatewayErrorMessage(err))
    } finally {
      setCustomBusy(false)
    }
  }

  if (loading) return <ConsoleLoading label="Loading servers…" />
  if (error || !data) return <ConsoleError message={error ?? 'Failed to load.'} onRetry={bump} />

  const toolCounts = new Map<string, number>()
  for (const t of data.tools) toolCounts.set(t.ConnectorID, (toolCounts.get(t.ConnectorID) ?? 0) + 1)

  return (
    <div className="space-y-4" data-testid="gateway-servers">
      <SettingsCard
        icon={<Server className="h-4 w-4 text-primary" />}
        title="All servers"
        count={<SettingsCount>{`${rows.length} servers`}</SettingsCount>}
        description="Every MCP server across AgentWorks and the gateway, in one list. Add an AgentWorks server to the gateway to govern it for every client."
      >
        {actionError && <ConsoleError message={actionError} onRetry={bump} />}
        {rows.length === 0 ? (
          <ConsoleEmpty>No servers yet. Connect one in AgentWorks, or add a custom URL below.</ConsoleEmpty>
        ) : (
          <div className="space-y-5">
            {grouped.map((group) => (
              <div key={group.id}>
                <h4 className="mb-1 text-xs font-semibold text-foreground">{group.label}</h4>
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
                            <span className="flex items-start gap-2">
                              <ConnectionIcon icon={brandSlugFor(name)} name={name} size="xs" />
                              <span>
                                <span className="block font-medium text-foreground">{name}</span>
                                <span className="block max-w-72 truncate text-muted-foreground" title={descriptionFor(name)}>
                                  {descriptionFor(name)}
                                </span>
                              </span>
                            </span>
                          </td>
                          <td className={tdClass}>
                            {row.agentworks && aw ? (
                              <span className="inline-flex items-center gap-1.5" title={aw.title}>
                                <span className={`h-2 w-2 shrink-0 rounded-full ${aw.dot}`} aria-hidden />
                                {row.agentworks.connection === 'connected'
                                  ? `${row.agentworks.toolCount} tools`
                                  : aw.title}
                              </span>
                            ) : (
                              <span className="text-muted-foreground">—</span>
                            )}
                          </td>
                          <td className={tdClass}>
                            {row.gateway.length > 0 ? (
                              <span className="flex flex-col gap-1.5">
                                {row.gateway.map((c) => (
                                  <span key={c.ID} className="inline-flex flex-wrap items-center gap-1.5">
                                    <span className={`h-2 w-2 shrink-0 rounded-full ${gatewayStatusDot(c.Status)}`} aria-hidden />
                                    <span className={codeClass}>
                                      {c.Label || c.Provider}
                                      {c.InstanceSlug ? `:${c.InstanceSlug}` : ''}
                                    </span>
                                    <span className="text-muted-foreground">{toolCounts.get(c.ID) ?? 0} tools</span>
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
                                ))}
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
            ))}
          </div>
        )}
      </SettingsCard>

      <SettingsCard
        title="Add a custom server"
        description="Any Streamable-HTTP MCP URL. Servers from the catalog are added from the list above."
      >
        <div className="grid grid-cols-2 gap-2">
          <Input aria-label="Provider name" placeholder="Provider name (e.g. acme-notes)" value={customName} onChange={(e) => setCustomName(e.target.value)} />
          <Input aria-label="Upstream URL" placeholder="https://…/mcp" value={customUrl} onChange={(e) => setCustomUrl(e.target.value)} data-testid="gateway-add-url" />
          <Input aria-label="Label" placeholder="Label (defaults to provider)" value={customLabel} onChange={(e) => setCustomLabel(e.target.value)} />
          <Input aria-label="Instance slug" placeholder="Instance slug (optional)" value={customSlug} onChange={(e) => setCustomSlug(e.target.value)} />
        </div>
        {customError && (
          <p className="text-destructive" role="alert">
            {customError}
          </p>
        )}
        <div>
          <Button size="sm" disabled={customBusy} onClick={() => void onAddCustom()} data-testid="gateway-add-submit">
            {customBusy && <Loader2 className="animate-spin" />}
            Connect server
          </Button>
        </div>
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

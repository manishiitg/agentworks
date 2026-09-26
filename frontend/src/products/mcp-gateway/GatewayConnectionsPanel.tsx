import { useState } from 'react'
import { Loader2, PlugZap, RefreshCw, Trash2 } from 'lucide-react'
import { SettingsCard, SettingsCount } from '../../components/ui/SettingsCard'
import { Button } from '../../components/ui/Button'
import { Input } from '../../components/ui/Input'
import ConfirmationDialog from '../../components/ui/ConfirmationDialog'
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
  tableClass,
  tdClass,
  thClass,
  useAttempt,
  useGatewayLoader,
} from './gatewayConsoleUtils'

function statusDot(status: string): string {
  if (status === 'active') return 'bg-green-500'
  if (status === 'quarantined') return 'bg-red-500'
  return 'bg-gray-400'
}

const selectClass =
  'h-8 rounded-md border border-input bg-background px-2 text-xs shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring'

export function GatewayConnectionsPanel({ base }: { base: string }) {
  const [attempt, bump] = useAttempt()
  const { data, loading, error } = useGatewayLoader(async () => {
    const [connectors, catalog, tools] = await Promise.all([
      listConnectors(base),
      listCatalog(base),
      listTools(base),
    ])
    return { connectors: connectors.connectors, providers: catalog.providers, tools: tools.tools }
  }, attempt)

  const [mode, setMode] = useState<'catalog' | 'custom'>('catalog')
  const [provider, setProvider] = useState('')
  const [customName, setCustomName] = useState('')
  const [label, setLabel] = useState('')
  const [slug, setSlug] = useState('')
  const [url, setUrl] = useState('')
  const [adding, setAdding] = useState(false)
  const [addError, setAddError] = useState<string | null>(null)
  const [syncing, setSyncing] = useState<string | null>(null)
  const [syncError, setSyncError] = useState<string | null>(null)
  const [deleting, setDeleting] = useState<GatewayConnector | null>(null)
  const [deleteBusy, setDeleteBusy] = useState(false)

  async function onAdd() {
    setAdding(true)
    setAddError(null)
    try {
      if (mode === 'catalog') {
        if (!provider) throw new Error('Pick a provider from the catalog.')
        await createConnector(base, { Provider: provider, Label: label, Slug: slug, URL: '' })
      } else {
        if (!customName.trim()) throw new Error('Name the custom provider.')
        if (!url.trim()) throw new Error('Enter the upstream MCP URL.')
        await createConnector(base, { Provider: customName.trim(), Label: label, Slug: slug, URL: url.trim() })
      }
      setProvider('')
      setCustomName('')
      setLabel('')
      setSlug('')
      setUrl('')
      bump()
    } catch (err: unknown) {
      setAddError(gatewayErrorMessage(err))
    } finally {
      setAdding(false)
    }
  }

  async function onSync(id: string) {
    setSyncing(id)
    setSyncError(null)
    try {
      await syncConnector(base, id)
      bump()
    } catch (err: unknown) {
      setSyncError(gatewayErrorMessage(err))
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
      setSyncError(gatewayErrorMessage(err))
      setDeleting(null)
    } finally {
      setDeleteBusy(false)
    }
  }

  if (loading) return <ConsoleLoading label="Loading connections…" />
  if (error || !data) return <ConsoleError message={error ?? 'Failed to load.'} onRetry={bump} />

  const toolCounts = new Map<string, number>()
  for (const t of data.tools) toolCounts.set(t.ConnectorID, (toolCounts.get(t.ConnectorID) ?? 0) + 1)

  return (
    <div className="space-y-4" data-testid="gateway-connections">
      <SettingsCard
        icon={<PlugZap className="h-4 w-4 text-primary" />}
        title="Connected servers"
        count={<SettingsCount>{`${data.connectors.length} connected`}</SettingsCount>}
        description="Each connection is one upstream MCP server instance. Tools are discovered on connect and re-synced on demand."
      >
        {syncError && <ConsoleError message={syncError} onRetry={bump} />}
        {data.connectors.length === 0 ? (
          <ConsoleEmpty>No servers connected yet. Add one below.</ConsoleEmpty>
        ) : (
          <table className={tableClass}>
            <thead>
              <tr>
                <th className={thClass}>Label</th>
                <th className={thClass}>Provider</th>
                <th className={thClass}>Upstream</th>
                <th className={thClass}>Tools</th>
                <th className={thClass}>Status</th>
                <th className={thClass}>
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {data.connectors.map((c) => (
                <tr key={c.ID}>
                  <td className={tdClass}>
                    <span className="font-medium text-foreground">{c.Label || c.Provider}</span>
                    {c.InstanceSlug && <span className={codeClass}> {c.InstanceSlug}</span>}
                  </td>
                  <td className={tdClass}>
                    <span className={codeClass}>{c.Provider}</span>
                  </td>
                  <td className={`${tdClass} max-w-64 truncate font-mono text-[11px]`} title={c.UpstreamURL}>
                    {c.UpstreamURL}
                  </td>
                  <td className={tdClass}>{toolCounts.get(c.ID) ?? 0}</td>
                  <td className={tdClass}>
                    <span className="inline-flex items-center gap-1.5">
                      <span className={`h-2 w-2 rounded-full ${statusDot(c.Status)}`} aria-hidden />
                      {c.Status}
                    </span>
                  </td>
                  <td className={`${tdClass} whitespace-nowrap text-right`}>
                    <Button
                      variant="ghost"
                      size="xs"
                      disabled={syncing === c.ID}
                      onClick={() => void onSync(c.ID)}
                      aria-label={`Sync ${c.Label || c.Provider}`}
                    >
                      {syncing === c.ID ? <Loader2 className="animate-spin" /> : <RefreshCw />}
                      Sync
                    </Button>
                    <Button variant="ghost" size="xs" onClick={() => setDeleting(c)} aria-label={`Delete ${c.Label || c.Provider}`}>
                      <Trash2 />
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </SettingsCard>

      <SettingsCard
        title="Add a server"
        description="Connect a catalog provider, or paste any Streamable-HTTP MCP URL as a custom server."
      >
        <div className="flex gap-1 rounded-md bg-muted p-1 text-xs" role="tablist" aria-label="Add mode">
          {(['catalog', 'custom'] as const).map((m) => (
            <button
              key={m}
              role="tab"
              aria-selected={mode === m}
              onClick={() => setMode(m)}
              className={`rounded px-3 py-1 font-medium capitalize ${
                mode === m ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'
              }`}
            >
              {m === 'catalog' ? 'Catalog' : 'Custom URL'}
            </button>
          ))}
        </div>
        <div className="grid grid-cols-2 gap-2">
          {mode === 'catalog' ? (
            <select
              aria-label="Provider"
              className={`${selectClass} col-span-2`}
              value={provider}
              onChange={(e) => setProvider(e.target.value)}
              data-testid="gateway-add-provider"
            >
              <option value="">Pick a provider… ({data.providers.length} available)</option>
              {data.providers.map((p) => (
                <option key={p.Key} value={p.Name}>
                  {p.Name}
                  {p.OAuth ? ' (OAuth)' : ''}
                </option>
              ))}
            </select>
          ) : (
            <>
              <Input
                aria-label="Provider name"
                placeholder="Provider name (e.g. acme-notes)"
                value={customName}
                onChange={(e) => setCustomName(e.target.value)}
              />
              <Input
                aria-label="Upstream URL"
                placeholder="https://…/mcp"
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                data-testid="gateway-add-url"
              />
            </>
          )}
          <Input aria-label="Label" placeholder="Label (defaults to provider)" value={label} onChange={(e) => setLabel(e.target.value)} />
          <Input
            aria-label="Instance slug"
            placeholder="Instance slug (optional, e.g. acme)"
            value={slug}
            onChange={(e) => setSlug(e.target.value)}
          />
        </div>
        {addError && (
          <p className="text-destructive" role="alert">
            {addError}
          </p>
        )}
        <div>
          <Button size="sm" disabled={adding} onClick={() => void onAdd()} data-testid="gateway-add-submit">
            {adding && <Loader2 className="animate-spin" />}
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

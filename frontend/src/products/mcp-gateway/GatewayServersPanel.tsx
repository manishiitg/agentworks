import { useVaultSetupDraft } from '../../components/integrations/useVaultSetupDraft'
import { useEffect, useMemo, useRef, useState } from 'react'
import { KeyRound, Plus, RefreshCw, Trash2 } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import ConfirmationDialog from '../../components/ui/ConfirmationDialog'
import { McpConnectionsPanel, type McpConnectionRow, type McpCatalogRow } from '../../components/integrations/McpConnectionsPanel'
import { McpNamedConnectionForm } from '../../components/integrations/McpConnectionForms'
import { descriptionFor, groupFor, statusIndicator } from '../../components/connectors/catalog'
import { useMCPStore } from '../../stores/useMCPStore'
import type { ToolDefinition, ToolDetail } from '../../stores/types'
import { approveTool, listCatalog, listConnectors, listTools, syncConnector, type GatewayConnector, type GatewayTool } from './gatewayAdminApi'
import { gatewayErrorMessage, mergeServerRows, plural, useAttempt, useGatewayLoader, type AgentWorksServer, type ServerRow } from './gatewayConsoleUtils'
import { GatewayToolReviewActions } from './GatewayToolReviewActions'
import { useVaultReadOnly } from './vaultReadOnly'

function displayName(row: ServerRow): string {
  return row.agentworks?.name ?? row.catalogMatch?.Name ?? row.gateway[0]?.Label ?? row.name
}

function gatewayStatusDot(status: string): string {
  if (status === 'active') return 'bg-green-500'
  if (status === 'quarantined') return 'bg-red-500'
  return 'bg-gray-400'
}

export function GatewayServersPanel({ base, standalone = false, view = 'connected', onConnected, onBrowse, onAddCustom, revision, chatSessionId }: {
  base: string
  standalone?: boolean
  view?: 'connected' | 'available'
  onConnected?: () => void
  onBrowse?: () => void
  onAddCustom?: () => Promise<void>
  revision?: string
  chatSessionId?: string
}) {
  const readOnly = useVaultReadOnly()
  const setup = useVaultSetupDraft()
  const [attempt, bump] = useAttempt()
  const { data, loading, error } = useGatewayLoader(async () => {
    const [connectors, catalog, tools] = await Promise.all([listConnectors(base), listCatalog(base), listTools(base)])
    return { connectors: connectors.connectors, providers: catalog.providers, tools: tools.tools, access: new Map((tools.access ?? []).map(row => [row.public_name, row])) }
  }, attempt)
  const toolList = useMCPStore((state) => state.toolList)
  const refreshTools = useMCPStore((state) => state.refreshTools)
  const agentWorksLoading = useMCPStore((state) => state.isLoadingTools)
  const agentWorksError = useMCPStore((state) => state.toolsError)

  useEffect(() => {
    if (!standalone) void refreshTools()
  }, [refreshTools, standalone])

  const [namingKey, setNamingKey] = useState<string | null>(null)
  const [addingKey, setAddingKey] = useState<string | null>(null)
  const [syncing, setSyncing] = useState<string | null>(null)
  const [approving, setApproving] = useState<string | null>(null)
  const [deleting, setDeleting] = useState<GatewayConnector | null>(null)
  const [deleteBusy, setDeleteBusy] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)
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
    return mergeServerRows([], data.connectors, data.providers)
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

  async function onAddToGateway(row: ServerRow, label: string) {
    if (!row.catalogMatch) return
    setAddingKey(row.key)
    setActionError(null)
    try {
      await setup.open(`Connect ${row.catalogMatch.Name} in Vault with account label ${label}. Use manage_vault_access connect_server, guide required credentials in chat, return a real sign-in link and verify discovery. Preserve existing accounts and grants.`)
      setNamingKey(null)
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
      await setup.open(`Disconnect Vault connection ${deleting.ID} because I want to remove it. Use the existing Vault tools and preserve unrelated accounts and permissions.`)
      setDeleting(null)
      bump()
    } catch (err: unknown) {
      setActionError(gatewayErrorMessage(err))
      setDeleting(null)
    } finally {
      setDeleteBusy(false)
    }
  }

  const servers: McpConnectionRow[] = rows.flatMap(row => {
    const central = row.gateway.map(c => {
      const tools = toolsByConnector.get(c.ID) ?? []
      const name = c.Label || displayName(row)
      const needsReview = tools.filter(tool => tool.Status === 'quarantined').length
      const approved = tools.filter(tool => tool.Status === 'active').length
      return {
        id: c.ID, name, provider: row.catalogMatch?.Name ?? c.Provider, source: c.OAuthCredentialID ? c.OAuthServer : undefined, status: c.Status === 'active' ? 'Connected' : c.Status === 'disabled' ? 'Disabled' : c.Status === 'quarantined' ? 'Connection needs review' : c.Status === 'authentication_required' ? 'Sign-in required' : c.Status,
        statusDot: gatewayStatusDot(c.Status), toolCount: c.Status === 'authentication_required' ? undefined : tools.length,
        controls: !readOnly && c.Status === 'authentication_required' ? <Button size="sm" variant="outline" onClick={()=>void setup.open(`Finish Vault sign-in for connection ${c.ID}. Guide OAuth app setup in chat if needed and return the real provider link.`)}>Finish setup in chat</Button> : undefined,
        toolsLabel: (open: boolean) => `${open ? 'Hide' : 'Show'} ${plural(tools.length, 'tool')} on ${name}`,
        tools: c.Status === 'authentication_required' && tools.length === 0 ? undefined : tools.map(tool => ({ id: tool.PublicName, name: tool.UpstreamName, description: tool.Description, schema: tool.InputSchema,
          status: tool.Status === 'quarantined' ? 'Needs review' : tool.Status === 'active' ? 'Approved' : tool.Status === 'disabled' ? 'Disabled' : tool.Status,
          details: <GatewayToolReviewActions tool={tool} base={base} onApprove={onApprove} approving={approving === tool.PublicName}
            access={data?.access.get(tool.PublicName)?.access} label={data?.access.get(tool.PublicName)?.label} onAccessChanged={bump} />,
        })),
        toolsNotice: <>{needsReview > 0 && <p className="text-muted-foreground">{needsReview} need review. Tools changed since connection. Review before approving. Group access is assigned separately.</p>}{approved > 0 && <p className="text-muted-foreground">{approved} approved</p>}</>,
        actions: readOnly ? [] : [
          { label: 'Refresh tool list', ariaLabel: `Sync ${name}`, icon: <RefreshCw />, disabled: syncing === c.ID, run: () => onSync(c.ID) },
          { label: 'Manage in chat', icon: <KeyRound />, run: () => setup.open(`Manage Vault connection ${c.ID}. Help me update its credentials or sign in again through the current Vault tools. Never repeat credential values.`) },
          { label: 'Disconnect server', icon: <Trash2 />, destructive: true, run: () => setDeleting(c) },
        ],

      }
    })
    return central
  })
  const catalog: McpCatalogRow[] = rows.filter(row => row.catalogMatch).map(row => ({
    id: row.key, name: row.catalogMatch!.Name, description: descriptionFor(row.catalogMatch!.Name), category: groupFor(row.catalogMatch!.Name), testId: `gateway-add-${row.key}`,
    connect: namingKey === row.key ? undefined : { label: 'Add connection', disabled: addingKey === row.key, run: () => setNamingKey(row.key) },
    details: namingKey === row.key ? <McpNamedConnectionForm provider={row.catalogMatch!.Name} busy={addingKey === row.key} cancel={() => setNamingKey(null)} submit={name => onAddToGateway(row, name)} /> : undefined,
  }))
  return <McpConnectionsPanel servers={servers} catalog={readOnly ? [] : catalog} view={view} loading={loading} refresh={bump} searchTestId="gateway-servers-search"
    density="directory" description={view === 'connected' ? 'Apps and tools your agents can use.' : 'Choose an app or MCP your agents can use.'}
    headerActions={view === 'connected' && !readOnly && onBrowse ? <Button size="sm" onClick={onBrowse}><Plus />Connect MCP</Button> : undefined}
    notices={[
      ...(error ? [{ message: data ? `Could not refresh: ${error}. Saved data may be outdated.` : error, retry: bump }] : []),
      ...((actionError||setup.error) ? [{ message: actionError || setup.error, retry: bump }] : []),
      ...(!standalone && agentWorksError ? [{ message: `This place's connections could not be refreshed: ${agentWorksError}`, retry: () => void refreshTools() }] : []),
    ]}
    addCustom={readOnly ? undefined : { label: 'Add custom server', disabled: !onAddCustom || chatRequestBusy, run: requestCustomServer }}>
    <ConfirmationDialog isOpen={deleting !== null} onClose={() => setDeleting(null)} onConfirm={() => void onDelete()} title="Disconnect server"
      message={`Disconnect "${deleting?.Label || deleting?.Provider}" from Vault? Its tools will become unavailable, and its group permissions will be removed. Reconnecting requires assigning permissions again.`}
      confirmText="Disconnect" loadingText="Disconnecting…" isLoading={deleteBusy} />
  </McpConnectionsPanel>
}

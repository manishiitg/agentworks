import { useEffect, useMemo, useRef, useState } from 'react'
import { KeyRound, RefreshCw, Trash2 } from 'lucide-react'
import OAuthStatusBadge from '../../components/OAuthStatusBadge'
import ConfirmationDialog from '../../components/ui/ConfirmationDialog'
import { McpConnectionsPanel, type McpConnectionRow, type McpCatalogRow } from '../../components/integrations/McpConnectionsPanel'
import { McpCredentialSettings, McpNamedConnectionForm } from '../../components/integrations/McpConnectionForms'
import { descriptionFor, groupFor, statusIndicator } from '../../components/connectors/catalog'
import { useMCPStore } from '../../stores/useMCPStore'
import { agentApi } from '../../services/api'
import type { ToolDefinition, ToolDetail } from '../../stores/types'
import { createConnector, approveTool, deleteConnector, listCatalog, listConnectors, listTools, syncConnector, setConnectorBearer, type GatewayConnector, type GatewayTool } from './gatewayAdminApi'
import { gatewayErrorMessage, mergeServerRows, plural, useAttempt, useGatewayLoader, type AgentWorksServer, type ServerRow } from './gatewayConsoleUtils'
import { GatewayToolReviewActions } from './GatewayToolReviewActions'
import { useVaultReadOnly } from './vaultReadOnly'

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

export function GatewayServersPanel({ base, standalone = false, view = 'connected', onConnected, onAddCustom, revision, chatSessionId }: {
  base: string
  standalone?: boolean
  view?: 'connected' | 'available'
  onConnected?: () => void
  onAddCustom?: () => Promise<void>
  revision?: string
  chatSessionId?: string
}) {
  const readOnly = useVaultReadOnly()
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

  async function onAddToGateway(row: ServerRow, label: string) {
    if (!row.catalogMatch) return
    setAddingKey(row.key)
    setActionError(null)
    try {
      await createConnector(base, { Provider: row.catalogMatch.Name, Label: label, Slug: '', URL: '' })
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

  const servers: McpConnectionRow[] = rows.flatMap(row => {
    const central = row.gateway.map(c => {
      const tools = toolsByConnector.get(c.ID) ?? []
      const name = c.Label || displayName(row)
      const needsReview = tools.filter(tool => tool.Status === 'quarantined').length
      const approved = tools.filter(tool => tool.Status === 'active').length
      return {
        id: c.ID, name, source: c.OAuthCredentialID ? `Vault · ${c.OAuthServer}` : 'Vault', status: c.Status === 'active' ? 'Connected' : c.Status === 'disabled' ? 'Disabled' : c.Status === 'quarantined' ? 'Connection needs review' : c.Status === 'authentication_required' ? 'Sign-in required' : c.Status,
        statusDot: gatewayStatusDot(c.Status), toolCount: c.Status === 'authentication_required' ? undefined : tools.length,
        controls: !readOnly && c.Status === 'authentication_required' && c.OAuthServer ? <OAuthStatusBadge chatSessionId={chatSessionId} scope="vault" serverName={c.OAuthServer} connectionId={c.OAuthCredentialID} requiresOAuth connection="available" connectLabel="Sign in" onAuthChange={valid => { if (valid) bump() }} /> : undefined,
        toolsLabel: (open: boolean) => `${open ? 'Hide' : 'Show'} ${plural(tools.length, 'tool')} on ${name}`,
        tools: c.Status === 'authentication_required' && tools.length === 0 ? undefined : tools.map(tool => ({ id: tool.PublicName, name: tool.UpstreamName, description: tool.Description, schema: tool.InputSchema,
          status: tool.Status === 'quarantined' ? 'Needs review' : tool.Status === 'active' ? 'Approved' : tool.Status === 'disabled' ? 'Disabled' : tool.Status,
          details: <GatewayToolReviewActions tool={tool} base={base} onApprove={onApprove} approving={approving === tool.PublicName}
            access={data?.access.get(tool.PublicName)?.access} label={data?.access.get(tool.PublicName)?.label} onAccessChanged={bump} />,
        })),
        toolsNotice: <>{needsReview > 0 && <p className="text-muted-foreground">{needsReview} need review. Tools changed since connection. Review before approving. Group access is assigned separately.</p>}{approved > 0 && <p className="text-muted-foreground">{approved} approved</p>}</>,
        actions: readOnly ? [] : [
          { label: 'Refresh tool list', ariaLabel: `Sync ${name}`, icon: <RefreshCw />, disabled: syncing === c.ID, run: () => onSync(c.ID) },
          { label: 'Connection settings', icon: <KeyRound />, run: () => { setCredentialFor(c.ID); setCredentialValue('') } },
          { label: 'Disconnect server', icon: <Trash2 />, destructive: true, run: () => setDeleting(c) },
        ],
        settings: credentialFor === c.ID ? <McpCredentialSettings name={name} value={credentialValue} change={setCredentialValue} save={() => void onRotateCredential(c.ID)} busy={credentialBusy}
          close={() => { setCredentialFor(null); setCredentialValue('') }}
          oauthControl={c.OAuthServer ? <OAuthStatusBadge chatSessionId={chatSessionId} scope="vault" serverName={c.OAuthServer} connectionId={c.OAuthCredentialID} requiresOAuth connection="available" connectLabel="Sign in again" onAuthChange={valid => { if (valid) { if (c.OAuthCredentialID) bump(); else void onSync(c.ID) } }} /> : undefined} /> : undefined,
      }
    })
    const personal = !standalone && row.agentworks?.connection === 'connected' ? [{
      id: `private:${row.key}`, name: row.agentworks.name, source: 'This place', status: row.agentworks.status === 'not_loaded' ? 'Connected · Tools not loaded' : statusIndicator(row.agentworks.connection, row.agentworks.status).title,
      toolCount: row.agentworks.status === 'not_loaded' ? undefined : row.agentworks.toolCount,
      toolsLabel: (open: boolean) => `${open ? 'Hide' : 'Show'} AgentWorks tools on ${row.agentworks!.name}`,
      loadTools: async () => {
        const result = await agentApi.getToolDetail(row.agentworks!.name)
        if (result.status === 'error' || result.status === 'not_connected') throw new Error(/authorization required|unauthorized|oauth/i.test(result.error || '') ? 'Authorization is required to load this server’s tools.' : result.error || 'Could not discover tools.')
        return (result.tools ?? []).map((tool: ToolDetail) => ({ id: tool.name, name: tool.name, description: tool.description, rawSchema: tool.parameters ? { type: 'object', properties: tool.parameters, ...(tool.required ? { required: tool.required } : {}) } : undefined }))
      },
    }] : []
    return [...central, ...personal]
  })
  const catalog: McpCatalogRow[] = rows.filter(row => row.catalogMatch).map(row => ({
    id: row.key, name: row.catalogMatch!.Name, description: descriptionFor(row.catalogMatch!.Name), category: groupFor(row.catalogMatch!.Name), testId: `gateway-add-${row.key}`,
    connect: namingKey === row.key ? undefined : { label: 'Add connection', disabled: addingKey === row.key, run: () => setNamingKey(row.key) },
    details: namingKey === row.key ? <McpNamedConnectionForm provider={row.catalogMatch!.Name} busy={addingKey === row.key} cancel={() => setNamingKey(null)} submit={name => onAddToGateway(row, name)} /> : undefined,
  }))
  return <McpConnectionsPanel servers={servers} catalog={readOnly ? [] : catalog} view={view} loading={loading} refresh={bump} searchTestId="gateway-servers-search"
    notices={[
      ...(error ? [{ message: data ? `Could not refresh: ${error}. Saved data may be outdated.` : error, retry: bump }] : []),
      ...(actionError ? [{ message: actionError, retry: bump }] : []),
      ...(!standalone && agentWorksError ? [{ message: `This place's connections could not be refreshed: ${agentWorksError}`, retry: () => void refreshTools() }] : []),
    ]}
    addCustom={readOnly ? undefined : { label: 'Add custom server', disabled: !onAddCustom || chatRequestBusy, run: requestCustomServer }}>
    <ConfirmationDialog isOpen={deleting !== null} onClose={() => setDeleting(null)} onConfirm={() => void onDelete()} title="Disconnect server"
      message={`Disconnect "${deleting?.Label || deleting?.Provider}" from Vault? Its tools will become unavailable, and its group permissions will be removed. Reconnecting requires assigning permissions again.`}
      confirmText="Disconnect" loadingText="Disconnecting…" isLoading={deleteBusy} />
  </McpConnectionsPanel>
}

import { McpConnectionsPanel } from './McpConnectionsPanel'
import { usePlaceMcpConnections } from './usePlaceMcpConnections'
import { useVaultMcpConnections } from './useVaultMcpConnections'

/** Project adapter. All MCP presentation is owned by McpConnectionsPanel. */
export function ProjectMcpPanel({ workspacePath, placeNoun, canEdit, onAsk, chatSessionId, view }: {
  view?: 'connected' | 'available'
  chatSessionId?: string
  workspacePath: string; placeNoun: string; canEdit: boolean; onAsk?: (message: string) => Promise<void>
  selectedServers: string[]; onSelectedServersChange: (servers: string[]) => Promise<unknown> | void
}) {
  const place = usePlaceMcpConnections({ workspacePath, placeNoun, canEdit, onAsk, chatSessionId })
  const vault = useVaultMcpConnections()
  const projectLabel = placeNoun.charAt(0).toUpperCase() + placeNoun.slice(1)
  return <McpConnectionsPanel view={view} servers={[
    ...place.servers.map(server => ({ ...server, sectionId: 'project' })),
    ...vault.servers.map(server => ({ ...server, sectionId: 'vault' })),
  ]} catalog={place.catalog} showTools={false}
    connectionSections={[{ id: 'project', label: `${projectLabel} MCPs`, loading: place.loading }, { id: 'vault', label: 'Vault MCPs', loading: vault.loading }]}
    loading={place.loading || vault.loading} notices={[...place.notices, ...vault.notices]}
    refresh={() => { void place.refresh(); void vault.refresh() }} addCustom={place.addCustom} help={place.help}>
    {place.dialogs}
  </McpConnectionsPanel>
}

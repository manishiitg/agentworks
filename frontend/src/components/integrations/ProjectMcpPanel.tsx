import { McpConnectionsPanel } from './McpConnectionsPanel'
import { usePlaceMcpConnections } from './usePlaceMcpConnections'

/** Project adapter. All MCP presentation is owned by McpConnectionsPanel. */
export function ProjectMcpPanel({ workspacePath, placeNoun, canEdit, onAsk, chatSessionId, view }: {
  view?: 'connected' | 'available'
  chatSessionId?: string
  workspacePath: string; placeNoun: string; canEdit: boolean; onAsk?: (message: string) => Promise<void>
  selectedServers: string[]; onSelectedServersChange: (servers: string[]) => Promise<unknown> | void
}) {
  const place = usePlaceMcpConnections({ workspacePath, placeNoun, canEdit, onAsk, chatSessionId })
  return <McpConnectionsPanel view={view} servers={place.servers} catalog={place.catalog} showTools={false}
    loading={place.loading} notices={place.notices}
    refresh={() => { void place.refresh() }} addCustom={place.addCustom} help={place.help}>
    {place.dialogs}
  </McpConnectionsPanel>
}

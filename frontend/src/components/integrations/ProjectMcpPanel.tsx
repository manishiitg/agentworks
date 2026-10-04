import { McpConnectionsPanel } from './McpConnectionsPanel'
import { usePrivateMcpConnections } from './usePrivateMcpConnections'

/** Project adapter. All MCP presentation is owned by McpConnectionsPanel. */
export function ProjectMcpPanel({ workspacePath, placeNoun, canEdit, onAsk, chatSessionId, view }: {
  view?: 'connected' | 'available'
  chatSessionId?: string
  workspacePath: string; placeNoun: string; canEdit: boolean; onAsk?: (message: string) => Promise<void>
  selectedServers: string[]; onSelectedServersChange: (servers: string[]) => Promise<unknown> | void
}) {
  const personal = usePrivateMcpConnections({ workspacePath, placeNoun, canEdit, onAsk, chatSessionId })
  return <McpConnectionsPanel view={view} servers={personal.servers} catalog={personal.catalog} showTools={false}
    loading={personal.loading} notices={personal.notices}
    refresh={() => { void personal.refresh() }} addCustom={personal.addCustom} help={personal.help}>
    {personal.dialogs}
  </McpConnectionsPanel>
}

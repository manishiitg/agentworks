import { OpenVaultButton } from './OpenVaultButton'
import { McpConnectionsPanel } from './McpConnectionsPanel'
import { usePrivateMcpConnections } from './usePrivateMcpConnections'
import { useVaultMcpConnections } from './useVaultMcpConnections'

/** Project adapter. All MCP presentation is owned by McpConnectionsPanel. */
export function ProjectMcpPanel({ workspacePath, placeNoun, canEdit, onAsk, selectedServers, onSelectedServersChange }: {
  workspacePath: string; placeNoun: string; canEdit: boolean; onAsk?: (message: string) => Promise<void>
  selectedServers: string[]; onSelectedServersChange: (servers: string[]) => Promise<unknown> | void
}) {
  const personal = usePrivateMcpConnections({ workspacePath, placeNoun, canEdit, onAsk })
  const vault = useVaultMcpConnections({ selectedServers, onSelectedServersChange, disabled: !canEdit })
  return <McpConnectionsPanel servers={[...personal.servers, ...vault.servers]} catalog={personal.catalog} showTools={false} headerActions={<OpenVaultButton panel="servers"/>}
    loading={personal.loading || vault.loading} notices={[...personal.notices, ...vault.notices]}
    refresh={() => { void personal.refresh(); void vault.refresh() }} addCustom={personal.addCustom} help={personal.help}>
    {personal.dialogs}
  </McpConnectionsPanel>
}

import { useEffect, useState } from 'react'
import { Button } from './ui/Button'
import { useMCPStore } from '../stores'
import { agentApi } from '../services/api'
import { McpConnectionsPanel, type McpConnectionRow } from './integrations/McpConnectionsPanel'
import { useVaultMcpConnections } from './integrations/useVaultMcpConnections'
import { descriptionFor } from './connectors/catalog'
import type { ToolDetail } from '../stores/types'
import MCPToolApiTester from './MCPToolApiTester'
import { OAuthStatusBadge } from './OAuthStatusBadge'

interface MCPDetailsModalProps {
  onClose: () => void; onOpenConfigEditor: () => void
  selectedServers: string[]; onSelectedServersChange: (servers: string[]) => void
}

/** Ordinary-chat adapter for the same MCP browser used by all products. */
export default function MCPDetailsModal({ onClose, onOpenConfigEditor, selectedServers, onSelectedServersChange }: MCPDetailsModalProps) {
  const inventory = useMCPStore()
  const [testTool, setTestTool] = useState<{ serverName: string; tool: ToolDetail } | null>(null)
  const vault = useVaultMcpConnections()
  useEffect(() => { void inventory.refreshTools?.() }, [inventory.refreshTools])
  const groups = Object.entries(inventory.getServerGroups()).filter(([name]) => !name.startsWith('vault_'))
  const toggle = (name: string) => onSelectedServersChange(selectedServers.includes(name) ? selectedServers.filter(item => item !== name && item !== 'NO_SERVERS') : [...selectedServers.filter(item => item !== 'NO_SERVERS'), name])
  const personal: McpConnectionRow[] = groups.filter(([, entries]) => entries[0].connection === 'connected').map(([name, entries]) => ({
    id: `private:${name}`, name, source: 'Your connection', status: 'Connected', statusDot: 'bg-green-500', toolCount: entries[0].function_names?.length,
    selection: { label: `Use ${name}`, checked: selectedServers.includes(name), change: () => toggle(name) },
    controls: <OAuthStatusBadge scope="private" serverName={name} connection={entries[0].connection} onAuthChange={() => { void inventory.refreshTools?.() }} />,
    loadTools: async () => {
      const detail = await agentApi.getToolDetail(name)
      if (detail.status !== 'ok') throw new Error(detail.error || 'Could not load tools.')
      return (detail.tools ?? []).map((tool: ToolDetail) => ({ id: tool.name, name: tool.name, description: tool.description,
        rawSchema: tool.parameters ? { type: 'object', properties: tool.parameters, ...(tool.required ? { required: tool.required } : {}) } : undefined,
        details: <Button size="xs" variant="ghost" onClick={() => setTestTool({ serverName: name, tool })}>Test tool</Button>,
      }))
    },
  }))
  const configure = () => { onClose(); onOpenConfigEditor() }
  return <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4" role="dialog" aria-modal="true" aria-label="MCP servers">
    <div className="flex h-[90vh] w-full max-w-6xl flex-col rounded-lg border border-border bg-background p-4 shadow-xl">
      <div className="mb-4 flex items-center justify-between"><h3 className="text-lg font-semibold">MCP servers</h3><Button size="sm" variant="ghost" onClick={onClose}>Close</Button></div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <McpConnectionsPanel servers={[...personal, ...vault.servers]} loading={vault.loading || inventory.isLoadingTools} notices={vault.notices}
          refresh={() => { void inventory.refreshTools?.(); void vault.refresh() }}
          catalog={groups.filter(([, entries]) => entries[0].connection !== 'connected').map(([name, entries]) => ({ id: name, name, description: descriptionFor(name), connect: { label: 'Connect', run: configure },
            connectControl: (entries[0] as { requires_oauth?: boolean }).requires_oauth ? <OAuthStatusBadge scope="private" serverName={name} requiresOAuth connection="available" onAuthChange={() => { void inventory.refreshTools?.() }} /> : undefined,
          }))}
          addCustom={{ label: 'Add custom server', run: configure }} help={<p>Connections here are private to you. Shared access is managed in Vault.</p>} />
      </div>
    </div>
    {testTool && <MCPToolApiTester isOpen onClose={() => setTestTool(null)} serverName={testTool.serverName} toolName={testTool.tool.name} toolDetail={testTool.tool} />}
  </div>
}

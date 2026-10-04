import { useCallback, useEffect, useState } from 'react'
import api from '../../services/api'
import type { McpConnectionRow } from './McpConnectionsPanel'
export interface VaultMcpServer {
  id: string; label: string; provider: string
  tools: Array<{ name: string; description: string; input_schema: Record<string, unknown> }>
}
export function useVaultMcpConnections({ selectedServers, onSelectedServersChange, disabled = false }: {
  selectedServers: string[]; onSelectedServersChange: (servers: string[]) => Promise<unknown> | void; disabled?: boolean
}) {
  const [servers, setServers] = useState<VaultMcpServer[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const refresh = useCallback(async () => {
    setLoading(true)
    try { const result = await api.get('/api/me/mcp/vault'); setServers(result.data.servers ?? []); setError(null); setLoaded(true) }
    catch { setError('Vault is unavailable. Your private MCPs still work.') }
    finally { setLoading(false) }
  }, [])
  useEffect(() => { void refresh() }, [refresh])
  const toggle = async (name: string) => {
    setSaving(true)
    try { await onSelectedServersChange(selectedServers.includes(name) ? selectedServers.filter(item => item !== name && item !== 'NO_SERVERS') : [...selectedServers.filter(item => item !== 'NO_SERVERS'), name]); setError(null) }
    catch { setError('Could not save Vault selection.') }
    finally { setSaving(false) }
  }
  const available = new Set(servers.map(server => `vault_${server.id}`))
  const unavailable = loaded ? selectedServers.filter(name => name.startsWith('vault_') && !available.has(name)) : []
  return { loading, refresh, notices: error ? [{ message: error, retry: () => void refresh() }] : [],
    servers: [
      ...servers.map(server => ({ id: `vault_${server.id}`, name: server.label, source: 'Vault', status: 'Available to your groups', statusDot: 'bg-primary', toolCount: server.tools.length,
        selection: { label: `Use ${server.label} from Vault`, checked: selectedServers.includes(`vault_${server.id}`), disabled: disabled || saving, change: () => toggle(`vault_${server.id}`) },
        tools: server.tools.map(tool => ({ id: tool.name, name: tool.name.split('__').slice(1).join('__') || tool.name, description: tool.description, rawSchema: tool.input_schema })),
      })),
      ...unavailable.map(name => ({ id: name, name: 'Selected Vault connection', source: 'Vault', status: 'No longer available', actions: [{ label: 'Remove selection', disabled: disabled || saving, run: () => toggle(name) }] })),
    ] satisfies McpConnectionRow[],
  }
}

import { useCallback, useEffect, useState } from 'react'
import api from '../../services/api'
import type { McpConnectionRow } from './McpConnectionsPanel'
import { isLocalProductInstallation } from '../../products/productSurfaceConfig'
export interface VaultMcpServer {
  id: string; label: string; provider: string
  tools: Array<{ name: string; description: string; input_schema: Record<string, unknown> }>
}
export function useVaultMcpConnections() {
  const [servers, setServers] = useState<VaultMcpServer[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const refresh = useCallback(async () => {
    if (isLocalProductInstallation()) { setServers([]); setError(null); setLoading(false); return }
    setLoading(true)
    try { const result = await api.get('/api/me/mcp/vault'); setServers(result.data.servers ?? []); setError(null) }
    catch { setError('Vault is unavailable. The connections of this place still work.') }
    finally { setLoading(false) }
  }, [])
  useEffect(() => { void refresh() }, [refresh])
  return { loading, refresh, notices: error ? [{ message: error, retry: () => void refresh() }] : [],
    servers: [
      ...servers.map(server => ({ id: `vault_${server.id}`, name: server.label, source: 'Vault', status: 'Available automatically', statusDot: 'bg-primary', toolCount: server.tools.length,
        tools: server.tools.map(tool => ({ id: tool.name, name: tool.name.split('__').slice(1).join('__') || tool.name, description: tool.description, rawSchema: tool.input_schema })),
      })),
    ] satisfies McpConnectionRow[],
  }
}

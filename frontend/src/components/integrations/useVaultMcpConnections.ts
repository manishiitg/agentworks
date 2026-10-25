import { useCallback, useEffect, useState } from 'react'
import { vaultConnectionsApi, type VaultConnectionRow } from '../../api/vaultConnections'
import type { McpConnectionRow } from './McpConnectionsPanel'
export type VaultMcpServer = VaultConnectionRow
export function useVaultMcpConnections(workspacePath?: string) {
 const [items, setItems] = useState<VaultConnectionRow[]>([])
 const [loading,setLoading] = useState(true)
 const [error,setError] = useState<string | null>(null)
 const refresh = useCallback(async () => {
  setLoading(true)
  try { setItems(await vaultConnectionsApi.list(workspacePath)); setError(null) }
  catch { setError('Apps are unavailable. Refresh to check your connections.') }
  finally { setLoading(false) }
 },[workspacePath])
 useEffect(() => { void refresh(); const focus=()=>void refresh(); window.addEventListener('focus',focus);return()=>window.removeEventListener('focus',focus) },[refresh])
 return { loading, refresh, items, notices: error ? [{ message:error,retry:()=>void refresh() }] : [],
  servers:items.map(server=>({id:`vault_${server.id}`,name:server.label,provider:server.provider,source:'Saved account',status:server.status==='active'?'Connected':server.status==='authentication_required'?'Sign-in required':'Not connected',toolCount:server.tools.length,
   tools:server.tools.map(tool=>({id:tool.name,name:tool.name.split('__').slice(1).join('__')||tool.name,description:tool.description,rawSchema:tool.input_schema})),
  })) satisfies McpConnectionRow[],
 }
}

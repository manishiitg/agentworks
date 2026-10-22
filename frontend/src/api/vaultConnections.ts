import api from '../services/api'
export interface VaultConnectionRow {
 id: string; label: string; provider: string; status: string; can_manage: boolean
 tools: Array<{ name: string; description: string; input_schema: Record<string, unknown> }>
}
export interface VaultCatalogApp { name:string; oauth:boolean; needs_admin_setup?:boolean }
export const vaultConnectionsApi = {
 catalog: async (): Promise<VaultCatalogApp[]> => {
  const response = await api.post('/api/my-vaults/op', { operation: 'apps' })
  return (JSON.parse(response.data.result || '{}') as { apps?:VaultCatalogApp[] }).apps ?? []
 },
 list: async (workspacePath?: string): Promise<VaultConnectionRow[]> => {
  const response = await api.get('/api/vault/connections', { params: workspacePath ? { workspace_path: workspacePath } : {} })
  return response.data.servers ?? []
 },
}

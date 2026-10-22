import api from '../services/api'
export interface VaultConnectionRow {
 id: string; label: string; provider: string; status: string; can_manage: boolean
 tools: Array<{ name: string; description: string; input_schema: Record<string, unknown> }>
}
export const vaultConnectionsApi = {
 list: async (workspacePath?: string): Promise<VaultConnectionRow[]> => {
  const response = await api.get('/api/vault/connections', { params: workspacePath ? { workspace_path: workspacePath } : {} })
  return response.data.servers ?? []
 },
}

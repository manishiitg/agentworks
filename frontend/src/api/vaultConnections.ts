import api from '../services/api'
export interface VaultConnectionRow {
 id: string; label: string; provider: string; status: string; can_manage: boolean
 detail?: string
 tools: Array<{ name: string; description: string; input_schema: Record<string, unknown> }>
}
export interface VaultCatalogApp { name: string; oauth: boolean; needs_admin_setup?: boolean }
export interface VaultConnectionResult {
 message?: string; auth_url?: string; connected?: boolean; connection_id?: string; tool_count?: number; requires_sign_in?: boolean
 setup?: { message: string; redirect_uri?: string; needs_client_secret?: boolean }
}
export const vaultConnectionsApi = {
 catalog: async (): Promise<VaultCatalogApp[]> => {
  const response = await api.post('/api/my-vaults/op', { operation: 'apps' }, { skipSessionContext: true })
  return (JSON.parse(response.data.result || '{}') as { apps?: VaultCatalogApp[] }).apps ?? []
 },
 list: async (workspacePath?: string): Promise<VaultConnectionRow[]> => (await vaultConnectionsApi.inventory(workspacePath)).servers,
 inventory: async (workspacePath?: string): Promise<{ servers: VaultConnectionRow[]; can_connect: boolean }> => {
  const response = await api.get('/api/vault/connections', { params: workspacePath ? { workspace_path: workspacePath } : {}, skipSessionContext: !workspacePath })
  return { servers: response.data.servers ?? [], can_connect: response.data.can_connect === true }
 },
 operate: async (operation: 'connect' | 'edit_connection' | 'sign_in' | 'sync' | 'remove_connection', fields: Record<string, string> = {}): Promise<VaultConnectionResult> => {
  const response = await api.post('/api/my-vaults/op', { ...fields, operation }, { skipSessionContext: true })
  const result = response.data.result || ''
  try { return JSON.parse(result) as VaultConnectionResult }
  catch { return { message: result } }
 },
}

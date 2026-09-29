import api from '../services/api'

// The deployment's sign-in apps (Google, GitHub, ...): set up once by an
// admin so people connect without an OAuth client of their own. The secret is
// write-only: it is never returned.

export interface McpAppGroup {
  key: string
  label: string
  servers: string[]
  configured: boolean
  client_id?: string
  updated_at?: string
  required: boolean
}

export const mcpAppsApi = {
  list: async (): Promise<{ apps: McpAppGroup[]; redirectUri: string }> => {
    const response = await api.get('/api/admin/mcp-apps')
    return { apps: response.data.apps || [], redirectUri: response.data.redirect_uri || '' }
  },
  save: async (key: string, clientId: string, clientSecret: string): Promise<void> => {
    await api.put(`/api/admin/mcp-apps/${encodeURIComponent(key)}`, { client_id: clientId, client_secret: clientSecret })
  },
  remove: async (key: string): Promise<void> => {
    await api.delete(`/api/admin/mcp-apps/${encodeURIComponent(key)}`)
  },
}

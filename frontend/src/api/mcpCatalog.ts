import api from '../services/api'

// Provider templates only. Saved connections and credentials are owned by Vault.

export interface PersonalMcpHeader { secret: string; format?: string }

/** A platform catalog server a person can connect with their own login. */
export interface McpCatalogServer {
  name: string
  catalog: string
  description?: string
  sign_in: boolean
  /** No dynamic registration: the person enters their OAuth app's client. */
  needs_client: boolean
  /** Sign-in group (google, github, ...): offered together, one login. */
  group?: string
}

export interface PersonalMcpConnectResult { auth_url?: string; status?: string; message?: string; redirect_uri?: string }

export const mcpCatalogApi = {
  catalog: async (): Promise<McpCatalogServer[]> => {
    const response = await api.get('/api/mcp/catalog')
    return response.data.servers || []
  },
}

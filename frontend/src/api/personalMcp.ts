import api from '../services/api'
import { secretsApi } from './secrets'

// A person's own MCP servers and secrets (docs/design/code_private_mcp.md).
// Every call acts on the signed-in person only.

export interface PersonalMcpServer {
  name: string
  url: string
  transport: 'http' | 'sse'
  oauth: boolean
  connected: boolean
  headers?: string[]
  enabled: boolean
  /** The catalog server it was added from, if any. */
  catalog?: string
}

export interface PersonalMcpHeader { secret: string; format?: string }

/** A platform catalog server a person can add as their own. */
export interface PersonalMcpCatalogServer {
  name: string
  catalog: string
  description?: string
  sign_in: boolean
  /** No dynamic registration: the person enters their OAuth app's client. */
  needs_client: boolean
}

export interface PersonalMcpConnectResult { auth_url?: string; status?: string; message?: string; redirect_uri?: string }

export const personalMcpApi = {
  list: async (projectId?: string): Promise<{ servers: PersonalMcpServer[]; secrets: string[] }> => {
    const response = await api.get('/api/me/mcp/servers', { params: projectId ? { code: projectId } : undefined })
    return { servers: response.data.servers || [], secrets: response.data.secrets || [] }
  },
  catalog: async (): Promise<PersonalMcpCatalogServer[]> => {
    const response = await api.get('/api/me/mcp/catalog')
    return response.data.servers || []
  },
  add: async (server: { name: string; url?: string; catalog?: string; transport?: 'http' | 'sse'; headers?: Record<string, PersonalMcpHeader> }): Promise<{ name: string; oauth: boolean }> => {
    const response = await api.post('/api/me/mcp/servers', server)
    return response.data
  },
  remove: async (name: string): Promise<void> => {
    await api.delete(`/api/me/mcp/servers/${encodeURIComponent(name)}`)
  },
  connect: async (name: string, client?: { clientId: string; clientSecret?: string }): Promise<PersonalMcpConnectResult> => {
    const body = client?.clientId ? { client_id: client.clientId, client_secret: client.clientSecret || undefined } : {}
    const response = await api.post(`/api/me/mcp/servers/${encodeURIComponent(name)}/connect`, body)
    return response.data
  },
  setEnabled: async (name: string, projectId: string, enabled: boolean): Promise<void> => {
    await api.put(`/api/me/mcp/servers/${encodeURIComponent(name)}/codes/${encodeURIComponent(projectId)}`, { enabled })
  },
  saveSecret: async (name: string, value: string): Promise<void> => {
    const { encrypted } = await secretsApi.encrypt(value)
    await api.put(`/api/me/secrets/${encodeURIComponent(name)}`, { encrypted_value: encrypted })
  },
  deleteSecret: async (name: string): Promise<void> => {
    await api.delete(`/api/me/secrets/${encodeURIComponent(name)}`)
  },
}

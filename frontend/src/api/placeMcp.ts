import { announceMcpOAuthStart } from '../utils/mcpOAuthNotification'
import api from '../services/api'
import type { PersonalMcpConnectResult, PersonalMcpHeader } from './mcpCatalog'
import type { ToolDefinition } from '../stores/types'

// Private MCP connections use the caller's login. Shared access is governed by Vault.

export interface PlaceMcpServer {
  name: string
  catalog?: string
  label?: string
  url: string
  owner: string
  owner_name: string
  mine: boolean
  connected: boolean
  sign_in?: boolean
  /** False once the person who added it can no longer edit this place. */
  active: boolean
  added_at?: string
}

/** A server that is not in the catalog: its own https URL, optionally with an API-key header. */
export interface PlaceMcpCustomServer {
  name: string
  url: string
  transport?: 'http' | 'sse'
  /** Header name -> the project secret that holds its value. */
  headers?: Record<string, PersonalMcpHeader>
}

export const placeMcpApi = {
  tools: async (name: string): Promise<ToolDefinition> => {
    const response = await api.get('/api/tools/detail', { params: { server_name: name } })
    return response.data
  },
  list: async (workspacePath: string): Promise<PlaceMcpServer[]> => {
    const response = await api.get('/api/mcp/place', { params: { workspace_path: workspacePath } })
    return response.data.servers || []
  },
  add: async (workspacePath: string, server: string | PlaceMcpCustomServer | { catalog: string; label: string }): Promise<{ name: string; oauth: boolean }> => {
    const body = typeof server === 'string' ? { catalog: server } : server
    const response = await api.post('/api/mcp/place', { workspace_path: workspacePath, ...body })
    return response.data
  },
  connect: async (workspacePath: string, name: string, client?: { clientId: string; clientSecret?: string }, chatSessionId?: string): Promise<PersonalMcpConnectResult> => {
    const body = { ...(chatSessionId ? { session_id: chatSessionId } : {}), ...(client?.clientId ? { client_id: client.clientId, client_secret: client.clientSecret || undefined } : {}) }
    const response = await api.post(`/api/mcp/place/${encodeURIComponent(name)}/connect`, body, { params: { workspace_path: workspacePath } })
    if (response.data.auth_url && response.data.status !== 'needs_client_id') announceMcpOAuthStart(chatSessionId)
    return response.data
  },
  remove: async (workspacePath: string, name: string, owner?: string): Promise<void> => {
    await api.delete(`/api/mcp/place/${encodeURIComponent(name)}`, { params: { workspace_path: workspacePath, owner } })
  },
}

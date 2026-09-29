import api from '../services/api'
import type { PersonalMcpConnectResult, PersonalMcpHeader } from './personalMcp'

// A Code's, Crew's or workflow's own MCP connections (docs/design/
// personal_mcp_attach.md): added there by its owner (or, for a workflow,
// someone who can edit it), with their own login, and used by every chat and
// run there like any other MCP server.

export interface PlaceMcpServer {
  name: string
  catalog?: string
  url: string
  owner: string
  owner_name: string
  mine: boolean
  connected: boolean
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
  list: async (workspacePath: string): Promise<PlaceMcpServer[]> => {
    const response = await api.get('/api/mcp/place', { params: { workspace_path: workspacePath } })
    return response.data.servers || []
  },
  add: async (workspacePath: string, server: string | PlaceMcpCustomServer): Promise<{ name: string; oauth: boolean }> => {
    const body = typeof server === 'string' ? { catalog: server } : server
    const response = await api.post('/api/mcp/place', { workspace_path: workspacePath, ...body })
    return response.data
  },
  connect: async (workspacePath: string, name: string, client?: { clientId: string; clientSecret?: string }): Promise<PersonalMcpConnectResult> => {
    const body = client?.clientId ? { client_id: client.clientId, client_secret: client.clientSecret || undefined } : {}
    const response = await api.post(`/api/mcp/place/${encodeURIComponent(name)}/connect`, body, { params: { workspace_path: workspacePath } })
    return response.data
  },
  remove: async (workspacePath: string, name: string, owner?: string): Promise<void> => {
    await api.delete(`/api/mcp/place/${encodeURIComponent(name)}`, { params: { workspace_path: workspacePath, owner } })
  },
}

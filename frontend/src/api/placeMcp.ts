import api from '../services/api'
import type { PersonalMcpConnectResult } from './personalMcp'

// A workflow's or Crew's own MCP connections (docs/design/personal_mcp_attach.md):
// added there by someone who can edit it, with their own login, and used by
// every chat and run there like any other MCP server.

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

export const placeMcpApi = {
  list: async (workspacePath: string): Promise<PlaceMcpServer[]> => {
    const response = await api.get('/api/mcp/place', { params: { workspace_path: workspacePath } })
    return response.data.servers || []
  },
  add: async (workspacePath: string, catalog: string): Promise<{ name: string; oauth: boolean }> => {
    const response = await api.post('/api/mcp/place', { workspace_path: workspacePath, catalog })
    return response.data
  },
  connect: async (workspacePath: string, name: string): Promise<PersonalMcpConnectResult> => {
    const response = await api.post(`/api/mcp/place/${encodeURIComponent(name)}/connect`, {}, { params: { workspace_path: workspacePath } })
    return response.data
  },
  remove: async (workspacePath: string, name: string, owner?: string): Promise<void> => {
    await api.delete(`/api/mcp/place/${encodeURIComponent(name)}`, { params: { workspace_path: workspacePath, owner } })
  },
}

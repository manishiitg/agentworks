import api, { agentApi } from '../services/api'

// The deployment's own Google app (an admin stores one Google OAuth client for the server).
// People connect their own Google accounts through it, so nobody uploads a client file.

export interface GoogleAppStatus {
  /** The server has a Google app to sign in through. */
  configured: boolean
  /** Where Google sends the person back (already registered on the app). */
  redirect_uri: string
}

export interface GoogleAppConnectRequest {
  workspace_path: string
  display_name?: string
  /** Services beyond Gmail (drive, sheets, docs, slides, calendar); read-only unless write is set. */
  services?: { service: string; write?: boolean }[]
  allow_read_access?: boolean
  allow_agent_write_access?: boolean
}

export const googleAppApi = {
  status: async (): Promise<GoogleAppStatus> => (await api.get('/api/human-feedback/gmail/google-app')).data,
  clients: async () => (await agentApi.listGmailOAuthClients()).clients,
  registerClient: async (name: string, json: unknown) => agentApi.createGmailOAuthClient(name, json),
  connectWithClient: async (clientName: string, req: GoogleAppConnectRequest): Promise<{ id: string; auth_url: string }> => {
    const result = (await api.post('/api/integrations/google/accounts', { ...req, action: 'connect', client_name: clientName })).data
    return { id: result.connection_id, auth_url: result.auth_url }
  },
  connect: async (req: GoogleAppConnectRequest): Promise<{ id: string; auth_url: string }> => {
    const result = (await api.post('/api/integrations/google/accounts', { ...req, action: 'connect' })).data
    return { id: result.connection_id, auth_url: result.auth_url }
  },
  reconnect: async (id: string, req: GoogleAppConnectRequest): Promise<{ id: string; auth_url: string }> => {
    const result = (await api.post('/api/integrations/google/accounts', { ...req, action: 'reconnect', connection_id: id })).data
    return { id, auth_url: result.auth_url }
  },
}

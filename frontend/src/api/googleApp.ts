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
    const created = await agentApi.createGmailConnection({ ...req, client_name: clientName, display_name: req.display_name || 'Google account' })
    const started = await agentApi.startGmailConnectionAuth(created.id)
    return { id: created.id, auth_url: started.auth_url }
  },
  /** Create the connection, then open Google's sign-in; the server stores the login when Google returns. */
  connect: async (req: GoogleAppConnectRequest): Promise<{ id: string; auth_url: string }> => {
    const created = (await api.post('/api/human-feedback/gmail/google-app/connect', req)).data as { id: string }
    const started = await agentApi.startGmailConnectionAuth(created.id)
    return { id: created.id, auth_url: started.auth_url }
  },
  /** Keep the existing connection and OAuth client, including legacy named clients. */
  reconnect: async (id: string, req: GoogleAppConnectRequest): Promise<{ id: string; auth_url: string }> => {
    await agentApi.updateGmailConnection(id, {
      services: req.services,
      services_set: req.services !== undefined,
      allow_read_access: req.allow_read_access,
      allow_agent_write_access: req.allow_agent_write_access,
    })
    const started = await agentApi.startGmailConnectionAuth(id)
    return { id, auth_url: started.auth_url }
  },
}

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
  /** Create the connection, then open Google's sign-in; the server stores the login when Google returns. */
  connect: async (req: GoogleAppConnectRequest): Promise<{ id: string; auth_url: string }> => {
    const created = (await api.post('/api/human-feedback/gmail/google-app/connect', req)).data as { id: string }
    const started = await agentApi.startGmailConnectionAuth(created.id)
    return { id: created.id, auth_url: started.auth_url }
  },
}

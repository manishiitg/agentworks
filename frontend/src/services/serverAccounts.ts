// Your AI accounts on a connected server, reached from the Mac app through its saved connection to that server
// (/api/connections/<server>/…, desktop_connections.go). Each call is the server's own route, so the server decides
// who you are there, whether it allows your own accounts and who you may share with. A pasted key goes from this
// page to the Mac's backend and on to the server; it is never kept on the Mac.
import axios from 'axios'
import { connectionApiBase, getAuthToken } from './api'
import { admissionWindowHeaders } from '../utils/chatDeliveryTelemetry'
import type { ProviderAccountSharing, ProviderConnection, ProviderSetupSession, ProviderShareTargets } from './llm-config-api'

function client(serverId: string) {
  return axios.create({
    baseURL: connectionApiBase(serverId),
    timeout: 30000,
    headers: { ...admissionWindowHeaders(), Authorization: `Bearer ${getAuthToken() || ''}`, 'Content-Type': 'application/json' },
  })
}

export type NewServerAccount = { provider: string; display_name: string; credential?: string; auth_method?: string; sharing?: ProviderAccountSharing }

export const serverAccounts = (serverId: string) => ({
  list: async (provider: string): Promise<ProviderConnection[]> =>
    ((await client(serverId).get('/api/provider-connections')).data?.connections as ProviderConnection[] ?? []).filter(record => record.provider === provider),
  add: async (account: NewServerAccount): Promise<ProviderConnection> => (await client(serverId).post('/api/provider-connections', account)).data,
  setSharing: async (id: string, sharing: ProviderAccountSharing): Promise<void> => { await client(serverId).patch(`/api/provider-connections/${encodeURIComponent(id)}`, { sharing }) },
  remove: async (id: string): Promise<void> => { await client(serverId).delete(`/api/provider-connections/${encodeURIComponent(id)}`) },
  shareTargets: async (): Promise<ProviderShareTargets> => {
    const data = (await client(serverId).get('/api/provider-connections/share-targets')).data
    return { workflows: data?.workflows || [], crews: data?.crews || [], users: data?.users || [] }
  },
  // The sign-in window: the server runs the program's own login for the account; it streams through the connection.
  startSignIn: async (provider: string, accountId: string, replaceRunning = false): Promise<ProviderSetupSession> =>
    (await client(serverId).post('/api/provider-setup/sessions', { provider, action: 'authenticate', connection_id: accountId, cols: 100, rows: 24, replace_running: replaceRunning || undefined })).data.session,
  cancelSignIn: async (sessionId: string): Promise<void> => { await client(serverId).delete(`/api/provider-setup/sessions/${encodeURIComponent(sessionId)}`) },
  signInStreamUrl: (sessionId: string): string => {
    const url = new URL(`${connectionApiBase(serverId).replace(/^http/i, 'ws')}/api/provider-setup/sessions/${encodeURIComponent(sessionId)}/stream`)
    const token = getAuthToken()
    if (token) url.searchParams.set('token', token)
    return url.toString()
  },
})

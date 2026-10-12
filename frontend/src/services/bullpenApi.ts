import api from './api'

export interface BullpenAgent {
  agent_id: string
  agent_name: string
  target: string
  purpose?: string
  provider?: string
  online: boolean
  owner?: string
  shared?: boolean
}

export interface BullpenShare {
  share_id: string
  agent_name: string
  person: string
  expires_at: string
  state: 'active' | 'revoked' | 'expired'
}

export interface BullpenSharing {
  shares: BullpenShare[]
  blocks: { person: string; blocked_at: string }[]
  shared_with_me: BullpenAgent[]
  incoming: { person: string; sender_agent: string; agent_name: string; messages: number; last_at: string }[]
}

export const bullpenApi = {
  agents: async (signal?: AbortSignal): Promise<BullpenAgent[]> =>
    (await api.get<{ agents: BullpenAgent[] }>('/api/bullpen/agents', { signal })).data.agents,
  sharing: async (signal?: AbortSignal): Promise<BullpenSharing> =>
    (await api.get<BullpenSharing>('/api/bullpen/shares', { signal })).data,
  share: async (agentName: string, person: string, expiresInHours: number): Promise<void> => {
    await api.post('/api/bullpen/shares', { agent_name: agentName, person, expires_in_hours: expiresInHours })
  },
  revoke: async (shareId: string): Promise<void> => {
    await api.delete('/api/bullpen/shares/' + encodeURIComponent(shareId))
  },
  block: async (person: string, unblock = false): Promise<void> => {
    await api.post('/api/bullpen/blocks', { person, unblock })
  },
}

export function bullpenError(error: unknown): string {
  const status = (error as { response?: { status?: number } })?.response?.status
  if (status === 403) return 'Bullpen is not enabled for your account on this server.'
  if (status === 401) return 'Sign in again to see your agents.'
  if (status === 404) return 'This server needs an update before it can show the agent directory.'
  return 'Could not load your agents. Try refreshing.'
}

export function sharingError(error: unknown): string {
  const data = (error as { response?: { data?: { error?: { message?: string }; message?: string } } })?.response?.data
  return data?.error?.message || data?.message || 'Could not change sharing. Try again.'
}

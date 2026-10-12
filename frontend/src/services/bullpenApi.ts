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

export interface BullpenNotice {
  notice_id: string
  agent_name: string
  text: string
  at: string
}

export interface BullpenAdminAgent {
  agent_id: string
  agent_name: string
  purpose?: string
  provider?: string
  online: boolean
  created_at: string
  attached: boolean
  attachment_handle?: string
}

export interface BullpenAdminPerson {
  username: string
  role: string
  disabled: boolean
  agents: BullpenAdminAgent[]
}

export interface BullpenAdminPage {
  people: BullpenAdminPerson[]
  total: number
  next_cursor?: string
}

export interface BullpenAdminAuditRow {
  at: string
  admin: string
  event: string
  person: string
  agent_id?: string
  reason: string
  result: string
}

export const bullpenApi = {
  agents: async (signal?: AbortSignal): Promise<BullpenAgent[]> =>
    (await api.get<{ agents: BullpenAgent[] | null }>('/api/bullpen/agents', { signal })).data.agents ?? [],
  sharing: async (signal?: AbortSignal): Promise<BullpenSharing> =>
    ((data: BullpenSharing) => ({ shares: data.shares ?? [], blocks: data.blocks ?? [], shared_with_me: data.shared_with_me ?? [], incoming: data.incoming ?? [] }))((await api.get<BullpenSharing>('/api/bullpen/shares', { signal })).data),
  share: async (agentName: string, person: string, expiresInHours: number): Promise<void> => {
    await api.post('/api/bullpen/shares', { agent_name: agentName, person, expires_in_hours: expiresInHours })
  },
  revoke: async (shareId: string): Promise<void> => {
    await api.delete('/api/bullpen/shares/' + encodeURIComponent(shareId))
  },
  block: async (person: string, unblock = false): Promise<void> => {
    await api.post('/api/bullpen/blocks', { person, unblock })
  },
  notices: async (signal?: AbortSignal): Promise<BullpenNotice[]> =>
    (await api.get<{ notices: BullpenNotice[] | null }>('/api/bullpen/notices', { signal })).data.notices ?? [],
  dismissNotice: async (id: string): Promise<void> => {
    await api.delete('/api/bullpen/notices/' + encodeURIComponent(id))
  },
  adminAgents: async (q: string, cursor: string, signal?: AbortSignal): Promise<BullpenAdminPage> =>
    ((data: BullpenAdminPage) => ({ ...data, people: (data.people ?? []).map(person => ({ ...person, agents: person.agents ?? [] })) }))((await api.get<BullpenAdminPage>('/api/bullpen/admin/agents', { params: { q, cursor }, signal })).data),
  adminAudit: async (signal?: AbortSignal): Promise<BullpenAdminAuditRow[]> =>
    (await api.get<{ audit: BullpenAdminAuditRow[] | null }>('/api/bullpen/admin/audit', { signal })).data.audit ?? [],
  adminCloseAttachment: async (handle: string, reason: string): Promise<void> => {
    await api.post('/api/bullpen/admin/attachments/close', { handle, reason })
  },
  adminRemoveAgent: async (agentId: string, reason: string): Promise<void> => {
    await api.post('/api/bullpen/admin/agents/remove', { agent_id: agentId, reason })
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

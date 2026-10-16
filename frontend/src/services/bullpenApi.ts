import api from './api'

export interface BullpenAgent {
  usage?: BullpenUsage
  usage_unavailable?: boolean
  agent_id: string
  agent_name: string
  target: string
  purpose?: string
  provider?: string
  online: boolean
  presence?: string
  update_needed?: boolean // its AgentWorks CLI is older than the one this server offers
  owner?: string
  shared?: boolean
  groups?: string[]
}

export interface BullpenShare {
  share_id: string
  agent_name: string
  person: string
  expires_at?: string
  scope?: 'everyone' | 'person'
  state: 'active' | 'revoked' | 'expired'
}

export interface BullpenGroup {
  name: string
  purpose: string
  member_count: number
  created_by?: string
  joined_with?: string[]
  members?: { agent_name: string; owner: string; purpose?: string; online: boolean; presence?: string }[]
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
  presence?: string
  created_at: string
  attached: boolean
  attachment_handle?: string
  groups?: string[]
}

export interface BullpenAdminPerson {
  username: string
  role: string
  disabled: boolean
  agents: BullpenAdminAgent[]
}

export interface BullpenAdminPage {
  groups?: BullpenGroup[]
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


export interface UsageCounters { input_tokens: number; output_tokens: number; cached_tokens: number }
export interface UsageReading { reading_id: string; agent_id: string; observed_at: string; window_start: string; session: UsageCounters & { model?: string; source: string; skipped_records?: number; turn_count?: number; model_call_count?: number }; delta: UsageCounters; baseline: boolean; discontinuity?: boolean }
export interface BullpenUsage extends UsageReading { reporting: boolean; available: boolean; reason?: string; today: UsageCounters; today_timezone: string }
export interface UsageHistory { readings: UsageReading[]; before: number; has_more: boolean; retention_days: number; row_cap: number }

export const bullpenApi = {
  usage: async (before?: number): Promise<UsageHistory> => (await api.get<UsageHistory>('/api/bullpen/usage', { params: { before } })).data,
  deleteUsage: async (): Promise<void> => { await api.delete('/api/bullpen/usage') },
  agents: async (signal?: AbortSignal): Promise<BullpenAgent[]> =>
    (await api.get<{ agents: BullpenAgent[] | null }>('/api/bullpen/agents', { signal, params: { timezone: Intl.DateTimeFormat().resolvedOptions().timeZone } })).data.agents ?? [],
  agentUsage: async (signal?: AbortSignal): Promise<Pick<BullpenAgent, 'agent_id' | 'usage' | 'usage_unavailable'>[]> =>
    (await api.get<{ agents: Pick<BullpenAgent, 'agent_id' | 'usage' | 'usage_unavailable'>[] | null }>('/api/bullpen/agents/usage', { signal, params: { timezone: Intl.DateTimeFormat().resolvedOptions().timeZone } })).data.agents ?? [],
  sharing: async (signal?: AbortSignal): Promise<BullpenSharing> =>
    ((data: BullpenSharing) => ({ shares: data.shares ?? [], blocks: data.blocks ?? [], shared_with_me: data.shared_with_me ?? [], incoming: data.incoming ?? [] }))((await api.get<BullpenSharing>('/api/bullpen/shares', { signal })).data),
  groups: async (signal?: AbortSignal): Promise<BullpenGroup[]> =>
    (await api.get<{ groups: BullpenGroup[] | null }>('/api/bullpen/groups', { signal })).data.groups ?? [],
  notices: async (signal?: AbortSignal): Promise<BullpenNotice[]> =>
    (await api.get<{ notices: BullpenNotice[] | null }>('/api/bullpen/notices', { signal })).data.notices ?? [],
  dismissNotice: async (id: string): Promise<void> => {
    await api.delete('/api/bullpen/notices/' + encodeURIComponent(id))
  },
  adminAgents: async (q: string, cursor: string, signal?: AbortSignal): Promise<BullpenAdminPage> =>
    ((data: BullpenAdminPage) => ({ ...data, people: (data.people ?? []).map(person => ({ ...person, agents: person.agents ?? [] })) }))((await api.get<BullpenAdminPage>('/api/bullpen/admin/agents', { params: { q, cursor }, signal })).data),
  adminAudit: async (signal?: AbortSignal): Promise<BullpenAdminAuditRow[]> =>
    (await api.get<{ audit: BullpenAdminAuditRow[] | null }>('/api/bullpen/admin/audit', { signal })).data.audit ?? [],
}

export function bullpenError(error: unknown): string {
  const status = (error as { response?: { status?: number } })?.response?.status
  if (status === 403) return 'AgentBridge is not enabled for your account on this server.'
  if (status === 401) return 'Sign in again to see your agents.'
  if (status === 404) return 'This server needs an update before it can show the agent directory.'
  return 'Could not load your agents. Try refreshing.'
}

export function sharingError(error: unknown): string {
  const data = (error as { response?: { data?: { error?: { message?: string }; message?: string } } })?.response?.data
  return data?.error?.message || data?.message || 'Could not change sharing. Try again.'
}

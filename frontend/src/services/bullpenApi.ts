import api from './api'

export interface BullpenAgent {
  agent_id: string
  agent_name: string
  target: string
  purpose?: string
  provider?: string
  online: boolean
}

export const bullpenApi = {
  agents: async (signal?: AbortSignal): Promise<BullpenAgent[]> =>
    (await api.get<{ agents: BullpenAgent[] }>('/api/bullpen/agents', { signal })).data.agents,
}

export function bullpenError(error: unknown): string {
  const status = (error as { response?: { status?: number } })?.response?.status
  if (status === 403) return 'Bullpen is not enabled for your account on this server.'
  if (status === 401) return 'Sign in again to see your agents.'
  if (status === 404) return 'This server needs an update before it can show the agent directory.'
  return 'Could not load your agents. Try refreshing.'
}

import axios from 'axios'
import { getApiBaseUrl, getAuthToken } from '../services/api'

export interface ConditionTrigger {
  id: string
  name: string
  enabled: boolean
  cron_expression: string
  timezone: string
  script: string
  sha256: string
  secrets?: string[]
  target: { kind: 'agent' | 'function' | 'route' | 'trigger'; name?: string; function?: string; chat_id?: string; message?: string; trigger_id?: string; route_selections?: Record<string, string>; group_names?: string[] }
  last_status?: string
  last_error?: string
  last_run_at?: string
}
export interface ConditionHistory {
  pending: number
  events: { event: { id: string; payload: Record<string, unknown> }; status: string; receipt?: string; error?: string; seen_at: string }[]
  checks: { id: string; status: string; started_at: string; final_response?: string; error?: string }[]
}
function config() {
  const token = getAuthToken()
  return { baseURL: getApiBaseUrl(), headers: token ? { Authorization: `Bearer ${token}` } : {} }
}
export const conditionTriggersApi = {
  list: (projectId: string) => axios.get<{ triggers: ConditionTrigger[] }>('/api/condition-triggers', { ...config(), params: { project_id: projectId } }).then(r => r.data),
  manage: (projectId: string, action: string, id: string, trigger?: Record<string, unknown>) => axios.post('/api/condition-triggers', { project_id: projectId, action, id, ...(trigger ? { trigger } : {}) }, config()).then(r => r.data),
  history: (projectId: string, id: string) => axios.get<ConditionHistory>(`/api/condition-triggers/${encodeURIComponent(id)}/history`, { ...config(), params: { project_id: projectId } }).then(r => r.data),
}

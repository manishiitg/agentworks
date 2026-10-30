import axios from 'axios'
import { getApiBaseUrl, getAuthToken } from '../services/api'

export interface ConditionTrigger {
  github_pr?: { session_id?: string; repository: string; number: number; mode: 'checks' | 'deployment' | 'autofix'; worktree?: string; branch?: string; login?: string }
  id: string
  name: string
  enabled: boolean
  check_mode?: 'schedule' | 'event' | 'both' | 'inbox'
  email_source?: 'crew_inbox' | 'personal_gmail'
  event_auth_mode?: 'bearer' | 'github'
  event_secret_name?: string
  event_path?: string
  once?: boolean
  completed_at?: string
  once_status?: string
  cron_expression: string
  timezone: string
  script: string
  sha256: string
  revision: string
  secrets?: string[]
  target: { kind: 'agent' | 'function' | 'route' | 'trigger'; name?: string; function?: string; chat_id?: string; message?: string; trigger_id?: string; route_selections?: Record<string, string>; group_names?: string[] }
  last_status?: string
  last_error?: string
  last_run_at?: string
}
export type WatcherCaller = { owner_id: string; id: string; name: string; target: ConditionTrigger['target']; enabled: boolean; last_status?: string; last_run_at?: string; last_dispatch_at?: string }
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
  list: (projectId: string, profileId = 'code', includeCompleted = false) => axios.get<{ triggers: ConditionTrigger[]; callers?: WatcherCaller[]; chats?: { id: string; name: string }[] }>('/api/condition-triggers', { ...config(), params: { project_id: projectId, profile_id: profileId, ...(includeCompleted ? { include_completed: true } : {}) } }).then(r => r.data),
  manage: (projectId: string, action: string, id: string, trigger?: Record<string, unknown>, expectedRevision?: string, profileId = 'code') => axios.post('/api/condition-triggers', { project_id: projectId, profile_id: profileId, action, id, ...(trigger ? { trigger } : {}), ...(expectedRevision ? { expected_revision: expectedRevision } : {}) }, config()).then(r => r.data),
  history: (projectId: string, id: string, profileId = 'code') => axios.get<ConditionHistory>(`/api/condition-triggers/${encodeURIComponent(id)}/history`, { ...config(), params: { project_id: projectId, profile_id: profileId } }).then(r => r.data),
}

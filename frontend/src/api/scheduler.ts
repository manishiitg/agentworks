import axios from 'axios'
import { dedupedGet, getApiBaseUrl, getAuthToken, invalidateDedupedGetPrefix } from '../services/api'
import type {
  ScheduledJob,
  CreateScheduledJobRequest,
  UpdateScheduledJobRequest,
  ListScheduledJobsResponse,
  ListScheduledJobRunsResponse,
  SchedulerConfig,
} from '../services/api-types'

const API_BASE_URL = getApiBaseUrl()

const SCHEDULER_JOBS_KEY = 'scheduler-jobs:'

function afterJobWrite<T>(result: Promise<T>): Promise<T> {
  return result.finally(() => invalidateDedupedGetPrefix(SCHEDULER_JOBS_KEY))
}

const api = axios.create({
  baseURL: API_BASE_URL,
  headers: { 'Content-Type': 'application/json' },
})

api.interceptors.request.use((config) => {
  config.baseURL = getApiBaseUrl()
  const token = getAuthToken()
  if (token && config.headers) {
    config.headers['Authorization'] = `Bearer ${token}`
  }
  return config
})

export const schedulerApi = {
  getConfig: () =>
    api.get<SchedulerConfig>('/api/scheduler/config').then(r => r.data),

  updateConfig: (req: SchedulerConfig) =>
    api.put<SchedulerConfig>('/api/scheduler/config', req).then(r => r.data),

  // Several panels list jobs together on page open; identical requests
  // share one call. Any job write drops the shared result.
  listJobs: (params?: { entity_type?: string; enabled?: boolean; limit?: number; offset?: number; mode?: string }) =>
    dedupedGet(`${SCHEDULER_JOBS_KEY}${JSON.stringify(params ?? {})}`, () =>
      api.get<ListScheduledJobsResponse>('/api/scheduler/jobs', { params }).then(r => r.data)),

  getJob: (id: string) =>
    api.get<ScheduledJob>(`/api/scheduler/jobs/${id}`).then(r => r.data),

  createJob: (req: CreateScheduledJobRequest) =>
    afterJobWrite(api.post<ScheduledJob>('/api/scheduler/jobs', req).then(r => r.data)),

  updateJob: (id: string, req: UpdateScheduledJobRequest) =>
    afterJobWrite(api.put<ScheduledJob>(`/api/scheduler/jobs/${id}`, req).then(r => r.data)),

  deleteJob: (id: string) =>
    afterJobWrite(api.delete(`/api/scheduler/jobs/${id}`)),

  cleanupJobRuns: (params: { workspace_path: string; older_than_days: number; schedule_ids?: string }) =>
    afterJobWrite(api.delete<{ deleted_count: number; workspace_path: string }>('/api/scheduler/jobs/runs/cleanup', { params }).then(r => r.data)),

  enableJob: (id: string) =>
    afterJobWrite(api.post<ScheduledJob>(`/api/scheduler/jobs/${id}/enable`).then(r => r.data)),

  disableJob: (id: string) =>
    afterJobWrite(api.post<ScheduledJob>(`/api/scheduler/jobs/${id}/disable`).then(r => r.data)),

  triggerJob: (id: string) =>
    afterJobWrite(api.post<{ session_id: string }>(`/api/scheduler/jobs/${id}/trigger`).then(r => r.data)),

  runPulse: (workspacePath: string) =>
    api.post<{ run_id: string }>('/api/scheduler/workflows/pulse-run', {
      workspace_path: workspacePath,
    }).then(r => r.data),

  runGoalCheck: (workspacePath: string) =>
    api.post<{ run_id: string }>('/api/scheduler/workflows/goal-check-run', {
      workspace_path: workspacePath,
    }).then(r => r.data),

  getJobRuns: (id: string, limit = 20, offset = 0) =>
    api.get<ListScheduledJobRunsResponse>(`/api/scheduler/jobs/${id}/runs`, { params: { limit, offset } }).then(r => r.data),

  stopJob: (id: string) =>
    afterJobWrite(api.post<ScheduledJob>(`/api/scheduler/jobs/${id}/stop`).then(r => r.data)),
}

// --- Provider API Keys (server-side encrypted storage) ---

export interface StoredProviderKeys {
  openrouter?: string
  openai?: string
  anthropic?: string
  zai?: string
  kimi?: string
  vertex?: string
  codex_cli?: string
  cursor_cli?: string
  pi_cli?: string
  // Retained only for Pi's MiniMax text-model routing; there is no standalone
  // MiniMax provider configuration in the frontend.
  minimax?: string
  pi_provider_keys?: Record<string, string>
  bedrock?: { region: string }
  azure?: { endpoint: string; api_key: string; api_version?: string; region?: string }
}

export const providerKeysApi = {
  save: (keys: StoredProviderKeys) =>
    api.put('/api/provider-keys', keys).then(r => r.data),

  load: () =>
    api.get<StoredProviderKeys>('/api/provider-keys').then(r => r.data),
}

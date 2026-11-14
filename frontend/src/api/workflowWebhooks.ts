import axios from 'axios'
import { getApiBaseUrl, getAuthToken } from '../services/api'

export interface WorkflowAPITrigger {
  chat_id?: string
  step_id?: string
  id: string
  name: string
  enabled: boolean
  auth_mode: 'bearer' | 'github' | ''
  path: string
  route_selections: Record<string, string>
  input_preset?: string
  inputs?: Record<string, string>
  group_names?: string[]
  max_concurrency?: number
  payload_mappings?: WebhookPayloadMappings
  secret?: string
  /** '' = external webhook, 'function' = callable function, 'internal' = one bound caller. */
  kind?: '' | 'internal' | 'function'
  caller?: { type: string; id: string; profile_id?: string }
  function?: WorkflowFunctionSpec
  input_schema?: { properties?: Record<string, { type?: string; default?: unknown }>; required?: string[] }
  /** One plain line from the server: what this webhook starts ("Runs the workflow · <saved setting>"). */
  starts?: string
}

export interface WorkflowFunctionSpec {
  name: string
  description?: string
  inputs?: string[]
  allowed_callers?: { type: string; id: string; profile_id?: string }[]
}

export interface WebhookValueMapping {
  source: string
  values: Record<string, string>
  default?: string
}

export interface WebhookPayloadMappings {
  routes?: Record<string, WebhookValueMapping>
  step?: WebhookValueMapping
}

export interface APITriggerOptions {
  chat_choices?: { id: string; label: string }[]
  steps?: { step_id: string; title: string }[]
  triggers: WorkflowAPITrigger[]
  routes: { step_id: string; step_title: string; route_id: string; route_name: string }[]
  input_presets: string[]
  route_error?: string
}

export type APITriggerRequest = Pick<WorkflowAPITrigger, 'name' | 'enabled' | 'auth_mode' | 'route_selections' | 'input_preset' | 'inputs'> & {
  workspace_path: string
  chat_id?: string
  step_id?: string
  payload_mappings?: WebhookPayloadMappings
  rotate_secret?: boolean
}

function config() {
  const token = getAuthToken()
  return { baseURL: getApiBaseUrl(), headers: token ? { Authorization: `Bearer ${token}` } : {} }
}

export const workflowWebhooksApi = {
  list: (workspacePath: string) => axios.get<APITriggerOptions>('/api/workflow-webhooks', {
    ...config(), params: { workspace_path: workspacePath },
  }).then(r => r.data),
  save: (request: APITriggerRequest, id?: string) => axios.request<WorkflowAPITrigger>({
    ...config(), method: id ? 'PUT' : 'POST', url: `/api/workflow-webhooks${id ? `/${encodeURIComponent(id)}` : ''}`, data: request,
  }).then(r => r.data),
  delete: (workspacePath: string, id: string) => axios.delete(`/api/workflow-webhooks/${encodeURIComponent(id)}`, {
    ...config(), params: { workspace_path: workspacePath },
  }),
  getPayload: (id: string, runId: string) => axios.get<{ raw_payload: string }>(
    `/api/workflow-webhooks/${encodeURIComponent(id)}/runs/${encodeURIComponent(runId)}/payload`,
    config(),
  ).then(r => r.data),
}

export function apiTriggerURL(path: string): string {
  return new URL(path, getApiBaseUrl() || window.location.origin).href
}

import axios from 'axios'
import { getApiBaseUrl, getAuthToken } from '../services/api'
import type { ProductTriggerScope } from './productWebhooks'

export type CrewFunctionSchema = {
  type?: string
  description?: string
  enum?: unknown[]
  required?: string[]
  properties?: Record<string, CrewFunctionSchema>
  items?: CrewFunctionSchema
}

export interface CrewFunction {
  name: string
  description?: string
  instructions?: string
  input_schema?: CrewFunctionSchema
  result_schema?: CrewFunctionSchema
  created_by?: string
  created_at?: string
  updated_at?: string
  implicit?: boolean
}

export interface CrewFunctionProgress { at: string; message: string; percent?: number }

export interface CrewFunctionCall {
  call_id: string
  function: string
  caller_kind: string
  caller_label: string
  status: 'queued' | 'running' | 'completed' | 'failed' | string
  latest_progress?: CrewFunctionProgress
  progress?: CrewFunctionProgress[]
  answer?: string
  result?: unknown
  isolated_execution?: boolean
  files?: { name: string; size: number; mime_type: string }[]
  error?: string
  started_at: string
  finished_at?: string
}

function config() {
  const token = getAuthToken()
  return { baseURL: getApiBaseUrl(), headers: token ? { Authorization: `Bearer ${token}` } : {} }
}

export const crewFunctionsApi = {
  list: (scope: ProductTriggerScope) => axios.get<{ functions: CrewFunction[]; calls: CrewFunctionCall[] }>('/api/crew-functions', {
    ...config(), params: { profile_id: scope.profileId, project_id: scope.projectId },
  }).then(response => response.data),
  delete: (scope: ProductTriggerScope, name: string) => axios.delete(`/api/crew-functions/${encodeURIComponent(name)}`, {
    ...config(), params: { profile_id: scope.profileId, project_id: scope.projectId },
  }),
}

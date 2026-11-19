import axios from 'axios'
import { getApiBaseUrl, getAuthToken } from '../services/api'
import type { ListScheduledJobRunsResponse } from '../services/api-types'

/** A Crew caller binding: another Crew, a workflow or a connection calls this Crew (its functions). A Crew has no
 * webhooks of its own (#3286); a webhook always starts a workflow. */
export interface ProductAPITrigger {
  id: string
  name: string
  enabled: boolean
  message: string
  chat_id?: string
  run_destination: 'chat' | 'isolated' | 'new'
  kind?: string
  caller?: { type: string; id: string; profile_id?: string }
  /** The server's "Changed by <name>, <date>": who last changed this binding, else who made it. */
  changed_by?: string
}

/** One place a trigger's messages can run, from the server's shared list of choices. */
export interface RunDestinationChoice { id: string; label: string }

export type ProductTriggerScope = { profileId: string; projectId: string }

function config() {
  const token = getAuthToken()
  return { baseURL: getApiBaseUrl(), headers: token ? { Authorization: `Bearer ${token}` } : {} }
}

export const productWebhooksApi = {
  list: (scope: ProductTriggerScope) => axios.get<{ triggers: ProductAPITrigger[]; run_destinations?: RunDestinationChoice[] }>('/api/product-webhooks', {
    ...config(), params: { profile_id: scope.profileId, project_id: scope.projectId },
  }).then(response => response.data),
  delete: (scope: ProductTriggerScope, id: string) => axios.delete(`/api/product-webhooks/${encodeURIComponent(id)}`, {
    ...config(), params: { profile_id: scope.profileId, project_id: scope.projectId },
  }),
  runs: (scope: ProductTriggerScope, id: string, limit = 30) => axios.get<ListScheduledJobRunsResponse>(`/api/product-webhooks/${encodeURIComponent(id)}/runs`, {
    ...config(), params: { profile_id: scope.profileId, project_id: scope.projectId, limit },
  }).then(response => response.data),
  getPayload: (scope: ProductTriggerScope, id: string, runId: string) => axios.get<{ raw_payload: string }>(`/api/product-webhooks/${encodeURIComponent(id)}/runs/${encodeURIComponent(runId)}/payload`, {
    ...config(), params: { profile_id: scope.profileId, project_id: scope.projectId },
  }).then(response => response.data),
}

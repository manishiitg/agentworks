import axios from 'axios'
import { getApiBaseUrl, getAuthToken } from '../services/api'

/** One input, key or app. `ready` is the server's answer to "would a run started now get it"; key values never come here. */
export type CrewWorkflowUsesItem = { name: string; value?: string; ready: boolean }

/** What a saved Crew workflow uses, as the server works it out from the same settings its run reads. */
export type CrewWorkflowUses = {
  /** Where it runs: "Local" (this Mac) or "Online" (a server). */
  place: string
  inputs: CrewWorkflowUsesItem[]
  presets: string[]
  keys: CrewWorkflowUsesItem[]
  apps: CrewWorkflowUsesItem[]
  skills: string[]
  /** What it would need on its Crew's Team to run online; absent when the Crew has no Team. */
  team?: CrewWorkflowTeamNeeds
}

/** One row of "What it needs online". `id` is set only when it can be copied (state can_copy). */
export type CrewWorkflowTeamNeed = {
  kind: 'copied' | 'key' | 'app'
  name: string
  id?: string
  state: 'on_team' | 'can_copy' | 'not_in_vault' | 'this_mac_only' | 'needs_sign_in'
  added_by?: string
}
export type CrewWorkflowTeamNeeds = { items: CrewWorkflowTeamNeed[]; need_you: number }

function config() {
  const token = getAuthToken()
  return { baseURL: getApiBaseUrl(), headers: token ? { Authorization: `Bearer ${token}` } : {} }
}

/** The server's answer after Move online: anything the person still has to do on the Team. */
export type CrewWorkflowMoveResult = { notes: string[] }

export const crewWorkflowUsesApi = {
  /** Copies one key or app to the Team's Vault; the value goes server to server and never comes here. */
  copyToTeam: (workspacePath: string, kind: 'key' | 'app', id: string) => axios.post<{ notice?: string }>('/api/crew-workflow-uses', { kind, id }, { ...config(), params: { workspace_path: workspacePath } }).then(response => response.data),
  get: (workspacePath: string) => axios.get<CrewWorkflowUses>('/api/crew-workflow-uses', { ...config(), params: { workspace_path: workspacePath } }).then(response => response.data),
  moveOnline: (workspacePath: string) => axios.post<CrewWorkflowMoveResult>('/api/crew-workflow-move-online', null, { ...config(), params: { workspace_path: workspacePath } }).then(response => response.data),
}

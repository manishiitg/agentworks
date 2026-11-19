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
}

function config() {
  const token = getAuthToken()
  return { baseURL: getApiBaseUrl(), headers: token ? { Authorization: `Bearer ${token}` } : {} }
}

/** The server's answer after Move online: anything the person still has to do on the Team. */
export type CrewWorkflowMoveResult = { notes: string[] }

export const crewWorkflowUsesApi = {
  get: (workspacePath: string) => axios.get<CrewWorkflowUses>('/api/crew-workflow-uses', { ...config(), params: { workspace_path: workspacePath } }).then(response => response.data),
  moveOnline: (workspacePath: string) => axios.post<CrewWorkflowMoveResult>('/api/crew-workflow-move-online', null, { ...config(), params: { workspace_path: workspacePath } }).then(response => response.data),
}

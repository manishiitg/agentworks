import axios from 'axios'
import { getApiBaseUrl, getAuthToken } from '../services/api'

/** A step written in a workflow's Python file: its title, what it does (the docstring's first line) and whether an agent does it. */
export type CrewTaskStep = { title: string; description?: string; kind: 'script' | 'agent'; line: number }

function config() {
  const token = getAuthToken()
  return { baseURL: getApiBaseUrl(), headers: token ? { Authorization: `Bearer ${token}` } : {} }
}

const lastSeen = new Map<string, Record<string, CrewTaskStep[]>>()

/** The steps written in each Python file of a workflow, keyed by file name (the Crew's Builder only). */
export const crewTaskStepsApi = {
  /** The last answer for this workflow, so a screen that comes back shows its steps at once instead of flashing empty. */
  peek: (workspacePath: string): Record<string, CrewTaskStep[]> | undefined => lastSeen.get(workspacePath),
  get: (workspacePath: string) => axios.get<{ files: Record<string, CrewTaskStep[]> }>('/api/crew-task-steps', { ...config(), params: { workspace_path: workspacePath } })
    .then(response => (response.data && typeof response.data.files === 'object' && response.data.files ? response.data.files : {}))
    .then(files => { lastSeen.set(workspacePath, files); return files }),
}

import axios from 'axios'
import { getApiBaseUrl, getAuthToken } from '../services/api'

/** A file in a workflow's code folder. */
export type CrewCodeFile = { name: string; size: number; lines: number }

function config() {
  const token = getAuthToken()
  return { baseURL: getApiBaseUrl(), headers: token ? { Authorization: `Bearer ${token}` } : {} }
}

const lastSeen = new Map<string, CrewCodeFile[]>()

/** The files in a workflow's code folder (the Crew's Builder only). Always a list, empty when the server cannot say. */
export const crewCodeFilesApi = {
  /** The last list read for this workflow, so a screen that comes back (after a file was open) shows it at once. */
  peek: (workspacePath: string): CrewCodeFile[] | undefined => lastSeen.get(workspacePath),
  get: (workspacePath: string) => axios.get<{ files: CrewCodeFile[] }>('/api/crew-code-files', { ...config(), params: { workspace_path: workspacePath } })
    .then(response => (Array.isArray(response.data?.files) ? response.data.files : []))
    .then(files => { lastSeen.set(workspacePath, files); return files }),
}

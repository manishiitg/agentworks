import axios from 'axios'
import { getApiBaseUrl, getAuthToken } from '../services/api'

/** A file a script run made in its run folder; `path` is the workspace path that opens it. */
export type CrewRunFileItem = { name: string; path: string; size: number }

function config() {
  const token = getAuthToken()
  return { baseURL: getApiBaseUrl(), headers: token ? { Authorization: `Bearer ${token}` } : {} }
}

/** The files one run made, such as a report (the Crew's Builder only). */
export const crewRunFilesApi = {
  get: (workspacePath: string, runFolder: string) => axios.get<{ files: CrewRunFileItem[] }>('/api/crew-run-files', { ...config(), params: { workspace_path: workspacePath, run_folder: runFolder } }).then(response => (Array.isArray(response.data?.files) ? response.data.files : [])),
}

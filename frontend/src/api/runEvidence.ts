import axios from 'axios'
import { getApiBaseUrl, getAuthToken } from '../services/api'

/** One check() or notify() a run made, as the server persisted it (bounded, redacted). */
export interface RunEvidenceItem {
  kind: 'check' | 'notify'
  at: string
  name?: string
  ok?: boolean
  detail: string
  source_call?: string
}

function config() {
  const token = getAuthToken()
  return { baseURL: getApiBaseUrl(), headers: token ? { Authorization: `Bearer ${token}` } : {} }
}

export const runEvidenceApi = {
  get: (workspacePath: string, runFolder: string) => axios.get<{ items: RunEvidenceItem[] }>('/api/workspace/run-evidence', { ...config(), params: { workspace_path: workspacePath, run_folder: runFolder } }).then(response => response.data.items ?? []),
}

import axios from 'axios'
import type { RelaySourceGraph } from '../components/workflow/relayGraphAnnotations'
import { getApiBaseUrl, getAuthToken } from '../services/api'

function config() {
  const token = getAuthToken()
  return { baseURL: getApiBaseUrl(), headers: token ? { Authorization: `Bearer ${token}` } : {} }
}

/** The outline of a saved Python workflow, read by the server from its own code (the same graph JSON a Relay has). */
export const crewScriptGraphApi = {
  get: (workspacePath: string) => axios.get<RelaySourceGraph>('/api/crew-script-graph', { ...config(), params: { workspace_path: workspacePath } }).then(response => response.data),
}

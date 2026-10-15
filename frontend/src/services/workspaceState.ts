import api, { getApiBaseUrl, getAuthToken } from './api'
import type { ProductRoute } from '../utils/productRoutes'
import type { ActiveSessionInfo } from './api-types'
export type WorkspaceLayout = { chat_open: boolean; panel_open: boolean; split_ratio: number; navigation_mode: 'fixed' | 'auto-hide' }
export type WorkspaceDestination = ProductRoute & {
  session_id?: string
  workspace_path?: string
  chat?: { name: string; read_only: boolean; conversation_key?: string }
  layout?: WorkspaceLayout
}
export type WorkspaceState = { version: 1; revision: number; last: WorkspaceDestination | null; recent: WorkspaceDestination[]; updated_at?: string; note?: string }
export type HomeWork = { surface: ProductRoute['surface']; id: string; label: string; workspace_path: string; updated_at?: string }
export type HomeAttention = { id: string; kind: string; title: string; work?: HomeWork; session_id?: string; view?: string; updated_at?: string }
export type HomeData = { attention: HomeAttention[]; work: HomeWork[]; running: ActiveSessionInfo[]; warnings: string[]; truncated: boolean }
export const workspaceStateApi = {
  get: async (signal?: AbortSignal): Promise<WorkspaceState> => (await api.get('/api/workspace-state', { signal, skipSessionContext: true })).data,
  save: async (state: WorkspaceState, signal?: AbortSignal): Promise<WorkspaceState> => (await api.put('/api/workspace-state', { version: 1, revision: state.revision, last: state.last }, { signal, skipSessionContext: true })).data,
  home: async (signal?: AbortSignal): Promise<HomeData> => (await api.get('/api/home', { signal, skipSessionContext: true })).data,
}

// Flush a settled pending snapshot when the document leaves. Keepalive uses the
// same authenticated API origin, without a current chat's session header.
export function saveWorkspaceStateOnExit(state: WorkspaceState) {
  const token = getAuthToken()
  return fetch(`${getApiBaseUrl()}/api/workspace-state`, {
    method: 'PUT', keepalive: true, credentials: 'include',
    headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}) },
    body: JSON.stringify({ version: 1, revision: state.revision, last: state.last }),
  }).catch(() => undefined)
}

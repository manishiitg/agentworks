import { captureChatIdentity } from '../utils/chatIdentity'
import api, { getApiBaseUrl, getAuthToken } from './api'
import type { ProductRoute } from '../utils/productRoutes'
import type { ActiveSessionInfo } from './api-types'
export type WorkspaceLayout = { chat_open: boolean; panel_open: boolean; split_ratio: number; navigation_mode: 'fixed' | 'auto-hide'; preview_device?: 'mobile' | 'tablet' | 'desktop'; focused_pane?: 'chat' | 'preview'; mobile_pane_width?: number }
export type WorkspaceDestination = ProductRoute & {
  session_id?: string
  workspace_path?: string
  chat?: { name: string; read_only?: boolean; execution_run?: boolean; scheduled_run?: boolean; scheduled_job_name?: string; bot_platform?: string; view_mode?: 'formatted' | 'terminal' | 'tree'; workshop_mode?: 'workshop' | 'run'; conversation_key?: string }
  layout?: WorkspaceLayout
}
export type WorkspaceState = { version: 1; revision: number; last: WorkspaceDestination | null; recent: WorkspaceDestination[]; updated_at?: string; note?: string; destination?: WorkspaceDestination; first_visit?: boolean }
export type HomeWork = { surface: ProductRoute['surface']; id: string; label: string; workspace_path: string; updated_at?: string }
export type HomeAttention = { id: string; kind: string; title: string; work?: HomeWork; session_id?: string; view?: string; updated_at?: string }
export type HomeData = { attention: HomeAttention[]; work: HomeWork[]; running: ActiveSessionInfo[]; running_work: Record<string, HomeWork>; warnings: string[]; truncated: boolean }
export const workspaceStateApi = {
  get: async (signal?: AbortSignal): Promise<WorkspaceState> => (await api.get('/api/workspace-state', { signal, skipSessionContext: true, chatIdentityGeneration: captureChatIdentity() })).data,
  save: async (state: WorkspaceState, signal?: AbortSignal): Promise<WorkspaceState> => (await api.put('/api/workspace-state', { version: 1, revision: state.revision, last: state.last }, { signal, skipSessionContext: true, chatIdentityGeneration: captureChatIdentity() })).data,
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

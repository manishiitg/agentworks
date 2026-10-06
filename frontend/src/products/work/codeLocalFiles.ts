import { useMemo, useSyncExternalStore } from 'react'
import api, { getApiBaseUrl } from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'

export interface CodeLocalFileTarget { device_id: string; resource_id: string }
export type CodeFilesPreference = { location: 'server' } | { location: 'computer'; target?: CodeLocalFileTarget }
export interface LocalFolderGuard { read_paths?: string[]; write_paths?: string[]; read_only_paths?: string[]; blocked_write_paths?: string[]; blocked_paths?: string[] }
export interface LocalFileDevice { device_id: string; resources: { id: string; writable: boolean; guard: LocalFolderGuard }[] }
export interface LocalFile { path: string; exists: boolean; content?: string; revision: string }
export interface LocalFileEntry { path: string; type: string; size?: number }
export interface LocalFileReceipt { revision: string; identity: { username?: string; user_id?: string }; applied: boolean }
export interface LocalFileResponse { file?: LocalFile; entries?: LocalFileEntry[]; receipt?: LocalFileReceipt }
const changed = 'code-files-location-changed'
const validID = /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$/

function preferenceKey(workspacePath: string) {
  return `code-files-location:${JSON.stringify([getApiBaseUrl(), useWorkspaceConnectionStore.getState().activeWorkspaceId, useAuthStore.getState().user?.id ?? 'local', workspacePath])}`
}
function stored(key: string) { try { return localStorage.getItem(key) } catch { return null } }
function parse(value: string | null): CodeFilesPreference {
  try {
    const pref = JSON.parse(value || 'null')
    if (pref?.location === 'computer') {
      const target = pref.target
      return { location: 'computer', ...(target && typeof target.device_id === 'string' && typeof target.resource_id === 'string' && validID.test(target.device_id) && validID.test(target.resource_id) ? { target } : {}) }
    }
  } catch { /* Unknown preferences use server files. */ }
  return { location: 'server' }
}
export function readCodeFilesPreference(workspacePath: string) { return parse(stored(preferenceKey(workspacePath))) }
export function writeCodeFilesPreference(workspacePath: string, pref: CodeFilesPreference) {
  localStorage.setItem(preferenceKey(workspacePath), JSON.stringify(pref))
  window.dispatchEvent(new Event(changed))
}
function subscribe(listener: () => void) {
  window.addEventListener(changed, listener)
  window.addEventListener('storage', listener)
  return () => { window.removeEventListener(changed, listener); window.removeEventListener('storage', listener) }
}
export function useCodeFilesPreference(workspacePath: string) {
  const user = useAuthStore(state => state.user?.id)
  const workspace = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const key = useMemo(() => preferenceKey(workspacePath), [workspacePath, user, workspace])
  const raw = useSyncExternalStore(subscribe, () => stored(key), () => null)
  return useMemo(() => parse(raw), [raw])
}
/** The selection is context, never authority; the backend rechecks local grants. */
export function codeLocalFilesForChat(workspacePath: string): CodeLocalFileTarget | undefined {
  const pref = readCodeFilesPreference(workspacePath)
  if (pref.location === 'server') return undefined
  if (!pref.target) throw new Error('Choose a connected computer and folder in Code → Files before sending a message.')
  return pref.target
}
export const codeLocalFilesApi = {
  devices: async (): Promise<LocalFileDevice[]> => (await api.get<{ devices: LocalFileDevice[] }>('/api/devices', { skipSessionContext: true })).data.devices,
  call: async (target: CodeLocalFileTarget, operation: 'list' | 'read' | 'write', path: string, write?: { content: string; expected_revision: string; request_id: string }): Promise<LocalFileResponse> =>
    (await api.post<LocalFileResponse>(`/api/devices/${encodeURIComponent(target.device_id)}/files`, { resource_id: target.resource_id, operation, path, ...write }, { skipSessionContext: true })).data,
}
export function localFileError(cause: unknown) {
  const err = cause as { response?: { data?: { error?: { message?: string } | string } }; message?: string }
  const error = err.response?.data?.error
  return typeof error === 'string' ? error : error?.message || err.message || 'Could not access local files.'
}

import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react'
import api, { getApiBaseUrl } from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'

export interface CodeLocalFileTarget { device_id: string; resource_id: string }
export type CodeFilesPreference = { location: 'server' } | { location: 'computer'; target?: CodeLocalFileTarget }
export interface LocalFolderGuard { read_paths?: string[]; write_paths?: string[]; read_only_paths?: string[]; blocked_write_paths?: string[]; blocked_paths?: string[] }
export interface LocalFileDevice { device_id: string; resources: { id: string; writable: boolean; shell?: boolean; downloads?: boolean; guard: LocalFolderGuard }[] }
const changed = 'code-files-location-changed'
const validID = /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$/

function preferenceKey(sessionId: string) {
  return `code-files-location:${JSON.stringify([getApiBaseUrl(), useWorkspaceConnectionStore.getState().activeWorkspaceId, useAuthStore.getState().user?.id ?? 'local', sessionId])}`
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
// `agentworks start` opens the site with the computer and folder it just shared; that becomes the default for Code chats
// that have no choice of their own, for 12 hours, so the chat starts in Local mode. A chat's own choice always wins.
const defaultKey = 'code-files-default-local'
const defaultTtlMs = 12 * 60 * 60 * 1000
export function setDefaultLocalTarget(target: CodeLocalFileTarget) {
  if (!validID.test(target.device_id) || !validID.test(target.resource_id)) return
  try { localStorage.setItem(defaultKey, JSON.stringify({ target, at: Date.now() })) } catch { /* Without storage the chat stays on server files. */ }
  window.dispatchEvent(new Event(changed))
}
function defaultLocalTarget(): CodeLocalFileTarget | undefined {
  try {
    const saved = JSON.parse(localStorage.getItem(defaultKey) || 'null')
    const target = saved?.target
    if (saved && Date.now() - saved.at < defaultTtlMs && typeof target?.device_id === 'string' && typeof target?.resource_id === 'string' && validID.test(target.device_id) && validID.test(target.resource_id)) return target
  } catch { /* An unreadable default is no default. */ }
  return undefined
}
function effectiveRaw(key: string) {
  const own = stored(key)
  if (own !== null) return own
  const target = defaultLocalTarget()
  return target ? JSON.stringify({ location: 'computer', target }) : null
}
export function readCodeFilesPreference(sessionId: string) { return parse(effectiveRaw(preferenceKey(sessionId))) }
export function writeCodeFilesPreference(sessionId: string, pref: CodeFilesPreference) {
  localStorage.setItem(preferenceKey(sessionId), JSON.stringify(pref))
  window.dispatchEvent(new Event(changed))
}
function subscribe(listener: () => void) {
  window.addEventListener(changed, listener)
  window.addEventListener('storage', listener)
  return () => { window.removeEventListener(changed, listener); window.removeEventListener('storage', listener) }
}
export function useCodeFilesPreference(sessionId: string) {
  const user = useAuthStore(state => state.user?.id)
  const workspace = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const key = useMemo(() => preferenceKey(sessionId), [sessionId, user, workspace])
  const raw = useSyncExternalStore(subscribe, () => effectiveRaw(key), () => null)
  return useMemo(() => parse(raw), [raw])
}
/** The selection is context, never authority; the backend rechecks local grants. */
export function codeChatModeForChat(sessionId: string): 'server' | 'local' {
  return readCodeFilesPreference(sessionId).location === 'computer' ? 'local' : 'server'
}
export function codeLocalFilesForChat(sessionId: string): CodeLocalFileTarget | undefined {
  const pref = readCodeFilesPreference(sessionId)
  if (pref.location === 'server') return undefined
  return pref.target
}
export const codeLocalFilesApi = {
  devices: async (): Promise<LocalFileDevice[]> => (await api.get<{ devices: LocalFileDevice[] }>('/api/devices', { skipSessionContext: true })).data.devices,

}
export function localFileError(cause: unknown) {
  const err = cause as { response?: { data?: { error?: { message?: string } | string } }; message?: string }
  const error = err.response?.data?.error
  return typeof error === 'string' ? error : error?.message || err.message || 'Could not access local files.'
}

export function useLocalFileDevices(enabled: boolean) {
  const account = useAuthStore(state => state.user?.id)
  const workspace = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const [devices, setDevices] = useState<LocalFileDevice[]>([])
  const [error, setError] = useState<string | null>(null)
  const [checked, setChecked] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const refreshRef = useRef<() => void>(() => {})
  useEffect(() => {
    setDevices([]); setChecked(false); setError(null); setRefreshing(false)
    if (!enabled) return
    let active = true
    let loading = false
    const refresh = async () => {
      if (loading) return
      loading = true
      if (active) setRefreshing(true)
      try { const listed = await codeLocalFilesApi.devices(); if (active) { setDevices(listed); setError(null); setChecked(true) } }
      catch (cause) { if (active) { setDevices([]); setError(localFileError(cause)); setChecked(true) } }
      finally { loading = false; if (active) setRefreshing(false) }
    }
    refreshRef.current = () => { void refresh() }
    void refresh()
    const timer = window.setInterval(() => void refresh(), 5000)
    return () => { active = false; refreshRef.current = () => {}; window.clearInterval(timer) }
  }, [enabled, account, workspace])
  return { devices, error, checked, refreshing, refresh: () => refreshRef.current() }
}

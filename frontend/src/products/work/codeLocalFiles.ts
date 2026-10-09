import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react'
import api, { getApiBaseUrl } from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { useChatStore } from '../../stores/useChatStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'

export interface CodeLocalFileTarget { device_id: string; resource_id: string }
export type CodeFilesPreference = { location: 'server' } | { location: 'computer'; target?: CodeLocalFileTarget }
export interface LocalFolderGuard { read_paths?: string[]; write_paths?: string[]; read_only_paths?: string[]; blocked_write_paths?: string[]; blocked_paths?: string[] }
export interface LocalFileDevice { device_id: string; resources: { id: string; writable: boolean; shell?: boolean; downloads?: boolean; guard: LocalFolderGuard }[] }
const changed = 'code-files-location-changed'
const validID = /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$/

type TabsLike = Record<string, { sessionId?: string | null; metadata?: { agentProfileProjectId?: string } }>
function projectOf(chatTabs: TabsLike, sessionId: string): string | undefined {
  return Object.values(chatTabs).find(tab => tab.sessionId === sessionId)?.metadata?.agentProfileProjectId || undefined
}
// Without a known workspace (no project behind the chat) the choice is kept per chat in this browser.
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
// `agentworks start` opens the site with the computer, the folder and the name of the Code workspace that uses it. The link is
// kept (12 hours, until used) so a sign-in redirect cannot lose it; the workspace screen finds the workspace by name and saves the
// folder in its product.json (takePendingLocalLink). Nothing is ever applied to a workspace the user did not name.
const pendingLinkKey = 'code-local-pending-link'
const pendingLinkTtlMs = 12 * 60 * 60 * 1000
export interface PendingLocalLink { target: CodeLocalFileTarget; workspace: string }
export function setPendingLocalLink(link: PendingLocalLink) {
  if (!validID.test(link.target.device_id) || !validID.test(link.target.resource_id) || !link.workspace.trim()) return
  try { localStorage.setItem(pendingLinkKey, JSON.stringify({ ...link, at: Date.now() })) } catch { /* Without storage the link is not remembered. */ }
  window.dispatchEvent(new Event(changed))
}
function readPendingLocalLink(): PendingLocalLink | undefined {
  try {
    const saved = JSON.parse(localStorage.getItem(pendingLinkKey) || 'null')
    const target = saved?.target
    if (saved && Date.now() - saved.at < pendingLinkTtlMs && typeof saved.workspace === 'string' && saved.workspace.trim() && typeof target?.device_id === 'string' && typeof target?.resource_id === 'string' && validID.test(target.device_id) && validID.test(target.resource_id)) return { target, workspace: saved.workspace }
  } catch { /* An unreadable link is no link. */ }
  return undefined
}
export function peekPendingLocalLink() { return readPendingLocalLink() }
export function clearPendingLocalLink() { try { localStorage.removeItem(pendingLinkKey) } catch { /* Nothing to clear. */ } }
export function takePendingLocalLink(): PendingLocalLink | undefined {
  const link = readPendingLocalLink()
  if (link) clearPendingLocalLink()
  return link
}

// The computer and folder a Code workspace works in is saved in its product.json (`local_files`), so it survives a refresh,
// another browser and another device; switching to server files is a deliberate act, not a toggle. The workspace screen
// loads it here (setProjectLocalFiles) and registers how to save it (registerLocalFilesPersister).
const projectLocal = new Map<string, CodeLocalFileTarget | null>() // project ID -> target; null = the server's files
type LocalFilesPersister = (projectId: string, target: CodeLocalFileTarget | null) => Promise<void>
let persister: LocalFilesPersister | undefined
export function registerLocalFilesPersister(fn: LocalFilesPersister | undefined) { persister = fn }
export function setProjectLocalFiles(projectId: string, target: CodeLocalFileTarget | null) {
  const current = projectLocal.get(projectId)
  if (projectLocal.has(projectId) && (current?.device_id ?? '') === (target?.device_id ?? '') && (current?.resource_id ?? '') === (target?.resource_id ?? '')) return
  projectLocal.set(projectId, target)
  window.dispatchEvent(new Event(changed))
}
// A project made in Local mode has no folder until `agentworks start` links one: it is Local already, and says so.
const projectModes = new Map<string, 'dev' | 'cowork' | 'local'>()
export function setProjectMode(projectId: string, mode: 'dev' | 'cowork' | 'local') {
  if (projectModes.get(projectId) === mode) return
  projectModes.set(projectId, mode)
  window.dispatchEvent(new Event(changed))
}
export function useProjectMode(sessionId: string): 'dev' | 'cowork' | 'local' {
  const project = useChatStore(state => projectOf(state.chatTabs, sessionId))
  return useSyncExternalStore(subscribe, () => (project && projectModes.get(project)) || 'dev', () => 'dev')
}
function resolveRaw(sessionId: string): string | null {
  const project = projectOf(useChatStore.getState().chatTabs, sessionId)
  if (project && projectLocal.has(project)) {
    const target = projectLocal.get(project)
    if (!target && projectModes.get(project) === 'local') return JSON.stringify({ location: 'computer' })
    return JSON.stringify(target ? { location: 'computer', target } : { location: 'server' })
  }
  return stored(preferenceKey(sessionId))
}
export function readCodeFilesPreference(sessionId: string) { return parse(resolveRaw(sessionId)) }
export function writeCodeFilesPreference(sessionId: string, pref: CodeFilesPreference) {
  const project = projectOf(useChatStore.getState().chatTabs, sessionId)
  if (project && projectLocal.has(project) && persister) {
    const target = pref.location === 'computer' && pref.target ? pref.target : null
    const previous = projectLocal.get(project) ?? null
    projectLocal.set(project, target)
    window.dispatchEvent(new Event(changed))
    void persister(project, target).catch(() => { projectLocal.set(project, previous); window.dispatchEvent(new Event(changed)) })
    return
  }
  localStorage.setItem(preferenceKey(sessionId), JSON.stringify(pref))
  window.dispatchEvent(new Event(changed))
}
function subscribe(listener: () => void) {
  window.addEventListener(changed, listener)
  window.addEventListener('storage', listener)
  return () => { window.removeEventListener(changed, listener); window.removeEventListener('storage', listener) }
}
export function useCodeFilesPreference(sessionId: string) {
  // These re-render the hook when the account, server workspace or the chat's project changes; the snapshot reads fresh state.
  useAuthStore(state => state.user?.id)
  useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  useChatStore(state => projectOf(state.chatTabs, sessionId))
  const raw = useSyncExternalStore(subscribe, () => resolveRaw(sessionId), () => null)
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

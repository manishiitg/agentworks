import { useEffect, useRef, useState, useSyncExternalStore } from 'react'
import api from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { useChatStore } from '../../stores/useChatStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'

export interface LocalFileDevice { device_id: string; settings_managed?: boolean; cli_version?: string; cli_build?: string; resources: { id: string; writable: boolean; publication?: boolean; shell?: boolean; downloads?: boolean; guard: { read_paths?: string[]; write_paths?: string[] } }[] }
const changed = 'crew-laptop-connection-changed'

type TabsLike = Record<string, { sessionId?: string | null; metadata?: { agentProfileProjectId?: string } }>
function projectOf(chatTabs: TabsLike, sessionId: string): string | undefined {
  return Object.values(chatTabs).find(tab => tab.sessionId === sessionId)?.metadata?.agentProfileProjectId || undefined
}
// Without a known workspace (no project behind the chat) the choice is kept per chat in this browser.
// Server mode and optional laptop connection are independent.
const projectModes = new Map<string, 'dev' | 'cowork'>()
export function setProjectMode(projectId: string, mode: 'dev' | 'cowork') {
  if (projectModes.get(projectId) === mode) return
  projectModes.set(projectId, mode)
  window.dispatchEvent(new Event(changed))
}
export function useProjectMode(sessionId: string): 'dev' | 'cowork' {
  const project = useChatStore(state => projectOf(state.chatTabs, sessionId))
  return useSyncExternalStore(subscribe, () => (project && projectModes.get(project)) || 'dev', () => 'dev')
}
function subscribe(listener: () => void) {
  window.addEventListener(changed, listener)
  return () => window.removeEventListener(changed, listener)
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
  const refreshRef = useRef<() => Promise<LocalFileDevice[] | null>>(async () => null)
  useEffect(() => {
    setDevices([]); setChecked(false); setError(null); setRefreshing(false)
    if (!enabled) return
    let active = true
    let loading = false
    const refresh = async (): Promise<LocalFileDevice[] | null> => {
      if (loading) return null
      loading = true
      if (active) setRefreshing(true)
      try { const listed = await codeLocalFilesApi.devices(); if (active) { setDevices(listed); setError(null); setChecked(true) } return listed }
      catch (cause) { if (active) { setDevices([]); setError(localFileError(cause)); setChecked(true) } return null }
      finally { loading = false; if (active) setRefreshing(false) }
    }
    refreshRef.current = refresh
    void refresh()
    const timer = window.setInterval(() => void refresh(), 5000)
    return () => { active = false; refreshRef.current = async () => null; window.clearInterval(timer) }
  }, [enabled, account, workspace])
  return { devices, error, checked, refreshing, refresh: () => refreshRef.current() }
}

// The CLI build this server currently offers (its public version.json), read once per page load; undefined until known or when the
// server publishes none.
export interface LatestCli { version: string; build?: string }
let latestCliPromise: Promise<LatestCli | undefined> | undefined
function fetchLatestCliVersion(): Promise<LatestCli | undefined> {
  latestCliPromise ??= api.get<{ version?: string; cli_build?: string }>('/api/downloads/cli/version.json', { skipSessionContext: true })
    .then((response): LatestCli | undefined => {
      const version = typeof response.data?.version === 'string' ? response.data.version.trim() : ''
      const build = typeof response.data?.cli_build === 'string' ? response.data.cli_build.trim() : ''
      return version ? { version, build: build || undefined } : undefined
    })
    .catch(() => undefined)
  return latestCliPromise
}
export function resetLatestCliVersionForTests() { latestCliPromise = undefined }
export function useLatestCliVersion(enabled: boolean): LatestCli | undefined {
  const [latest, setLatest] = useState<LatestCli | undefined>()
  useEffect(() => {
    if (!enabled) return
    let live = true
    void fetchLatestCliVersion().then(version => { if (live) setLatest(version) })
    return () => { live = false }
  }, [enabled])
  return latest
}
/** The connected CLI is not the build this server offers (a CLI from before it reported its version counts as older). */
export function cliUpdateNeeded(device: LocalFileDevice | undefined, latest: LatestCli | undefined): boolean {
  if (!device || !latest) return false
  if (device.cli_version === 'dev') return false
  // By the CLI's own build when both sides report one (it changes only when the CLI does), else by the whole-repository revision.
  if (latest.build && device.cli_build && device.cli_build !== 'dev') return device.cli_build !== latest.build
  return device.cli_version !== latest.version
}
export function shortCliVersion(version: string | undefined) { return version ? version.slice(0, 7) : 'older' }

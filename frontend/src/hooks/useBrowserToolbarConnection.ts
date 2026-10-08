import { useEffect, useRef, useState } from 'react'
import api from '../services/api'
import { useLiveRefetch } from './useLiveRefetch'
import type { ChromeExtensionStatus } from '../components/workflow/ChromeExtensionConnection'

/** Read-only discovery; opening another pane must not hide browser health. */
export function useBrowserToolbarConnection(workspacePath: string | null | undefined, profileId: string, enabled = true, checkExtension = true): boolean {
  const scope = `${profileId}:${workspacePath}:${enabled}:${checkExtension}`
  const [connection, setConnection] = useState({ scope: '', connected: false })
  const refreshRef = useRef<() => void>(() => {})
  useEffect(() => {
    setConnection({ scope, connected: false })
    if (!enabled || !workspacePath) return
    const controller = new AbortController()
    let alive = true
    let refreshing = false
    async function refresh() {
      if (refreshing) return
      refreshing = true
      const config = { params: { workspace_path: workspacePath, profile_id: profileId }, skipSessionContext: true, signal: controller.signal, timeout: 5000 }
      try {
        if (checkExtension) {
          const { data } = await api.get<ChromeExtensionStatus>('/api/browser/extension', config)
          if (data.selected) {
            if (alive) setConnection({ scope, connected: data.connected })
            return
          }
        }
        const { data } = await api.get<{ sessions: Array<{ kind?: string; state?: string }> }>('/api/browser/live/sessions', config)
        // Completed Playwright recordings are replays, not live connections.
        if (alive) setConnection({ scope, connected: (data.sessions ?? []).some(session => session.state !== 'completed') })
      } catch {
        if (alive) setConnection({ scope, connected: false })
      } finally { refreshing = false }
    }
    refreshRef.current = () => { void refresh() }
    void refresh()
    window.addEventListener('focus', refresh)
    return () => { alive = false; controller.abort(); refreshRef.current = () => {}; window.removeEventListener('focus', refresh) }
  }, [scope, workspacePath, profileId, enabled, checkExtension])
  // The server announces a browser change on the live feed; the old 2.5 s poll only runs while the feed is down. The
  // slow check also covers Playwright sessions, which the feed does not announce.
  useLiveRefetch(() => refreshRef.current(), { kinds: ['browser'], fallbackMs: 2500, safetyMs: 30_000, minIntervalMs: 500, enabled: enabled && !!workspacePath })
  return connection.scope === scope && connection.connected
}

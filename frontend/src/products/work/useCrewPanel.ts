import { useEffect, useRef, useState } from 'react'
import api from '../../services/api'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import type { CloudState, CrewAccessLine, CrewAsk } from './YourComputerPanel'

export type CrewPanelRow = { id: string; label: string; detail?: string; destination?: string; connected?: boolean; attention?: boolean; path?: string; group?: string; count?: string; motion?: boolean; artifact?: boolean }
export type CrewPanelSection = { id: string; title: string; detail?: string; rows: CrewPanelRow[]; busy?: boolean; stale?: boolean; error?: string; access?: { where?: 'mac' | 'web'; place?: CloudState; role?: string; view?: string; summary?: string; tree?: CrewAccessLine[]; asking?: (CrewAsk & { label: string; detail: string })[] } }
export type CrewPanel = { project: string; workspace: string; busy: boolean; mac_elsewhere: boolean; sections: CrewPanelSection[]; details: Record<string, string>; refresh_after_ms?: number; refresh_attempt?: number; refresh_note?: string }

// The last answer for each scope and project: the menu is mounted again every time a person comes back to it, and
// a far-away server needs a few seconds to answer. Show what was last known at once; the new answer replaces it.
const lastKnown = new Map<string, CrewPanel>()

/** One current-account read. The existing tab-wide feed handles reconnect and
 * resync; timers are a fallback only. Settings screens retain their own APIs. */
export function useCrewPanel(scope: string, project: string, workspace: string) {
  const key = JSON.stringify([scope, project, workspace])
  const [state, setState] = useState<{ key: string; data?: CrewPanel; error?: string }>()
  const refreshRef = useRef<() => void>(() => {})
  useEffect(() => {
    const controller = new AbortController()
    let reading = false
    let again = false
    let retryTimer: ReturnType<typeof setTimeout> | undefined
    async function read(attempt = 0) {
      clearTimeout(retryTimer)
      retryTimer = undefined
      if (reading) { again = true; return }
      if (!project || controller.signal.aborted) return
      reading = true
      try {
        const { data } = await api.get<CrewPanel>(`/api/crews/${encodeURIComponent(project)}/panel`, { params: { refresh_attempt: attempt || undefined }, signal: controller.signal, skipSessionContext: true })
        if (!controller.signal.aborted) {
          lastKnown.set(key, data)
          setState({ key, data, error: data.refresh_note })
          // The server alone chooses the retry time and next attempt. A fresh
          // answer supplies neither, so this never becomes a polling loop.
          if (data.refresh_after_ms !== undefined && data.refresh_after_ms > 0) {
            retryTimer = setTimeout(() => { void read(data.refresh_attempt) }, data.refresh_after_ms)
          }
        }
      } catch {
        // Do not retain a previous account's or now-revoked Crew's data after
        // a failed admission. Section-level last-good values live on the server.
        if (!controller.signal.aborted) { lastKnown.delete(key); setState({ key, error: 'Could not load this Crew’s status. Try again.' }) }
      } finally {
        reading = false
        if (again && !controller.signal.aborted) { again = false; void read() }
      }
    }
    refreshRef.current = () => { void read() }
    void read()
    return () => { controller.abort(); clearTimeout(retryTimer); refreshRef.current = () => {} }
  }, [key, project])
  const data = state?.key === key ? state.data : lastKnown.get(key)
  useLiveRefetch(() => refreshRef.current(), {
    kinds: ['crew_panel', 'sessions', 'schedules', 'report', 'plan', 'browser'],
    project, workflow: workspace, scopeKey: scope, enabled: !!project,
    refreshOnFocus: true, fallbackMs: data?.busy ? 2_000 : 15_000, safetyMs: 0, minIntervalMs: 500,
  })
  return { data, error: state?.key === key ? state.error : undefined, refresh: () => refreshRef.current() }
}

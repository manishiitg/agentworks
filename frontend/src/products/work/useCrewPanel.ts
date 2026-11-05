import { useEffect, useRef, useState } from 'react'
import api from '../../services/api'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import type { CloudState, CrewAccessLine, CrewAsk } from './YourComputerPanel'

export type CrewPanelRow = { id: string; label: string; detail?: string; destination?: string; connected?: boolean; attention?: boolean; path?: string; used_in_chat?: boolean; motion?: boolean; artifact?: boolean }
export type CrewPanelSection = { id: string; title: string; detail?: string; rows: CrewPanelRow[]; busy?: boolean; stale?: boolean; error?: string; access?: { where?: 'mac' | 'web'; place?: CloudState; role?: string; view?: string; summary?: string; tree?: CrewAccessLine[]; asking?: (CrewAsk & { label: string; detail: string })[] } }
export type CrewPanel = { project: string; workspace: string; busy: boolean; mac_elsewhere: boolean; sections: CrewPanelSection[]; details: Record<string, string> }

/** One current-account read. The existing tab-wide feed handles reconnect and
 * resync; timers are a fallback only. Settings screens retain their own APIs. */
export function useCrewPanel(scope: string, project: string, workspace: string, session?: string) {
  const key = JSON.stringify([scope, project, workspace, session])
  const [state, setState] = useState<{ key: string; data?: CrewPanel; error?: string }>()
  const refreshRef = useRef<() => void>(() => {})
  useEffect(() => {
    const controller = new AbortController()
    let reading = false
    let again = false
    async function read() {
      if (reading) { again = true; return }
      if (!project || controller.signal.aborted) return
      reading = true
      try {
        const { data } = await api.get<CrewPanel>(`/api/crews/${encodeURIComponent(project)}/panel`, { params: { session_id: session }, signal: controller.signal, skipSessionContext: true })
        if (!controller.signal.aborted) setState({ key, data })
      } catch {
        // Do not retain a previous account's or now-revoked Crew's data after
        // a failed admission. Section-level last-good values live on the server.
        if (!controller.signal.aborted) setState({ key, error: 'Could not load this Crew’s status. Try again.' })
      } finally {
        reading = false
        if (again && !controller.signal.aborted) { again = false; void read() }
      }
    }
    refreshRef.current = () => { void read() }
    void read()
    return () => { controller.abort(); refreshRef.current = () => {} }
  }, [key, project, session])
  const data = state?.key === key ? state.data : undefined
  useLiveRefetch(() => refreshRef.current(), {
    kinds: ['crew_panel', 'sessions', 'schedules', 'report', 'plan', 'browser'],
    project, workflow: workspace, scopeKey: scope, enabled: !!project,
    refreshOnFocus: true, fallbackMs: data?.busy ? 2_000 : 15_000, safetyMs: 0, minIntervalMs: 500,
  })
  return { data, error: state?.key === key ? state.error : undefined, refresh: () => refreshRef.current() }
}

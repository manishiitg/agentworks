import { useCallback, useEffect, useState } from 'react'
import { useLiveRefetch } from '../../../hooks/useLiveRefetch'
import { BOTS_CHANGED_EVENT, readProjectBots, type ProjectBots } from './botsApi'

/** The bots added to a project on the server (#4545), read again whenever bots change: here, or anywhere else (an
 * agent's `bots add`, another tab), which the server announces as a project change. On a Mac, a project here keeps the
 * signed-in server's bots it added. */
export function useProjectBots(projectId: string | null | undefined): ProjectBots | null {
  const [state, setState] = useState<{ projectId: string; value: ProjectBots } | null>(null)
  const [reads, setReads] = useState(0)
  const reload = useCallback(() => setReads(n => n + 1), [])
  useEffect(() => {
    if (!projectId) return
    let cancelled = false
    void readProjectBots(projectId).then(value => { if (!cancelled) setState({ projectId, value }) }).catch(() => { if (!cancelled) setState({ projectId, value: { bots: [], unavailable: [] } }) })
    return () => { cancelled = true }
  }, [projectId, reads])
  useEffect(() => {
    window.addEventListener(BOTS_CHANGED_EVENT, reload)
    return () => window.removeEventListener(BOTS_CHANGED_EVENT, reload)
  }, [reload])
  useLiveRefetch(reload, { kinds: ['crew_panel'], project: projectId, enabled: !!projectId, refreshOnFocus: true, fallbackMs: 0, safetyMs: 0, minIntervalMs: 3_000 })
  return state && state.projectId === projectId ? state.value : null
}

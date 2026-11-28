import { useEffect, useState } from 'react'
import { BOTS_CHANGED_EVENT, readProjectBots, type ProjectBots } from './botsApi'

/** The bots added to a project on the server (#4545), read again whenever bots change. On a Mac, a project here keeps the signed-in server's bots it added. */
export function useProjectBots(projectId: string | null | undefined): ProjectBots | null {
  const [state, setState] = useState<{ projectId: string; value: ProjectBots } | null>(null)
  useEffect(() => {
    if (!projectId) return
    let cancelled = false
    const load = () => { void readProjectBots(projectId).then(value => { if (!cancelled) setState({ projectId, value }) }).catch(() => { if (!cancelled) setState({ projectId, value: { bots: [], unavailable: [] } }) }) }
    load()
    window.addEventListener(BOTS_CHANGED_EVENT, load)
    return () => { cancelled = true; window.removeEventListener(BOTS_CHANGED_EVENT, load) }
  }, [projectId])
  return state && state.projectId === projectId ? state.value : null
}

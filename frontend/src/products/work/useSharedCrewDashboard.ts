import { useEffect, useState } from 'react'

/**
 * Whether someone else's Crew has a dashboard this person can open (null while
 * it is being checked). Uses the same dashboard catalog as the dashboard view,
 * so a draft the person cannot see does not count. A failed check counts as
 * none: the person then sees the chat alone.
 */
export function useSharedCrewHasDashboard(workspacePath: string | undefined): boolean | null {
  const [result, setResult] = useState<{ path: string; has: boolean } | null>(null)
  useEffect(() => {
    if (!workspacePath) return
    let cancelled = false
    void (async () => {
      let has = false
      try {
        const { default: api } = await import('../../services/api')
        const { data } = await api.post<{ dashboards?: { managed?: boolean; published_revision?: string }[] }>('/api/dashboards', { workspace: workspacePath, action: 'list', limit: 20 })
        has = (data.dashboards || []).some(dashboard => !dashboard.managed || Boolean(dashboard.published_revision))
      } catch {
        has = false
      }
      if (!cancelled) setResult({ path: workspacePath, has })
    })()
    return () => { cancelled = true }
  }, [workspacePath])
  return workspacePath && result?.path === workspacePath ? result.has : null
}

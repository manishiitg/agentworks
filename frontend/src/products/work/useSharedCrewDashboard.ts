import { useEffect, useState } from 'react'
import { WORKFLOW_REPORT_REFRESH_EVENT } from '../../components/workflow/reportRefreshEvent'

/**
 * Whether someone else's product has a dashboard this person can open (null
 * while it is being checked). When its owner built none, the server writes the
 * default one from what the product declares (purpose, functions, Ask), so a
 * person with access always lands on a dashboard. A failed check counts as
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
        const catalog = async () => {
          const { data } = await api.post<{ dashboards?: { managed?: boolean; published_revision?: string }[] }>('/api/dashboards', { workspace: workspacePath, action: 'list', limit: 20 })
          return (data.dashboards || []).some(dashboard => !dashboard.managed || Boolean(dashboard.published_revision))
        }
        has = await catalog()
        if (!has) {
          await api.post('/api/dashboards/default', { workspace: workspacePath })
          has = await catalog()
        }
      } catch {
        has = false
      }
      if (!cancelled) {
        setResult({ path: workspacePath, has })
        if (has) window.dispatchEvent(new CustomEvent(WORKFLOW_REPORT_REFRESH_EVENT, { detail: { workspacePath } }))
      }
    })()
    return () => { cancelled = true }
  }, [workspacePath])
  return workspacePath && result?.path === workspacePath ? result.has : null
}

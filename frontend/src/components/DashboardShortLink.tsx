import { useEffect, useState } from 'react'
import api from '../services/api'

type Resolved = { root: string; document_path: string }

/** Opens /d/<code>: asks the server which dashboard the code names (it answers
 * only for a signed-in person who can already open it), then hands the dashboard
 * to the app's normal report page. */
export function DashboardShortLink({ code, onResolved }: { code: string; onResolved: (root: string, documentPath: string) => void }) {
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    let cancelled = false
    void (async () => {
      try {
        const response = await api.get<Resolved>(`/api/dashboards/by-code/${encodeURIComponent(code)}`)
        const found = response?.data
        if (cancelled) return
        if (!found?.root || !found?.document_path) { setFailed(true); return }
        onResolved(found.root, found.document_path)
      } catch {
        if (!cancelled) setFailed(true)
      }
    })()
    return () => { cancelled = true }
  }, [code, onResolved])
  return failed
    ? <div role="alert" className="grid min-h-screen place-items-center p-6 text-center text-sm">This link does not open a dashboard you can use. It may have been removed, or you may need access.</div>
    : <div role="status" className="grid min-h-screen place-items-center text-sm">Opening dashboard…</div>
}

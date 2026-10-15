import { useEffect, useState } from 'react'
import api from '../services/api'

type SharedDashboard = {
  dashboard_id: string
  title?: string
  workspace: string
  project_title?: string
  product?: string
  document_path?: string
  published_revision?: string
}

function encodeBase64Utf8(value: string): string {
  const bytes = new TextEncoder().encode(value)
  let binary = ''
  bytes.forEach(byte => { binary += String.fromCharCode(byte) })
  return btoa(binary)
}

/** Everything other people shared with this person: one row per dashboard, opened on the app's normal report page.
 * The server decides what is listed (only dashboards this person can open). */
export function SharedWithMePage() {
  const [rows, setRows] = useState<SharedDashboard[] | null>(null)
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    let cancelled = false
    void (async () => {
      try {
        const { data } = await api.post<{ dashboards?: SharedDashboard[] }>('/api/dashboards', { action: 'list', shared_with_me: true, limit: 200 })
        if (!cancelled) setRows((data.dashboards || []).filter(row => row.document_path && row.published_revision))
      } catch {
        if (!cancelled) setFailed(true)
      }
    })()
    return () => { cancelled = true }
  }, [])

  const open = (row: SharedDashboard) => {
    const params = new URLSearchParams({ document: row.document_path || '', path: encodeBase64Utf8(row.workspace) })
    window.location.assign(`/report?${params.toString()}`)
  }

  return (
    <main className="mx-auto min-h-screen max-w-2xl p-6">
      <div className="mb-4 flex items-center justify-between gap-2">
        <h1 className="text-lg font-semibold">Shared with me</h1>
        <a href="/" className="text-sm text-muted-foreground hover:text-foreground">Back</a>
      </div>
      {failed && <p role="alert" className="text-sm">The list could not be loaded. Try again in a moment.</p>}
      {!failed && rows === null && <p role="status" className="text-sm text-muted-foreground">Loading…</p>}
      {rows && rows.length === 0 && <p className="text-sm text-muted-foreground">Nothing has been shared with you yet. When someone shares a dashboard, it appears here.</p>}
      {rows && rows.length > 0 && (
        <ul className="space-y-2">
          {rows.map(row => (
            <li key={`${row.workspace}/${row.dashboard_id}`}>
              <button type="button" onClick={() => open(row)} className="w-full rounded-lg border border-border p-3 text-left hover:bg-muted">
                <span className="block text-sm font-medium">{row.project_title || row.title || row.dashboard_id}</span>
                {row.title && row.project_title && row.title !== row.project_title && <span className="block text-xs text-muted-foreground">{row.title}</span>}
              </button>
            </li>
          ))}
        </ul>
      )}
    </main>
  )
}

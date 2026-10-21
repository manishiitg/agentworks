import { useCallback, useEffect, useState } from 'react'
import { ArrowUpRight, LayoutDashboard, RefreshCw } from 'lucide-react'
import api from '../services/api'
import { useAuthStore } from '../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../stores/useWorkspaceConnectionStore'
import { sharedReportLink } from '../utils/sharedLinks'
import { WorkspaceTopToolbar } from './workspace/WorkspaceTopToolbar'

type Dashboard = {
  dashboard_id: string
  title?: string
  workspace: string
  project_title?: string
  product?: string
  document_path: string
  short_path?: string
  shared_with_me: boolean
}
type DashboardPage = { dashboards: Dashboard[]; next_offset: number; has_more: boolean; unavailable_projects?: string[] }

/** Publication and access are decided by the same discovery service used by MCP. */
export function DashboardsPage() {
  const account = useAuthStore(state => state.user?.id)
  const workspace = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const [attempt, setAttempt] = useState(0)
  const [rows, setRows] = useState<Dashboard[] | null>(null)
  const [failed, setFailed] = useState(false)
  const [partial, setPartial] = useState(false)
  const refresh = useCallback(() => setAttempt(value => value + 1), [])
  useEffect(() => {
    window.addEventListener('focus', refresh)
    return () => window.removeEventListener('focus', refresh)
  }, [refresh])
  useEffect(() => {
    const controller = new AbortController()
    setRows(null); setFailed(false); setPartial(false)
    void (async () => {
      const all: Dashboard[] = []
      let offset = 0
      let incomplete = false
      try {
        for (;;) {
          const { data } = await api.post<DashboardPage>('/api/dashboards', { action: 'list', published_only: true, limit: 200, offset }, { signal: controller.signal })
          if (controller.signal.aborted) return
          all.push(...data.dashboards)
          incomplete ||= Boolean(data.unavailable_projects?.length)
          if (!data.has_more) break
          if (data.next_offset <= offset) throw new Error('Dashboard list did not advance')
          offset = data.next_offset
        }
        setRows(all); setPartial(incomplete)
      } catch {
        if (!controller.signal.aborted) setFailed(true)
      }
    })()
    return () => controller.abort()
  }, [account, workspace, attempt])

  return <main className="flex h-full min-h-0 flex-col bg-background text-foreground">
    <WorkspaceTopToolbar navigationKind="page">
      <div className="flex items-center gap-3"><LayoutDashboard className="h-5 w-5 text-muted-foreground" /><h1 className="text-lg font-semibold">Dashboards</h1></div>
      <a href="/" className="text-sm text-muted-foreground hover:text-foreground">Workspace</a>
    </WorkspaceTopToolbar>
    <div className="min-h-0 flex-1 overflow-y-auto"><div className="mx-auto max-w-5xl space-y-8 px-4 py-6 sm:px-8">
      <div className="flex items-start justify-between gap-4">
        <p className="text-sm text-muted-foreground">Your published dashboards and dashboards shared with you, all in one place.</p>
        <button type="button" onClick={refresh} className="inline-flex shrink-0 items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-sm hover:bg-muted"><RefreshCw className="h-3.5 w-3.5" />Refresh</button>
      </div>
      {failed && <p role="alert" className="text-sm">Dashboards could not be loaded. Use Refresh to try again.</p>}
      {!failed && rows === null && <p role="status" className="text-sm text-muted-foreground">Loading dashboards…</p>}
      {partial && <p role="status" className="text-sm text-muted-foreground">Some projects could not be loaded. Use Refresh to try again.</p>}
      {rows && ([false, true] as const).map(shared => <section key={String(shared)} aria-label={shared ? 'Shared with you' : 'Yours'} className="space-y-3">
        <h2 className="text-base font-medium">{shared ? 'Shared with you' : 'Yours'}</h2>
        {!rows.some(row => row.shared_with_me === shared) && <p className="text-sm text-muted-foreground">{shared ? 'When someone shares a dashboard with you, it appears here automatically.' : 'Publish a dashboard from your project to find it here.'}</p>}
        <ul className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {rows.filter(row => row.shared_with_me === shared).map(row => <li key={`${row.workspace}/${row.dashboard_id}`}>
            <a href={row.short_path || sharedReportLink(window.location.origin, row.workspace, row.document_path)} className="flex h-full items-start justify-between gap-3 rounded-lg border border-border bg-card p-4 hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
              <div className="min-w-0"><span className="block break-words text-sm font-medium">{row.title || row.project_title || 'Dashboard'}</span><span className="mt-1 block break-words text-xs text-muted-foreground">{row.project_title}</span></div>
              <ArrowUpRight aria-hidden="true" className="h-4 w-4 shrink-0 text-muted-foreground" />
            </a>
          </li>)}
        </ul>
      </section>)}
    </div></div>
  </main>
}

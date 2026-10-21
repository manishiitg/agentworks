import { useCallback, useEffect, useState } from 'react'
import { ArrowRight, Bot, LayoutDashboard, Radio, RefreshCw, Search, Users, Workflow, X } from 'lucide-react'
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

// Loose HTML pages often have a filename for a title. Keep publication metadata
// intact; use the project's name as the human-facing label in this library.
function dashboardTitle(row: Dashboard) {
  const title = row.title?.trim()
  return (!title || /^index(?:\.html?)?$/i.test(title)) ? row.project_title?.trim() || 'Dashboard' : title
}

function productPresentation(product?: string) {
  switch (product) {
    case 'crew': return { label: 'Crew', Icon: Bot, color: 'bg-violet-500/10 text-violet-700 dark:text-violet-300' }
    case 'workflow': return { label: 'Goal', Icon: Workflow, color: 'bg-sky-500/10 text-sky-700 dark:text-sky-300' }
    case 'relay': return { label: 'Flow', Icon: Radio, color: 'bg-amber-500/10 text-amber-700 dark:text-amber-300' }
    default: return { label: 'Dashboard', Icon: LayoutDashboard, color: 'bg-primary/10 text-primary' }
  }
}

/** Publication and access are decided by the same discovery service used by MCP. */
export function DashboardsPage() {
  const account = useAuthStore(state => state.user?.id)
  const workspace = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const [query, setQuery] = useState('')
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

  const search = query.trim().toLocaleLowerCase()
  const visible = rows?.filter(row => [dashboardTitle(row), row.project_title, productPresentation(row.product).label]
    .some(value => value?.toLocaleLowerCase().includes(search))) ?? []

  return <main className="flex h-full min-h-0 flex-col bg-background text-foreground">
    <WorkspaceTopToolbar navigationKind="page">
      <div className="flex items-center gap-3"><LayoutDashboard className="h-5 w-5 text-muted-foreground" /><h1 className="text-lg font-semibold">Dashboards</h1></div>
      <a href="/" className="text-sm text-muted-foreground hover:text-foreground">Workspace</a>
    </WorkspaceTopToolbar>
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div className="mx-auto max-w-6xl px-5 py-7 sm:px-8 sm:py-9">
        <div className="mb-7 flex flex-col gap-5 sm:flex-row sm:items-end sm:justify-between">
          <div>
            <h2 className="text-2xl font-semibold tracking-tight">Your dashboards</h2>
            <p className="mt-1.5 text-sm text-muted-foreground">Published by you. Shared by your team.</p>
          </div>
          <div className="flex w-full items-center gap-2 sm:w-auto">
            <div className="relative min-w-0 flex-1 sm:w-72">
              <Search aria-hidden="true" className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
              <input type="search" aria-label="Search dashboards" placeholder="Search dashboards…" value={query} onChange={event => setQuery(event.target.value)} className="h-10 w-full appearance-none rounded-lg border border-border bg-card pl-9 pr-9 text-sm outline-none placeholder:text-muted-foreground focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-search-cancel-button]:hidden" />
              {query && <button type="button" aria-label="Clear search" onClick={() => setQuery('')} className="absolute right-2 top-1/2 -translate-y-1/2 rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><X aria-hidden="true" className="h-3.5 w-3.5" /></button>}
            </div>
            <button type="button" onClick={refresh} disabled={rows === null && !failed} aria-label="Refresh dashboards" title="Refresh dashboards" className="inline-flex h-10 w-10 shrink-0 items-center justify-center rounded-lg border border-border bg-card text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50"><RefreshCw aria-hidden="true" className="h-4 w-4" /></button>
          </div>
        </div>
        {failed && <p role="alert" className="rounded-lg border border-border p-4 text-sm">Dashboards could not be loaded. Use Refresh to try again.</p>}
        {!failed && rows === null && <p role="status" className="py-8 text-sm text-muted-foreground">Loading dashboards…</p>}
        {partial && <p role="status" className="mb-5 rounded-lg border border-border p-3 text-sm text-muted-foreground">Some projects could not be loaded. Use Refresh to try again.</p>}
        {rows && search && <p role="status" className="mb-5 text-sm text-muted-foreground">{visible.length} {visible.length === 1 ? 'dashboard' : 'dashboards'} found</p>}
        {rows && search && visible.length === 0 && <div className="rounded-xl border border-dashed border-border px-5 py-12 text-center">
          <Search aria-hidden="true" className="mx-auto mb-3 h-6 w-6 text-muted-foreground" />
          <h3 className="text-sm font-medium">No matching dashboards</h3>
          <p className="mt-1 text-sm text-muted-foreground">Try a different name or project.</p>
          <button type="button" onClick={() => setQuery('')} className="mt-4 rounded-md px-3 py-1.5 text-sm font-medium text-primary hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">Clear search</button>
        </div>}
        {rows && <div className="space-y-7">{([false, true] as const).map(shared => {
          const items = visible.filter(row => row.shared_with_me === shared)
          if (search && !items.length) return null
          const SectionIcon = shared ? Users : LayoutDashboard
          return <section key={String(shared)} aria-label={shared ? 'Shared with you' : 'Yours'}>
            <div className="mb-3 flex items-center gap-2">
              <SectionIcon aria-hidden="true" className="h-4 w-4 text-muted-foreground" />
              <h3 className="text-sm font-semibold">{shared ? 'Shared with you' : 'Yours'}</h3>
              <span className="rounded-md bg-muted px-1.5 py-0.5 text-xs tabular-nums text-muted-foreground">{items.length}</span>
            </div>
            {!items.length && <p className="rounded-lg border border-dashed border-border px-4 py-3 text-sm text-muted-foreground">{shared ? 'When someone shares a dashboard with you, it appears here automatically.' : 'Publish a dashboard from any project to see it here.'}</p>}
            {items.length > 0 && <ul className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
              {items.map(row => {
                const title = dashboardTitle(row)
                const { label, Icon, color } = productPresentation(row.product)
                const project = row.project_title?.trim()
                const showProject = project && project.toLocaleLowerCase() !== title.toLocaleLowerCase()
                return <li key={`${row.workspace}/${row.dashboard_id}`} className="min-w-0">
                  <a href={row.short_path || sharedReportLink(window.location.origin, row.workspace, row.document_path)} className="group flex h-full min-h-40 flex-col rounded-xl border border-border bg-card p-5 transition-colors hover:border-primary/40 hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                    <div className="mb-4 flex items-center justify-between gap-3">
                      <div className="flex items-center gap-2.5"><span className={`flex h-9 w-9 items-center justify-center rounded-lg ${color}`}><Icon aria-hidden="true" className="h-[18px] w-[18px]" /></span><span className="text-xs font-medium text-muted-foreground">{label}</span></div>
                      <ArrowRight aria-hidden="true" className="h-4 w-4 shrink-0 text-muted-foreground group-hover:text-foreground" />
                    </div>
                    <span className="break-words text-base font-semibold leading-snug">{title}</span>
                    {showProject && <span className="mt-1.5 break-words text-xs text-muted-foreground">{project}</span>}
                  </a>
                </li>
              })}
            </ul>}
          </section>
        })}</div>}
      </div>
    </div>
  </main>
}

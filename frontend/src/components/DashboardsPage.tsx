import { useCallback, useEffect, useState } from 'react'
import { ArrowUpRight, Globe2, LayoutDashboard, RefreshCw, Search, Users, X } from 'lucide-react'
import { dashboardDisplayName as dashboardTitle, dashboardDirectoryLink, loadDashboardDirectory, type DashboardDirectoryEntry as Dashboard } from '../services/dashboardDirectory'
import { useAuthStore } from '../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../stores/useWorkspaceConnectionStore'
import { WorkspaceTopToolbar } from './workspace/WorkspaceTopToolbar'
import { ProductSurfaceSwitcher } from './ProductSurfaceSwitcher'
import { WorkspacePageContent } from './workspace/WorkspacePageContent'

/** Publication and access are decided by the same discovery service used by MCP. */
export function DashboardsPage({ navigationActive = true }: { navigationActive?: boolean } = {}) {
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
      try {
        const result = await loadDashboardDirectory(controller.signal)
        if (controller.signal.aborted) return
        setRows(result.dashboards); setPartial(result.partial)
      } catch {
        if (!controller.signal.aborted) setFailed(true)
      }
    })()
    return () => controller.abort()
  }, [account, workspace, attempt])

  const search = query.trim().toLocaleLowerCase()
  const visible = rows?.filter(row => [dashboardTitle(row), row.project_title]
    .some(value => value?.toLocaleLowerCase().includes(search))) ?? []

  return <main className="flex h-full min-h-0 flex-col bg-background text-foreground">
    <WorkspaceTopToolbar navigationKind="page" navigationActive={navigationActive}>
      <ProductSurfaceSwitcher variant="header" menuTitle="Dashboards" menuContent={close => <div role="group" aria-label="Published dashboards" className="p-1">
        <button type="button" role="menuitem" aria-current="page" onClick={close} className="flex min-h-8 w-full items-center gap-2 rounded-md bg-muted/60 px-2 py-1.5 text-left text-xs"><LayoutDashboard className="h-4 w-4" />All dashboards</button>
        {rows?.map(row => <a key={`${row.workspace}/${row.dashboard_id}`} role="menuitem" onClick={close} href={dashboardDirectoryLink(row)} className="flex min-h-8 items-center gap-2 rounded-md px-2 py-1.5 text-xs text-muted-foreground hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><Globe2 className="h-4 w-4 shrink-0" /><span className="truncate">{dashboardTitle(row)}</span></a>)}
        {rows === null && <p role="status" className="px-2 py-2 text-xs text-muted-foreground">{failed ? 'Dashboards could not be loaded.' : 'Loading dashboards…'}</p>}
      </div>} />
    </WorkspaceTopToolbar>
    <div className="min-h-0 flex-1 overflow-y-auto">
      <WorkspacePageContent width="wide">
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
        {rows?.length === 0 && !search && <div className="rounded-xl border border-dashed border-border px-5 py-10 text-center">
          <Globe2 aria-hidden="true" className="mx-auto mb-3 h-6 w-6 text-muted-foreground" />
          <h3 className="text-sm font-medium">Your dashboards will appear here</h3>
          <p className="mt-1 text-sm text-muted-foreground">Dashboards you publish or your team shares will appear here automatically.</p>
        </div>}
        {rows && <div className="space-y-6">{([false, true] as const).map(shared => {
          const items = visible.filter(row => row.shared_with_me === shared)
          if (!items.length) return null
          const SectionIcon = shared ? Users : LayoutDashboard
          return <section key={String(shared)} aria-label={shared ? 'Shared with you' : 'Yours'}>
            <div className="mb-3 flex items-center gap-2">
              <SectionIcon aria-hidden="true" className="h-4 w-4 text-muted-foreground" />
              <h3 className="text-sm font-semibold">{shared ? 'Shared with you' : 'Yours'}</h3>
              <span className="rounded-md bg-muted px-1.5 py-0.5 text-xs tabular-nums text-muted-foreground">{items.length}</span>
            </div>
            {items.length > 0 && <ul className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
              {items.map(row => {
                const title = dashboardTitle(row)
                return <li key={`${row.workspace}/${row.dashboard_id}`} className="min-w-0">
                  <a href={dashboardDirectoryLink(row)} className="group flex h-full min-h-20 items-center gap-3 rounded-lg border border-border bg-card p-4 transition-colors hover:border-primary/40 hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                    <Globe2 aria-hidden="true" className="h-5 w-5 shrink-0 text-muted-foreground" />
                    <span className="min-w-0 flex-1 break-words text-sm font-medium leading-snug">{title}</span>
                    <ArrowUpRight aria-hidden="true" className="h-4 w-4 shrink-0 text-muted-foreground group-hover:text-foreground" />
                  </a>
                </li>
              })}
            </ul>}
          </section>
        })}</div>}
      </WorkspacePageContent>
    </div>
  </main>
}

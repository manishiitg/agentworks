import React, { useCallback, useEffect, useId, useMemo, useState } from 'react'
import {
  Activity, AlertTriangle, CheckCircle2, CircleAlert, CircleHelp,
  ArrowUpRight, ChevronRight, LayoutDashboard, Loader2, RefreshCw, Search,
} from 'lucide-react'
import { agentApi } from '../../services/api'
import type { OrgDashboardNotification, OrgDashboardRouteNotifications, ReportHumanInput } from '../../services/api-types'

type SummaryStatus = OrgDashboardNotification['status']

interface WorkflowDashEntry {
  workspacePath: string
  label: string
	runSummary: OrgDashboardNotification | null
	pulseSummary: OrgDashboardNotification | null
	recent: OrgDashboardNotification[]
	byRoute: OrgDashboardRouteNotifications[]
	pendingInputs: ReportHumanInput[]
  failed: boolean
}

interface OrgDashboardProps {
  embedded?: boolean
  workflows: Array<{ workspacePath: string; label: string }>
  selectedWorkflowPath?: string | null
  onOpenWorkflow: (workspacePath: string) => void
}

const PILL_BASE = 'font-runloop-mono inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.06em]'
const STATUS_PILL: Record<SummaryStatus, { className: string; label: string; Icon: React.ComponentType<{ className?: string }> }> = {
  completed: { className: 'border-emerald-500/25 bg-emerald-500/10 text-emerald-600 dark:text-emerald-300', label: 'Completed', Icon: CheckCircle2 },
  failed: { className: 'border-red-500/25 bg-red-500/10 text-red-600 dark:text-red-300', label: 'Failed', Icon: CircleAlert },
  blocked: { className: 'border-amber-500/25 bg-amber-500/10 text-amber-600 dark:text-amber-300', label: 'Blocked', Icon: AlertTriangle },
  waiting_for_user: { className: 'border-violet-500/25 bg-violet-500/10 text-violet-600 dark:text-violet-300', label: 'Waiting for you', Icon: CircleHelp },
  waiting_for_platform: { className: 'border-amber-500/25 bg-amber-500/10 text-amber-600 dark:text-amber-300', label: 'Waiting for platform', Icon: CircleAlert },
  monitoring: { className: 'border-sky-500/25 bg-sky-500/10 text-sky-600 dark:text-sky-300', label: 'Monitoring', Icon: Activity },
  informational: { className: 'border-border bg-muted/70 text-muted-foreground', label: 'Update', Icon: CircleHelp },
  no_run: { className: 'border-amber-500/25 bg-amber-500/10 text-amber-600 dark:text-amber-300', label: 'Did not run', Icon: AlertTriangle },
}

const Pill: React.FC<{ status: SummaryStatus; label?: string }> = ({ status, label }) => {
  const config = STATUS_PILL[status] || STATUS_PILL.informational
  return <span className={`${PILL_BASE} ${config.className}`}><config.Icon className="h-3 w-3" />{label || config.label}</span>
}

const REVIEW_STATUS_LABEL = {
  clean: 'Clean', issues_found: 'Issues found', fixed: 'Fixed', incomplete: 'Incomplete',
  failed: 'Failed', skipped: 'Skipped',
} as const

function relativeTime(iso: string): string {
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return ''
  const mins = Math.floor((Date.now() - then) / 60000)
  if (mins < 1) return 'just now'
  if (mins < 60) return `${mins}m ago`
  const hours = Math.floor(mins / 60)
  return hours < 24 ? `${hours}h ago` : `${Math.floor(hours / 24)}d ago`
}

function absoluteTime(iso: string): string {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return iso
  return date.toLocaleString(undefined, { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

function summaryScopes(entry: WorkflowDashEntry) {
  const routes = entry.byRoute.map(route => ({
    key: JSON.stringify([route.legacy ? 'legacy' : 'route', route.routing_step_id, route.route_id]),
    label: route.label, runSummary: route.run_summary || null, pulseSummary: route.pulse_summary || null,
  }))
  // The API's workflow summary can be the same notification projected into a
  // route. Show that update once, under its route, while preserving distinct
  // aggregate content and different runs that happen to use identical prose.
  const representedByRoute = (summary: OrgDashboardNotification | null) => !!summary &&
    routes.some(route => [route.runSummary, route.pulseSummary].some(candidate =>
      candidate?.id === summary.id && candidate.status === summary.status &&
      candidate.title === summary.title && candidate.message === summary.message &&
      JSON.stringify(candidate.fields) === JSON.stringify(summary.fields) &&
      JSON.stringify(candidate.sections) === JSON.stringify(summary.sections) &&
      (summary.routes?.length || 0) <= 1))
  const runSummary = representedByRoute(entry.runSummary) ? null : entry.runSummary
  const pulseSummary = representedByRoute(entry.pulseSummary) ? null : entry.pulseSummary
  return [
    ...(runSummary || pulseSummary ? [{ key: 'workflow', label: 'Automation overview', runSummary, pulseSummary }] : []),
    ...routes.filter(route => route.runSummary || route.pulseSummary),
  ]
}

function hasPendingDecisions(entry: WorkflowDashEntry): boolean {
  return entry.pendingInputs.length > 0
}

function latestSummary(entry: WorkflowDashEntry): OrgDashboardNotification | undefined {
  return [...summaryScopes(entry).flatMap(scope => [scope.runSummary, scope.pulseSummary]), ...entry.recent]
    .filter((summary): summary is OrgDashboardNotification => !!summary)
    .sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at))[0]
}

const SummaryDetail: React.FC<{ title: string; summary: OrgDashboardNotification | null }> = ({ title, summary }) => (
  <section className="rounded-lg border border-border bg-card/95 p-3">
    <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
      <div className="flex flex-wrap items-center gap-2"><h3 className="text-sm font-semibold text-foreground">{title}</h3>{summary && <Pill status={summary.status} />}{summary?.route && <span className={`${PILL_BASE} border-sky-500/25 bg-sky-500/10 text-sky-600 dark:text-sky-300`}>Route · {summary.route}</span>}</div>
      {summary && <span className="font-runloop-mono text-[10px] text-muted-foreground">{absoluteTime(summary.created_at)}</span>}
    </div>
    {summary ? <div className="space-y-4">
      {summary.title && <h4 className="text-sm font-semibold text-foreground">{summary.title}</h4>}
      <p className="text-sm leading-6 text-foreground/90">{summary.message}</p>
      {!!summary.reviews?.length && <section aria-label="Pulse reviews" className="overflow-hidden rounded-md border border-border bg-background/50">
        <div className="flex items-center justify-between border-b border-border px-3 py-2.5"><h5 className="text-xs font-medium text-foreground">Reviews</h5><span className="text-xs text-muted-foreground">{summary.reviews.length}</span></div>
        <div className="divide-y divide-border">{summary.reviews.map((review, index) => <div key={`${review.module}:${index}`} className="px-3 py-3">
          <div className="flex flex-wrap items-center gap-2"><span className="text-xs font-semibold text-foreground">{review.label || review.module.replaceAll('_', ' ')}</span><span className={`${PILL_BASE} border-border bg-muted/70 text-muted-foreground`}>{REVIEW_STATUS_LABEL[review.status]}</span>{review.verification !== 'not_applicable' && <span className="text-[10px] text-muted-foreground">{review.verification.replaceAll('_', ' ')}</span>}</div>
          <p className="mt-1.5 text-xs leading-5 text-foreground/85">{review.summary}</p>
          {(review.issues_found > 0 || review.fixes_applied > 0) && <div className="mt-1.5 flex gap-3 font-runloop-mono text-[10px] text-muted-foreground"><span>{review.issues_found} issue{review.issues_found === 1 ? '' : 's'}</span><span>{review.fixes_applied} fix{review.fixes_applied === 1 ? '' : 'es'}</span></div>}
        </div>)}</div>
      </section>}
      {!!summary.fields?.length && <div className="grid gap-2 sm:grid-cols-2">{summary.fields.map((field, index) => <div key={`${field.label}:${index}`} className="rounded-md border border-border bg-background/70 px-3 py-2"><div className="font-runloop-mono text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">{field.label}</div><div className="mt-1 text-sm text-foreground">{field.value}</div></div>)}</div>}
      {!!summary.sections?.length && <details aria-label="Evidence and metric details" className="group overflow-hidden rounded-md border border-border bg-background/50">
        <summary className="flex cursor-pointer list-none items-center gap-2 px-3 py-2.5 text-xs font-medium text-foreground transition-colors hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary">
          <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform group-open:rotate-90" />
          <span>Evidence and metric details</span>
          <span className="ml-auto text-muted-foreground">{summary.sections.length} section{summary.sections.length === 1 ? '' : 's'}</span>
        </summary>
        <div className="divide-y divide-border border-t border-border">{summary.sections.map((section, index) => <div key={`${section.heading}:${index}`} className="px-3 py-3"><div className="font-runloop-mono text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">{section.heading}</div><p className="mt-1 whitespace-pre-wrap break-words text-xs leading-5 text-foreground/85">{section.body}</p></div>)}</div>
      </details>}
      {!!summary.routes?.length && <details aria-label="Route update details" className="group overflow-hidden rounded-md border border-border bg-background/50">
        <summary className="flex cursor-pointer list-none items-center gap-2 px-3 py-2.5 text-xs font-medium text-foreground transition-colors hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary">
          <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform group-open:rotate-90" />
          <span>Route updates</span>
          <span className="ml-auto text-muted-foreground">{summary.routes.length}</span>
        </summary>
        <div className="space-y-3 border-t border-border p-3">{summary.routes.map(route => <SummaryDetail
          key={JSON.stringify([route.routing_step_id, route.route_id])}
          title={route.label || route.route_id}
          summary={{ ...summary, ...route, route: undefined, routes: undefined }}
        />)}</div>
      </details>}
    </div> : <p className="text-sm text-muted-foreground">No {title.toLowerCase()} notification has been recorded yet.</p>}
  </section>
)

const WorkflowSummaryRow: React.FC<{
  label: 'Run' | 'Pulse'
  summary: OrgDashboardNotification | null
}> = ({ label, summary }) => {
  const [expanded, setExpanded] = useState(false)
  const detailsId = useId()
  if (!summary) return null

  return (
    <div className="overflow-hidden rounded-md border border-border bg-background/60">
      <button
        type="button"
        aria-expanded={expanded}
        aria-controls={expanded ? detailsId : undefined}
        onClick={() => setExpanded(value => !value)}
        className="w-full px-3 py-2.5 text-left transition-colors hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary"
      >
        <div className="flex items-start justify-between gap-2">
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <span className="shrink-0 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">{label}</span>
            <Pill status={summary.status} />
            <span className="min-w-0 break-words text-sm font-semibold text-foreground/90">{summary.title || `${label} summary`}</span>
          </div>
          <div className="flex shrink-0 items-center gap-2 pt-0.5">
            <span className="font-runloop-mono text-[10px] text-muted-foreground">{relativeTime(summary.created_at)}</span>
            <ChevronRight aria-hidden="true" className={`h-4 w-4 text-muted-foreground transition-transform ${expanded ? 'rotate-90' : ''}`} />
          </div>
        </div>
        {!expanded && <p className="mt-1.5 line-clamp-2 text-xs leading-5 text-muted-foreground">{summary.message}</p>}
      </button>
      {expanded && <div id={detailsId} role="region" aria-label={`${label} details: ${summary.title || 'Summary'}`} className="border-t border-border p-3">
        <SummaryDetail title={label === 'Pulse' ? 'Pulse update' : 'Workflow run'} summary={summary} />
      </div>}
    </div>
  )
}

export const OrgDashboard: React.FC<OrgDashboardProps> = ({ workflows, selectedWorkflowPath, onOpenWorkflow, embedded = false }) => {
  const [entries, setEntries] = useState<WorkflowDashEntry[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [selectedPath, setSelectedPath] = useState<string | null>(selectedWorkflowPath ?? null)
  const [search, setSearch] = useState('')
  const [decisionsOnly, setDecisionsOnly] = useState(false)

  const load = useCallback(async () => {
    setLoading(true); setError(null)
    try {
      const paths = workflows.map(workflow => workflow.workspacePath)
      const [notifications, humanInputs] = await Promise.all([
        paths.length ? agentApi.getOrgDashboardNotifications(paths, 10) : Promise.resolve({ success: true, workflows: [] }),
        paths.length ? agentApi.listNeedsYouDecisionsAggregate(paths).catch(() => ({ success: false, inputs: [] as ReportHumanInput[] })) : Promise.resolve({ success: true, inputs: [] as ReportHumanInput[] }),
      ])
      const notificationByPath = new Map((notifications.workflows || []).map(item => [item.workspace_path, item]))
      const results = workflows.map((workflow): WorkflowDashEntry => {
        const notification = notificationByPath.get(workflow.workspacePath)
        return {
          workspacePath: workflow.workspacePath,
		  label: workflow.label,
		  runSummary: notification?.run_summary || null,
		  pulseSummary: notification?.pulse_summary || null,
		  recent: notification?.recent || [],
		  byRoute: notification?.by_route || [],
          pendingInputs: humanInputs.success ? humanInputs.inputs.filter(input => input.workspace_path === workflow.workspacePath) : [],
          failed: !notifications.success || !humanInputs.success || !!notification?.error,
        }
      })
      setEntries(results)
      if (results.some(result => result.failed)) setError('Some workflow notification data could not be loaded.')
    } catch { setError('Could not load the Org Dashboard.') }
    finally { setLoading(false) }
  }, [workflows])

  useEffect(() => { void load() }, [load])

  useEffect(() => {
    if (selectedWorkflowPath) setSelectedPath(selectedWorkflowPath)
  }, [selectedWorkflowPath])


  const sortedEntries = useMemo(() => [...entries].sort((a, b) =>
    Number(hasPendingDecisions(b)) - Number(hasPendingDecisions(a)) ||
    b.pendingInputs.length - a.pendingInputs.length ||
    (Date.parse(latestSummary(b)?.created_at || '') || 0) - (Date.parse(latestSummary(a)?.created_at || '') || 0) ||
    a.label.localeCompare(b.label)), [entries])
  const visibleEntries = sortedEntries.filter(entry =>
    (!decisionsOnly || hasPendingDecisions(entry)) && entry.label.toLowerCase().includes(search.trim().toLowerCase()))
  const selected = visibleEntries.find(entry => entry.workspacePath === selectedPath) || visibleEntries[0]
  const decisionAutomationCount = entries.filter(hasPendingDecisions).length
  const decisionCount = entries.reduce((count, entry) => count + entry.pendingInputs.length, 0)
  const selectedScopes = selected ? summaryScopes(selected) : []
  const selectedLatest = selected ? latestSummary(selected) : undefined
  const selectedHistory = selected ? [...selected.recent].sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at)) : []

  const header = <div className={`flex shrink-0 items-center justify-between gap-3 ${embedded ? 'border-b border-border px-4 py-2.5 sm:px-6' : 'px-6 pt-6 pb-5'}`}>
    {embedded ? <p className="text-xs text-muted-foreground">{entries.length} automations<span className="mx-2 text-border">/</span>{decisionCount ? `${decisionCount} pending decision${decisionCount === 1 ? '' : 's'}` : 'No pending decisions'}</p> : <div className="flex items-center gap-3"><LayoutDashboard className="h-6 w-6 text-primary" /><div>
      <h2 className="text-xl font-semibold tracking-tight text-foreground">Activity</h2>
      <p className="mt-0.5 text-sm text-muted-foreground">Runs, Pulse updates, and decisions — by automation</p>
    </div></div>}
    <button type="button" onClick={() => void load()} disabled={loading} className="inline-flex items-center gap-1.5 rounded-md px-2 py-1.5 text-xs text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50"><RefreshCw className={`h-3.5 w-3.5 ${loading ? 'animate-spin' : ''}`} />Refresh</button>
  </div>

  if (loading && !entries.length) return <div className="flex h-full min-h-[320px] flex-col items-center justify-center gap-3 text-muted-foreground"><Loader2 className="h-6 w-6 animate-spin" /><p className="text-sm">Loading activity…</p></div>
  if (!workflows.length) return <div className="flex h-full min-h-[320px] flex-col">{header}<div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">No automations yet</div></div>

  return <div className="flex h-full min-h-0 flex-col">{header}
    {error && <div role="status" className="mx-6 mb-4 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-600">{error}</div>}
    <div className={`grid min-h-0 flex-1 grid-rows-[auto_minmax(0,1fr)] overflow-hidden md:grid-cols-[280px_minmax(0,1fr)] md:grid-rows-1 ${embedded ? '' : 'mx-6 mb-6 rounded-xl border border-border'}`}>
      <aside className="flex min-h-0 flex-col border-b border-border bg-card/70 md:border-b-0 md:border-r">
        <div className="space-y-2.5 p-3">
          {!embedded && <div className="flex items-center justify-between gap-2"><h3 className="text-sm font-semibold">Automations</h3><span className="text-xs text-muted-foreground">{entries.length}</span></div>}
          <label className="flex items-center gap-2 rounded-md border border-border bg-background px-2.5 py-2 focus-within:ring-2 focus-within:ring-primary/50"><Search className="h-3.5 w-3.5 shrink-0 text-muted-foreground" /><input aria-label="Search automations" value={search} onChange={event => setSearch(event.target.value)} placeholder="Find an automation" className="w-full min-w-0 bg-transparent text-xs outline-none" /></label>
          <div className="flex flex-wrap gap-1" aria-label="Activity filters">
            {([{ label: `All ${entries.length}`, value: false }, { label: `Pending decisions ${decisionAutomationCount}`, value: true }]).map(filter => <button key={filter.label} type="button" aria-pressed={decisionsOnly === filter.value} onClick={() => setDecisionsOnly(filter.value)} className={`rounded-md px-2 py-1.5 text-xs font-medium transition-colors ${decisionsOnly === filter.value ? 'bg-primary/10 text-primary' : 'text-muted-foreground hover:bg-muted'}`}>{filter.label}</button>)}
          </div>
          {!embedded && <p className="text-xs text-muted-foreground">{decisionCount} pending decision{decisionCount !== 1 ? 's' : ''} across your automations</p>}
        </div>
        <nav aria-label="Automations" className="max-h-44 min-h-0 overflow-y-auto px-2 pb-2 md:max-h-none md:flex-1">
          {visibleEntries.map(entry => {
            const latest = latestSummary(entry)
            const active = selected?.workspacePath === entry.workspacePath
            const decision = hasPendingDecisions(entry)
            const statusColor = decision ? 'text-violet-600 dark:text-violet-300'
              : entry.failed ? 'text-amber-600 dark:text-amber-300' : 'text-muted-foreground'
            const statusLabel = decision ? 'Needs your decision' : entry.failed ? 'Data unavailable'
              : latest ? 'Latest update' : 'No activity yet'
            return <button key={entry.workspacePath} type="button" aria-pressed={active} aria-label={`View ${entry.label} activity`} onClick={() => setSelectedPath(entry.workspacePath)} className={`mb-0.5 w-full rounded-md border px-3 py-2.5 text-left transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary ${active ? 'border-primary/30 bg-primary/[0.08]' : 'border-transparent hover:bg-muted/60'}`}>
              <div className="flex items-center gap-2"><span className="min-w-0 flex-1 truncate text-sm font-medium text-foreground" title={entry.label}>{entry.label}</span>{!!entry.pendingInputs.length && <span className="flex shrink-0 items-center gap-1 rounded-full bg-violet-500/10 px-1.5 py-0.5 text-xs text-violet-600 dark:text-violet-300" aria-label={`${entry.pendingInputs.length} decisions`}><CircleHelp className="h-3 w-3" />{entry.pendingInputs.length}</span>}<ChevronRight className={`h-3.5 w-3.5 shrink-0 ${active ? 'text-primary' : 'text-muted-foreground/50'}`} /></div>
              <div className="mt-1.5 flex items-center justify-between gap-2 text-[11px]"><span className={statusColor}>{statusLabel}</span>{latest && <span className="shrink-0 text-muted-foreground" title={absoluteTime(latest.created_at)}>{relativeTime(latest.created_at)}</span>}</div>
            </button>
          })}
          {!visibleEntries.length && <p className="px-3 py-4 text-xs text-muted-foreground">No matching automations.</p>}
        </nav>
      </aside>
      {selected ? <section key={selected.workspacePath} aria-label={`${selected.label} activity`} className="min-h-0 min-w-0 overflow-y-auto bg-background p-4 sm:p-5">
        <div className="flex flex-wrap items-start justify-between gap-3 border-b border-border pb-3">
          <div className="min-w-0"><h3 className="break-words text-lg font-semibold tracking-tight text-foreground">{selected.label}</h3><p className="mt-1 text-xs text-muted-foreground">{selectedLatest ? `Latest update ${relativeTime(selectedLatest.created_at)}` : 'Run and Pulse updates'}{selected.byRoute.length > 0 && ` · ${selected.byRoute.length} route${selected.byRoute.length !== 1 ? 's' : ''}`}</p></div>
          <button type="button" onClick={() => onOpenWorkflow(selected.workspacePath)} className="inline-flex items-center gap-1.5 rounded-md border border-border px-3 py-2 text-xs font-medium text-foreground hover:bg-muted">Open automation<ArrowUpRight className="h-3.5 w-3.5" /></button>
        </div>
        {selected.failed && <p className="mt-4 text-sm text-amber-600 dark:text-amber-300">Some data could not be loaded. Refresh to try again.</p>}
        {!!selected.pendingInputs.length && <section aria-label="Decisions required" className="mt-5 overflow-hidden rounded-lg border border-violet-500/25">
          <div className="flex items-center gap-2 bg-violet-500/[0.05] px-4 py-3"><CircleHelp className="h-4 w-4 text-violet-500" /><h4 className="text-sm font-semibold">Needs your decision</h4><span className="text-xs text-muted-foreground">{selected.pendingInputs.length}</span></div>
          <div className="divide-y divide-border">{[...selected.pendingInputs].sort((a, b) => Date.parse(b.updated_at) - Date.parse(a.updated_at)).map(input => <button key={input.id} type="button" onClick={() => onOpenWorkflow(selected.workspacePath)} className="flex w-full items-start gap-3 px-4 py-3 text-left hover:bg-muted/50"><span className={`${PILL_BASE} mt-0.5 shrink-0 border-violet-500/25 text-violet-600 dark:text-violet-300`}>{input.priority}</span><span className="min-w-0 flex-1 text-sm leading-5 text-foreground/90">{input.question}</span><ArrowUpRight className="mt-1 h-3.5 w-3.5 shrink-0 text-muted-foreground" /></button>)}</div>
        </section>}
        <div className="mt-5 space-y-4">
          {!!selectedScopes.length && <div className="flex items-center gap-2"><Activity className="h-4 w-4 text-primary" /><h4 className="text-sm font-semibold">Latest updates</h4></div>}
          {selectedScopes.map(scope => <section key={scope.key} aria-label={`${selected.label} · ${scope.label}`} className="space-y-2">
            <h5 className="text-xs font-semibold text-muted-foreground">{scope.label}</h5>
            <WorkflowSummaryRow label="Run" summary={scope.runSummary} />
            <WorkflowSummaryRow label="Pulse" summary={scope.pulseSummary} />
          </section>)}
          {!selectedScopes.length && <div className="flex min-h-48 flex-col items-center justify-center px-4 py-8 text-center"><Activity className="mb-3 h-6 w-6 text-muted-foreground/50" /><p className="text-sm font-medium text-foreground">No updates yet</p><p className="mt-1 max-w-sm text-sm leading-6 text-muted-foreground">Run summaries and Pulse updates will appear here when this automation reports activity.</p></div>}
        </div>
        {!!selectedHistory.length && <details className="group mt-6 border-t border-border pt-4">
          <summary className="flex cursor-pointer list-none items-center gap-2 rounded-md py-2 text-sm font-medium text-muted-foreground hover:text-foreground [&::-webkit-details-marker]:hidden"><ChevronRight className="h-4 w-4 transition-transform group-open:rotate-90" />Activity history<span className="ml-auto text-xs font-normal">{selectedHistory.length} update{selectedHistory.length === 1 ? '' : 's'}</span></summary>
          <div className="mt-3 space-y-2">{selectedHistory.map(summary => <WorkflowSummaryRow key={summary.id} label={summary.kind === 'pulse_summary' ? 'Pulse' : 'Run'} summary={summary} />)}</div>
        </details>}
      </section> : <div className="flex items-center justify-center p-6 text-sm text-muted-foreground">{decisionsOnly && !search ? 'No automations have pending decisions.' : 'No automations match your search.'}</div>}
    </div>
  </div>
}

export default OrgDashboard

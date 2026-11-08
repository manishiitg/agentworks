import { lazy, Suspense, useCallback, useEffect, useState } from 'react'
import { ArrowLeft } from 'lucide-react'
import { agentApi } from '../../services/api'
import { crewScriptGraphApi } from '../../api/crewScriptGraph'
import type { RelaySourceGraph } from '../../components/workflow/relayGraphAnnotations'
import type { ExecutionInspectionTarget, RunFolderInfo, WorkspaceStepInspection } from '../../services/api-types'
import { PageCard, PageRow, RowButton, RunDot } from '../../components/automation/AutomationKit'
import { useCostsData } from '../../components/workflow/costs/useCostsData'
import { formatCost, formatDuration } from '../../components/workflow/costs/helpers'

const Logs = lazy(() => import('../../components/workflow/ExecutionLogsPopup'))
const Costs = lazy(() => import('../../components/workflow/CostsPopup'))
const Inspect = lazy(() => import('../../components/chat/StepInspectionPanel'))

type Dot = 'running' | 'finished' | 'stopped' | 'idle'

/** The server's run status, as a dot. */
function runDot(status?: string): Dot {
  if (status === 'running') return 'running'
  if (status === 'completed') return 'finished'
  if (status === 'failed') return 'stopped'
  return 'idle'
}

function stepDot(status: string): Dot {
  if (status === 'running') return 'running'
  if (status === 'completed' || status === 'succeeded') return 'finished'
  if (status === 'failed' || status === 'error') return 'stopped'
  return 'idle'
}

const STARTED_BY: Record<string, string> = { cron: 'by schedule', manual: 'started by hand', workflow_builder: 'from the Builder' }

function startedAt(folder: RunFolderInfo) {
  return folder.metadata?.started_at || folder.metadata?.created_at || ''
}

/** Clock time for today, the short date for earlier days. */
function whenLabel(iso: string) {
  const at = new Date(iso)
  if (!iso || Number.isNaN(at.getTime())) return ''
  return at.toDateString() === new Date().toDateString()
    ? at.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
    : at.toLocaleDateString([], { month: 'short', day: 'numeric' })
}

function statusLine(folder: RunFolderInfo) {
  const meta = folder.metadata
  const by = meta?.triggered_by ? STARTED_BY[meta.triggered_by] : undefined
  const head = meta?.status === 'running' ? 'Running'
    : meta?.status === 'completed' ? `Finished in ${formatDuration(meta.duration_ms)}`
    : meta?.status === 'failed' ? 'Stopped'
    : meta?.status === 'canceled' ? 'Canceled'
    : ''
  return [head, by].filter(Boolean).join(' · ')
}

/** Every run's total spend, added up by the server: one small read for the whole list. */
function useRunTotals(workspacePath: string, refreshKey: number) {
  const [totals, setTotals] = useState<Record<string, number> | null>(null)
  useEffect(() => {
    let current = true
    agentApi.getRunCostTotals(workspacePath)
      .then(response => { if (current) setTotals(Object.fromEntries((response.runs ?? []).map(run => [run.run_folder, run.total_cost_usd]))) })
      .catch(() => { if (current) setTotals({}) })
    return () => { current = false }
  }, [workspacePath, refreshKey])
  return totals
}

function useRunInspections(workspacePath: string, runFolder: string) {
  const [records, setRecords] = useState<WorkspaceStepInspection[]>([])
  const [cursor, setCursor] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const load = useCallback(async (after?: string) => {
    setLoading(true)
    setError('')
    try {
      const page = await agentApi.getWorkspaceStepInspections(workspacePath, { cursor: after })
      const matches = page.items.filter(record => record.inspection.workspace_path === workspacePath && record.inspection.run_folder === runFolder)
      setRecords(current => (after ? [...current, ...matches] : matches))
      setCursor(page.next_cursor)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not load step records.')
    } finally {
      setLoading(false)
    }
  }, [workspacePath, runFolder])
  useEffect(() => { void load() }, [load])
  return { records, cursor, error, loading, loadMore: () => void load(cursor ?? undefined) }
}

/** A Python workflow's source outline, so an opened run can list its steps in order, including those it never reached. */
function useScriptOutline(workspacePath: string, enabled: boolean) {
  const [outline, setOutline] = useState<RelaySourceGraph | null>(null)
  useEffect(() => {
    let current = true
    if (!enabled) { setOutline(null); return }
    crewScriptGraphApi.get(workspacePath).then(graph => { if (current) setOutline(graph.nodes.length && !graph.errors.length ? graph : null) }).catch(() => { if (current) setOutline(null) })
    return () => { current = false }
  }, [workspacePath, enabled])
  return outline
}

type StepLine = { key: string; title: string; record?: WorkspaceStepInspection }

/** A run's step lines: the source outline in order (each with its records, or "not reached"), then any record the outline does not name. */
export function runStepLines(records: WorkspaceStepInspection[], outline: RelaySourceGraph | null): StepLine[] {
  if (!outline) return records.map(record => ({ key: `${record.inspection.session_id}:${record.inspection.execution_id}`, title: record.inspection.name || record.inspection.step_id || '', record }))
  const named = outline.nodes.filter(node => !node.id.startsWith('other-') && (node.type === 'script' || node.type === 'agent'))
  const used = new Set<WorkspaceStepInspection>()
  const lines = named.flatMap((node): StepLine[] => {
    const matches = records.filter(record => record.inspection.source_call && record.inspection.source_call === node.call)
    matches.forEach(record => used.add(record))
    return matches.length ? matches.map(record => ({ key: `${record.inspection.session_id}:${record.inspection.execution_id}`, title: node.label || record.inspection.name, record }))
      : [{ key: `outline:${node.id}`, title: node.label, record: undefined }]
  })
  const rest = records.filter(record => !used.has(record)).map(record => ({ key: `${record.inspection.session_id}:${record.inspection.execution_id}`, title: record.inspection.name || record.inspection.step_id || '', record }))
  return [...lines, ...rest]
}

/** The Runs tab: a card of runs, a Costs card, and one opened run with its steps, logs and cost. */
export function CrewWorkflowRuns({ workspacePath, script = false, folders, loading, onRefresh, onAsk }: {
  workspacePath: string
  /** A saved Python workflow: its source outline orders the opened run's steps. */
  script?: boolean
  folders: RunFolderInfo[]
  loading: boolean
  onRefresh: () => Promise<void>
  onAsk: (task: string) => void | Promise<void>
}) {
  const [open, setOpen] = useState<string | null>(null)
  const [showCosts, setShowCosts] = useState(false)
  const totals = useRunTotals(workspacePath, folders.length)
  const costOf = (name: string) => totals?.[name]
  const opened = open ? folders.find(folder => folder.name === open) : undefined
  if (showCosts) return <div className="space-y-3">
    <button type="button" onClick={() => setShowCosts(false)} className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"><ArrowLeft className="h-3.5 w-3.5" /> Back to runs</button>
    <div className="h-[70vh] min-h-96 overflow-hidden rounded-xl border border-border"><Suspense fallback={<p className="p-4 text-sm text-muted-foreground">Loading…</p>}>
      <Costs workspacePath={workspacePath} runFolders={folders.map(folder => folder.name)} selectedRunFolder={null} />
    </Suspense></div>
  </div>
  if (opened) return <OpenedRun workspacePath={workspacePath} script={script} folder={opened} cost={costOf(opened.name)} onBack={() => setOpen(null)} onRefresh={onRefresh} onAsk={onAsk} />
  const weekAgo = Date.now() - 7 * 86_400_000
  const thisWeek = folders.filter(folder => new Date(startedAt(folder)).getTime() >= weekAgo)
  const needLook = thisWeek.filter(folder => folder.metadata?.status === 'failed').length
  const costsLoaded = totals !== null && Object.keys(totals).length > 0
  const spend = (since: number) => folders.filter(folder => new Date(startedAt(folder)).getTime() >= since).reduce((sum, folder) => sum + (costOf(folder.name) ?? 0), 0)
  const startOfToday = new Date(new Date().toDateString()).getTime()
  const hint = [needLook ? `${needLook} this week ${needLook === 1 ? 'needs' : 'need'} a look.` : thisWeek.length ? 'Nothing this week needs a look.' : '', costsLoaded ? `Spent ${formatCost(spend(weekAgo))}.` : ''].filter(Boolean).join(' ')
  return <div className="space-y-4">
    <PageCard title="Runs" hint={hint || undefined} onAsk={() => onAsk('Tell me what happened in this workflow’s recent runs.')}>
      {folders.length === 0 && <p className="px-4 py-3 text-xs text-muted-foreground">{loading ? 'Loading runs…' : 'No runs yet. Run the workflow from your Crew chat.'}</p>}
      {folders.map(folder => {
        const cost = costOf(folder.name)
        return <PageRow key={folder.name} lead={<RunDot state={runDot(folder.metadata?.status)} />} title={folder.name} lines={[statusLine(folder)]}
          aside={<><span className="block">{whenLabel(startedAt(folder))}</span>{cost !== undefined && <span className="block">{formatCost(cost)}</span>}</>}
          open={false} onToggle={() => setOpen(folder.name)}>{null}</PageRow>
      })}
    </PageCard>
    {folders.length > 0 && <PageCard title="Costs" hint={costsLoaded ? <>{formatCost(spend(weekAgo))} this week · {formatCost(spend(startOfToday))} today. <button type="button" onClick={() => setShowCosts(true)} className="text-primary hover:underline">By day and by step</button></> : <button type="button" onClick={() => setShowCosts(true)} className="text-primary hover:underline">By day and by step</button>} onAsk={() => onAsk('Explain what this workflow has cost recently.')}>{null}</PageCard>}
  </div>
}

function OpenedRun({ workspacePath, script, folder, cost, onBack, onRefresh, onAsk }: {
  workspacePath: string
  script: boolean
  folder: RunFolderInfo
  cost?: number
  onBack: () => void
  onRefresh: () => Promise<void>
  onAsk: (task: string) => void | Promise<void>
}) {
  const { records, cursor, error, loading, loadMore } = useRunInspections(workspacePath, folder.name)
  const outline = useScriptOutline(workspacePath, script)
  // The one opened run's step costs, read for that run only.
  const stepCosts = useCostsData({ workspacePath, selectedRunFolder: folder.name, runOnly: true }).runCosts.find(run => run.runFolder === folder.name)?.costSummary?.stepCosts ?? []
  const [target, setTarget] = useState<ExecutionInspectionTarget | null>(null)
  const [detail, setDetail] = useState<'log' | 'costs' | null>(null)
  const stepCost = (record: WorkspaceStepInspection) => stepCosts.find(step => (record.inspection.step_id && step.stepID === record.inspection.step_id) || step.stepTitle === record.inspection.name)?.totalCost
  const failed = records.find(record => stepDot(record.status) === 'stopped')
  const meta = folder.metadata
  const summary = [whenLabel(startedAt(folder)), statusLine(folder).toLowerCase(), cost !== undefined ? formatCost(cost) : ''].filter(Boolean).join(' · ')
  return <PageCard title={folder.name} hint={summary} onAsk={() => onAsk(`Tell me what happened in the run "${folder.name}".`)}
    action={<button type="button" onClick={onBack} aria-label="Back to runs" className="shrink-0 text-muted-foreground hover:text-foreground"><ArrowLeft className="h-4 w-4" /></button>}>
    {meta?.status === 'failed' && <p role="alert" className="px-4 py-3 text-xs text-red-600 dark:text-red-400">{failed ? `Stopped at ${failed.inspection.name || failed.inspection.step_id}.` : 'This run stopped.'}</p>}
    {error && <p role="alert" className="px-4 py-3 text-xs text-destructive">{error}</p>}
    {runStepLines(records, outline).map(line => {
      const record = line.record
      if (!record) return <PageRow key={line.key} lead={<RunDot state="idle" />} title={line.title} lines={[meta?.status === 'running' ? 'Not started yet' : 'Not reached']} />
      const spent = stepCost(record)
      const isOpen = target?.execution_id === record.inspection.execution_id && target.session_id === record.inspection.session_id
      return <PageRow key={line.key} lead={<RunDot state={stepDot(record.status)} />} title={line.title}
        lines={[[whenLabel(record.inspection.started_at), record.duration].filter(Boolean).join(' · ')]} aside={spent !== undefined ? formatCost(spent) : undefined}
        open={isOpen} onToggle={() => setTarget(isOpen ? null : record.inspection)}>
        <div className="h-[60vh] min-h-72 overflow-hidden rounded-lg border border-border bg-background"><Suspense fallback={<p className="p-3 text-xs text-muted-foreground">Loading…</p>}><Inspect target={record.inspection} /></Suspense></div>
      </PageRow>
    })}
    {!loading && records.length === 0 && <p className="px-4 py-3 text-xs text-muted-foreground">No step records were saved for this run. The full log may still be available.</p>}
    <div className="flex flex-wrap items-center gap-2 px-4 py-3">
      {cursor && <RowButton disabled={loading} onClick={loadMore}>{loading ? 'Loading…' : 'Load more steps'}</RowButton>}
      <RowButton onClick={() => setDetail(detail === 'log' ? null : 'log')}>Full log</RowButton>
      <RowButton onClick={() => setDetail(detail === 'costs' ? null : 'costs')}>Cost details</RowButton>
    </div>
    {detail && <div className="h-[70vh] min-h-96 overflow-hidden"><Suspense fallback={<p className="p-4 text-sm text-muted-foreground">Loading…</p>}>
      {detail === 'costs'
        ? <Costs key={folder.name} workspacePath={workspacePath} runFolders={[folder.name]} selectedRunFolder={folder.name} runOnly />
        : <Logs key={folder.name} workspacePath={workspacePath} runFolder={folder.name} runFolders={[folder.name]} runFolderInfos={[folder]} allowDefaultRunFolder={false} onRefreshRunFolders={onRefresh} />}
    </Suspense></div>}
  </PageCard>
}

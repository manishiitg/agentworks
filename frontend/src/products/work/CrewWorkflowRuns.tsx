import { uniqueStepChecks } from './workflowStepChecks'
import { lazy, Suspense, useCallback, useEffect, useState } from 'react'
import { ArrowLeft } from 'lucide-react'
import api, { agentApi } from '../../services/api'
import { isAxiosError } from 'axios'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkflowManifestStore } from '../../stores/useWorkflowManifestStore'
import { crewScriptGraphApi } from '../../api/crewScriptGraph'
import { runEvidenceApi, type RunEvidenceItem } from '../../api/runEvidence'
import type { RelaySourceGraph } from '../../components/workflow/relayGraphAnnotations'
import type { ExecutionInspectionTarget, RunFolderInfo, RunPythonStep, WorkspaceStepInspection } from '../../services/api-types'
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

/** A run's name for people: "Today 09:00", "Yesterday 18:30", "Mon 2 Nov 09:00" (the folder name when it has no start time). */
function runTitle(folder: RunFolderInfo) {
  const at = new Date(startedAt(folder))
  if (Number.isNaN(at.getTime())) return folder.name
  const time = at.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  const day = at.toDateString()
  if (day === new Date().toDateString()) return `Today ${time}`
  if (day === new Date(Date.now() - 86_400_000).toDateString()) return `Yesterday ${time}`
  return `${at.toLocaleDateString([], { weekday: 'short', day: 'numeric', month: 'short' })} ${time}`
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

function useRunInspections(workspacePath: string, runFolder: string, attempt?: string, status?: string) {
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
  }, [workspacePath, runFolder, attempt, status])
  useEffect(() => { void load() }, [load])
  return { records, cursor, error, loading, loadMore: () => void load(cursor ?? undefined) }
}

/** The checks and messages one run made (server-written, read once per opened run and again when its status changes). */
function useRunEvidence(workspacePath: string, runFolder: string, status?: string) {
  const [items, setItems] = useState<RunEvidenceItem[]>([])
  useEffect(() => {
    let current = true
    runEvidenceApi.get(workspacePath, runFolder).then(next => { if (current) setItems(next) }).catch(() => { if (current) setItems([]) })
    return () => { current = false }
  }, [workspacePath, runFolder, status])
  return items
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

type StepLine = { key: string; title: string; record?: WorkspaceStepInspection; persisted?: RunPythonStep }

/**
 * A run's step lines: the source outline in order (each with its records, or "not reached"), then any step the outline
 * does not name. A step the run recorded in its own metadata (python_steps) fills in when no inspection row exists for
 * its attempt; an inspection row, when present, is the richer one and wins.
 */
export function runStepLines(records: WorkspaceStepInspection[], outline: RelaySourceGraph | null, persistedSteps: RunPythonStep[] = []): StepLine[] {
  const recordLine = (record: WorkspaceStepInspection, title?: string): StepLine => ({ key: `${record.inspection.session_id}:${record.inspection.execution_id}`, title: title || record.inspection.name || record.inspection.step_id || '', record })
  const persistedLine = (step: RunPythonStep, title?: string): StepLine => ({ key: `persisted:${step.id}`, title: title || step.name, persisted: step })
  const extra = [...persistedSteps].filter(step => !records.some(record => record.inspection.execution_id === step.execution_id)).sort((a, b) => a.started_at.localeCompare(b.started_at))
  if (!outline) return [...records.map(record => recordLine(record)), ...extra.map(step => persistedLine(step))]
  const named = outline.nodes.filter(node => !node.id.startsWith('other-') && (node.type === 'script' || node.type === 'agent'))
  const used = new Set<WorkspaceStepInspection | RunPythonStep>()
  const lines = named.flatMap((node): StepLine[] => {
    // The same call can appear in two cases (a helper called from two branches) and the run records only the call, so a record is shown once, at the first row that names it.
    const fromRecords = records.filter(record => !used.has(record) && record.inspection.source_call && record.inspection.source_call === node.call)
    const fromMetadata = extra.filter(step => !used.has(step) && step.source_call && step.source_call === node.call)
    fromRecords.forEach(record => used.add(record))
    fromMetadata.forEach(step => used.add(step))
    const found = [...fromRecords.map(record => recordLine(record, node.label || record.inspection.name)), ...fromMetadata.map(step => persistedLine(step, node.label))]
    return found.length ? found : [{ key: `outline:${node.id}`, title: node.label }]
  })
  return [...lines, ...records.filter(record => !used.has(record)).map(record => recordLine(record)), ...extra.filter(step => !used.has(step)).map(step => persistedLine(step))]
}

export type RunsView = { run: string | null; costs: boolean; detail: 'log' | 'costs' | null }
export const noRunsView: RunsView = { run: null, costs: false, detail: null }

/** The Runs tab: a card of runs, a Costs card, and one opened run with its steps, logs and cost. */
export function CrewWorkflowRuns({ workflowId, workspacePath, script = false, folders, loading, onRefresh, onAsk, view, onView }: {
  workflowId: string
  workspacePath: string
  /** Which run (or the Costs card) is open. The page owns it so its back arrow can close it first. */
  view: RunsView
  onView: (view: RunsView) => void
  /** A saved Python workflow: its source outline orders the opened run's steps. */
  script?: boolean
  folders: RunFolderInfo[]
  loading: boolean
  onRefresh: () => Promise<void>
  onAsk: (task: string) => void | Promise<void>
}) {
  const accountCanEdit = useAuthStore(state => state.user?.can_write_workflows === true)
  const access = useWorkflowManifestStore(state => state.workflows.find(item => item.manifest.id === workflowId && item.workspace_path === workspacePath)?.my_access)
  const canResume = script && accountCanEdit && (access === 'owner' || access === 'write')
  const open = view.run
  const showCosts = view.costs
  const setOpen = (run: string | null) => onView({ run, costs: false, detail: null })
  const setShowCosts = (costs: boolean) => onView({ run: null, costs, detail: null })
  const totals = useRunTotals(workspacePath, folders.length)
  const costOf = (name: string) => totals?.[name]
  const opened = open ? folders.find(folder => folder.name === open) : undefined
  if (open && !opened && !loading) return <p role="alert" className="p-3 text-sm text-destructive">Run view unavailable.</p>
  if (showCosts) return <div className="space-y-3">
    <button type="button" onClick={() => setShowCosts(false)} className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"><ArrowLeft className="h-3.5 w-3.5" /> Back to runs</button>
    <div className="h-[70vh] min-h-96 overflow-hidden rounded-xl border border-border"><Suspense fallback={<p className="p-4 text-sm text-muted-foreground">Loading…</p>}>
      <Costs workspacePath={workspacePath} runFolders={folders.map(folder => folder.name)} selectedRunFolder={null} />
    </Suspense></div>
  </div>
  if (opened) return <OpenedRun key={`${workflowId}:${opened.name}`} workflowId={workflowId} canResume={canResume} workspacePath={workspacePath} script={script} folder={opened} cost={costOf(opened.name)} detail={view.detail} onDetail={detail => onView({ ...view, detail })} onBack={() => view.detail ? onView({ ...view, detail: null }) : setOpen(null)} onRefresh={onRefresh} onAsk={onAsk} />
  const weekAgo = Date.now() - 7 * 86_400_000
  const finishedCount = folders.filter(folder => folder.metadata?.status === 'completed').length
  const failedCount = folders.filter(folder => folder.metadata?.status === 'failed').length
  const costsLoaded = totals !== null && Object.keys(totals).length > 0
  const spend = (since: number) => folders.filter(folder => new Date(startedAt(folder)).getTime() >= since).reduce((sum, folder) => sum + (costOf(folder.name) ?? 0), 0)
  const startOfToday = new Date(new Date().toDateString()).getTime()
  const hint = folders.length ? [`${folders.length} run${folders.length === 1 ? '' : 's'}`, finishedCount ? `${finishedCount} finished` : '', failedCount ? `${failedCount} failed` : '', costsLoaded ? `${formatCost(spend(weekAgo))} this week` : ''].filter(Boolean).join(' · ') : undefined
  return <div className="space-y-4">
    <PageCard title="Runs" hint={hint || undefined} onAsk={() => onAsk('Tell me what happened in this workflow’s recent runs.')}>
      {folders.length === 0 && <p className="px-4 py-3 text-xs text-muted-foreground">{loading ? 'Loading runs…' : 'No runs yet. Run the workflow from your Crew chat.'}</p>}
      {folders.map(folder => {
        const cost = costOf(folder.name)
        return <PageRow key={folder.name} lead={<RunDot state={runDot(folder.metadata?.status)} />} title={runTitle(folder)} lines={[statusLine(folder)]}
          aside={cost !== undefined ? formatCost(cost) : undefined}
          open={false} onToggle={() => setOpen(folder.name)}>{null}</PageRow>
      })}
    </PageCard>
    {folders.length > 0 && <PageCard title="Costs" hint={costsLoaded ? <>{formatCost(spend(weekAgo))} this week · {formatCost(spend(startOfToday))} today. <button type="button" onClick={() => setShowCosts(true)} className="text-primary hover:underline">By day and by step</button></> : <button type="button" onClick={() => setShowCosts(true)} className="text-primary hover:underline">By day and by step</button>} onAsk={() => onAsk('Explain what this workflow has cost recently.')}>{null}</PageCard>}
  </div>
}

function OpenedRun({ workflowId, canResume, workspacePath, script, folder, cost, detail, onDetail: setDetail, onBack, onRefresh, onAsk }: {
  workflowId: string
  canResume: boolean
  workspacePath: string
  script: boolean
  folder: RunFolderInfo
  cost?: number
  detail: RunsView['detail']
  onDetail: (detail: RunsView['detail']) => void
  onBack: () => void
  onRefresh: () => Promise<void>
  onAsk: (task: string) => void | Promise<void>
}) {
  const { records, cursor, error, loading, loadMore } = useRunInspections(workspacePath, folder.name, folder.metadata?.execution_id, folder.metadata?.status)
  const outline = useScriptOutline(workspacePath, script)
  const fetchedEvidence = useRunEvidence(workspacePath, folder.name, folder.metadata?.status)
  // The run's own metadata keeps the latest checks and messages too: used when the read returns none.
  const evidence: RunEvidenceItem[] = fetchedEvidence.length ? fetchedEvidence : (folder.metadata?.python_evidence ?? []).map(item => ({ kind: item.kind, at: item.at, name: item.name, ok: item.ok, detail: item.detail, source_call: item.source_call }))
  const persistedSteps = Object.values(folder.metadata?.python_steps ?? {})
  const lines = runStepLines(records, outline, persistedSteps)
  const checkLines = uniqueStepChecks(evidence, lines.map(line => line.record?.inspection.source_call ?? line.persisted?.source_call))
  const checks = evidence.filter(item => item.kind === 'check')
  const messages = evidence.filter(item => item.kind === 'notify')
  // The one opened run's step costs, read for that run only.
  const stepCosts = useCostsData({ workspacePath, selectedRunFolder: folder.name, runOnly: true }).runCosts.find(run => run.runFolder === folder.name)?.costSummary?.stepCosts ?? []
  const [resuming, setResuming] = useState(false)
  const [resumeError, setResumeError] = useState('')
  const [resumed, setResumed] = useState(false)
  useEffect(() => { setResumed(false); setResumeError('') }, [folder.metadata?.execution_id, folder.metadata?.status])
  async function resume() {
    setResuming(true); setResumeError('')
    try {
      await api.post('/api/external/call', { name: 'runs', arguments: { action: 'resume', workflow_id: workflowId, run_folder: folder.name } })
      setResumed(true)
    } catch (cause) {
      const data: unknown = isAxiosError(cause) ? cause.response?.data : undefined
      const message = typeof data === 'string' ? data : (data as { error?: { message?: string } } | undefined)?.error?.message
      setResumeError(message || 'Could not resume this run. Try again.')
      return
    } finally { setResuming(false) }
    // Keep this folder selected. A refresh failure must not offer to submit an already accepted Resume again.
    try { await onRefresh() } catch { setResumeError('Resume started. Could not refresh progress; reopen this run to check it.') }
  }
  const [target, setTarget] = useState<ExecutionInspectionTarget | null>(null)
  const stepCost = (record: WorkspaceStepInspection) => stepCosts.find(step => (record.inspection.step_id && step.stepID === record.inspection.step_id) || step.stepTitle === record.inspection.name)?.totalCost
  const failed = records.find(record => stepDot(record.status) === 'stopped')
  const meta = folder.metadata
  const summary = [statusLine(folder).toLowerCase(), cost !== undefined ? formatCost(cost) : ''].filter(Boolean).join(' · ')
  return <PageCard title={runTitle(folder)} hint={summary} onAsk={() => onAsk(`Tell me what happened in the run "${folder.name}".`)}
    action={<button type="button" onClick={onBack} aria-label={detail ? "Back to run" : "Back to runs"} className="shrink-0 text-muted-foreground hover:text-foreground"><ArrowLeft className="h-4 w-4" /></button>}>
    {meta?.status === 'failed' && <p role="alert" className="px-4 py-3 text-xs text-red-600 dark:text-red-400">{failed ? `Stopped at ${failed.inspection.name || failed.inspection.step_id}.` : 'This run stopped.'}</p>}
    {resumeError && <p role="alert" className="px-4 py-3 text-xs text-destructive">{resumeError}</p>}
    {resumed && <p role="status" className="px-4 py-3 text-xs text-muted-foreground">Resume started in this run.</p>}
    {error && <p role="alert" className="px-4 py-3 text-xs text-destructive">{error}</p>}
    {lines.map(line => {
      const record = line.record
      const step = line.persisted
      if (step) return <PageRow key={line.key} lead={<RunDot state={stepDot(step.status)} />} title={line.title}
        lines={[[whenLabel(step.started_at), step.duration_ms !== undefined ? formatDuration(step.duration_ms) : ''].filter(Boolean).join(' · '), checkLines.get(step.source_call ?? ''), step.detail ?? '']} />
      if (!record) return <PageRow key={line.key} lead={<RunDot state="idle" />} title={line.title} lines={[meta?.status === 'running' ? 'Not started yet' : 'Not reached']} />
      const spent = stepCost(record)
      const isOpen = target?.execution_id === record.inspection.execution_id && target.session_id === record.inspection.session_id
      return <PageRow key={line.key} lead={<RunDot state={stepDot(record.status)} />} title={line.title}
        lines={[[whenLabel(record.inspection.started_at), record.duration].filter(Boolean).join(' · '), checkLines.get(record.inspection.source_call ?? '')]} aside={spent !== undefined ? formatCost(spent) : undefined}
        open={isOpen} onToggle={() => setTarget(isOpen ? null : record.inspection)}>
        <div className="h-[60vh] min-h-72 overflow-hidden rounded-lg border border-border bg-background"><Suspense fallback={<p className="p-3 text-xs text-muted-foreground">Loading…</p>}><Inspect target={record.inspection} /></Suspense></div>
      </PageRow>
    })}
    {checks.length > 0 && <>
      <p className="px-4 pt-3 text-xs font-semibold">Checks</p>
      {checks.map((item, index) => <PageRow key={`check:${index}`} lead={<RunDot state={item.ok === false ? 'stopped' : 'finished'} />} title={item.name || 'Check'} lines={[item.detail]} aside={whenLabel(item.at)} />)}
    </>}
    {messages.length > 0 && <>
      <p className="px-4 pt-3 text-xs font-semibold">Messages</p>
      {messages.map((item, index) => <PageRow key={`notify:${index}`} title={item.detail} aside={whenLabel(item.at)} />)}
    </>}
    {!loading && records.length === 0 && persistedSteps.length === 0 && <p className="px-4 py-3 text-xs text-muted-foreground">No step records were saved for this run. The full log may still be available.</p>}
    <div className="flex flex-wrap items-center gap-2 px-4 py-3">
      {canResume && meta?.status === 'failed' && <>
        <RowButton disabled={resuming || resumed} onClick={() => void resume()}>{resuming ? 'Resuming…' : 'Resume'}</RowButton>
        <span className="text-xs text-muted-foreground">Uses saved source and inputs. Other Python code and app calls run again.</span>
      </>}
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

import { stepChecks } from './workflowStepChecks'
import { lazy, Suspense, useCallback, useEffect, useState } from 'react'
import { ArrowLeft } from 'lucide-react'
import { agentApi } from '../../services/api'
import { crewScriptGraphApi } from '../../api/crewScriptGraph'
import { runEvidenceApi, type RunEvidenceItem } from '../../api/runEvidence'
import type { RelaySourceGraph } from '../../components/workflow/relayGraphAnnotations'
import type { ExecutionInspectionTarget, RunFolderInfo, RunMetadata, RunPythonStep, WorkspaceStepInspection } from '../../services/api-types'
import { MarkdownRenderer } from '../../components/ui/MarkdownRenderer'
import { AiStepTag, PageCard, PageRow, RowButton, RunDot } from '../../components/automation/AutomationKit'
import { CrewRunStepCard } from './CrewRunStepCard'
import { CrewRunCost } from './CrewRunCost'
import { CrewRunSummary } from './CrewRunSummary'
import { CrewRunFiles } from './CrewRunFiles'
import { CrewRunRow } from './CrewRunRow'
import { CrewRunLog } from './CrewRunLog'
import { runTaskName, taskFileName, taskStepRows } from './CrewWorkflowTasks'
import { crewTaskStepsApi, type CrewTaskStep } from '../../api/crewTaskSteps'
import { useCostsData } from '../../components/workflow/costs/useCostsData'
import { formatCost, formatDuration } from '../../components/workflow/costs/helpers'

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

/** `kind`: plain Python (`script`) or a step an agent works out (`agent`). */
type StepLine = { key: string; title: string; kind: 'script' | 'agent'; record?: WorkspaceStepInspection; persisted?: RunPythonStep }

/**
 * A run's step lines: the source outline in order (each with its records, or "not reached"), then any step the outline
 * does not name. A step the run recorded in its own metadata (python_steps) fills in when no inspection row exists for
 * its attempt; an inspection row, when present, is the richer one and wins.
 */
export function runStepLines(records: WorkspaceStepInspection[], outline: RelaySourceGraph | null, persistedSteps: RunPythonStep[] = []): StepLine[] {
  // A step the run inspected is an agent's; a step the script labelled (@step) is plain Python. The outline, when it names the step, says which.
  const recordLine = (record: WorkspaceStepInspection, title?: string, kind: StepLine['kind'] = 'agent'): StepLine => ({ key: `${record.inspection.session_id}:${record.inspection.execution_id}`, title: title || record.inspection.name || record.inspection.step_id || '', kind, record })
  const persistedLine = (step: RunPythonStep, title?: string, kind: StepLine['kind'] = 'script'): StepLine => ({ key: `persisted:${step.id}`, title: title || step.name, kind, persisted: step })
  const extra = [...persistedSteps].filter(step => !records.some(record => record.inspection.execution_id === step.id)).sort((a, b) => a.started_at.localeCompare(b.started_at))
  if (!outline) return [...records.map(record => recordLine(record)), ...extra.map(step => persistedLine(step))]
  const named = outline.nodes.filter(node => !node.id.startsWith('other-') && (node.type === 'script' || node.type === 'agent'))
  const used = new Set<WorkspaceStepInspection | RunPythonStep>()
  const lines = named.flatMap((node): StepLine[] => {
    // The same call can appear in two cases (a helper called from two branches) and the run records only the call, so a record is shown once, at the first row that names it.
    const fromRecords = records.filter(record => !used.has(record) && record.inspection.source_call && record.inspection.source_call === node.call)
    const fromMetadata = extra.filter(step => !used.has(step) && step.source_call && step.source_call === node.call)
    fromRecords.forEach(record => used.add(record))
    fromMetadata.forEach(step => used.add(step))
    const one = fromRecords.length + fromMetadata.length === 1 && named.filter(other => other.call === node.call).length === 1
    const kind: StepLine['kind'] = node.type === 'agent' ? 'agent' : 'script'
    const found = [...fromRecords.map(record => recordLine(record, one ? node.label : undefined, kind)), ...fromMetadata.map(step => persistedLine(step, one ? node.label : undefined, kind))]
    return found.length ? found : [{ key: `outline:${node.id}`, title: node.label, kind }]
  })
  return [...lines, ...records.filter(record => !used.has(record)).map(record => recordLine(record)), ...extra.filter(step => !used.has(step)).map(step => persistedLine(step))]
}

export type RunsView = { run: string | null; costs: boolean; detail: 'log' | 'costs' | null }
export const noRunsView: RunsView = { run: null, costs: false, detail: null }

type RunAsk = NonNullable<RunMetadata['python_asks']>[string]

/** Read the server's answer only on expansion; its normal file grants still decide access. */
function AskAnswer({ path }: { path: string }) {
  const [text, setText] = useState<string | null>(null)
  useEffect(() => {
    let current = true
    setText(null)
    agentApi.getPlannerFileContent(path).then(response => {
      if (current && response.success && typeof response.data?.content === 'string') setText(response.data.content)
    }).catch(() => { /* A refused file read adds nothing to the receipt. */ })
    return () => { current = false }
  }, [path])
  if (text === null) return null
  return <>
    <p className="whitespace-pre-wrap break-words">{text.slice(0, 4096)}{text.length > 4096 ? '…' : ''}</p>
    <MarkdownRenderer content={`[Open full reply](#workspace/${encodeURIComponent(path)})`} />
  </>
}

function AskRow({ ask }: { ask: RunAsk }) {
  const [open, setOpen] = useState(false)
  const status = ask.status === 'replied' ? 'replied'
    : ask.status === 'pending' ? 'waiting for a reply (sent, do not resend)'
    : ask.status === 'refused' ? `refused${ask.detail ? `: ${ask.detail}` : ''}`
    : ask.status === 'unknown' ? 'receipt unclear (do not resend)'
    : 'status unavailable'
  const answer = ask.status === 'replied' ? ask.answer_file : undefined
  return <PageRow title={`Asked ${ask.crew}: ${status}`}
    lines={ask.status !== 'replied' && ask.status !== 'refused' ? [ask.detail] : undefined}
    open={open} onToggle={answer ? () => setOpen(value => !value) : undefined}>
    {answer ? <AskAnswer path={answer} /> : undefined}
  </PageRow>
}

/** The Runs tab: a Costs card on top, so it stays in view however many runs there are, then the runs. A run opens in its own row, with its steps, checks, full log and cost. */
export function CrewWorkflowRuns({ workflowId, workspacePath, script = false, tasks = [], folders, loading, onRefresh, onAsk, view, onView }: {
  workflowId: string
  workspacePath: string
  /** Which run (or the Costs card) is open. The page owns it so its back arrow can close it first. */
  view: RunsView
  onView: (view: RunsView) => void
  /** A saved Python workflow: its source outline orders the opened run's steps. */
  script?: boolean
  /** The workflow's task names, when it has several: each run says which one it executed. */
  tasks?: string[]
  folders: RunFolderInfo[]
  loading: boolean
  onRefresh: () => Promise<void>
  onAsk: (task: string) => void | Promise<void>
}) {
  const open = view.run
  const showCosts = view.costs
  const setOpen = (run: string | null) => onView({ run, costs: false, detail: null })
  const setShowCosts = (costs: boolean) => onView({ run: null, costs, detail: null })
  const totals = useRunTotals(workspacePath, folders.length)
  // The steps written in each Python file, so an opened run can list every step of its task, run or not.
  const [codeSteps, setCodeSteps] = useState<Record<string, CrewTaskStep[]>>(() => crewTaskStepsApi.peek(workspacePath) ?? {})
  useEffect(() => {
    let current = true
    if (script) crewTaskStepsApi.get(workspacePath).then(files => { if (current) setCodeSteps(files) }).catch(() => { /* Without them an opened run shows what it recorded. */ })
    return () => { current = false }
  }, [workspacePath, script])
  const costOf = (name: string) => totals?.[name]
  const opened = open ? folders.find(folder => folder.name === open) : undefined
  if (open && !opened && !loading) return <p role="alert" className="p-3 text-sm text-destructive">Run view unavailable.</p>
  if (showCosts) return <div className="space-y-3">
    <button type="button" onClick={() => setShowCosts(false)} className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"><ArrowLeft className="h-3.5 w-3.5" /> Back to runs</button>
    <div className="h-[70vh] min-h-96 overflow-hidden rounded-xl border border-border"><Suspense fallback={<p className="p-4 text-sm text-muted-foreground">Loading…</p>}>
      <Costs workspacePath={workspacePath} runFolders={folders.map(folder => folder.name)} selectedRunFolder={null} />
    </Suspense></div>
  </div>
  const weekAgo = Date.now() - 7 * 86_400_000
  const finishedCount = folders.filter(folder => folder.metadata?.status === 'completed').length
  const failedCount = folders.filter(folder => folder.metadata?.status === 'failed').length
  const costsLoaded = totals !== null && Object.values(totals).some(amount => amount > 0)
  const spend = (since: number) => folders.filter(folder => new Date(startedAt(folder)).getTime() >= since).reduce((sum, folder) => sum + (costOf(folder.name) ?? 0), 0)
  const startOfToday = new Date(new Date().toDateString()).getTime()
  const hint = folders.length ? [`${folders.length} run${folders.length === 1 ? '' : 's'}`, finishedCount ? `${finishedCount} finished` : '', failedCount ? `${failedCount} failed` : '', costsLoaded ? `${formatCost(spend(weekAgo))} this week` : ''].filter(Boolean).join(' · ') : undefined
  return <div className="space-y-4">
    {folders.length > 0 && <PageCard title="Costs" hint={costsLoaded ? <>{formatCost(spend(weekAgo))} this week · {formatCost(spend(startOfToday))} today. <button type="button" onClick={() => setShowCosts(true)} className="text-primary hover:underline">By day and by step</button></> : <button type="button" onClick={() => setShowCosts(true)} className="text-primary hover:underline">By day and by step</button>} onAsk={() => onAsk('Explain what this workflow has cost recently.')}>{null}</PageCard>}
    <PageCard title="Runs" hint={hint || undefined}>
      {folders.length === 0 && <p className="px-4 py-3 text-xs text-muted-foreground">{loading ? 'Loading runs…' : 'No runs yet. Run the workflow from your Crew chat.'}</p>}
      {folders.map(folder => {
        const cost = costOf(folder.name)
        const isOpen = opened?.name === folder.name
        return <CrewRunRow key={folder.name} testId={`run-${folder.name}`} lead={<RunDot state={runDot(folder.metadata?.status)} />}
          title={<>{runTitle(folder)}{runTaskName(tasks, folder.metadata) && <span className="font-normal text-muted-foreground"> · {runTaskName(tasks, folder.metadata)}</span>}</>} lines={[statusLine(folder)]}
          aside={cost ? formatCost(cost) : undefined}
          showDetails={!script}
          detail={isOpen ? view.detail : null} onDetail={detail => onView({ run: folder.name, costs: false, detail })} onAsk={() => onAsk(`Tell me what happened in the run "${folder.name}".`)}
          open={isOpen} onToggle={() => setOpen(isOpen ? null : folder.name)}>
          {isOpen && <OpenedRun key={`${workflowId}:${folder.name}`} workflowId={workflowId} workspacePath={workspacePath} script={script} folder={folder} task={runTaskName(tasks, folder.metadata)} codeSteps={codeSteps} cost={cost} costKnown={totals !== null} detail={view.detail} />}
        </CrewRunRow>
      })}
    </PageCard>
  </div>
}

function OpenedRun({ workflowId, workspacePath, script, folder, task, codeSteps, cost, costKnown, detail }: {
  workflowId: string
  /** The task this run executed, when the page can tell. */
  task?: string
  /** The steps written in each Python file of the workflow. */
  codeSteps: Record<string, CrewTaskStep[]>
  workspacePath: string
  script: boolean
  folder: RunFolderInfo
  cost?: number
  /** The run totals have been read, so a missing cost means none. */
  costKnown: boolean
  detail: RunsView['detail']
}) {
  const { records, cursor, error, loading, loadMore } = useRunInspections(workspacePath, folder.name, folder.metadata?.execution_id, folder.metadata?.status)
  const outline = useScriptOutline(workspacePath, script)
  const fetchedEvidence = useRunEvidence(workspacePath, folder.name, folder.metadata?.status)
  // The run's own metadata keeps the latest checks and messages too: used when the read returns none.
  const evidence: RunEvidenceItem[] = fetchedEvidence.length ? fetchedEvidence : (folder.metadata?.python_evidence ?? []).map(item => ({ kind: item.kind, at: item.at, name: item.name, ok: item.ok, detail: item.detail, source_call: item.source_call, step_execution_id: item.step_execution_id, step_name: item.step_name }))
  const persistedSteps = Object.values(folder.metadata?.python_steps ?? {})
  const lines = runStepLines(records, outline, persistedSteps)
  // A script's own run is recorded as one wrapper line named after the workflow; its real steps follow.
  const visibleLines = script ? lines.filter(line => line.record?.inspection.step_id !== workflowId) : lines
  // A script run of a known task lists every step of that task's code: each with what the run recorded, or "Not run".
  const taskRows = script && task && records.length === 0 ? taskStepRows(codeSteps[taskFileName(task)] ?? [], [...persistedSteps].sort((a, b) => a.started_at.localeCompare(b.started_at))) : []
  const useTaskRows = taskRows.length > 0
  const checkLines = stepChecks(evidence)
  const checks = evidence.filter(item => item.kind === 'check')
  const messages = evidence.filter(item => item.kind === 'notify')
  // The one opened run's step costs, read for that run only.
  const stepCosts = useCostsData({ workspacePath, selectedRunFolder: folder.name, runOnly: true }).runCosts.find(run => run.runFolder === folder.name)?.costSummary?.stepCosts ?? []
  const [target, setTarget] = useState<ExecutionInspectionTarget | null>(null)
  const stepCost = (record: WorkspaceStepInspection) => stepCosts.find(step => (record.inspection.step_id && step.stepID === record.inspection.step_id) || step.stepTitle === record.inspection.name)?.totalCost
  const meta = folder.metadata
  const costRows = stepCosts.map(step => ({ title: step.stepTitle || step.stepID || 'Step', cost: step.totalCost ?? 0 }))
  const started = startedAt(folder)
  return <div className="-mx-4 -my-3 space-y-1 py-2">
    {error && <p role="alert" className="px-4 py-3 text-xs text-destructive">{error}</p>}
    {script
      ? <CrewRunSummary facts={[started ? `Started ${whenLabel(started)}` : '', meta?.duration_ms ? `took ${formatDuration(meta.duration_ms)}` : '', meta?.triggered_by ? STARTED_BY[meta.triggered_by] ?? '' : '']} cost={cost} costKnown={costKnown} steps={costRows} />
      : <>
        {detail === 'costs' && <CrewRunCost total={cost} known={costKnown} steps={costRows} />}
        {detail === 'log' && <CrewRunLog workspacePath={workspacePath} runFolder={folder.name} />}
      </>}
    {useTaskRows && <p className="px-4 pt-3 text-xs font-semibold">Steps{task ? ` of ${task}` : ''}</p>}
    {useTaskRows && taskRows.map((row, index) => {
      const step = row.recorded
      const waiting = meta?.status === 'running' ? 'Waiting to start' : undefined
      return <div key={row.key} className="px-4 py-1.5"><CrewRunStepCard title={row.title} kind={row.kind} description={row.description} position={`Step ${index + 1} of ${taskRows.length}`}
        state={step ? stepDot(step.status) : 'idle'}
        when={step ? [whenLabel(step.started_at), step.duration_ms !== undefined ? formatDuration(step.duration_ms) : ''].filter(Boolean).join(' · ') : waiting}
        checks={step ? checkLines.get(step.id) : undefined} detail={step?.detail} /></div>
    })}
    {!useTaskRows && visibleLines.length > 0 && <p className="px-4 pt-3 text-xs font-semibold">Steps</p>}
    {!useTaskRows && visibleLines.map(line => {
      const record = line.record
      const step = line.persisted
      if (step) return <div key={line.key} className="px-4 py-1.5"><CrewRunStepCard title={line.title} kind={line.kind} state={stepDot(step.status)}
        when={[whenLabel(step.started_at), step.duration_ms !== undefined ? formatDuration(step.duration_ms) : ''].filter(Boolean).join(' · ')} checks={checkLines.get(step.id)} detail={step.detail} /></div>
      if (!record) return <PageRow key={line.key} lead={<RunDot state="idle" />} title={line.title} lines={[meta?.status === 'running' ? 'Not started yet' : 'Not reached']} />
      const spent = stepCost(record)
      const isOpen = target?.execution_id === record.inspection.execution_id && target.session_id === record.inspection.session_id
      return <PageRow key={line.key} lead={<RunDot state={stepDot(record.status)} />} title={<>{line.title}<AiStepTag kind={line.kind} /></>}
        lines={[[whenLabel(record.inspection.started_at), record.duration].filter(Boolean).join(' · '), checkLines.get(record.inspection.execution_id)]} aside={spent ? formatCost(spent) : undefined}
        open={isOpen} onToggle={() => setTarget(isOpen ? null : record.inspection)}>
        <div className="h-[60vh] min-h-72 overflow-hidden rounded-lg border border-border bg-background"><Suspense fallback={<p className="p-3 text-xs text-muted-foreground">Loading…</p>}><Inspect target={record.inspection} /></Suspense></div>
      </PageRow>
    })}
    {!!meta?.python_resume_calls?.length && <>
      <p className="px-4 pt-3 text-xs font-semibold">Calls during Resume</p>
      {meta.python_resume_calls.map((call, index) => <PageRow key={`resume:${index}`} title={call.key} lines={[call.reused ? 'Reused saved result' : 'Ran again']} aside={whenLabel(call.at)} />)}
    </>}
    {Object.values(meta?.python_asks ?? {}).map(ask => <AskRow key={ask.id} ask={ask} />)}
    {checks.length > 0 && <>
      <p className="px-4 pt-3 text-xs font-semibold">Checks</p>
      {checks.map((item, index) => <PageRow key={`check:${index}`} lead={<RunDot state={item.ok === false ? 'stopped' : 'finished'} />} title={item.name || 'Check'} lines={[item.detail]} aside={whenLabel(item.at)} />)}
    </>}
    {messages.length > 0 && <>
      <p className="px-4 pt-3 text-xs font-semibold">Messages</p>
      {messages.map((item, index) => <PageRow key={`notify:${index}`} title={item.detail} aside={whenLabel(item.at)} />)}
    </>}
    {cursor && <div className="px-4 py-3"><RowButton disabled={loading} onClick={loadMore}>{loading ? 'Loading…' : 'Load more steps'}</RowButton></div>}
    {script && <>
      <CrewRunFiles workspacePath={workspacePath} runFolder={folder.name} />
      <CrewRunLog workspacePath={workspacePath} runFolder={folder.name} />
    </>}
    {meta?.status === 'failed' && !loading && visibleLines.length === 0 && <p role="alert" className="px-4 py-3 text-xs text-red-600 dark:text-red-400">This run stopped.</p>}
    {!loading && records.length === 0 && persistedSteps.length === 0 && <p className="px-4 py-3 text-xs text-muted-foreground">No step records were saved for this run. The full log may still be available.</p>}
  </div>
}

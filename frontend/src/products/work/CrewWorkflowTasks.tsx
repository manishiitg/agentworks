import { useEffect, useState } from 'react'
import { FileCode2, Trash2 } from 'lucide-react'
import { AiStepTag, PageCard, PageRow, RowButton, RunDot, RunningTag, Spinner, relativeTime } from '../../components/automation/AutomationKit'
import { crewTaskStepsApi, type CrewTaskStep } from '../../api/crewTaskSteps'
import type { RunPythonStep } from '../../services/api-types'

/** The Python file a task keeps its steps in: `slack-message-notifier` is `slack_message_notifier.py`. */
export const taskFileName = (task: string): string => `${task.trim().toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_+|_+$/g, '')}.py`

/** The file a recorded step was written in, from its source call (`file.py:function@file.py:line`). */
const stepFile = (step: RunPythonStep): string => (step.source_call?.split(':')[0] ?? '').split('/').pop()?.toLowerCase() ?? ''

/**
 * Splits the steps of the latest run by task. A step belongs to the task whose file it was written in;
 * steps written anywhere else (main.py, a helper) are returned as `other`, so none is hidden.
 */
export function groupStepsByTask(tasks: string[], steps: RunPythonStep[]): { byTask: Map<string, RunPythonStep[]>; other: RunPythonStep[] } {
  const files = new Map(tasks.map(task => [taskFileName(task), task]))
  const byTask = new Map<string, RunPythonStep[]>(tasks.map(task => [task, []]))
  const other: RunPythonStep[] = []
  for (const step of steps) {
    const task = files.get(stepFile(step))
    if (task) byTask.get(task)!.push(step)
    else other.push(step)
  }
  return { byTask, other }
}

/**
 * The task a run executed: the one its saved setting is named after, else the one whose file its steps were written in.
 * Undefined when neither says (a run with custom inputs that stopped before its first step).
 */
export function runTaskName(tasks: string[], run: { input_preset?: string | null; python_steps?: Record<string, RunPythonStep> } | undefined): string | undefined {
  if (!run || !tasks.length) return undefined
  const preset = run.input_preset
  if (preset && tasks.includes(preset)) return preset
  const files = new Map(tasks.map(task => [taskFileName(task), task]))
  for (const step of Object.values(run.python_steps ?? {})) {
    const task = files.get(stepFile(step))
    if (task) return task
  }
  return undefined
}

/** One step of a task as the page shows it: what the code says, with what the latest run recorded for it (if it ran). */
export type TaskStepRow = { key: string; title: string; description?: string; kind: 'script' | 'agent'; recorded?: RunPythonStep }

/**
 * The steps of one task: every step written in its file (in code order), each with the latest run's record when it ran.
 * A recorded step the code does not list (a step named while running) is added at the end, so none is hidden.
 * Without code steps (the code could not be read) only the recorded steps show.
 */
export function taskStepRows(codeSteps: CrewTaskStep[], recorded: RunPythonStep[]): TaskStepRow[] {
  const label = (text: string) => text.trim().toLowerCase()
  const left = [...recorded]
  const rows: TaskStepRow[] = codeSteps.map((code, index) => {
    const at = left.findIndex(step => label(step.name) === label(code.title))
    const match = at >= 0 ? left.splice(at, 1)[0] : undefined
    return { key: `code-${index}`, title: code.title, description: code.description, kind: code.kind, recorded: match }
  })
  return [...rows, ...left.map(step => ({ key: step.id, title: step.name, kind: 'script' as const, recorded: step }))]
}

const dotState = (status?: string) => status === 'running' ? 'running' : status === 'failed' ? 'stopped' : status ? 'finished' : 'idle'

function summary(rows: TaskStepRow[], anyRun: boolean): string {
  const count = `${rows.length} step${rows.length === 1 ? '' : 's'}`
  const ran = rows.filter(row => row.recorded)
  if (!ran.length) return `${count} · ${anyRun ? 'not in the latest run' : 'not run yet'}`
  if (ran.some(row => row.recorded!.status === 'failed')) return `${count} · last run failed`
  if (ran.some(row => row.recorded!.status === 'running')) return `${count} · running now`
  const at = ran.map(row => row.recorded!.completed_at || row.recorded!.started_at).sort().pop()
  return `${count} · ${ran.length < rows.length ? `${ran.length} ran, ` : ''}finished ${relativeTime(at)}`.trim()
}


/** A file of the workflow as a small button: pressing it opens the file on this page. */
function FileButton({ file, onOpen }: { file: string; onOpen: (file: string) => void }) {
  return <button type="button" title={`Open ${file}`} onClick={() => onOpen(file)}
    className="inline-flex items-center gap-1.5 rounded-full border border-border px-3 py-1 text-xs hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><FileCode2 aria-hidden="true" className="h-3.5 w-3.5" />{file}</button>
}

/**
 * The tasks of a workflow whose `task` input picks between them, one row each, with the steps the latest run
 * recorded for that task. Before any run, only the names show.
 */
export function CrewWorkflowTasks({ tasks, descriptions = {}, workspacePath, files = [], onOpenFile, runActive = false, runningTask, latestSteps, onAskTask, onRunTask, onDeleteTask }: {
  tasks: string[]
  /** One plain line per task, keyed by task name. A task without one shows "No description yet." */
  descriptions?: Record<string, string>
  /** The workflow's path, to read its step descriptions from its code. Without it no step description shows. */
  workspacePath?: string
  /** The workflow's files: each task shows a button for its own file. The Files tab lists all of them. */
  files?: string[]
  /** Opens a file on this page. Without it no file shows. */
  onOpenFile?: (file: string) => void
  /** A run of this workflow is in progress, and the task it executes when that is known. */
  runActive?: boolean
  runningTask?: string
  latestSteps: RunPythonStep[]
  /** Ask the Crew's agent about this one task (opens a draft; the page changes nothing itself). */
  onAskTask?: (task: string) => void | Promise<void>
  /** Start a run of this task now. Throws an Error with a plain message when it cannot start. */
  onRunTask?: (task: string) => Promise<void>
  /** Delete is a request to the Crew's agent: a task is code in the script, so the page does not remove it itself. */
  onDeleteTask?: (task: string) => void | Promise<void>
}) {
  const [codeSteps, setCodeSteps] = useState<Record<string, CrewTaskStep[]>>(() => (workspacePath ? crewTaskStepsApi.peek(workspacePath) : undefined) ?? {})
  useEffect(() => {
    let current = true
    if (workspacePath) crewTaskStepsApi.get(workspacePath).then(files => { if (current) setCodeSteps(files) }).catch(() => { /* Without the code's steps only the recorded ones show. */ })
    return () => { current = false }
  }, [workspacePath])
  const [open, setOpen] = useState<Set<string>>(new Set())
  const [starting, setStarting] = useState<string | null>(null)
  const [notes, setNotes] = useState<Record<string, { text: string; failed: boolean }>>({})
  const toggle = (id: string) => setOpen(current => { const next = new Set(current); if (!next.delete(id)) next.add(id); return next })
  const { byTask, other } = groupStepsByTask(tasks, latestSteps)
  async function start(task: string) {
    if (!onRunTask) return
    setStarting(task)
    setNotes(current => { const next = { ...current }; delete next[task]; return next })
    try {
      await onRunTask(task)
      setNotes(current => ({ ...current, [task]: { text: 'Started. It shows in Runs.', failed: false } }))
    } catch (cause) {
      setNotes(current => ({ ...current, [task]: { text: cause instanceof Error && cause.message ? cause.message : 'Could not start this task. Try again.', failed: true } }))
    } finally { setStarting(null) }
  }
  /** Starts every task, one after the other. A task that cannot start says why on its own row and the rest still start. */
  async function startAll() {
    for (const task of tasks) await start(task)
  }
  const stepRows = (rows: TaskStepRow[]) => <div className="-mx-4 -my-3 divide-y divide-border">
    {rows.map(row => {
      const running = row.recorded?.status === 'running'
      return <PageRow key={row.key} lead={running ? <Spinner /> : <RunDot state={dotState(row.recorded?.status)} />} title={<>{row.title}<AiStepTag kind={row.kind} />{running && <RunningTag />}</>}
        lines={[row.description, running ? <span className="font-medium text-amber-700 dark:text-amber-300">Running now</span> : row.recorded?.detail ?? (row.recorded ? undefined : 'Not run yet')]} />
    })}
  </div>
  return <PageCard title="Tasks" hint="The jobs this workflow can do." testId="crew-workflow-tasks"
    action={onRunTask && tasks.length > 1 ? <RowButton title="Start every task now" disabled={starting !== null} onClick={() => void startAll()}>{starting !== null ? 'Starting…' : 'Run all'}</RowButton> : undefined}>
    {tasks.map(task => {
      const rows = taskStepRows(codeSteps[taskFileName(task)] ?? [], byTask.get(task) ?? [])
      const ownFile = taskFileName(task)
      const taskFile = files.includes(ownFile) || codeSteps[ownFile] ? ownFile : undefined
      const runningAt = rows.findIndex(row => row.recorded?.status === 'running')
      const isRunning = runningAt >= 0 || (runActive && runningTask === task)
      const runningLine = isRunning ? <span className="font-medium text-amber-700 dark:text-amber-300">Running now{runningAt >= 0 ? ` · step ${runningAt + 1} of ${rows.length}: ${rows[runningAt].title}` : ''}</span> : undefined
      const note = notes[task]
      const action = (onAskTask || onRunTask || onDeleteTask) && <span className="flex items-center gap-1">
        {onAskTask && <RowButton title={`Ask about the task ${task}`} onClick={() => void onAskTask(task)}>Ask</RowButton>}
        {onRunTask && <RowButton title={`Run the task ${task} now`} disabled={starting !== null} onClick={() => void start(task)}>{starting === task ? 'Starting…' : 'Run now'}</RowButton>}
        {onDeleteTask && <button type="button" title={`Delete the task ${task}`} aria-label={`Delete the task ${task}`} onClick={() => void onDeleteTask(task)}
          className="grid h-8 w-8 place-items-center rounded-full text-muted-foreground hover:bg-muted hover:text-destructive focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><Trash2 className="h-4 w-4" aria-hidden="true" /></button>}
      </span>
      return <PageRow key={task} testId={`workflow-task-${task}`} title={<>{task}{isRunning && <RunningTag />}</>}
        lines={[descriptions[task] || 'No description yet.', runningLine ?? summary(rows, latestSteps.length > 0), note && <span className={note.failed ? 'text-destructive' : ''}>{note.text}</span>]} action={action} open={open.has(task)} onToggle={() => toggle(task)}>
        {rows.length ? stepRows(rows) : <p className="text-muted-foreground">No steps found in this task's code yet.</p>}
        {onOpenFile && taskFile && <div className="flex flex-wrap items-center gap-2 pt-3"><span className="text-xs font-semibold">File</span><FileButton file={taskFile} onOpen={onOpenFile} /></div>}
      </PageRow>
    })}
    {other.length > 0 && <PageRow testId="workflow-task-other" title="Other steps" lines={[`${other.length} step${other.length === 1 ? '' : 's'} not in a task's file`]} open={open.has('')} onToggle={() => toggle('')}>{stepRows(taskStepRows([], other))}</PageRow>}
  </PageCard>
}

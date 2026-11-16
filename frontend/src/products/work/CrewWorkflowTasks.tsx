import { useState } from 'react'
import { Trash2 } from 'lucide-react'
import { PageCard, PageRow, RowButton, RunDot, relativeTime } from '../../components/automation/AutomationKit'
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

const dotState = (status?: string) => status === 'running' ? 'running' : status === 'failed' ? 'stopped' : status ? 'finished' : 'idle'

function summary(steps: RunPythonStep[], anyRun: boolean): string {
  const count = `${steps.length} step${steps.length === 1 ? '' : 's'}`
  if (!steps.length) return `${count} · ${anyRun ? 'not in the latest run' : 'not run yet'}`
  if (steps.some(step => step.status === 'failed')) return `${count} · last run failed`
  if (steps.some(step => step.status === 'running')) return `${count} · running now`
  const at = steps.map(step => step.completed_at || step.started_at).sort().pop()
  return `${count} · finished ${relativeTime(at)}`.trim()
}

/** A small label saying a step is plain Python (`Scripted`) or an AI agent's work (`Agentic`). */
const kindChip = (kind: 'script' | 'agent') => <span className="ml-2 rounded-full border border-border px-1.5 py-px text-[10px] font-normal text-muted-foreground">{kind === 'agent' ? 'Agentic' : 'Scripted'}</span>

/**
 * The tasks of a workflow whose `task` input picks between them, one row each, with the steps the latest run
 * recorded for that task. Before any run, only the names show.
 */
export function CrewWorkflowTasks({ tasks, latestSteps, onAskTask, onRunTask, onDeleteTask }: {
  tasks: string[]
  latestSteps: RunPythonStep[]
  /** Ask the Crew's agent about this one task (opens a draft; the page changes nothing itself). */
  onAskTask?: (task: string) => void | Promise<void>
  /** Start a run of this task now. Throws an Error with a plain message when it cannot start. */
  onRunTask?: (task: string) => Promise<void>
  /** Delete is a request to the Crew's agent: a task is code in the script, so the page does not remove it itself. */
  onDeleteTask?: (task: string) => void | Promise<void>
}) {
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
  const stepRows = (steps: RunPythonStep[]) => <div className="-mx-4 -my-3 divide-y divide-border">
    {steps.map(step => <PageRow key={step.id} lead={<RunDot state={dotState(step.status)} />} title={<>{step.name}{kindChip('script')}</>} lines={[step.detail]} />)}
  </div>
  return <PageCard title="Tasks" hint="The jobs this workflow can do." testId="crew-workflow-tasks"
    action={onRunTask && tasks.length > 1 ? <RowButton title="Start every task now" disabled={starting !== null} onClick={() => void startAll()}>{starting !== null ? 'Starting…' : 'Run all'}</RowButton> : undefined}>
    {tasks.map(task => {
      const steps = byTask.get(task) ?? []
      const note = notes[task]
      const action = (onAskTask || onRunTask || onDeleteTask) && <span className="flex items-center gap-1">
        {onAskTask && <RowButton title={`Ask about the task ${task}`} onClick={() => void onAskTask(task)}>Ask</RowButton>}
        {onRunTask && <RowButton title={`Run the task ${task} now`} disabled={starting !== null} onClick={() => void start(task)}>{starting === task ? 'Starting…' : 'Run now'}</RowButton>}
        {onDeleteTask && <button type="button" title={`Delete the task ${task}`} aria-label={`Delete the task ${task}`} onClick={() => void onDeleteTask(task)}
          className="grid h-8 w-8 place-items-center rounded-full text-muted-foreground hover:bg-muted hover:text-destructive focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><Trash2 className="h-4 w-4" aria-hidden="true" /></button>}
      </span>
      return <PageRow key={task} testId={`workflow-task-${task}`} title={task}
        lines={[summary(steps, latestSteps.length > 0), note && <span className={note.failed ? 'text-destructive' : ''}>{note.text}</span>]} action={action} open={open.has(task)} onToggle={() => toggle(task)}>
        {steps.length ? stepRows(steps) : <p className="text-muted-foreground">No steps yet. They appear once this task runs.</p>}
      </PageRow>
    })}
    {other.length > 0 && <PageRow testId="workflow-task-other" title="Other steps" lines={[`${other.length} step${other.length === 1 ? '' : 's'} not in a task's file`]} open={open.has('')} onToggle={() => toggle('')}>{stepRows(other)}</PageRow>}
  </PageCard>
}

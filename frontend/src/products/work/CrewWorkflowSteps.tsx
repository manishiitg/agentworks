import { stepChecks } from './workflowStepChecks'
import { useEffect, useState } from 'react'
import { crewScriptGraphApi } from '../../api/crewScriptGraph'
import type { RelaySourceGraph as SourceGraph, RelaySourceNode } from '../../components/workflow/relayGraphAnnotations'
import { PageCard, PageRow, RowButton, RunDot, relativeTime } from '../../components/automation/AutomationKit'
import type { RunPythonEvidence, RunPythonStep } from '../../services/api-types'

/** A step's place in the order, ringed by how it went in the latest run. */
function StepNumber({ n, status }: { n: number; status?: string }) {
  const ring = status === 'failed' ? 'border-red-500 text-red-500' : status === 'running' ? 'border-amber-400 text-amber-400' : status ? 'border-emerald-500 text-emerald-500' : 'border-border text-muted-foreground'
  return <span aria-hidden="true" className={`flex h-7 w-7 items-center justify-center rounded-full border text-xs ${ring}`}>{n}</span>
}

const dotState = (status?: string) => status === 'running' ? 'running' : status === 'failed' ? 'stopped' : status ? 'finished' : 'idle'

/** The outline nests: a branch holds its cases, a case its steps; a loop, try or block holds its steps. Nodes arrive in source order with `parent` set. */
function OutlineRows({ nodes, byName, checkLines, parent, numbered = false, open, toggle }: {
  nodes: RelaySourceNode[]
  byName: Map<string, RunPythonStep>
  checkLines: Map<string, string>
  parent?: string
  /** The top level is numbered 1, 2, 3 …; nested rows use a dot. */
  numbered?: boolean
  open: Set<string>
  toggle: (id: string) => void
}) {
  const stepsIn = (id: string): RelaySourceNode[] => nodes.filter(node => node.parent === id).flatMap(node => node.role ? stepsIn(node.id) : [node])
  const stepOf = (node: RelaySourceNode) => node.type === 'script' ? byName.get(node.label.trim().toLowerCase()) : undefined
  return <>
    {nodes.filter(node => node.parent === parent).map((node, index) => {
      if (!node.role) {
        const step = stepOf(node)
        return <PageRow key={node.id} lead={numbered ? <StepNumber n={index + 1} status={step?.status} /> : <RunDot state={dotState(step?.status)} />} title={node.label}
          lines={[node.type === 'agent' ? 'Agent' : node.label === 'Other code' ? 'Code with no step or agent inside' : '', step?.detail, checkLines.get(step?.id ?? '')]} />
      }
      const inside = stepsIn(node.id)
      const count = node.role === 'branch' ? `${nodes.filter(child => child.parent === node.id).length} cases` : `${inside.length} step${inside.length === 1 ? '' : 's'}`
      const failed = inside.some(step => stepOf(step)?.status === 'failed')
      return <PageRow key={node.id} lead={numbered ? <StepNumber n={index + 1} status={failed ? 'failed' : undefined} /> : <RunDot state={failed ? 'stopped' : 'idle'} />} title={node.label} lines={[count]} open={open.has(node.id)} onToggle={() => toggle(node.id)}>
        <div className="-mx-4 -my-3 divide-y divide-border">
          <OutlineRows nodes={nodes} byName={byName} checkLines={checkLines} parent={node.id} open={open} toggle={toggle} />
        </div>
      </PageRow>
    })}
  </>
}

/**
 * What a workflow does, in order: the server's outline of its steps (labelled with @step, agents and "Other code"),
 * each with how it went in the latest run. With no outline (too large, or unreadable) the steps of the latest run
 * are listed instead; with neither, the card still shows Run and a way to the code.
 */
export function CrewWorkflowSteps({ workspacePath, title, latestSteps, latestEvidence = [], onRun, runPresets = [], runBusy = false, onCode, onAsk }: {
  workspacePath: string
  title: string
  /** The python steps of the latest run, in the order they started. */
  latestSteps: RunPythonStep[]
  latestEvidence?: RunPythonEvidence[]
  onRun: (preset?: string) => void
  runPresets?: string[]
  runBusy?: boolean
  onCode: () => void
  onAsk: (task: string) => void | Promise<void>
}) {
  const [state, setState] = useState<{ workspacePath: string; graph?: SourceGraph } | null>(null)
  const [open, setOpen] = useState<Set<string>>(new Set())
  const toggle = (id: string) => setOpen(current => { const next = new Set(current); if (!next.delete(id)) next.add(id); return next })
  useEffect(() => {
    let current = true
    crewScriptGraphApi.get(workspacePath)
      .then(graph => { if (current) setState({ workspacePath, graph }) })
      .catch(() => { if (current) setState({ workspacePath }) })
    return () => { current = false }
  }, [workspacePath])
  if (state?.workspacePath !== workspacePath) return <p className="p-4 text-sm text-muted-foreground">Loading…</p>
  const nodes = state.graph?.nodes ?? []
  const checkLines = stepChecks(latestEvidence)
  // An outline row has no execution identity. Match a single recorded label only; repeated labels stay in Runs.
  const nameOf = (name: string) => name.trim().toLowerCase()
  const byName = new Map(latestSteps.filter(step => latestSteps.filter(other => nameOf(other.name) === nameOf(step.name)).length === 1 && nodes.filter(node => !node.role && nameOf(node.label) === nameOf(step.name)).length === 1).map(step => [nameOf(step.name), step]))
  const note = state.graph?.errors[0]
  const count = nodes.length ? nodes.filter(node => !node.role && node.label !== 'Other code').length : latestSteps.length
  const lastAt = latestSteps.map(step => step.completed_at || step.started_at).sort().pop()
  const lastRun = !latestSteps.length ? 'not run yet' : latestSteps.some(step => step.status === 'failed') ? 'last run failed' : latestSteps.some(step => step.status === 'running') ? 'running now' : `last run finished ${relativeTime(lastAt)}`.trim()
  return <PageCard title={title} hint={`${count} step${count === 1 ? '' : 's'} · ${lastRun}`} onAsk={() => onAsk('Help me change this workflow.')}
    action={<div className="flex shrink-0 items-center gap-1">
      <RowButton primary disabled={runBusy} onClick={() => onRun()}>{runBusy ? 'Starting…' : 'Run'}</RowButton>
      {runPresets.length > 0 && <select aria-label="Run with saved settings" value="" disabled={runBusy}
        onChange={event => { if (event.target.value) onRun(event.target.value) }}
        className="max-w-40 rounded-full border border-border bg-background px-2 py-0.5 text-xs disabled:opacity-50">
        <option value="" disabled>Run with…</option>
        {runPresets.map(preset => <option key={preset} value={preset}>{preset}</option>)}
      </select>}
    </div>} testId="crew-workflow-steps">
    {nodes.length
      ? <OutlineRows nodes={nodes} byName={byName} checkLines={checkLines} numbered open={open} toggle={toggle} />
      : !latestSteps.length ? <div className="flex items-center gap-3 px-4 py-3"><p className="min-w-0 flex-1 text-xs text-muted-foreground">No steps to show yet. They appear once the workflow has run, or you can read its code.</p><RowButton onClick={onCode}>View code</RowButton></div>
      : latestSteps.map((step, index) => <PageRow key={step.id} lead={<StepNumber n={index + 1} status={step.status} />} title={step.name} lines={[step.detail, checkLines.get(step.id)]} />)}
    {!!state.graph?.unkeyed_calls?.length && <p className="px-4 py-3 text-xs text-muted-foreground">{state.graph.unkeyed_calls.length} calls have no saved-result key; add one so a failed run can continue.</p>}
    {note && <p className="px-4 py-3 text-xs text-muted-foreground">{nodes.length ? note : `${note} The steps above are from the latest run.`}</p>}
  </PageCard>
}

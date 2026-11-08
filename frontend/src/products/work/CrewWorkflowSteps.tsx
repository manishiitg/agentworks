import { uniqueStepChecks } from './workflowStepChecks'
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
          lines={[node.type === 'agent' ? 'Agent' : node.label === 'Other code' ? 'Code with no step or agent inside' : '', step?.detail, checkLines.get(node.call ?? '')]} />
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
export function CrewWorkflowSteps({ workspacePath, title, latestSteps, latestEvidence = [], onRun, runBusy = false, onCode, onAsk }: {
  workspacePath: string
  title: string
  /** The python steps of the latest run, in the order they started. */
  latestSteps: RunPythonStep[]
  latestEvidence?: RunPythonEvidence[]
  onRun: () => void
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
  const checkLines = uniqueStepChecks(latestEvidence, latestSteps.map(step => step.source_call))
  // A shared call can also appear in two branches of the outline; do not repeat one run's checks on both.
  for (const node of nodes) if (node.call && nodes.filter(other => other.call === node.call).length > 1) checkLines.delete(node.call)
  const byName = new Map(latestSteps.map(step => [step.name.trim().toLowerCase(), step]))
  const note = state.graph?.errors[0]
  const count = nodes.length ? nodes.filter(node => !node.role && node.label !== 'Other code').length : latestSteps.length
  const lastAt = latestSteps.map(step => step.completed_at || step.started_at).sort().pop()
  const lastRun = !latestSteps.length ? 'not run yet' : latestSteps.some(step => step.status === 'failed') ? 'last run failed' : latestSteps.some(step => step.status === 'running') ? 'running now' : `last run finished ${relativeTime(lastAt)}`.trim()
  return <PageCard title={title} hint={`${count} step${count === 1 ? '' : 's'} · ${lastRun}`} onAsk={() => onAsk('Help me change this workflow.')}
    action={<RowButton primary disabled={runBusy} onClick={onRun}>{runBusy ? 'Starting…' : 'Run'}</RowButton>} testId="crew-workflow-steps">
    {nodes.length
      ? <OutlineRows nodes={nodes} byName={byName} checkLines={checkLines} numbered open={open} toggle={toggle} />
      : !latestSteps.length ? <div className="flex items-center gap-3 px-4 py-3"><p className="min-w-0 flex-1 text-xs text-muted-foreground">No steps to show yet. They appear once the workflow has run, or you can read its code.</p><RowButton onClick={onCode}>View code</RowButton></div>
      : latestSteps.map((step, index) => <PageRow key={step.id} lead={<StepNumber n={index + 1} status={step.status} />} title={step.name} lines={[step.detail, checkLines.get(step.source_call ?? '')]} />)}
    {note && <p className="px-4 py-3 text-xs text-muted-foreground">{nodes.length ? note : `${note} The steps above are from the latest run.`}</p>}
  </PageCard>
}

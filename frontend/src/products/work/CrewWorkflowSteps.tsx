import { useEffect, useState, type ReactNode } from 'react'
import { crewScriptGraphApi } from '../../api/crewScriptGraph'
import type { RelaySourceGraph as SourceGraph, RelaySourceNode } from '../../components/workflow/relayGraphAnnotations'
import { PageCard, PageRow, RowButton, RunDot } from '../../components/automation/AutomationKit'
import type { RunPythonStep } from '../../services/api-types'

const dotState = (status?: string) => status === 'running' ? 'running' : status === 'failed' ? 'stopped' : status ? 'finished' : 'idle'

/** The outline nests: a branch holds its cases, a case its steps; a loop, try or block holds its steps. Nodes arrive in source order with `parent` set. */
function OutlineRows({ nodes, byName, parent, open, toggle }: {
  nodes: RelaySourceNode[]
  byName: Map<string, RunPythonStep>
  parent?: string
  open: Set<string>
  toggle: (id: string) => void
}) {
  const stepsIn = (id: string): RelaySourceNode[] => nodes.filter(node => node.parent === id).flatMap(node => node.role ? stepsIn(node.id) : [node])
  const stepOf = (node: RelaySourceNode) => node.type === 'script' ? byName.get(node.label.trim().toLowerCase()) : undefined
  return <>
    {nodes.filter(node => node.parent === parent).map(node => {
      if (!node.role) {
        const step = stepOf(node)
        return <PageRow key={node.id} lead={<RunDot state={dotState(step?.status)} />} title={node.label}
          lines={[node.type === 'agent' ? 'Agent' : node.label === 'Other code' ? 'Code with no step or agent inside' : '', step?.detail]} />
      }
      const inside = stepsIn(node.id)
      const count = node.role === 'branch' ? `${nodes.filter(child => child.parent === node.id).length} cases` : `${inside.length} step${inside.length === 1 ? '' : 's'}`
      const failed = inside.some(step => stepOf(step)?.status === 'failed')
      return <PageRow key={node.id} lead={<RunDot state={failed ? 'stopped' : 'idle'} />} title={node.label} lines={[count]} open={open.has(node.id)} onToggle={() => toggle(node.id)}>
        <div className="-mx-4 -my-3 divide-y divide-border">
          <OutlineRows nodes={nodes} byName={byName} parent={node.id} open={open} toggle={toggle} />
        </div>
      </PageRow>
    })}
  </>
}

/**
 * What a workflow does, in order: the server's outline of its steps (labelled with @step, agents and "Other code"),
 * each with how it went in the latest run. With no outline (too large, or unreadable) the steps of the latest run
 * are listed instead; with neither, `empty` is shown.
 */
export function CrewWorkflowSteps({ workspacePath, latestSteps, onCode, onAsk, empty }: {
  workspacePath: string
  /** The python steps of the latest run, in the order they started. */
  latestSteps: RunPythonStep[]
  onCode: () => void
  onAsk: (task: string) => void | Promise<void>
  empty: ReactNode
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
  const byName = new Map(latestSteps.map(step => [step.name.trim().toLowerCase(), step]))
  if (!nodes.length && !latestSteps.length) return <>{empty}</>
  const note = state.graph?.errors[0]
  return <PageCard title="Steps" hint="What this workflow does, in order." onAsk={() => onAsk('Help me label the steps of this workflow so its outline is clear.')}
    action={<RowButton onClick={onCode}>Open code</RowButton>} testId="crew-workflow-steps">
    {nodes.length
      ? <OutlineRows nodes={nodes} byName={byName} open={open} toggle={toggle} />
      : latestSteps.map(step => <PageRow key={step.id} lead={<RunDot state={dotState(step.status)} />} title={step.name} lines={[step.detail]} />)}
    {note && <p className="px-4 py-3 text-xs text-muted-foreground">{nodes.length ? note : `${note} The steps above are from the latest run.`}</p>}
  </PageCard>
}

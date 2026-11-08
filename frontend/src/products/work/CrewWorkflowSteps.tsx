import { useEffect, useState, type ReactNode } from 'react'
import { crewScriptGraphApi } from '../../api/crewScriptGraph'
import type { RelaySourceGraph as SourceGraph } from '../../components/workflow/relayGraphAnnotations'
import { PageCard, PageRow, RowButton, RunDot } from '../../components/automation/AutomationKit'
import type { RunPythonStep } from '../../services/api-types'

const dotState = (status?: string) => status === 'running' ? 'running' : status === 'failed' ? 'stopped' : status ? 'finished' : 'idle'

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
      ? nodes.map(node => {
        const step = node.type === 'script' ? byName.get(node.label.trim().toLowerCase()) : undefined
        return <PageRow key={node.id} lead={<RunDot state={dotState(step?.status)} />} title={node.label}
          lines={[node.type === 'agent' ? 'Agent' : node.label === 'Other code' ? 'Code with no step or agent inside' : '', step?.detail]} />
      })
      : latestSteps.map(step => <PageRow key={step.id} lead={<RunDot state={dotState(step.status)} />} title={step.name} lines={[step.detail]} />)}
    {note && <p className="px-4 py-3 text-xs text-muted-foreground">{nodes.length ? note : `${note} The steps above are from the latest run.`}</p>}
  </PageCard>
}

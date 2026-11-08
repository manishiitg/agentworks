import { lazy, Suspense, useEffect, useState, type ReactNode } from 'react'
import { crewScriptGraphApi } from '../../api/crewScriptGraph'
import type { RelaySourceGraph as SourceGraph } from '../../components/workflow/relayGraphAnnotations'
import { PageCard, RowButton } from '../../components/automation/AutomationKit'

const RelaySourceGraph = lazy(() => import('../../components/workflow/RelaySourceGraph').then(module => ({ default: module.RelaySourceGraph })))

/**
 * The Plan of a saved Python workflow: the server's outline of its steps (labelled with @step, agents and "Other code"),
 * drawn with the graph a Relay uses. A script with no outline yet, or one the server could not read, shows its code.
 */
export function CrewScriptPlan({ workspacePath, onCode, onAsk, code }: {
  workspacePath: string
  onCode: () => void
  onAsk: (task: string) => void | Promise<void>
  /** What to show when there is no outline. */
  code: ReactNode
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
  const graph = state.graph
  if (!graph || (!graph.errors.length && !graph.nodes.length)) return <>{code}</>
  return <PageCard title="Steps" hint="What this script does, in order." onAsk={() => onAsk('Help me label the steps of this script so its outline is clear.')}
    action={<RowButton onClick={onCode}>Open code</RowButton>} testId="crew-script-plan">
    <div className="h-[60vh] min-h-72">
      <Suspense fallback={<p className="p-4 text-sm text-muted-foreground">Loading…</p>}>
        <RelaySourceGraph source="" graph={graph} onBuild={() => void onAsk('Help me label the steps of this script so its outline is clear.')} onCode={onCode} />
      </Suspense>
    </div>
  </PageCard>
}

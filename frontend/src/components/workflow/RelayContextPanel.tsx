import { WorkflowContextPanel } from './WorkflowContextPanel'

export function RelayContextPanel({ workspacePath }: { workspacePath: string | null }) {
  return <WorkflowContextPanel workspacePath={workspacePath} relayMode />
}

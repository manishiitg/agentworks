import { create } from 'zustand'
import type { ActiveSessionInfo, RunFolderInfo, SessionExecutionTreeResponse, SessionExecutionTreeNode } from '../../services/api-types'
import type { CrewExecutable } from './CrewExecutableEditor'

export type WorkflowRunTarget = { crewId: string; executableId: string; workspacePath: string; runFolder: string }

/** PLAT-746: a monitor click names an execution, never the latest run or a chat. */
export function workflowRunTarget(
  session: ActiveSessionInfo,
  tree: SessionExecutionTreeResponse,
  folders: RunFolderInfo[],
  executables: CrewExecutable[],
): WorkflowRunTarget | null {
  if (!session.crew_project_id || !session.workspace_path || tree.session_id !== session.session_id) return null
  const executable = executables.find(item => item.workspace_path === session.workspace_path)
  if (!executable) return null
  const executions = new Set<string>()
  const visit = (node: SessionExecutionTreeNode) => {
    if (node.session_id === session.session_id && node.source === 'workflow_run') executions.add(node.execution_id)
    node.children?.forEach(visit)
  }
  visit(tree.root)
  const matching = folders.filter(folder => folder.metadata?.execution_id && executions.has(folder.metadata.execution_id))
  // Ambiguity or an absent saved record must never open an unrelated run.
  if (matching.length !== 1) return null
  return { crewId: session.crew_project_id, executableId: executable.id, workspacePath: executable.workspace_path, runFolder: matching[0].name }
}

/** One pending navigation intent, scoped to its Crew; it is never saved across app restarts. */
export const useWorkflowRunNavigation = create<{ target: WorkflowRunTarget | null }>(() => ({ target: null }))

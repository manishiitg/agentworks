import { create } from 'zustand'
import type { ActiveSessionInfo, RunFolderInfo, RunningWorkflowInfo } from '../../services/api-types'
import type { CrewExecutable } from './CrewExecutableEditor'

export type WorkflowRunTarget = { crewId: string; executableId: string; workspacePath: string; runFolder: string }

/** PLAT-746: a monitor click names an execution, never the latest run or a chat. */
export function workflowRunTarget(
  session: ActiveSessionInfo,
  run: RunningWorkflowInfo,
  folders: RunFolderInfo[],
  executables: CrewExecutable[],
): WorkflowRunTarget | null {
  if (!session.crew_project_id || !session.workspace_path || run.session_id !== session.session_id || run.workspace_path !== session.workspace_path || !run.query_id || !run.run_folder) return null
  const executable = executables.find(item => item.workspace_path === session.workspace_path)
  if (!executable) return null
  // The authorized workflow tracker owns this link. Direct/script runs have
  // no chat execution tree (PLAT-746); do not manufacture a chat to open one.
  const matching = folders.filter(folder => folder.name === run.run_folder &&
    (!folder.metadata?.execution_id || folder.metadata.execution_id === run.query_id))
  // Ambiguity or an absent saved record must never open an unrelated run.
  if (matching.length !== 1) return null
  return { crewId: session.crew_project_id, executableId: executable.id, workspacePath: executable.workspace_path, runFolder: matching[0].name }
}

/** One pending navigation intent, scoped to its Crew; it is never saved across app restarts. */
export const useWorkflowRunNavigation = create<{ target: WorkflowRunTarget | null }>(() => ({ target: null }))

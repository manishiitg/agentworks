import api, { agentApi } from '../../services/api'
import type { ActiveSessionInfo } from '../../services/api-types'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import type { CrewExecutable } from './CrewExecutableEditor'
import { useWorkflowRunNavigation, workflowRunTarget } from './workflowRunNavigation'

export async function openCrewWorkflowRun(session: ActiveSessionInfo): Promise<void> {
  if (!session.crew_project_id || !session.workspace_path) throw new Error('Run view unavailable.')
  const [run, folders, { data }] = await Promise.all([
    agentApi.getRunningWorkflow(session.session_id),
    agentApi.getRunFolders(session.workspace_path),
    api.get<{ executables: CrewExecutable[] }>('/api/crew-executables', { params: { crew_id: session.crew_project_id } }),
  ])
  const target = workflowRunTarget(session, run, folders.folders, data.executables)
  if (!target) throw new Error('Run view unavailable.')
  useWorkflowRunNavigation.setState({ target })
  useProductSurfaceStore.getState().setSelectedWorkProjectId(target.crewId)
  openProductWorkspace('work')
}

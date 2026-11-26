import { selectReportDocument } from '../../components/workflow/reportDocuments'
import { setWorkspaceViewTarget } from '../../hooks/useWorkspaceViewTarget'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import type { ProductPanel } from '../productPanels'
import { crewMenuTarget, crewProjectMenuItems, type CrewMenuItem } from './crewProjectMenu'
import { openCrewAccess } from './MacSharedAgents'
import type { WorkWorkspaceView } from './WorkWorkspacePane'

export function isSameRightPanelScope(userId?: string, serverId?: string): boolean {
  return (
    useAuthStore.getState().user?.id === userId &&
    useWorkspaceConnectionStore.getState().activeWorkspaceId === serverId
  )
}

export function findAdmittedCrewMenuItem(
  itemId: string,
  options: {
    panels: ProductPanel<WorkWorkspaceView>[]
    enabledPanels?: Set<string>
    incomingEmail?: boolean
    projectId?: string
    userId?: string
    serverId?: string
  },
): CrewMenuItem | null {
  if (!isSameRightPanelScope(options.userId, options.serverId)) return null
  const current = useAuthStore.getState().user
  const admitted = crewProjectMenuItems({
    panels: options.panels,
    allowedProducts: current?.allowed_products,
    enabledPanels: options.enabledPanels,
    incomingEmail: options.incomingEmail,
    projectId: options.projectId,
  })
  return admitted.find(candidate => candidate.id === itemId) ?? null
}

export function navigateCrewMenuItem(
  item: CrewMenuItem,
  options: {
    projectId: string
    workspacePath?: string
    onOpen: (view: WorkWorkspaceView) => void
    directTeamAccess?: boolean
  },
): boolean {
  if (options.directTeamAccess && item.id === 'mcp:team') {
    openCrewAccess(options.projectId, '', 'team')
    return true
  }
  if (item.external) {
    openProductWorkspace(item.external)
    return true
  }
  if (!item.view) return false
  if (item.reportPath && options.workspacePath) {
    selectReportDocument(options.workspacePath, item.reportPath)
  }
  if (item.view === 'files') setWorkspaceViewTarget('files', 'browse')
  else if (item.target) setWorkspaceViewTarget(crewMenuTarget(item.view), item.target)
  options.onOpen(item.view)
  return true
}

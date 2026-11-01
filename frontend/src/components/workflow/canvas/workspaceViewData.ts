import { createContext, useContext } from 'react'
import type { UsePlanDataReturn } from '../hooks/usePlanData'
import type { UseWorkspaceStateReturn } from '../hooks/useWorkspaceState'
import type { WorkflowExecutionStatus } from '../hooks/useWorkflowExecution'
import type { VariablesManifest } from '../../../services/api-types'
import type { WorkspaceViewId } from '../workspaceViews'

/** Embedded editors own their selection without changing the active product/chat. */
export interface EmbeddedWorkflowScope {
  crewAgents?: Array<{ id: string; name: string }>
  view: WorkspaceViewId
  selectedRunFolder: string | null
  setSelectedRunFolder: (folder: string | null) => void
  openView: (view: WorkspaceViewId, target?: string) => void
}

export type WorkflowImageExportFormat = 'svg' | 'png' | 'jpeg'

/** The flow view's shell state. `loading` and `error` replace the whole pane
 * (and hide the toolbar) exactly as the flow canvas always did before the
 * data layer moved into the host; `ready` renders the plan or the empty state. */
export type FlowShell = 'loading' | 'error' | 'ready'

/**
 * Toolbar-and-view data that `WorkspaceViewHost` loads once and every view
 * body reads from. Before this context each view variant called the same
 * four hooks itself, so switching views remounted the data layer.
 */
export interface WorkspaceViewData {
  scope?: EmbeddedWorkflowScope
  planData: UsePlanDataReturn
  status: WorkflowExecutionStatus
  workspace: UseWorkspaceStateReturn
  selectedRunFolder: string | null
  runFolderNames: string[]
  /** The manifest the toolbar and flow canvas show: workspace state's copy,
   * overridden by an in-flow VariablesSidebar edit until the next refresh. */
  variablesManifest: VariablesManifest | null
  isLoadingVariables: boolean
  setVariablesManifest: (manifest: VariablesManifest | null) => void
  /** A manual Plan refresh must not swap the flow for the initial-load
   * screen, so the flow body reports it here and the host's shell honours it. */
  isRefreshingPlan: boolean
  setIsRefreshingPlan: (refreshing: boolean) => void
  flowShell: FlowShell
  /** Plan error with "plan.json not found" already treated as "no plan". */
  flowError: string | null
  /** The flow canvas registers its image export here so the host-owned
   * toolbar can trigger it; null when no flow canvas is mounted. */
  registerExportHandler: (handler: ((format: WorkflowImageExportFormat) => Promise<void>) | null) => void
}

export const WorkspaceViewDataContext = createContext<WorkspaceViewData | null>(null)

export function useWorkspaceViewData(): WorkspaceViewData {
  const data = useContext(WorkspaceViewDataContext)
  if (!data) {
    throw new Error('useWorkspaceViewData must be used inside WorkspaceViewHost')
  }
  return data
}

import type { ReactNode } from 'react'
import type { ExecutionOptions } from '../../../services/api-types'
import type { WorkspaceViewId } from '../workspaceViews'

/** An embedded view owns selection without changing the active product/chat. */
export interface EmbeddedWorkflowScope {
  onEditInputs?: () => void
  crewAgents?: Array<{ id: string; name: string }>
  crewScripts?: Array<{ id: string; name: string }>
  view: WorkspaceViewId
  selectedRunFolder: string | null
  setSelectedRunFolder: (folder: string | null) => void
  openView: (view: WorkspaceViewId, target?: string) => void
}

export interface WorkflowCanvasProps {
  scope?: EmbeddedWorkflowScope
  workspacePath: string | null
  presetQueryId: string | null
  relayMode?: boolean
  currentPhase?: string
  onStartPhase?: (phaseId: string, executionOptions?: ExecutionOptions) => void
  onCreatePlan?: () => void
  showChatArea?: boolean
  onToggleChatArea?: () => void
  toolbarOnly?: boolean  // When true, render only the toolbar
  sharedToolbar?: boolean
  chatTabsSlot?: ReactNode  // Chat tab strip rendered inline in the toolbar (shared-toolbar mode)
  workshopPanel?: ReactNode
  paneClassName?: string
  className?: string
  hideToolbar?: boolean
  openHumanActionsOnMount?: boolean
}

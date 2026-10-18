import { workspacePanelGroup } from '../../workspace/workspacePanelGroups'
import { WorkspaceContextButton } from '../../workspace/WorkspaceContextButton'
import { WorkflowContextTools } from '../WorkflowContextPanel'
import React, { useEffect, useRef, useMemo, useCallback } from 'react'
import { useShallow } from 'zustand/react/shallow'
import { useWorkflowStore, type RunFolder } from '../../../stores/useWorkflowStore'
import { PRIMARY_WORKSPACE_TOOLBAR_VIEWS, WORKSPACE_VIEWS, type WorkspaceViewId } from '../workspaceViews'
import { useChatStore } from '../../../stores/useChatStore'
import { useAuthStore } from '../../../stores/useAuthStore'
import type { VariablesManifest } from '../../../services/api-types'
import type { PlanningResponse } from '../../../utils/stepConfigMatching'
import type { WorkflowExecutionStatus } from '../hooks/useWorkflowExecution'
import type { ExecutionOptions } from '../../../services/api-types'
import { TooltipProvider } from '../../ui/tooltip'
import { GlobalActivityMonitor } from '../../GlobalActivityMonitor'
import { useModeStore } from '../../../stores/useModeStore'
import { WorkspaceTopToolbar } from '../../workspace/WorkspaceTopToolbar'
import { useRegisterPanelSwitcher, type PanelSwitcherPanel } from '../../../stores/usePanelSwitcherStore'
import { RELAY_INTEGRATION_SECTION_IDS, WORKFLOW_PANEL_SECTIONS } from '../../../products/productPanels'

/** Adds each panel's tabs from the registry; a Flow's panels show fewer tabs. */
function withPanelSections(panels: PanelSwitcherPanel[], relayMode: boolean): PanelSwitcherPanel[] {
  return panels.map(panel => {
    const sections = (WORKFLOW_PANEL_SECTIONS[panel.id] ?? []).filter(section =>
      !relayMode || (panel.id === 'mcp' ? RELAY_INTEGRATION_SECTION_IDS.has(section.id) : panel.id === 'identity' ? section.id !== 'upgrades' : panel.id === 'workshop' ? ['triggers', 'conditions', 'chats'].includes(section.id) : true))
    return sections.length ? { ...panel, sections } : panel
  })
}
import { useAppStore } from '../../../stores/useAppStore'
import { useLLMStore } from '../../../stores/useLLMStore'

// Execution phase ID - special phase that should be displayed separately
const EXECUTION_PHASE_ID = 'execution'
const PRIMARY_TOOLBAR_VIEW_IDS = new Set<WorkspaceViewId>(['pulse', 'browser'])
const OPERATIONS_TOOLBAR_VIEW_IDS = new Set<WorkspaceViewId>(['flow', 'workshop', 'knowledge', 'costs', 'execution-logs', 'files', 'backup', 'publish', 'notify'])
const SETUP_TOOLBAR_LABELS: Partial<Record<WorkspaceViewId, string>> = {
  identity: 'Details & instructions',
  playbooks: 'Playbooks',
  mcp: 'Connections',
}

interface WorkflowToolbarProps {
  relayMode?: boolean
  pythonRelay?: boolean
  status: WorkflowExecutionStatus
  plan?: PlanningResponse | null  // Plan data used by toolbar actions
  currentPhase?: string
  workspacePath?: string | null
  presetQueryId?: string | null  // Used to persist settings per workflow
  // API data passed as props (avoids store subscription issues)
  runFolders: RunFolder[]
  variablesManifest: VariablesManifest | null
  isLoadingWorkspaceState?: boolean  // Whether workspace state (iterations, manifest) is loading
  onStartPhase: (phaseId: string, executionOptions?: ExecutionOptions) => void
  onCreatePlan: () => void
  showChatArea?: boolean
  onToggleChatArea?: () => void
  onExport?: () => void
  // Chat tab strip (WorkflowChatTabs) rendered inline on the left of this bar so the
  // workflow chat tabs + new-chat share one row with the status/tools instead of
  // sitting in a separate bar below.
  chatTabsSlot?: React.ReactNode
  // Whether Pulse review is enabled for this workflow -- owned by the host
  // (shared with the pane's PulseView), just read here for the badge.
  monitorOn: boolean
  className?: string
}

export const WorkflowToolbar: React.FC<WorkflowToolbarProps> = ({
  relayMode = false,
  status,
  workspacePath,
  presetQueryId,
  variablesManifest,
  isLoadingWorkspaceState = false,
  chatTabsSlot,
  className = ''
}) => {
  // Workspace store for opening folders

  // Workflow store - use useShallow to prevent unnecessary re-renders
  // Note: runFolders, variablesManifest come from props (passed from WorkflowCanvas)
  const {
    selectedRunFolder,
    selectedGroupIds,
    currentRunningGroupId,
    loadSavedSettings,
    setSelectedGroupIds,
    restoreSelectionFromLocalStorage,
  } = useWorkflowStore(useShallow(state => ({
    selectedRunFolder: state.selectedRunFolder,
    selectedGroupIds: state.selectedGroupIds,
    currentRunningGroupId: state.currentRunningGroupId,
    loadSavedSettings: state.loadSavedSettings,
    setSelectedGroupIds: state.setSelectedGroupIds,
    restoreSelectionFromLocalStorage: state.restoreSelectionFromLocalStorage,
  })))

  const workflowWorkspaceView = useWorkflowStore(state => state.workflowWorkspaceView)
  const lastCanvasView = useWorkflowStore(state => state.lastCanvasView)
  const openWorkspaceView = useWorkflowStore(state => state.openWorkspaceView)

  // No explicit view means the pane is on whichever canvas view was last open.
  const activeWorkspaceView: WorkspaceViewId = workflowWorkspaceView ?? lastCanvasView
  // Button clusters come from the view registry, in registry order. Plan is
  // always present, including for a new workflow with no steps yet.
  // Browser stays visible in Views; Plan lives in Ops. A Flow keeps its Graph in Views.
  const workspaceViewDefinitions = PRIMARY_WORKSPACE_TOOLBAR_VIEWS.filter(view => (relayMode ? view.id === 'flow' : PRIMARY_TOOLBAR_VIEW_IDS.has(view.id)) && view.id !== 'report')
  const operationsWorkspaceViewDefinitions = PRIMARY_WORKSPACE_TOOLBAR_VIEWS.filter(view => OPERATIONS_TOOLBAR_VIEW_IDS.has(view.id) && (!relayMode || ['workshop', 'costs', 'execution-logs', 'files'].includes(view.id)))
  const capabilityViewDefinitions = useMemo(
    () => WORKSPACE_VIEWS.filter(view => view.toolbarGroup === 'capabilities' && (!relayMode || view.id === 'mcp' || view.id === 'identity')),
    [relayMode],
  )

  // Share is for this workflow's owners (or an admin), multi-user mode only.
  const isMultiUser = useAuthStore(state => state.isMultiUserMode)
  // ⌘/Ctrl+K searches the same panels this toolbar shows (owner, 2026-10-07).
  const panelSwitcherPanels = useMemo<PanelSwitcherPanel[]>(() => withPanelSections([
    { id: 'context', label: 'Project overview', group: 'Views' },
    { id: 'report', label: 'Dashboard', group: 'Views' },
    ...(relayMode ? [] : [{ id: 'pulse', label: 'Pulse reviews', group: 'Views' }, { id: 'human-actions', label: 'Decisions', group: 'Views' }]),
    ...workspaceViewDefinitions.map(view => ({ id: view.id, label: relayMode && view.id === 'flow' ? 'Flow' : view.label, group: 'Views' })),
    ...operationsWorkspaceViewDefinitions.map(view => ({ id: view.id, label: relayMode && view.id === 'workshop' ? 'Triggers' : view.label, group: 'Ops' })),
    ...(relayMode ? [] : [{ id: 'backup', label: 'Backup', group: 'Ops' }, { id: 'publish', label: 'Publish', group: 'Ops' }, { id: 'notify', label: 'Notifications', group: 'Ops' }]),
    ...capabilityViewDefinitions.map(view => ({ id: view.id, label: SETUP_TOOLBAR_LABELS[view.id] ?? view.label, group: 'Setup' })),
    ...(isMultiUser ? [{ id: 'access', label: 'Access', group: 'Setup' }] : []),
  ], relayMode).map(panel => ({ ...panel, group: workspacePanelGroup(panel.id) })), [relayMode, workspaceViewDefinitions, operationsWorkspaceViewDefinitions, capabilityViewDefinitions, isMultiUser])
  useRegisterPanelSwitcher(workspacePath ? (relayMode ? 'relays' : 'agentworks') : null, panelSwitcherPanels, (id, section) => openWorkspaceView(id as WorkspaceViewId, id === 'mcp' ? section ?? 'overview' : section))
  // Main workflow execution phase for the canvas toolbar
  const targetExecutionPhaseId = EXECUTION_PHASE_ID
  
  // Check if execution phase specifically is running (not just any phase)
  // Use a selector that only recalculates when chatTabs, pollingInterval, or sseConnections change
  const isExecutionRunning = useChatStore(state => {
    const chatTabs = state.chatTabs
    const pollingInterval = state.pollingInterval
    const sseConnections = state.sseConnections
    const allTabs = Object.values(chatTabs)

    try {
      // Filter for execution phase tabs belonging to the current preset
      const executionTabs = allTabs.filter(tab =>
        tab.metadata?.mode === 'workflow' &&
        tab.metadata?.phaseId === targetExecutionPhaseId &&
        tab.metadata?.presetQueryId === presetQueryId
      )

      // Check if any execution tab is streaming
      return executionTabs.some(tab => {
        // If tab is completed, it's not streaming
        if (tab.isCompleted) return false

        // Tab is streaming if there's an active connection (SSE or polling) and tab is not manually paused
        const hasActiveConnection = pollingInterval !== null
          || (tab.sessionId != null && sseConnections[tab.sessionId] != null)
        if (hasActiveConnection) {
          return tab.isStreaming !== false // Respect manual pause
        }

        // Also show Stop if tab.isStreaming is explicitly true (set immediately on query submit,
        // before SSE/polling connects)
        return tab.isStreaming === true
      })
    } catch (error) {
      console.error('[WorkflowToolbar] Error checking execution phase status:', error)
      return false
    }
  }) // Zustand will handle memoization - only re-render if result changes

  // Per-tab live status (busy/idle/stopped) + Stop now live inside each chat tab
  // pill (see WorkflowChatTabs), so the toolbar no longer renders a status badge.

  // Load saved settings when preset changes
  useEffect(() => {
    if (presetQueryId) {
      loadSavedSettings(presetQueryId)
    }
  }, [presetQueryId, loadSavedSettings])


  // Restore selection from localStorage after workspace state finishes loading
  // This ensures localStorage values are restored AFTER all API data is loaded
  const hasRestoredRef = useRef(false)
  useEffect(() => {
    // Only restore once when workspace loading completes and manifest is available
    if (!isLoadingWorkspaceState && variablesManifest && !hasRestoredRef.current) {
      restoreSelectionFromLocalStorage()
      hasRestoredRef.current = true
    }
    // Reset the flag when workspace starts loading (preset change)
    if (isLoadingWorkspaceState) {
      hasRestoredRef.current = false
    }
  }, [isLoadingWorkspaceState, variablesManifest, restoreSelectionFromLocalStorage])

  // Restore selectedGroupIds from execution state when page refreshes during execution
  // This handles the case where execution is running but selectedGroupIds was lost on page refresh
  useEffect(() => {
    if (relayMode) return
    if (isExecutionRunning && selectedGroupIds.length === 0 && currentRunningGroupId) {
      // If execution is running but no groups are selected, restore from currentRunningGroupId
      console.log('[WorkflowToolbar] Restoring selectedGroupIds from currentRunningGroupId:', currentRunningGroupId)
      setSelectedGroupIds([currentRunningGroupId])
    } else if (isExecutionRunning && selectedGroupIds.length === 0 && variablesManifest?.groups) {
      // If we have groups in manifest but none selected, try to infer from selectedRunFolder
      // Extract group ID from selectedRunFolder if it's a group path
      if (selectedRunFolder && selectedRunFolder.includes('/')) {
        const parts = selectedRunFolder.split('/')
        if (parts.length === 2) {
          const groupFolderName = parts[1]
          // Try to find matching group in manifest
          const matchingGroup = variablesManifest.groups.find(g => {
            if (g.name === groupFolderName) return true
            const sanitized = groupFolderName.toLowerCase().replace(/[^a-z0-9-]/g, '-').replace(/-+/g, '-').trim()
            const groupSanitized = g.name.toLowerCase().replace(/[^a-z0-9-]/g, '-').replace(/-+/g, '-').trim()
            return sanitized === groupSanitized
          })
          if (matchingGroup) {
            console.log('[WorkflowToolbar] Restoring selectedGroupIds from selectedRunFolder:', matchingGroup.name)
            setSelectedGroupIds([matchingGroup.name])
          }
        }
      }
    }
  }, [relayMode, isExecutionRunning, selectedGroupIds.length, currentRunningGroupId, variablesManifest, selectedRunFolder, setSelectedGroupIds])

  // selectedGroupIds is already included in the batched selector above
  
  // Settings are no longer persisted to localStorage - removed save logic

  const workflowMode = useModeStore(state => state.selectedModeCategory === 'workflow')
  const globalPageOpen = useAppStore(state => state.showWorkflowsOverview || state.showSchedulesOverview || state.adminPage !== null)
  const providersOpen = useLLMStore(state => state.showLLMModal)

  // NOTE: loadRunFolders is NOT called here anymore.
  // useWorkspaceState in WorkflowCanvas handles initial load of:
  // - run_folders (via setRunFolders)
  // - variables_manifest (via setVariablesManifest)
  // This eliminates duplicate API calls on initial page load.

  return (
    <>
    <WorkspaceTopToolbar className={className}>
      {/* Left side - the single persistent Chat. */}
      <div className="flex min-w-0 flex-1 items-center gap-1 overflow-hidden">
        {chatTabsSlot}
      </div>

      {/* Center - Status indicator */}
      <div className="flex shrink-0 items-center gap-1.5">
        {status === 'waiting_feedback' && (
          <div className="flex items-center gap-1.5 px-2 py-1 bg-amber-100 dark:bg-amber-900/30 text-amber-700 dark:text-amber-300 rounded-md text-xs">
            <div className="w-1.5 h-1.5 bg-amber-500 rounded-full animate-pulse" />
            Waiting for feedback
          </div>
        )}
        {status === 'failed' && (
          <div className="flex items-center gap-1.5 px-2 py-1 bg-red-100 dark:bg-red-900/30 text-red-700 dark:text-red-300 rounded-md text-xs">
            <div className="w-1.5 h-1.5 bg-red-500 rounded-full" />
            Failed
          </div>
        )}
      </div>

      {/* Right side - View controls */}
      <div data-tour="workflow-tools" data-testid="tour-workflow-tools" className="ml-auto flex shrink-0 items-center gap-1">
        <TooltipProvider delayDuration={150}>
          {workflowMode && !globalPageOpen && !providersOpen && <GlobalActivityMonitor />}
          {workspacePath && <>
            <WorkspaceContextButton
              inContext={activeWorkspaceView === 'context'}
              onClick={() => useWorkflowStore.getState().openWorkspaceView('context')}
            />
            <WorkflowContextTools relayMode={relayMode} />
          </>}

        </TooltipProvider>
      </div>
    </WorkspaceTopToolbar>
    {/* Access: this workflow's sharing + (admins) the deployment's users */}
    </>
  )
}

export default WorkflowToolbar

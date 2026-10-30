import { workflowProjectMenuItems } from '../workflowProjectMenu'
import { projectMenuHistoryScope, useProjectMenuHistoryStore } from '../../../stores/useProjectMenuHistoryStore'
import { useWorkspaceConnectionStore } from '../../../stores/useWorkspaceConnectionStore'
import { openProductWorkspace } from '../../../utils/productWorkspaceNavigation'
import { useWorkflowManifestStore } from '../../../stores/useWorkflowManifestStore'
import { useCanWriteWorkflow } from '../../../hooks/useCanWriteWorkflow'
import React, { useEffect, useRef } from 'react'
import { useShallow } from 'zustand/react/shallow'
import { useWorkflowStore, type RunFolder } from '../../../stores/useWorkflowStore'
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

import { useAppStore } from '../../../stores/useAppStore'
import { useLLMStore } from '../../../stores/useLLMStore'

// Execution phase ID - special phase that should be displayed separately
const EXECUTION_PHASE_ID = 'execution'
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
  chatTabId?: string
  // Whether Pulse review is enabled for this workflow -- owned by the host
  // (shared with the pane's PulseView), just read here for the badge.
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

  const openWorkspaceView = useWorkflowStore(state => state.openWorkspaceView)
  const isMultiUser = useAuthStore(state => state.isMultiUserMode)
  const incomingEmail = false
  const allowedProducts = useAuthStore(state => state.user?.allowed_products)
  const canWrite = useCanWriteWorkflow(workspacePath)
  const user = useAuthStore(state => state.user)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const projectID = useWorkflowManifestStore(state => state.workflows.find(item => item.workspace_path === workspacePath)?.manifest.id)
  const menuOptions = { relayMode, isMultiUser, incomingEmail, allowedProducts }
  const menuItems = workflowProjectMenuItems(menuOptions)
  const panelSwitcherPanels: PanelSwitcherPanel[] = canWrite
    ? [{ id: 'context', label: 'Project menu' }, ...menuItems.map(item => ({ id: item.id, label: item.label, group: item.group }))]
    : [{ id: 'report', label: 'Dashboard', group: 'Build' }]
  useRegisterPanelSwitcher(workspacePath ? (relayMode ? 'relays' : 'agentworks') : null, panelSwitcherPanels, (key, section) => {
    const current = useAuthStore.getState().user
    if (current?.id !== user?.id || useWorkspaceConnectionStore.getState().activeWorkspaceId !== server) return
    if (key === 'context' && canWrite) { openWorkspaceView('context'); return }
    if (!canWrite) { if (key === 'report') openWorkspaceView('report'); return }
    const id = section ? `${key}:${section}` : key
    const admitted = workflowProjectMenuItems({ ...menuOptions, allowedProducts: current?.allowed_products })
    const item = admitted.find(candidate => candidate.id === id)
      ?? admitted.find(candidate => candidate.view === key && !section)
    if (!item) return
    useProjectMenuHistoryStore.getState().visit(projectMenuHistoryScope(server || 'local', user?.id || 'pending', relayMode ? 'relays' : 'agentworks', projectID || workspacePath || ''), item.id)
    if (item.external) { openProductWorkspace(item.external); return }
    if (item.view) openWorkspaceView(item.view, item.target)
  })
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
      <div data-tour="workflow-tools" data-testid="tour-workflow-tools" className="order-last ml-auto flex shrink-0 items-center gap-1">
        <TooltipProvider delayDuration={150}>
          {workflowMode && !globalPageOpen && !providersOpen && <GlobalActivityMonitor />}

        </TooltipProvider>
      </div>
    </WorkspaceTopToolbar>
    {/* Access: this workflow's sharing + (admins) the deployment's users */}
    </>
  )
}

export default WorkflowToolbar

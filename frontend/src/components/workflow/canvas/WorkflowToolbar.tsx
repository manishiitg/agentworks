import React, { useEffect, useRef, useMemo, useCallback, useState } from 'react'
import { useShallow } from 'zustand/react/shallow'
import {
  Cloud,
  Globe,
  ShieldCheck,
  Activity,
  Hand,
  BellRing,
} from 'lucide-react'
import { useWorkflowStore, type RunFolder } from '../../../stores/useWorkflowStore'
import { PRIMARY_WORKSPACE_TOOLBAR_VIEWS, WORKSPACE_VIEWS, type WorkspaceViewId } from '../workspaceViews'
import { useChatStore } from '../../../stores/useChatStore'
import { useAuthStore } from '../../../stores/useAuthStore'
import type { VariablesManifest } from '../../../services/api-types'
import type { PlanningResponse } from '../../../utils/stepConfigMatching'
import type { WorkflowExecutionStatus } from '../hooks/useWorkflowExecution'
import type { ExecutionOptions } from '../../../services/api-types'
import { agentApi } from '../../../services/api'
import { getBackupDotClass } from '../backupStatus'
import { getPublishDotClass } from '../publishStatus'
import { getNotificationDotClass } from '../notificationStatus'
import { loadWorkflowNotificationInfo, type WorkflowNotificationState } from '../../../services/workflow-notifications'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '../../ui/tooltip'
import { hasWorkflowOwnerAccess } from '../../../utils/workflowPermissions'
import { usePendingDecisionState } from '../hooks/usePendingDecisionCount'
import { useDefaultHumanActionsView } from '../hooks/useDefaultHumanActionsView'
import { useCanWriteWorkflow } from '../../../hooks/useCanWriteWorkflow'
import { GlobalActivityMonitor } from '../../GlobalActivityMonitor'
import { useModeStore } from '../../../stores/useModeStore'
import { WorkspaceTopToolbar } from '../../workspace/WorkspaceTopToolbar'
import { WorkspaceToolbarFrame } from '../../workspace/WorkspaceToolbarFrame'
import { useRegisterPanelSwitcher, type PanelSwitcherPanel } from '../../../stores/usePanelSwitcherStore'
import { WorkspaceToolbarGroup } from '../../workspace/WorkspaceToolbarGroup'
import { ReportDocumentSwitcher } from '../ReportDocumentSwitcher'
import { WorkflowActivityButton } from '../../topbar/WorkflowActivityButton'
import { useAppStore } from '../../../stores/useAppStore'
import { useLLMStore } from '../../../stores/useLLMStore'
import { useBrowserToolbarConnection } from '../../../hooks/useBrowserToolbarConnection'

// Execution phase ID - special phase that should be displayed separately
const EXECUTION_PHASE_ID = 'execution'
const PRIMARY_TOOLBAR_VIEW_IDS = new Set<WorkspaceViewId>(['pulse', 'browser'])
const OPERATIONS_TOOLBAR_VIEW_IDS = new Set<WorkspaceViewId>(['flow', 'workshop', 'knowledge', 'costs', 'execution-logs', 'files', 'backup', 'publish', 'notify'])
const SETUP_TOOLBAR_LABELS: Partial<Record<WorkspaceViewId, string>> = {
  identity: 'Identity',
  playbooks: 'Playbooks',
  mcp: 'Integrations',
}

// Icon button inside an inline Ops/Setup group: same size and tooltip behavior
// as the always-visible primary buttons, with an optional status dot.
function ToolbarInlineItem({ label, Icon, active, onClick, indicatorClass, statusLabel, ...attrs }: {
  label: string
  Icon: React.ComponentType<{ className?: string }>
  active: boolean
  onClick: () => void
  indicatorClass?: string
  statusLabel?: string
} & Record<`data-${string}`, string | undefined>) {
  const button = (
    <button
      type="button"
      onClick={onClick}
      {...attrs}
      className={`flex h-6 w-7 items-center justify-center rounded transition-colors ${active ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:bg-background/70 hover:text-foreground'}`}
      aria-label={label}
      aria-description={statusLabel}
      title={statusLabel ? `${label} · ${statusLabel}` : undefined}
      aria-pressed={active}
    >
      <span className="relative shrink-0">
        <Icon className="h-3.5 w-3.5" />
        {indicatorClass && <span aria-hidden="true" className={`absolute -right-1 -top-1 h-1.5 w-1.5 rounded-full border border-border ${indicatorClass}`} />}
      </span>
    </button>
  )
  if (active) return button
  return (
    <Tooltip>
      <TooltipTrigger asChild>{button}</TooltipTrigger>
      <TooltipContent side="bottom"><p>{label}{statusLabel ? ` · ${statusLabel}` : ''}</p></TooltipContent>
    </Tooltip>
  )
}

// Product-tour / test hooks on specific toolbar buttons. Kept here rather
// than in the view registry because they describe this toolbar's buttons,
// not the views themselves.
// Primary views and setup controls stay expanded. Ops starts collapsed and
// expands inline when the user opens it.

const CAPABILITY_BUTTON_ATTRS: Partial<Record<WorkspaceViewId, { 'data-tour': string; 'data-testid': string }>> = {
  mcp: { 'data-tour': 'bot-connector', 'data-testid': 'tour-bot-connector' },
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
  pythonRelay = false,
  status,
  workspacePath,
  presetQueryId,
  variablesManifest,
  isLoadingWorkspaceState = false,
  chatTabsSlot,
  monitorOn,
  className = ''
}) => {
  const [operationsOpen, setOperationsOpen] = useState(false)
  const { count: pendingDecisionCount, loaded: pendingDecisionsLoaded } = usePendingDecisionState(workspacePath)
  const canWriteWorkflow = useCanWriteWorkflow(workspacePath)
  const browserConnected = useBrowserToolbarConnection(workspacePath, 'workflow', !relayMode, canWriteWorkflow)
  const canManageAccess = useAuthStore(state => state.isMultiUserMode && (state.user?.is_admin === true || hasWorkflowOwnerAccess(state.user, state.isMultiUserMode)))

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
  // Browser stays visible in Views; Plan lives in Ops. A Relay keeps its Graph in Views.
  const workspaceViewDefinitions = PRIMARY_WORKSPACE_TOOLBAR_VIEWS.filter(view => (relayMode ? view.id === 'flow' : PRIMARY_TOOLBAR_VIEW_IDS.has(view.id)) && view.id !== 'report')
  const operationsWorkspaceViewDefinitions = PRIMARY_WORKSPACE_TOOLBAR_VIEWS.filter(view => OPERATIONS_TOOLBAR_VIEW_IDS.has(view.id) && (!relayMode || ['workshop', 'costs', 'execution-logs', 'files'].includes(view.id)))
  const capabilityViewDefinitions = useMemo(
    () => WORKSPACE_VIEWS.filter(view => view.toolbarGroup === 'capabilities' && (!relayMode || view.id === 'mcp' || view.id === 'identity')),
    [relayMode],
  )

  // Backup/publish/notify status dots -- lightweight polls independent of
  // whether the pane is showing that view.
  const [backupState, setBackupState] = useState<string>('loading')
  const [publishState, setPublishState] = useState<string>('not_configured')
  const [notificationState, setNotificationState] = useState<WorkflowNotificationState | 'loading'>('loading')
  // Share is for this workflow's owners (or an admin), multi-user mode only.
  const isMultiUser = useAuthStore(state => state.isMultiUserMode)
  // ⌘/Ctrl+J searches the same panels this toolbar shows (owner, 2026-10-07).
  const panelSwitcherPanels = useMemo<PanelSwitcherPanel[]>(() => [
    ...(relayMode ? [] : [{ id: 'report', label: 'Dashboard', group: 'Views' }, { id: 'pulse', label: 'Pulse', group: 'Views' }, { id: 'human-actions', label: 'Human actions', group: 'Views' }]),
    ...workspaceViewDefinitions.map(view => ({ id: view.id, label: relayMode && view.id === 'flow' ? 'Graph' : view.label, group: 'Views' })),
    ...operationsWorkspaceViewDefinitions.map(view => ({ id: view.id, label: relayMode && view.id === 'workshop' ? 'Triggers' : view.label, group: 'Ops' })),
    ...(relayMode ? [] : [{ id: 'backup', label: 'Backup', group: 'Ops' }, { id: 'publish', label: 'Publish', group: 'Ops' }, { id: 'notify', label: 'Notifications', group: 'Ops' }]),
    ...capabilityViewDefinitions.map(view => ({ id: view.id, label: SETUP_TOOLBAR_LABELS[view.id] ?? view.label, group: 'Setup' })),
    ...(isMultiUser ? [{ id: 'access', label: 'Access', group: 'Setup' }] : []),
  ], [relayMode, workspaceViewDefinitions, operationsWorkspaceViewDefinitions, capabilityViewDefinitions, isMultiUser])
  useRegisterPanelSwitcher(workspacePath ? (relayMode ? 'relays' : 'agentworks') : null, panelSwitcherPanels, id => openWorkspaceView(id as WorkspaceViewId))
  // Lightweight backup-status poll so the toolbar dot reflects health at a glance.
  const refreshBackupState = useCallback(async () => {
    if (!workspacePath) {
      setBackupState('not_configured')
      return
    }
    try {
      const resp = await agentApi.getWorkflowBackup(workspacePath)
      setBackupState(resp.effective_state || 'not_configured')
    } catch {
      // Leave the last known state; a transient fetch failure shouldn't flip the dot.
    }
  }, [workspacePath])

  useEffect(() => {
    setBackupState('loading')
    void refreshBackupState()
  }, [refreshBackupState])

  const refreshPublishState = useCallback(async () => {
    if (!workspacePath) {
      setPublishState('not_configured')
      return
    }
    try {
      const resp = await agentApi.getWorkflowPublish(workspacePath)
      setPublishState(resp.effective_state || 'not_configured')
    } catch {
      // Leave the last known state.
    }
  }, [workspacePath])

  useEffect(() => {
    refreshPublishState()
  }, [refreshPublishState])

  const refreshNotificationState = useCallback(async () => {
    if (!workspacePath) {
      setNotificationState('not_configured')
      return
    }
    try {
      const info = await loadWorkflowNotificationInfo(workspacePath)
      setNotificationState(info.effectiveState)
    } catch {
      setNotificationState('not_configured')
    }
  }, [workspacePath])

  useEffect(() => {
    void refreshNotificationState()
  }, [refreshNotificationState])

  // Backup/publish/notify each load richer status than this toolbar's own
  // lightweight poll while their pane view is open; catch the dot up once the
  // user navigates away, rather than leaving it stale until workspacePath
  // next changes.
  const prevWorkspaceViewRef = useRef(workflowWorkspaceView)
  useEffect(() => {
    const prev = prevWorkspaceViewRef.current
    if (prev !== workflowWorkspaceView) {
      if (prev === 'backup') void refreshBackupState()
      if (prev === 'publish') void refreshPublishState()
      if (prev === 'notify') void refreshNotificationState()
    }
    prevWorkspaceViewRef.current = workflowWorkspaceView
  }, [workflowWorkspaceView, refreshBackupState, refreshPublishState, refreshNotificationState])

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

  useDefaultHumanActionsView(workspacePath, pendingDecisionsLoaded, pendingDecisionCount)

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
          {/* Report stays visible beside the expanded tool groups. */}
          {workspacePath && !relayMode && <ReportDocumentSwitcher workspacePath={workspacePath} active={activeWorkspaceView === 'report'} onOpen={() => openWorkspaceView('report')} />}

          {/* One continuous pill: frequent views | operations | setup icons. */}
          {(workspacePath || canWriteWorkflow) && (
          <WorkspaceToolbarFrame>
          {/* Setup and primary views stay open; Ops expands on demand. */}
          {workspacePath && (
            <WorkspaceToolbarGroup
              label="Views"
              data-tour="workflow-views"
              hideLabel
              hideToggleWhenOpen
              open
              title={relayMode ? `Relay views: ${pythonRelay ? 'Source and calls' : 'Graph'}` : 'Views: Pulse, Needs you, Activity and Browser'}
            >
              <div className="inline-flex items-center gap-0.5 px-0.5">
                {!relayMode && <Tooltip>
                  <TooltipTrigger asChild>
                    <button
                      type="button"
                      onClick={() => openWorkspaceView('pulse')}
                      className={`relative flex h-6 w-7 items-center justify-center rounded transition-colors ${activeWorkspaceView === 'pulse' ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:bg-background/70 hover:text-foreground'}`}
                      aria-label="Pulse"
                      aria-pressed={activeWorkspaceView === 'pulse'}
                    >
                      <Activity aria-hidden="true" className={`h-3.5 w-3.5 ${monitorOn ? 'text-primary' : ''}`} />
                    </button>
                  </TooltipTrigger>
                  <TooltipContent side="bottom"><p>Pulse</p></TooltipContent>
                </Tooltip>}
                {!relayMode && <Tooltip>
                  <TooltipTrigger asChild>
                    <button
                      type="button"
                      onClick={() => openWorkspaceView('human-actions')}
                      className={`relative flex h-6 w-7 items-center justify-center rounded transition-colors ${activeWorkspaceView === 'human-actions' ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:bg-background/70 hover:text-foreground'}`}
                      aria-label={pendingDecisionCount > 0 ? `Human actions, ${pendingDecisionCount} pending` : 'Human actions'}
                      aria-pressed={activeWorkspaceView === 'human-actions'}
                    >
                      <Hand aria-hidden="true" className={`h-3.5 w-3.5 ${pendingDecisionCount > 0 ? 'text-amber-500' : ''}`} />
                      {pendingDecisionCount > 0 && <span className="absolute -right-0.5 -top-0.5 flex h-3 min-w-3 items-center justify-center rounded-full bg-amber-500 px-0.5 text-[8px] font-bold leading-none text-background">{pendingDecisionCount > 9 ? '9+' : pendingDecisionCount}</span>}
                    </button>
                  </TooltipTrigger>
                  <TooltipContent side="bottom"><p>{pendingDecisionCount > 0 ? `${pendingDecisionCount} human ${pendingDecisionCount === 1 ? 'action' : 'actions'} pending` : 'Human actions'}</p></TooltipContent>
                </Tooltip>}
                {!relayMode && <WorkflowActivityButton
                  workspacePath={workspacePath}
                  onOpen={() => {
                    useLLMStore.getState().setShowLLMModal(false)
                    const app = useAppStore.getState()
                    app.setShowSchedulesOverview(false)
                    app.setActivityWorkflowPath(workspacePath)
                    app.setShowWorkflowsOverview(true)
                  }}
                />}
                {workspaceViewDefinitions.map(({ id: view, icon: Icon, label }) => (
                  <ToolbarInlineItem key={view} label={relayMode && view === 'flow' ? (pythonRelay ? 'Relay' : 'Graph') : label} Icon={Icon} indicatorClass={view === 'browser' && browserConnected ? 'bg-emerald-500' : undefined} statusLabel={view === 'browser' ? browserConnected ? 'Connected' : 'Not connected' : undefined} active={view === activeWorkspaceView} onClick={() => openWorkspaceView(view)} />
                ))}
              </div>
            </WorkspaceToolbarGroup>
          )}

        {/* Operational views keep status and history separate from setup. */}
        {workspacePath && (
          <WorkspaceToolbarGroup
            label="Ops"
            data-tour="workflow-operations"
            open={operationsOpen}
            onToggle={() => setOperationsOpen(open => !open)}
            active={operationsWorkspaceViewDefinitions.some(view => view.id === activeWorkspaceView) || (!relayMode && ['backup', 'publish', 'notify'].includes(activeWorkspaceView))}
            title={relayMode ? 'Relay operations: triggers, costs, execution logs and files' : 'Operations: plan, automation, knowledge, costs, execution logs, files, backup, publish and notifications'}
          >
            <div className="inline-flex items-center gap-0.5">
              {operationsWorkspaceViewDefinitions.map(({ id: view, icon: Icon, label }) => (
                <ToolbarInlineItem key={view} label={relayMode && view === 'workshop' ? 'Triggers' : label} Icon={Icon} active={view === activeWorkspaceView} onClick={() => openWorkspaceView(view)} />
              ))}
              {!relayMode && <ToolbarInlineItem label="Backup" Icon={Cloud} active={activeWorkspaceView === 'backup'} onClick={() => openWorkspaceView('backup')} indicatorClass={getBackupDotClass(backupState)} />}
              {!relayMode && <ToolbarInlineItem label="Publish" Icon={Globe} active={activeWorkspaceView === 'publish'} onClick={() => openWorkspaceView('publish')} indicatorClass={getPublishDotClass(publishState)} />}
              {!relayMode && <ToolbarInlineItem label="Notifications" Icon={BellRing} active={activeWorkspaceView === 'notify'} onClick={() => openWorkspaceView('notify')} indicatorClass={getNotificationDotClass(notificationState)} data-testid="workflow-notification-settings-button" />}
            </div>
          </WorkspaceToolbarGroup>
        )}

        {/* Workflow capabilities remain discoverable for readers. Each panel
            owns its read-only state and disables mutations in place. Setup
            has no toggle: it stays expanded next to Views/Ops. */}
        {workspacePath && (
          <WorkspaceToolbarGroup
            label="Setup"
            data-tour="workflow-setup"
            hideToggleWhenOpen
            open
            title={relayMode ? 'Relay setup: identity, models, and integrations' : 'Setup: identity, integrations, playbooks and access'}
          >
            <div className="inline-flex items-center gap-0.5">
              {capabilityViewDefinitions.map(({ id, icon: Icon, label }) => {
                const active = workflowWorkspaceView === id
                return <ToolbarInlineItem key={id} label={SETUP_TOOLBAR_LABELS[id] ?? label} Icon={Icon} active={active} onClick={() => openWorkspaceView(id)} {...CAPABILITY_BUTTON_ATTRS[id]} />
              })}
              {/* Access lives with the rest of setup: one button, one panel
                  with two tabs -- who may see or edit THIS workflow, and (for
                  admins) the deployment's accounts, roles and products. */}
              {isMultiUser && workspacePath && (
                <ToolbarInlineItem label={canManageAccess ? 'Access and users' : 'Access'} Icon={ShieldCheck} active={workflowWorkspaceView === 'access'} onClick={() => openWorkspaceView('access')} />
              )}
            </div>
          </WorkspaceToolbarGroup>
        )}
          </WorkspaceToolbarFrame>
          )}

        </TooltipProvider>
      </div>
    </WorkspaceTopToolbar>
    {/* Access: this workflow's sharing + (admins) the deployment's users */}
    </>
  )
}

export default WorkflowToolbar

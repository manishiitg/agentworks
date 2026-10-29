import React, { useState, useEffect, useCallback } from 'react'
import { workflowTriggerLabel } from '../utils/workflowSessionKinds'
import { useShallow } from 'zustand/react/shallow'
import { Settings, Copy, Eye, CalendarClock } from 'lucide-react'
import { useAuthStore } from '../stores/useAuthStore'
import { useProductCreateRequest } from '../hooks/useProductCreateRequest'
import { hasProductCreateAccess, isWorkflowReadOnly } from '../utils/workflowPermissions'
import { useModeStore } from '../stores/useModeStore'
import { useGlobalPresetStore, usePresetApplication, usePresetManagement } from '../stores/useGlobalPresetStore'
import type { CustomPreset, PredefinedPreset } from '../types/preset'
import type { PlannerFile, PresetLLMConfig, WorkflowManifest } from '../services/api-types'
import PresetModal from './PresetModal'
import { agentApi, workflowManifestApi } from '../services/api'
import { Tooltip, TooltipContent, TooltipTrigger } from './ui/tooltip'
import ModalPortal from './ui/ModalPortal'
import { useChatStore, useLLMStore } from '../stores'
import { useMCPStore } from '../stores/useMCPStore'
import { useAppStore } from '../stores/useAppStore'
import { useCommandDialogStore } from '../stores/useCommandDialogStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useWorkflowManifestStore } from '../stores/useWorkflowManifestStore'
import { dedupeServerNames } from '../utils/mcpServerAlias'
import { GlobalActivityMonitor } from './GlobalActivityMonitor'
import { useGlobalSchedulerPaused } from '../hooks/useGlobalSchedulerPaused'
import { ProductTopBar, ProductTopBarMain } from './workspace/ProductTopBar'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import { TopBarEntitySelector } from './topbar/TopBarEntitySelector'
import { WorkflowIcon } from './workflow/WorkflowIcon'
import ConfirmationDialog from './ui/ConfirmationDialog'
import { openWorkflowPresetPage } from '../utils/workflowSessionRestore'
import { openAppProduct } from '../utils/appNavigation'
import { captureChatIdentity, isChatIdentityCurrent } from '../utils/chatIdentity'
import { currentActiveSession, currentSessionId, headerStatusLabel } from '../utils/globalActivityMonitorStatus'

const workflowManifestToPreset = (manifest: WorkflowManifest, workspacePath: string): CustomPreset => {
  const caps = manifest.capabilities
  return {
    id: manifest.id || workspacePath,
    label: manifest.label || workspacePath.split('/').pop() || workspacePath,
    icon: manifest.icon,
    createdAt: new Date(manifest.created_at || 0).getTime(),
    agentMode: 'workflow',
    selectedFolder: {
      filepath: workspacePath,
      content: '',
      last_modified: manifest.updated_at || '',
      type: 'folder',
      children: [],
    },
    selectedServers: caps?.selected_servers || [],
    selectedTools: caps?.selected_tools || [],
    selectedSkills: caps?.selected_skills || [],
    selectedSecrets: caps?.selected_secrets || [],
    selectedGlobalSecretNames: caps?.selected_global_secret_names ?? [],
    browserMode: caps?.browser_mode,
    useCodeExecutionMode: caps?.use_code_execution_mode || false,
    llmConfig: caps?.llm_config ? { ...caps.llm_config } : undefined,
    employee_id: manifest.ownership?.employee_id ?? undefined,
    workflowKind: manifest.kind === 'relay' ? 'relay' : 'workflow',
  }
}

/**
 * Global Mode & Preset Bar - always visible at the top level
 * Allows users to select mode (multi-agent/workflow) and presets regardless of active tabs
 */
interface ModePresetBarProps {
  /** Product-owned control rendered in the same slot as AgentWorks' automation selector. */
  productControl?: React.ReactNode
  /** Keep the AgentWorks bar and shared controls while omitting automation-only actions. */
  reduced?: boolean
  /** An open product workspace renders the shared monitor beside its tools. */
  activityMonitorInWorkspace?: boolean
}

export const ModePresetBar: React.FC<ModePresetBarProps> = ({ productControl, reduced = false, activityMonitorInWorkspace = false }) => {
  const productSurface = useProductSurfaceStore(state => state.productSurface)
  const isRelaySurface = productSurface === 'relays'
  const hasProductSchedules = ['agentworks', 'work'].includes(productSurface)
  const { selectedModeCategory, setModeCategory, getAgentModeFromCategory } = useModeStore(useShallow(state => ({
    selectedModeCategory: state.selectedModeCategory,
    setModeCategory: state.setModeCategory,
    getAgentModeFromCategory: state.getAgentModeFromCategory,
  })))
  const presetModeCategory = selectedModeCategory === 'workflow' || selectedModeCategory === 'multi-agent'
    ? selectedModeCategory
    : null
  const { setWorkspaceMinimized, agentMode } = useAppStore(useShallow(state => ({
    setWorkspaceMinimized: state.setWorkspaceMinimized,
    agentMode: state.agentMode,
  })))
  const isReadOnlyUser = useAuthStore(state => isWorkflowReadOnly(state.user, state.isMultiUserMode))
  // Workflows and Flows each have their own create permission (PLAT-767).
  const canCreateWorkflows = useAuthStore(state => hasProductCreateAccess(state.user, state.isMultiUserMode, isRelaySurface ? 'relays' : 'agentworks'))
  const schedulerPaused = useGlobalSchedulerPaused(productSurface)
  // Use toolList to get all available servers, not just enabled ones
  const toolList = useMCPStore(state => state.toolList)
  const availableServers = React.useMemo(() =>
    [...new Set(toolList.map(t => t.server).filter(Boolean) as string[])],
    [toolList]
  )

  // Use the new global preset store
  const {
    workflowPresets,
    savePreset,
    duplicatePreset,
    refreshPresets,
    loading: presetsLoading,
    workflowPresetsLoaded,
  } = usePresetManagement()

  const {
    applyPreset,
    getActivePreset,
    isPresetActive,
    getPresetsForMode,
    clearActivePreset,
  } = usePresetApplication()

  // Get active preset for current mode (for schedule popup, supports all modes)
  const selectedPreset = presetModeCategory === null
    ? null
    : getActivePreset(presetModeCategory)
  const activePreset = selectedPreset && (selectedPreset.workflowKind === 'relay') === isRelaySurface ? selectedPreset : null
  const activeWorkspacePath = activePreset?.selectedFolder?.filepath?.replace(/\/+$/, '')
  const activeWorkflowAccess = useWorkflowManifestStore(state =>
    activeWorkspacePath ? state.workflows.find(workflow => workflow.workspace_path === activeWorkspacePath)?.my_access : undefined,
  )
  // A member may create their own workflows while being a reader on this
  // particular workflow. Use the manifest-level answer for this header, not
  // only the account-wide role.
  const isActiveWorkflowReadOnly = selectedModeCategory === 'workflow' && activeWorkflowAccess === 'read'
  const isEffectiveReadOnly = isReadOnlyUser || isActiveWorkflowReadOnly
  // Get presets for current mode
  const allPresetsForMode = presetModeCategory === null
    ? []
    : getPresetsForMode(presetModeCategory)
  const presetsForMode = selectedModeCategory === 'workflow'
    ? allPresetsForMode.filter(preset => (preset.workflowKind === 'relay') === isRelaySurface)
    : allPresetsForMode

  const [showPresetDropdown, setShowPresetDropdown] = useState(false)
  const [showPresetModal, setShowPresetModal] = useState(false)
  const [editingPreset, setEditingPreset] = useState<CustomPreset | null>(null)
  const [showShortcuts, setShowShortcuts] = useState(false)
  const [pendingDuplicatePreset, setPendingDuplicatePreset] = useState<{ id: string; label: string } | null>(null)
  const [duplicatingPreset, setDuplicatingPreset] = useState(false)
  const showWorkflowsOverview = useAppStore(s => s.showWorkflowsOverview)
  const setShowWorkflowsOverview = useAppStore(s => s.setShowWorkflowsOverview)
  const showSchedulesOverview = useAppStore(s => s.showSchedulesOverview)
  const setShowSchedulesOverview = useAppStore(s => s.setShowSchedulesOverview)
  const adminPage = useAppStore(s => s.adminPage)
  const setAdminPage = useAppStore(s => s.setAdminPage)
  const setSelectedFile = useWorkspaceStore(state => state.setSelectedFile)
  const setShowFileContent = useWorkspaceStore(state => state.setShowFileContent)
  const showProviders = useLLMStore(state => state.showLLMModal)
  const isGlobalPage = showWorkflowsOverview || showProviders || showSchedulesOverview || adminPage !== null
  // GlobalActivityMonitor excludes only the current session from its pills.
  // A simultaneous scheduled run for the same workflow remains a separate
  // activity, while this selector gives the current session its own live
  // running/needs-input/idle status.
  // This mirrors GlobalActivityMonitor's own currentSessionId derivation so
  // the two stay in agreement about which session is "current".
  // Select the two rendered labels, not the raw session/tab records: those
  // change identity on every 5s poll and every chat event, which re-rendered
  // the whole bar (and made header clicks feel laggy) far more often than
  // the labels themselves change.
  const { currentTriggerLabel, currentSessionStatusLabel } = useChatStore(useShallow(state => {
    const currentSession = currentActiveSession(
      state.activeSessionsCache,
      currentSessionId(state.activeTabId, state.chatTabs, selectedModeCategory, isGlobalPage),
    )
    return {
      currentTriggerLabel: currentSession ? workflowTriggerLabel({ sessionId: currentSession.session_id, triggeredBy: currentSession.triggered_by }) : undefined,
      currentSessionStatusLabel: currentSession ? headerStatusLabel(currentSession) : null,
    }
  }))

  // Handle ESC and Enter keys for shortcuts modal
  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (showShortcuts) {
        if (event.key === 'Escape' || event.key === 'Enter') {
          event.preventDefault()
          setShowShortcuts(false)
        }
      }
    }

    if (showShortcuts) {
      window.addEventListener('keydown', handleKeyDown)
      return () => window.removeEventListener('keydown', handleKeyDown)
    }
  }, [showShortcuts])

  const handleEditWorkflowPreset = useCallback(async (preset: CustomPreset) => {
    const workspacePath = preset.selectedFolder?.filepath
    if (!workspacePath) {
      setEditingPreset(preset)
      setShowPresetModal(true)
      return
    }

    try {
      const response = await workflowManifestApi.getWorkflowManifest(workspacePath)
      setEditingPreset(workflowManifestToPreset(response.manifest, response.workspace_path || workspacePath))
    } catch (error) {
      console.error('[ModePresetBar] Failed to load latest workflow manifest before edit:', error)
      setEditingPreset(preset)
    }

    setShowPresetModal(true)
  }, [])

  // Keep the create action discoverable beside the automation selector. It is
  // intentionally rendered for accounts without create access too, but the
  // account-level create permission keeps the action disabled rather than
  // opening a form that the server can never save.
  const handleAddWorkflow = useCallback(() => {
    if (!canCreateWorkflows) {
      useChatStore.getState().addToast(
        'Your account can use existing automations but cannot create new ones. Ask an administrator to enable automation creation.',
        'info',
      )
      return
    }
    setEditingPreset(null)
    setShowPresetDropdown(false)
    setShowPresetModal(true)
    setWorkspaceMinimized(true)
  }, [canCreateWorkflows, setWorkspaceMinimized])

  useProductCreateRequest(
    !reduced && presetModeCategory === 'workflow' && (productSurface === 'agentworks' || productSurface === 'relays') ? productSurface : null,
    handleAddWorkflow,
  )

  // Intro pages reuse the same creation dialog and permission check as the plus button.
  const showPresetCreate = useCommandDialogStore(s => s.showPresetCreate)
  useEffect(() => {
    if (!showPresetCreate || presetModeCategory !== 'workflow' || reduced) return
    useCommandDialogStore.getState().closeDialog('presetCreate')
    handleAddWorkflow()
  }, [showPresetCreate, presetModeCategory, reduced, handleAddWorkflow])

  // Listen for external trigger to open preset settings (e.g. from workflow toolbar)
  const showPresetSettings = useCommandDialogStore(s => s.showPresetSettings)
  useEffect(() => {
    if (showPresetSettings) {
      useCommandDialogStore.getState().closeDialog('presetSettings')
      const preset = presetModeCategory === null
        ? null
        : getActivePreset(presetModeCategory)
      if (preset) {
        handleEditWorkflowPreset(preset as CustomPreset)
      }
    }
  }, [showPresetSettings, presetModeCategory, getActivePreset, handleEditWorkflowPreset])

  // Preset click handler - now uses the global store
  const handlePresetClick = useCallback((preset: CustomPreset | PredefinedPreset) => {
    // Determine the mode category based on the preset's agentMode
    const presetModeCategory = preset.agentMode === 'workflow' ? 'workflow' : 'multi-agent'

    if (presetModeCategory === 'workflow') {
      setShowPresetDropdown(false)
      void openWorkflowPresetPage(preset, {
        title: preset.label,
        source: 'workflow-dropdown',
      }).catch(error => {
        console.error('Failed to open automation:', error)
      })
      return
    }

    // If preset is for simple mode, route to multi-agent category
    if (presetModeCategory === 'multi-agent' && selectedModeCategory !== 'multi-agent') {
      setModeCategory('multi-agent')
    }

    // Apply the preset with the correct mode category
    const result = applyPreset(preset, presetModeCategory)

    if (result.success) {
      setShowPresetDropdown(false)
    } else {
      console.error('Failed to apply preset:', result.error)
    }
  }, [applyPreset, selectedModeCategory, setModeCategory])

  // Memoized callbacks for PresetModal
  const handleClosePresetModal = useCallback(() => {
    setShowPresetModal(false)
    setEditingPreset(null)
  }, [])

  const handleSavePreset = useCallback(async (
    label: string,
    query: string,
    selectedServers?: string[],
    selectedTools?: string[],
    selectedSkills?: string[], // Skill folder names for workflow
    agentMode?: 'multi-agent' | 'workflow',
    selectedFolder?: PlannerFile,
    llmConfig?: PresetLLMConfig,
    useCodeExecutionMode?: boolean,
    selectedSecrets?: string[],
    selectedGlobalSecretNames?: string[] | null,
    browserMode?: 'none' | 'extension' | 'headless' | 'cdp',
    icon?: string,
    workflowKind?: 'relay' | 'workflow'
  ) => {
    const startedAtHome = window.location.pathname === '/home'
    const identity = captureChatIdentity()
    try {
      const effectiveMode = editingPreset ? editingPreset.agentMode : agentMode
      const globalSecretNamesForBackend = selectedGlobalSecretNames === undefined
        ? (editingPreset?.selectedGlobalSecretNames === undefined ? [] : editingPreset.selectedGlobalSecretNames)
        : selectedGlobalSecretNames
      // PLAT-169: collapse any hyphen/underscore-alias duplicate before this
      // ever reaches the backend. ToolSelectionSection.tsx no longer creates
      // new ones, but a manifest saved before that fix (or edited outside
      // the UI) can still carry both spellings — this self-heals it on the
      // very next successful save instead of permanently failing the
      // backend's duplicate validation until someone hand-edits the JSON.
      const dedupedSelectedServers = selectedServers ? dedupeServerNames(selectedServers) : selectedServers

      // Workflow mode: save to file-backed manifest (not DB)
      if (effectiveMode === 'workflow' && editingPreset?.selectedFolder?.filepath) {
        const workspacePath = editingPreset.selectedFolder.filepath
        const payload = {
          workspace_path: workspacePath,
          label,
          icon: icon?.trim() || '',
          capabilities: {
            selected_servers: dedupedSelectedServers || [],
            selected_tools: selectedTools || [],
            selected_skills: selectedSkills || [],
            selected_secrets: selectedSecrets || [],
            selected_global_secret_names: globalSecretNamesForBackend,
            browser_mode: browserMode,
            use_code_execution_mode: useCodeExecutionMode || false,
            llm_config: llmConfig || undefined,
          },
        }
        await agentApi.updateWorkflowManifest(payload)
        // Refresh manifests and rebuild workflow presets in zustand (triggers re-renders)
        await refreshPresets()
        return true
      }

      // Multi-agent mode: save the user's chat capability profile.
      const savedPreset = await savePreset(
        label,
        query,
        dedupedSelectedServers,
        selectedTools,
        selectedSkills,
        effectiveMode,
        selectedFolder,
        llmConfig,
        useCodeExecutionMode,
        editingPreset?.id,
        selectedSecrets,
        selectedGlobalSecretNames,
        browserMode,
        icon,
        workflowKind
      )

      // Apply the preset immediately if it's a new one
      if (savedPreset && !editingPreset) {
        handlePresetClick(savedPreset)
        if (startedAtHome && effectiveMode === 'workflow' && window.location.pathname === '/home' && isChatIdentityCurrent(identity)) openAppProduct({ surface: workflowKind === 'relay' ? 'relays' : 'agentworks', id: savedPreset.id })
      }

      setShowPresetModal(false)
      setEditingPreset(null)
      return true
    } catch (error) {
      console.error('[ModePresetBar] Failed to save preset:', error)
      // Surface the failure — previously this was swallowed (no toast), so a
      // rejected manifest save looked like a silent no-op. The server returns
      // the validation reason as the response body.
      const response = (error as { response?: { status?: number; data?: unknown } })?.response
      if (response?.status === 403) {
        const action = editingPreset ? 'edit this automation' : 'create new automations'
        useChatStore.getState().addToast(
          `You don’t have permission to ${action}. Ask an administrator to update your automation access.`,
          'error',
        )
        return false
      }
      const serverDetail = response?.data
      const detail =
        typeof serverDetail === 'string' && serverDetail.trim() !== ''
          ? serverDetail.trim()
          : typeof serverDetail === 'object' && serverDetail !== null && 'error' in serverDetail && typeof serverDetail.error === 'string'
            ? serverDetail.error
          : error instanceof Error
            ? error.message
            : 'Unknown error'
      useChatStore.getState().addToast(`Failed to save configuration: ${detail}`, 'error')
      return false
    }
  }, [editingPreset, savePreset, handlePresetClick, refreshPresets])

  const handleDeleteWorkflow = useCallback(async (preset: CustomPreset) => {
    const workspacePath = preset.selectedFolder?.filepath
    if (!workspacePath) {
      throw new Error('Automation folder is missing')
    }

    try {
      setShowPresetModal(false)
      setEditingPreset(null)
      setShowPresetDropdown(false)
      await useGlobalPresetStore.getState().deleteWorkflow(workspacePath)

      // A user may have switched workflows while deletion was in flight.
      if (!useGlobalPresetStore.getState().activePresetIds.workflow) {
        setSelectedFile(null)
        setShowFileContent(false)
      }
    } catch (error) {
      console.error('[ModePresetBar] Failed to delete workflow:', error)
      useChatStore.getState().addToast('Failed to delete automation. Please try again.', 'error')
      throw error
    }
  }, [setSelectedFile, setShowFileContent])

  const requestDuplicatePreset = useCallback((preset: CustomPreset | PredefinedPreset, e: React.MouseEvent) => {
    e.stopPropagation()
    setShowPresetDropdown(false)
    setPendingDuplicatePreset({ id: preset.id, label: preset.label })
  }, [])

  const handleDuplicatePresetConfirm = useCallback(async () => {
    if (!pendingDuplicatePreset || duplicatingPreset) return

    setDuplicatingPreset(true)
    try {
      const duplicatedPreset = await duplicatePreset(pendingDuplicatePreset.id)
      if (duplicatedPreset) {
        setPendingDuplicatePreset(null)
        handlePresetClick(duplicatedPreset)
      }
    } catch (error) {
      console.error('Failed to duplicate preset:', error)
      useChatStore.getState().addToast('Failed to duplicate automation. Please try again.', 'error')
    } finally {
      setDuplicatingPreset(false)
    }
  }, [duplicatePreset, duplicatingPreset, handlePresetClick, pendingDuplicatePreset])

  // An empty automation list is a valid loaded state. Checking its length here
  // retriggers refresh after every empty response and blocks the UI thread.
  useEffect(() => {
    if (selectedModeCategory === 'workflow' && !workflowPresetsLoaded && !presetsLoading) {
      refreshPresets().catch(error => {
        console.error('[ModePresetBar] Failed to refresh presets:', error)
      })
    }
  }, [selectedModeCategory, workflowPresetsLoaded, presetsLoading, refreshPresets])

  // Refresh presets when dropdown is opened for workflow mode
  const handlePresetDropdownToggle = useCallback(() => {
    const newState = !showPresetDropdown
    setShowPresetDropdown(newState)

    // If opening dropdown and in workflow mode, ensure presets are refreshed
    if (newState && selectedModeCategory === 'workflow') {
      const currentPresets = getPresetsForMode('workflow')

      // Always refresh when opening dropdown in workflow mode to ensure latest presets
      if (currentPresets.length === 0 && !presetsLoading) {
        refreshPresets().catch(error => {
          console.error('[ModePresetBar] Failed to refresh presets when opening dropdown:', error)
        })
      }
    }
  }, [showPresetDropdown, selectedModeCategory, presetsLoading, refreshPresets, getPresetsForMode])


  return (
    <>
      <ProductTopBar>
          {/* Product and current automation */}
          <ProductTopBarMain>
            {/* Product-level navigation stays separate from AgentWorks modes. */}
            {!activityMonitorInWorkspace && (reduced || isGlobalPage || selectedModeCategory !== 'workflow' || !activePreset) && <GlobalActivityMonitor />}

            {productControl}



            {isEffectiveReadOnly && (
              <Tooltip>
                <TooltipTrigger asChild>
                  <div className="flex shrink-0 items-center gap-1 rounded-md border border-amber-200 bg-amber-50 px-2 py-1 text-xs font-medium text-amber-700 dark:border-amber-800 dark:bg-amber-900/30 dark:text-amber-300">
                    <Eye className="w-3 h-3" />
                    <span className="sr-only">Run</span>
                  </div>
                </TooltipTrigger>
                <TooltipContent side="bottom">
                  {isActiveWorkflowReadOnly
                    ? 'You can run this and watch its runs; only the owner changes its plans, secrets, schedules, or config.'
                    : "You can run this and watch its runs; only the owner changes its plans, secrets, schedules, or config."}
                </TooltipContent>
              </Tooltip>
            )}

            {/* Center: Preset Information */}
            <div className="flex min-w-0 items-center gap-3">
              {/* Preset Information - Show ONLY for workflow mode */}
              {(() => {
                // For workflow mode only, always show preset selector
                // Chat mode no longer supports presets
                if (selectedModeCategory === 'workflow' && (productSurface === 'agentworks' || productSurface === 'relays')) {
                  return (
                    <TopBarEntitySelector
                      label={activePreset?.label}
                      leading={activePreset ? <WorkflowIcon icon={activePreset.icon} label={activePreset.label} /> : undefined}
                      compactOnNarrow
                      placeholder={isRelaySurface ? 'Select Flow' : 'Select Automation'}
                      title={activePreset?.label
                        ? (currentSessionStatusLabel ? `${activePreset.label} · ${currentSessionStatusLabel}` : activePreset.label)
                        : isRelaySurface ? 'Select Flow' : 'Select Automation'}
                      open={showPresetDropdown}
                      onToggle={handlePresetDropdownToggle}
                      onClose={() => setShowPresetDropdown(false)}
                      onAdd={handleAddWorkflow}
                      addLabel={isRelaySurface ? 'Add Flow' : 'Add automation'}
                      addTitle={!canCreateWorkflows ? 'Your account cannot create workflows. Ask an administrator to enable creation.' : isRelaySurface ? 'Add Flow' : 'Add automation'}
                      addDisabled={!canCreateWorkflows}
                      addTestId="add-workflow-button"
                      dataTour="workflow-add-edit"
                      testId="tour-workflow-add-edit"
                      badge={currentTriggerLabel && <span className="rounded border border-border px-1 text-[10px] text-muted-foreground">{currentTriggerLabel}</span>}
                    >
                          <div className="p-2 space-y-1 max-h-96 overflow-y-auto">
                            {/* Add New Workflow Option */}
                            <button
                              onClick={handleAddWorkflow}
                              disabled={!canCreateWorkflows}
                              title={!canCreateWorkflows ? 'Ask an administrator to enable automation creation for your account.' : undefined}
                              className="w-full rounded-md p-2 text-left text-sm text-gray-700 hover:bg-gray-100 disabled:cursor-not-allowed disabled:opacity-50 dark:text-gray-300 dark:hover:bg-slate-700"
                            >
                              <div className="flex items-center gap-2">
                                <div className="w-2 h-2 bg-blue-500 rounded-full"></div>
                                <span className="font-medium">+ Add {isRelaySurface ? 'Flow' : 'Automation'}</span>
                              </div>
                            </button>

                            {!canCreateWorkflows && (
                              <div className="rounded-md bg-amber-50 px-2.5 py-2 text-xs leading-5 text-amber-800 dark:bg-amber-950/40 dark:text-amber-200">
                                Your account can use existing automations, but it cannot create new ones. Ask an administrator to enable automation creation.
                              </div>
                            )}

                            {/* Loading state */}
                            {presetsLoading && (
                              <div className="p-2 text-sm text-gray-500 dark:text-gray-400 text-center">
                                Loading {isRelaySurface ? 'relays' : 'automations'}...
                              </div>
                            )}

                            {/* No workflows message */}
                            {!presetsLoading && presetsForMode.length === 0 && (
                              <div className="p-2 text-sm text-gray-500 dark:text-gray-400 text-center">
                                {canCreateWorkflows
                                  ? `No ${isRelaySurface ? 'relays' : 'automations'} available. Create one to get started.`
                                  : `No ${isRelaySurface ? 'relays' : 'automations'} are available to your account.`}
                              </div>
                            )}

                            {/* Available Workflows */}
                            {!presetsLoading && presetsForMode.length > 0 && presetsForMode
                              .map((preset: CustomPreset | PredefinedPreset) => (
                                <div key={preset.id} className="flex items-center gap-1">
                                  <button
                                    onClick={() => {
                                      handlePresetClick(preset)
                                      setShowPresetDropdown(false)
                                    }}
                                    className={`flex-1 text-left p-2 rounded-md text-sm transition-colors ${
                                      presetModeCategory !== null && isPresetActive(preset.id, presetModeCategory)
                                        ? 'bg-blue-100 dark:bg-blue-900/30 text-blue-900 dark:text-blue-100'
                                        : 'hover:bg-gray-100 dark:hover:bg-slate-700 text-gray-700 dark:text-gray-300'
                                    }`}
                                  >
                                    <div className="flex items-center gap-2">
                                      <WorkflowIcon icon={preset.icon} label={preset.label} />
                                      <div className="flex-1">
                                        <div className="font-medium">{preset.label}</div>
                                        {'workflowKind' in preset && preset.workflowKind === 'relay' && <span className="text-[10px] font-medium uppercase tracking-wide text-blue-600 dark:text-blue-400">Flow</span>}
                                      </div>
                                    </div>
                                  </button>

                                  {/* Edit/Duplicate/Delete buttons */}
                                  {(
                                    <div className="flex gap-1">
                                      {presetModeCategory !== null && isPresetActive(preset.id, presetModeCategory) && !isEffectiveReadOnly && (
                                        <button
                                          onClick={(e) => {
                                            e.stopPropagation()
                                            handleEditWorkflowPreset(preset as CustomPreset)
                                            setShowPresetDropdown(false)
                                            setWorkspaceMinimized(true)
                                          }}
                                          className="p-1 rounded hover:bg-gray-200 dark:hover:bg-gray-600 text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-300"
                                          title="Edit automation"
                                        >
                                          <Settings className="w-3 h-3" />
                                        </button>
                                      )}
                                      <button
                                        onClick={(e) => requestDuplicatePreset(preset, e)}
                                        className="p-1 rounded hover:bg-blue-100 dark:hover:bg-blue-900/20 text-blue-600 dark:text-blue-400 hover:text-blue-700 dark:hover:text-blue-300"
                                        title="Duplicate automation"
                                      >
                                        <Copy className="w-3 h-3" />
                                      </button>
                                    </div>
                                  )}
                                </div>
                              ))}
                          </div>
                    </TopBarEntitySelector>
                  )
                }
                return null
              })()}
            </div>
          </ProductTopBarMain>

      </ProductTopBar>

      {/* Keyboard Shortcuts & Tips Modal */}
      {showShortcuts && (
        <ModalPortal>
          <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-[9999] p-4" onClick={() => setShowShortcuts(false)}>
            <div role="dialog" aria-modal="true" aria-labelledby="keyboard-shortcuts-title" className="bg-white dark:bg-gray-800 rounded-xl shadow-2xl w-full max-w-lg max-h-[calc(100vh-2rem)] overflow-hidden text-gray-900 dark:text-gray-100 flex flex-col" onClick={e => e.stopPropagation()}>
              {/* Header */}
              <div className="flex items-center justify-between px-5 py-4 border-b border-gray-200 dark:border-gray-700">
                <h3 id="keyboard-shortcuts-title" className="text-base font-semibold">Keyboard Shortcuts</h3>
                <button
                  onClick={() => setShowShortcuts(false)}
                  aria-label="Close keyboard shortcuts"
                  className="p-1 rounded-md text-gray-400 hover:text-gray-600 dark:hover:text-gray-200 hover:bg-gray-100 dark:hover:bg-gray-700 transition-colors"
                >
                  <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
                  </svg>
                </button>
              </div>

              <div className="px-5 py-4 space-y-5 overflow-y-auto min-h-0 flex-1">
                {/* Quick Switcher — featured */}
                <div className="rounded-lg border border-blue-200 dark:border-blue-800 bg-blue-50 dark:bg-blue-900/20 p-3.5">
                  <div className="flex items-center justify-between mb-2">
                    <div className="flex items-center gap-2">
                      <svg className="w-4 h-4 text-blue-500" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" /></svg>
                      <span className="text-sm font-semibold text-blue-700 dark:text-blue-300">Quick Switcher</span>
                    </div>
                    <kbd className="px-2 py-1 bg-blue-100 dark:bg-blue-800 text-blue-700 dark:text-blue-200 text-xs rounded font-mono font-semibold">⌘K / Ctrl+K</kbd>
                  </div>
                  <p className="text-xs text-blue-600 dark:text-blue-400 leading-relaxed">
                    Search products, projects, workflows, chats, toolbar panels and their tabs. Use @active or @panels to narrow the list. Panels open even with the toolbar hidden.
                  </p>
                </div>

                {/* Chat: the keys the chat box handles in every product. */}
                <div>
                  <p className="text-xs font-semibold text-gray-500 dark:text-gray-400 uppercase tracking-wider mb-2.5">Chat</p>
                  <div className="space-y-1.5">
                    {[
                      ['Send', 'Enter'],
                      ['New line', 'Shift+Enter'],
                      ['Stop a running chat', 'Esc'],
                    ].map(([label, key]) => (
                      <div key={key} className="flex items-center justify-between py-1">
                        <span className="text-sm text-gray-600 dark:text-gray-300">{label}</span>
                        <kbd className="px-2 py-0.5 bg-gray-100 dark:bg-gray-700 text-gray-600 dark:text-gray-300 text-xs rounded font-mono">{key}</kbd>
                      </div>
                    ))}
                  </div>
                </div>

                {/* Code chat tabs (PLAT-571); same keys as Code's terminal tabs. */}
                <div>
                  <p className="text-xs font-semibold text-gray-500 dark:text-gray-400 uppercase tracking-wider mb-2.5">Code</p>
                  <div className="space-y-1.5">
                    {[
                      ['Switch chat tab', window.electronAPI ? '⌘1–5 / Ctrl+1–5' : '⌥1–5 / Alt+1–5 (⌘1–5 in the desktop app)'],
                      ['New chat tab', '⌥⇧T / Alt+Shift+T'],
                    ].map(([label, key]) => (
                      <div key={key} className="flex items-center justify-between py-1">
                        <span className="text-sm text-gray-600 dark:text-gray-300">{label}</span>
                        <kbd className="px-2 py-0.5 bg-gray-100 dark:bg-gray-700 text-gray-600 dark:text-gray-300 text-xs rounded font-mono">{key}</kbd>
                      </div>
                    ))}
                  </div>
                </div>

                <p className="text-[11px] text-gray-400 dark:text-gray-500 text-center">
                  Mac keys first: ⌘ Cmd and ⌥ Option. On Windows/Linux use Ctrl and Alt
                </p>
              </div>
            </div>
          </div>
        </ModalPortal>
      )}

      {/* Preset Modal */}
      {showPresetModal && <PresetModal
        isOpen={showPresetModal}
        onClose={handleClosePresetModal}
        onSave={handleSavePreset}
        editingPreset={editingPreset}
        availableServers={availableServers}
        hideAgentModeSelection={!!editingPreset}
        fixedAgentMode={editingPreset?.agentMode || (selectedModeCategory ? (getAgentModeFromCategory(selectedModeCategory) as 'multi-agent' | 'workflow') : undefined)}
        agentMode={agentMode}
        onDeleteWorkflow={handleDeleteWorkflow}
        fixedWorkflowKind={isRelaySurface ? 'relay' : 'workflow'}
      />}

      <ConfirmationDialog
        isOpen={pendingDuplicatePreset !== null}
        onClose={() => {
          if (!duplicatingPreset) setPendingDuplicatePreset(null)
        }}
        onConfirm={handleDuplicatePresetConfirm}
        title="Duplicate automation?"
        message={`Create a copy of "${pendingDuplicatePreset?.label || 'this automation'}"? The copy will include its workflow files and configuration.`}
        confirmText="Duplicate"
        type="info"
        isLoading={duplicatingPreset}
        loadingText="Duplicating..."
      />

    </>
  )
}

import { create } from 'zustand'
import { WORKFLOW_REPORT_REFRESH_EVENT } from '../components/workflow/reportRefreshEvent'
import type { WorkflowPhase, ExecutionOptions, VariablesManifest } from '../services/api-types'
import type { WorkshopMode } from '../commands/types'
import type { AgentConfigs } from '../utils/stepConfigMatching'

// Interactive automation chats always use Workshop. Run remains a backend
// execution mode for bot and scheduled routes, but is not selectable or
// restored by the frontend.
function migrateWorkshopMode(raw: unknown): WorkshopMode {
  void raw
  return 'workshop'
}

function migrateWorkshopModeMap(raw: unknown): Record<string, WorkshopMode> {
  if (!raw || typeof raw !== 'object') return {}
  const out: Record<string, WorkshopMode> = {}
  for (const [k, v] of Object.entries(raw as Record<string, unknown>)) {
    out[k] = migrateWorkshopMode(v)
  }
  return out
}
import { ExecutionStrategy } from '../services/api-types'
import { agentApi } from '../services/api'
import { useChatStore } from './useChatStore'
import { useGlobalPresetStore } from './useGlobalPresetStore'
import { resolveGroupFolderPath } from '../utils/workflowUtils'
import { normalizeRunFolder } from '../utils/workflowStateNormalization'
import { getRawActiveWorkspaceId, getWorkspaceScopedStorageKey } from './useWorkspaceConnectionStore'
import {
  getWorkspaceView,
  isCanvasView,
  normalizeCanvasViewId,
  normalizeWorkspaceViewId,
  type CanvasViewId,
  type WorkspaceViewId,
} from '../components/workflow/workspaceViews'
import { useAppStore } from './useAppStore'

// The set of views lives in one place: components/workflow/workspaceViews.ts.
export type WorkflowWorkspaceView = WorkspaceViewId | null

// Layout direction for workflow canvas
export type LayoutDirection = 'LR' | 'TB'

const SELECTED_GROUP_IDS_KEY = 'workflow_selected_group_ids'
const CURRENT_RUNNING_GROUP_ID_KEY = 'workflow_current_running_group_id'
const SELECTED_RUN_FOLDER_KEY = 'workflow_selected_run_folder'
const LAYOUT_DIRECTION_KEY = 'workflow_layout_direction'
const WORKSHOP_MODE_BY_PRESET_KEY = 'workflow_workshop_mode_by_preset'
const WORKFLOW_UI_STATE_BY_PRESET_KEY = 'workflow_ui_state_by_preset'
// Legacy keys: read once for migration, never written. `canvasViewMode` and
// the per-preset view map both folded into WORKFLOW_UI_STATE_BY_PRESET_KEY.
const LEGACY_CANVAS_VIEW_MODE_KEY = 'workflow_canvas_view_mode'
const LEGACY_WORKSPACE_VIEW_BY_PRESET_KEY = 'workflow_workspace_view_by_preset'

function workflowStorageKey(key: string): string {
  return getWorkspaceScopedStorageKey(key)
}

function getWorkflowStorageItem(key: string): string | null {
  const scoped = window.localStorage.getItem(workflowStorageKey(key))
  if (scoped !== null) return scoped

  // Preserve existing local users' state after introducing workspace scoping.
  if (getRawActiveWorkspaceId() === 'local') {
    return window.localStorage.getItem(key)
  }
  return null
}

function setWorkflowStorageItem(key: string, value: string): void {
  window.localStorage.setItem(workflowStorageKey(key), value)
}

function removeWorkflowStorageItem(key: string): void {
  window.localStorage.removeItem(workflowStorageKey(key))
}

// Which pane gets ~75% of the split. 'preview' = report/plan/workspace canvas,
// 'chat' = the chat/agents/terminal column. Drives focus-follows-click sizing.
export type FocusedPane = 'chat' | 'preview'

type PersistedWorkflowUIState = {
  showChatArea?: boolean
  showWorkspacePane?: boolean
  workflowWorkspaceView?: WorkflowWorkspaceView
  /** The canvas view (Plan or Report) to fall back to when no explicit view is set. */
  lastCanvasView?: CanvasViewId
  focusedPane?: FocusedPane
}

function normalizeWorkflowWorkspaceView(view: unknown): WorkflowWorkspaceView {
  return normalizeWorkspaceViewId(view)
}

// Legacy read only: the per-preset view now lives in the ui-state map. Old
// saves that only have this key still restore through it.
function loadLegacyWorkspaceViewByPreset(): Record<string, WorkflowWorkspaceView> {
  try {
    const saved = getWorkflowStorageItem(LEGACY_WORKSPACE_VIEW_BY_PRESET_KEY)
    if (saved) {
      const parsed = JSON.parse(saved)
      if (parsed && typeof parsed === 'object') {
        const out: Record<string, WorkflowWorkspaceView> = {}
        for (const [presetId, rawView] of Object.entries(parsed as Record<string, unknown>)) {
          out[presetId] = normalizeWorkflowWorkspaceView(rawView)
        }
        return out
      }
    }
  } catch (error) {
    console.error('[WorkflowStore] Failed to load workspaceViewByPreset:', error)
  }
  return {}
}

function loadWorkflowUIStateByPreset(): Record<string, PersistedWorkflowUIState> {
  try {
    const saved = getWorkflowStorageItem(WORKFLOW_UI_STATE_BY_PRESET_KEY)
    if (!saved) return {}
    const parsed = JSON.parse(saved)
    if (!parsed || typeof parsed !== 'object') return {}
    const out: Record<string, PersistedWorkflowUIState> = {}
    for (const [presetId, raw] of Object.entries(parsed as Record<string, unknown>)) {
      if (!raw || typeof raw !== 'object') continue
      const candidate = raw as Record<string, unknown>
      out[presetId] = {
        showChatArea: typeof candidate.showChatArea === 'boolean' ? candidate.showChatArea : undefined,
        showWorkspacePane: typeof candidate.showWorkspacePane === 'boolean' ? candidate.showWorkspacePane : undefined,
        workflowWorkspaceView: normalizeWorkflowWorkspaceView(candidate.workflowWorkspaceView) ?? undefined,
        // Legacy saves stored this as `canvasViewMode` (which also allowed
        // log/soul/plan); read either field, normalized to Plan or Report.
        lastCanvasView: normalizeCanvasViewId(candidate.lastCanvasView ?? candidate.canvasViewMode),
        focusedPane:
          candidate.focusedPane === 'chat' || candidate.focusedPane === 'preview'
            ? candidate.focusedPane as FocusedPane
            : undefined,
      }
    }
    return out
  } catch (error) {
    console.error('[WorkflowStore] Failed to load workflowUIStateByPreset:', error)
    return {}
  }
}

export function hasSavedWorkflowWorkspaceView(presetId: string): boolean {
  const saved = loadWorkflowUIStateByPreset()[presetId]
  return Boolean(
    saved?.workflowWorkspaceView ||
    saved?.lastCanvasView ||
    loadLegacyWorkspaceViewByPreset()[presetId],
  )
}

function persistWorkflowUIStateForPreset(
  presetId: string | null,
  patch: PersistedWorkflowUIState,
) {
  if (!presetId) return
  try {
    const current = loadWorkflowUIStateByPreset()
    const next = {
      ...current[presetId],
      ...patch,
      workflowWorkspaceView:
        patch.workflowWorkspaceView === undefined
          ? current[presetId]?.workflowWorkspaceView
          : normalizeWorkflowWorkspaceView(patch.workflowWorkspaceView),
    }
    current[presetId] = next
    setWorkflowStorageItem(WORKFLOW_UI_STATE_BY_PRESET_KEY, JSON.stringify(current))
  } catch (error) {
    console.error('[WorkflowStore] Failed to save workflowUIStateByPreset:', error)
  }
}
// NOTE: Running workflows logic has been moved to useRunningWorkflowsStore.ts
// This store now focuses on workflow execution state and configuration

export interface RunFolder {
  name: string
}

export interface WorkflowChatTab {
  tabId: string  // Unique ID: `phase_${phaseId}_${timestamp}`
  phaseId: string  // Workflow phase ID (e.g., "planning", "execution")
  phaseName: string  // Display name from WorkflowPhase
  observerId: string  // Unique observer ID for this tab
  sessionId: string | null  // Chat session ID if exists
  isActive: boolean  // Whether this phase is currently running
  isStreaming: boolean  // Whether this tab's execution is currently running
  isCompleted: boolean  // Whether this tab's execution has completed (detected from completion events)
  createdAt: number  // Timestamp for ordering
}

// Per-preset workflow state — isolated so switching presets doesn't cross-contaminate
export interface PresetWorkflowState {
  selectedRunFolder: string | null
  activePhase: string | null
  showChatArea: boolean
  showWorkspacePane: boolean
  chatAreaExpanded: boolean
  workflowWorkspaceView: WorkflowWorkspaceView
  focusedPane: FocusedPane
  workflowChatTabs: Record<string, WorkflowChatTab>
  activeWorkflowTabId: string | null
  selectedGroupIds: string[]
  currentRunningGroupId: string | null
}

function createDefaultPresetState(): PresetWorkflowState {
  return {
    selectedRunFolder: null,
    activePhase: null,
    // First open shows BOTH the chat column and the preview canvas, with the
    // preview (report/plan) focused at ~75%. focus-follows-click swaps which
    // side gets 75% (see WorkflowLayout). showWorkspacePane true so the canvas
    // pane renders alongside chat rather than being hidden.
    showChatArea: true,
    showWorkspacePane: true,
    chatAreaExpanded: true,
    workflowWorkspaceView: null,
    focusedPane: 'preview',
    workflowChatTabs: {},
    activeWorkflowTabId: null,
    selectedGroupIds: [],
    currentRunningGroupId: null,
  }
}

// Snapshot current flat fields into a PresetWorkflowState object
function snapshotPresetState(state: WorkflowStore): PresetWorkflowState {
  return {
    selectedRunFolder: state.selectedRunFolder,
    activePhase: state.activePhase,
    showChatArea: state.showChatArea,
    showWorkspacePane: state.showWorkspacePane,
    chatAreaExpanded: state.chatAreaExpanded,
    workflowWorkspaceView: state.workflowWorkspaceView,
    focusedPane: state.focusedPane,
    workflowChatTabs: state.workflowChatTabs,
    activeWorkflowTabId: state.activeWorkflowTabId,
    selectedGroupIds: state.selectedGroupIds,
    currentRunningGroupId: state.currentRunningGroupId,
  }
}

interface WorkflowStore {
  // === CONSTANTS (loaded once from API) ===
  phases: WorkflowPhase[]
  isLoadingPhases: boolean
  phasesError: string | null
  phasesInitialized: boolean
  // Promise to prevent duplicate API calls during concurrent access
  _loadPhasesPromise: Promise<void> | null

  // === EXECUTION STATE (per workflow session) ===
  // Run folder management
  runFolders: RunFolder[]
  selectedRunFolder: string | null // Folder name or null
  isLoadingRunFolders: boolean

  // Execution options
  saveValidationResponses: boolean  // If true, save validation responses to workspace validation folder
  disableShellExecAccess: boolean  // If true, disable all execute_shell_command tool access globally
  disableReadImageAccess: boolean  // If true, disable all read_image tool access globally
  // Variables manifest (for batch execution with multiple groups)
  variablesManifest: VariablesManifest | null

  // Track current preset ID to detect page reload vs preset switch
  _currentPresetId: string | null
  // Per-preset state map — saves/restores state when switching between workflows
  _presetStates: Record<string, PresetWorkflowState>

  // Selected group IDs for execution (multi-select)
  selectedGroupIds: string[] // Array of group IDs to execute

  // Current running group (for batch execution)
  currentRunningGroupId: string | null

  // UI state
  activePhase: string | null // Currently running phase
  showChatArea: boolean
  showWorkspacePane: boolean
  chatAreaExpanded: boolean
  workflowWorkspaceView: WorkflowWorkspaceView
  /** Bumped by refreshWorkspaceView; the view host remounts the open view on change. Transient, not per preset. */
  workspaceViewRefreshToken: number
  /** What to focus inside the open view — a report tab, a plan step, a file path.
   * The token lets the same target fire twice. Views that cannot focus ignore it. */
  workspaceViewTarget: { view: WorkspaceViewId; target: string; token: number } | null
  focusedPane: FocusedPane // Which pane gets ~75% — 'preview' (canvas) or 'chat'
  layoutDirection: LayoutDirection // Canvas layout direction ('LR' = horizontal, 'TB' = vertical)
  // The canvas view (Plan or Report) the pane shows when workflowWorkspaceView
  // is null, and returns to when leaving Files. Recorded by openWorkspaceView.
  lastCanvasView: CanvasViewId

  // Multi-tab chat state
  workflowChatTabs: Record<string, WorkflowChatTab>  // tabId -> tab
  activeWorkflowTabId: string | null  // Currently selected tab

  // === WORKFLOW MODE STATE ===
  workflowMode: 'plan'
  workshopMode: WorkshopMode
  workshopModeByPreset: Record<string, WorkshopMode>
  setWorkshopMode: (mode: WorkshopMode) => void

  // Global step override (from step_override.json - applies to all steps)
  stepOverride: AgentConfigs | null

  // === ACTIONS ===
  // Constants
  loadPhases: () => Promise<void>
  setPhases: (phases: WorkflowPhase[]) => void
  getPhaseById: (id: string) => WorkflowPhase | undefined
  getDefaultPhase: () => string
  getStepSpecificPhases: () => WorkflowPhase[]

  // Run folders
  loadRunFolders: (workspacePath: string) => Promise<void>
  setRunFolders: (folders: RunFolder[]) => void
  setSelectedRunFolder: (folder: string | null) => void

  // Execution options
  buildExecutionOptions: () => ExecutionOptions
  setSaveValidationResponses: () => void
  setDisableShellExecAccess: (enabled: boolean) => void
  setDisableReadImageAccess: (enabled: boolean) => void

  // Variables manifest
  setVariablesManifest: (manifest: VariablesManifest | null) => void

  // Selected group IDs
  toggleGroupSelection: (groupName: string) => void
  setSelectedGroupIds: (groupNames: string[]) => void
  clearSelectedGroupIds: () => void
  // Restore selection state from localStorage (called after API load completes)
  restoreSelectionFromLocalStorage: () => void

  // Current running group
  setCurrentRunningGroupId: (groupName: string | null) => void

  // UI
  setActivePhase: (phase: string | null) => void
  setShowChatArea: (show: boolean) => void
  setShowWorkspacePane: (show: boolean) => void
  setChatAreaExpanded: (expanded: boolean) => void
  setFocusedPane: (pane: FocusedPane) => void
  setWorkflowWorkspaceView: (view: WorkflowWorkspaceView) => void
  /** Show the workspace pane on `view` -- the one way to navigate the pane
   * (toolbar buttons, chat file links, jump-to-plan). Records Plan/Report as
   * `lastCanvasView` and un-minimizes the workspace for Files. */
  openWorkspaceView: (view: WorkspaceViewId, target?: string) => void
  /** Reload whatever the workspace pane is showing: the report re-reads its
   * HTML, every other view remounts and refetches. Used when the agent opens
   * a view that is already on screen after changing what it shows. */
  refreshWorkspaceView: (target?: string) => void
  setLayoutDirection: (direction: LayoutDirection) => void

  // Workflow chat tabs
  createWorkflowTab: (phaseId: string, phaseName: string) => Promise<string>  // Returns tabId
  switchWorkflowTab: (tabId: string) => void
  closeWorkflowTab: (tabId: string) => Promise<void>
  getWorkflowTab: (tabId: string) => WorkflowChatTab | undefined
  getActiveWorkflowTab: () => WorkflowChatTab | undefined
  getTabsByPhase: (phaseId: string) => WorkflowChatTab[]
  setTabStreaming: (tabId: string, isStreaming: boolean) => void
  setTabCompleted: (tabId: string, isCompleted: boolean) => void
  updateTabSessionId: (tabId: string, sessionId: string) => void
  // Computed: Get tab's streaming status (derived from polling status)
  getTabStreamingStatus: (tabId: string) => boolean
  // Check if tab has completion events
  checkTabCompletion: (tabId: string, events: Array<{ type: string }>) => boolean

  // Global step override
  setStepOverride: (override: AgentConfigs | null) => void

  // Workflow Mode Actions
  setWorkflowMode: (mode: 'plan') => void

  // Persistence (localStorage)
  loadSavedSettings: (presetId: string) => void
  saveSettings: () => void
  // Switch to a new preset - resets context and loads settings in one update
  switchToPreset: (presetId: string) => void

  // Reset
  resetExecutionState: () => void
}

export const useWorkflowStore = create<WorkflowStore>()(
    (set, get) => ({
      // === Initial State ===
      workspaceViewRefreshToken: 0,
      workspaceViewTarget: null,
      // Constants
      phases: [],
      isLoadingPhases: false,
      phasesError: null,
      phasesInitialized: false,
      _loadPhasesPromise: null,

      // Run folders
      runFolders: [],
      // Selected run folder (persists across page refreshes via localStorage)
      // Can be stored in combined format: { groupIds: [...], runFolder: "..." } or separate key
      selectedRunFolder: (() => {
        try {
          // First try combined format
          const groupData = getWorkflowStorageItem(SELECTED_GROUP_IDS_KEY)
          if (groupData) {
            const parsed = JSON.parse(groupData)
            if (parsed && typeof parsed === 'object' && !Array.isArray(parsed) && parsed.runFolder) {
              return parsed.runFolder
            }
          }
          // Fallback to separate key
          const saved = getWorkflowStorageItem(SELECTED_RUN_FOLDER_KEY)
          if (saved) {
            return saved
          }
        } catch (error) {
          console.error('[WorkflowStore] Failed to load selectedRunFolder:', error)
        }
        return null
      })(),
      isLoadingRunFolders: false,

      // Save validation responses to workspace (always enabled, not persisted)
      saveValidationResponses: true,
      // Disable shell exec access (defaults to false, not persisted)
      disableShellExecAccess: false,
      // Disable read image access (defaults to false, not persisted)
      disableReadImageAccess: false,

      // Variables manifest
      variablesManifest: null,

      // Track current preset ID to detect page reload vs preset switch
      _currentPresetId: null as string | null,
      _presetStates: {} as Record<string, PresetWorkflowState>,

      // Selected group IDs (persists across page refreshes via localStorage)
      // Now stored in combined format: { groupIds: [...], runFolder: "..." }
      selectedGroupIds: (() => {
        try {
          const saved = getWorkflowStorageItem(SELECTED_GROUP_IDS_KEY)
          if (saved) {
            const parsed = JSON.parse(saved)
            // Handle new format: { groupIds: [...], runFolder: "..." }
            if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
              if (Array.isArray(parsed.groupIds)) {
                return parsed.groupIds
              }
            }
            // Handle old format: just array of groupIds
            else if (Array.isArray(parsed)) {
              return parsed
            }
          }
        } catch (error) {
          console.error('[WorkflowStore] Failed to load selectedGroupIds:', error)
        }
        return []
      })(),

      // Current running group (persists across page refreshes via localStorage)
      currentRunningGroupId: (() => {
        try {
          const saved = getWorkflowStorageItem(CURRENT_RUNNING_GROUP_ID_KEY)
          if (saved) {
            return saved
          }
        } catch (error) {
          console.error('[WorkflowStore] Failed to load currentRunningGroupId from localStorage:', error)
        }
        return null
      })(),

      // UI state
      activePhase: null,
      showChatArea: false,
      showWorkspacePane: false,
      chatAreaExpanded: true,
      workflowWorkspaceView: null,
      focusedPane: 'preview',
      // Layout direction (persists across page refreshes via localStorage)
      layoutDirection: (() => {
        try {
          const saved = getWorkflowStorageItem(LAYOUT_DIRECTION_KEY)
          if (saved === 'LR' || saved === 'TB') {
            return saved
          }
        } catch (error) {
          console.error('[WorkflowStore] Failed to load layout direction from localStorage:', error)
        }
        return 'LR' // Default to horizontal layout
      })() as LayoutDirection,
      // Seeded once from the legacy global canvas-view key; per-preset restore
      // (switchToPreset) overrides it from the ui-state map.
      lastCanvasView: (() => {
        try {
          return normalizeCanvasViewId(getWorkflowStorageItem(LEGACY_CANVAS_VIEW_MODE_KEY)) ?? 'report'
        } catch {
          return 'report'
        }
      })(),

      // Multi-tab chat state
      workflowChatTabs: {},
      activeWorkflowTabId: null,

      // === Workflow Mode State ===
      workflowMode: 'plan',
      workshopMode: 'workshop' as const,
      workshopModeByPreset: (() => {
        try {
          const saved = getWorkflowStorageItem(WORKSHOP_MODE_BY_PRESET_KEY)
          if (saved) {
            // Migrate any persisted legacy mode values from the older persona set.
            return migrateWorkshopModeMap(JSON.parse(saved))
          }
        } catch (error) {
          console.error('[WorkflowStore] Failed to load workshopModeByPreset:', error)
        }
        return {}
      })(),
      setWorkshopMode: (mode: WorkshopMode) => {
        const normalizedMode = migrateWorkshopMode(mode)
        const presetId = useGlobalPresetStore.getState().activePresetIds.workflow
        set((state) => {
          const updated = presetId
            ? { ...state.workshopModeByPreset, [presetId]: normalizedMode }
            : state.workshopModeByPreset
          if (presetId) {
            try {
              setWorkflowStorageItem(WORKSHOP_MODE_BY_PRESET_KEY, JSON.stringify(updated))
            } catch (error) {
              console.error('[WorkflowStore] Failed to save workshopModeByPreset:', error)
            }
          }
          // Workflow workshop personas all live under workflowMode='plan'.
          // Eval/output editing happens inside Builder mode now.
          return {
            workshopMode: normalizedMode,
            workflowMode: 'plan' as const,
            workshopModeByPreset: updated,
          }
        })
      },

      // Global step override
      stepOverride: null,

      // === Actions ===

      // Load phases from backend (with deduplication)
      loadPhases: async () => {
        const state = get()

        // If already initialized, return immediately
        if (state.phasesInitialized) {
          return
        }

        // If a load is already in progress, wait for it
        if (state._loadPhasesPromise) {
          return state._loadPhasesPromise
        }

        // Create and store the promise to prevent duplicate calls
        const loadPromise = (async () => {
          set({ isLoadingPhases: true, phasesError: null })

          try {
            const response = await agentApi.getWorkflowConstants()
            if (response.success) {
              const phases = response.constants?.phases || []
              set({
                phases: phases,
                isLoadingPhases: false,
                phasesInitialized: true,
                _loadPhasesPromise: null
              })
            } else {
              throw new Error(response.message || 'Failed to load workflow constants')
            }
          } catch (error) {
            console.error('[WorkflowStore] Failed to load phases:', error)
            set({
              phasesError: error instanceof Error ? error.message : 'Failed to load phases',
              isLoadingPhases: false,
              _loadPhasesPromise: null
            })
          }
        })()

        set({ _loadPhasesPromise: loadPromise })
        return loadPromise
      },

      setPhases: (phases: WorkflowPhase[]) => {
        set({
          phases: phases,
          phasesInitialized: true
        })
      },

      getPhaseById: (id: string) => {
        return get().phases.find(p => p.id === id)
      },

      getDefaultPhase: () => {
        const phases = get().phases
        return phases.length > 0 ? phases[0].id : 'planning'
      },

      // Get phases that can work on individual steps (currently none — use Automation Builder instead)
      getStepSpecificPhases: () => {
        return []
      },

      // Load run folders for a workspace
      loadRunFolders: async (workspacePath: string) => {
        if (!workspacePath) {
          set({ runFolders: [] })
          return
        }

        set({ isLoadingRunFolders: true })

        try {
          const response = await agentApi.getRunFolders(workspacePath)

          if (!response) {
            set({ runFolders: [], isLoadingRunFolders: false })
            return
          }

          let folders = response.folders
          if (folders === null || folders === undefined) {
            folders = []
          }

          if (!Array.isArray(folders)) {
            set({ runFolders: [], isLoadingRunFolders: false })
            return
          }

          // Sort folders by iteration number (descending - newest first)
          const sorted = [...folders].sort((a, b) => {
            const numA = parseInt(a.name.replace('iteration-', '')) || 0
            const numB = parseInt(b.name.replace('iteration-', '')) || 0
            return numB - numA
          })

          set({ runFolders: sorted, isLoadingRunFolders: false })

          const defaultRunFolder = sorted.find(folder => folder.name === 'iteration-0')?.name
            ?? sorted[0]?.name
            ?? null

          // Auto-select iteration-0 by default if no selection exists
          const currentSelection = get().selectedRunFolder
          if (!currentSelection && defaultRunFolder) {
            set({ selectedRunFolder: defaultRunFolder })
            try {
              setWorkflowStorageItem(SELECTED_RUN_FOLDER_KEY, defaultRunFolder)
            } catch { /* ignore */ }
          }

          // Validate current selection
          if (currentSelection && !sorted.some(f => f.name === currentSelection)) {
            // Check if the selection looks like a valid iteration folder (e.g., "iteration-3")
            // This handles the case where a folder was just created but hasn't appeared in the list yet
            const isValidIterationPattern = /^iteration-\d+/.test(currentSelection)

            if (isValidIterationPattern) {
              // Preserve the selection even if not in list yet (it was likely just created)
              // The folder should appear in the next refresh
            } else {
              // Saved folder no longer exists and doesn't match iteration pattern, default to
              // iteration-0 when present so the builder/workspace consistently anchors there.
              const newSelection = defaultRunFolder
              set({ selectedRunFolder: newSelection })

              // Persist to localStorage
              try {
                if (newSelection) {
                  setWorkflowStorageItem(SELECTED_RUN_FOLDER_KEY, newSelection)
                } else {
                  removeWorkflowStorageItem(SELECTED_RUN_FOLDER_KEY)
                }
              } catch (error) {
                console.error('[WorkflowStore] Failed to save selectedRunFolder to localStorage:', error)
              }
            }
          }
        } catch (error) {
          console.error('[WorkflowStore] Failed to load run folders:', error)
          set({ runFolders: [], isLoadingRunFolders: false })
        }
      },

      setRunFolders: (folders: RunFolder[]) => {
        set({ runFolders: folders })
      },

      setSelectedRunFolder: (folder: string | null) => {
        // Use normalization utility to validate folder path
        const state = get()
        const normalized = normalizeRunFolder(folder, state.variablesManifest)
        set({ selectedRunFolder: normalized })

        // Persist to localStorage - update combined format AND separate key
        // Note: startPoint is NOT persisted - it's calculated from progress
        try {
          // Update combined format
          const existingData = getWorkflowStorageItem(SELECTED_GROUP_IDS_KEY)
          if (existingData) {
            const parsed = JSON.parse(existingData)
            if (parsed && typeof parsed === 'object') {
              parsed.runFolder = normalized
              setWorkflowStorageItem(SELECTED_GROUP_IDS_KEY, JSON.stringify(parsed))
            }
          } else if (normalized) {
            // Create new combined format if it doesn't exist
            const persistData = {
              groupIds: state.selectedGroupIds,
              runFolder: normalized,
              // eslint-disable-next-line @typescript-eslint/no-explicit-any
              presetId: (state as any)._currentPresetId
            }
            setWorkflowStorageItem(SELECTED_GROUP_IDS_KEY, JSON.stringify(persistData))
          }

          // Also update separate key for backward compatibility
          if (normalized) {
            setWorkflowStorageItem(SELECTED_RUN_FOLDER_KEY, normalized)
          } else {
            removeWorkflowStorageItem(SELECTED_RUN_FOLDER_KEY)
          }
        } catch (error) {
          console.error('[WorkflowStore] Failed to save selectedRunFolder to localStorage:', error)
        }
      },

      // Build execution options from current state
      buildExecutionOptions: () => {
        const state = get()

        // Runs always start from the beginning — resume-from-step logic has been removed.
        const executionStrategy: string = ExecutionStrategy.START_FROM_BEGINNING_NO_HUMAN
        // Resolve the specific group folder path for phases that need context
        // Uses utility function to consolidate logic
        const resolvedRunFolder = resolveGroupFolderPath({
          currentRunningGroupId: state.currentRunningGroupId,
          selectedRunFolder: state.selectedRunFolder,
          selectedGroupIds: state.selectedGroupIds,
          manifest: state.variablesManifest
        })

        const options: ExecutionOptions = {
          selected_run_folder: resolvedRunFolder,
          execution_strategy: executionStrategy,
          workshop_mode: (() => {
            const presetId = useGlobalPresetStore.getState().activePresetIds.workflow
            return (presetId && state.workshopModeByPreset[presetId]) || state.workshopMode
          })(),
        }

        // Include save validation responses flag (always send to ensure backend knows user preference)
        options.save_validation_responses = state.saveValidationResponses

        // Prefer explicit group selection from the UI, but fall back to enabled groups
        // from the manifest so workflow builder chat still works before the user has
        // manually clicked a group selector in the canvas.
        if (state.selectedGroupIds.length > 0) {
          options.enabled_group_names = state.selectedGroupIds
        } else {
          const enabledManifestGroupIds = (state.variablesManifest?.groups || [])
            .filter(group => group.enabled)
            .map(group => group.name)

          if (enabledManifestGroupIds.length > 0) {
            options.enabled_group_names = enabledManifestGroupIds
          }
        }

        // Read feature toggles from preset and include when disabled (backend defaults to enabled)
        const presetStore = useGlobalPresetStore.getState()
        const activePreset = presetStore.getActivePreset('workflow')
        const presetLLMConfig = activePreset?.llmConfig

        if (presetLLMConfig?.use_knowledgebase === false) {
          options.enable_knowledgebase = false
        }

        return options
      },
      
      setSaveValidationResponses: () => {
        set({ saveValidationResponses: true })
        // No longer persisted to localStorage
      },
      
      setDisableShellExecAccess: (enabled: boolean) => {
        set({ disableShellExecAccess: enabled })
        // No longer persisted to localStorage
      },
      
      setDisableReadImageAccess: (enabled: boolean) => {
        set({ disableReadImageAccess: enabled })
        // No longer persisted to localStorage
      },

      setVariablesManifest: (manifest: VariablesManifest | null) => {
        set({ variablesManifest: manifest })
      },

      // Toggle group selection (add if not selected, remove if selected)
      toggleGroupSelection: (groupName: string) => {
        const state = get()
        const currentIds = state.selectedGroupIds
        const isSelected = currentIds.includes(groupName)

        const newIds = isSelected
          ? currentIds.filter(id => id !== groupName)
          : [...currentIds, groupName]

        set({ selectedGroupIds: newIds })

        // Persist to localStorage - save groupIds, runFolder, AND presetId
        // Note: startPoint is NOT persisted - it's calculated from progress
        try {
          const persistData = {
            groupIds: newIds,
            runFolder: state.selectedRunFolder,
            // eslint-disable-next-line @typescript-eslint/no-explicit-any
            presetId: (state as any)._currentPresetId
          }
          setWorkflowStorageItem(SELECTED_GROUP_IDS_KEY, JSON.stringify(persistData))
        } catch (error) {
          console.error('[WorkflowStore] Failed to persist group selection:', error)
        }
      },

      // Set selected group IDs directly
      setSelectedGroupIds: (groupNames: string[]) => {
        const state = get()
        set({ selectedGroupIds: groupNames })

        // Persist to localStorage - save groupIds, runFolder, AND presetId
        // Note: startPoint is NOT persisted - it's calculated from progress
        try {
          const persistData = {
            groupIds: groupNames,
            runFolder: state.selectedRunFolder,
            // eslint-disable-next-line @typescript-eslint/no-explicit-any
            presetId: (state as any)._currentPresetId
          }
          setWorkflowStorageItem(SELECTED_GROUP_IDS_KEY, JSON.stringify(persistData))
        } catch (error) {
          console.error('[WorkflowStore] Failed to persist group selection:', error)
        }
      },

      // Clear all selected group IDs
      clearSelectedGroupIds: () => {
        set({ selectedGroupIds: [] })

        // Clear from localStorage
        try {
          removeWorkflowStorageItem(SELECTED_GROUP_IDS_KEY)
        } catch (error) {
          console.error('[WorkflowStore] Failed to clear group selection:', error)
        }
      },

      // Restore selection state from localStorage (called after API load completes)
      // Load per-preset settings from localStorage (selectedRunFolder, selectedGroupIds, currentRunningGroupId)
      restoreSelectionFromLocalStorage: () => {
        try {
          let hasChanges = false
          const updates: Partial<WorkflowStore> = {}
          const currentPresetId = get()._currentPresetId
          let canRestoreLegacyKeys = true

          // Restore selectedGroupIds and selectedRunFolder from combined storage
          const savedGroupData = getWorkflowStorageItem(SELECTED_GROUP_IDS_KEY)
          if (savedGroupData) {
            const parsed = JSON.parse(savedGroupData)
            // Handle new format: { groupIds: [...], runFolder: "..." }
            if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
              const storedPresetId = typeof parsed.presetId === 'string' ? parsed.presetId : null
              const presetMatches = !currentPresetId || !storedPresetId || storedPresetId === currentPresetId
              canRestoreLegacyKeys = !storedPresetId

              if (presetMatches) {
                if (Array.isArray(parsed.groupIds) && parsed.groupIds.length > 0) {
                  updates.selectedGroupIds = parsed.groupIds
                  hasChanges = true
                }
                if (parsed.runFolder) {
                  updates.selectedRunFolder = parsed.runFolder
                  hasChanges = true
                }
              }
              // Note: startPoint is intentionally NOT restored - calculated from progress
            }
            // Handle old format: just array of groupIds (backward compatibility)
            else if (Array.isArray(parsed) && parsed.length > 0 && !currentPresetId) {
              updates.selectedGroupIds = parsed
              hasChanges = true
            }
          }

          // Also check separate runFolder key (backward compatibility)
          if (!updates.selectedRunFolder && canRestoreLegacyKeys && !currentPresetId) {
            const savedRunFolder = getWorkflowStorageItem(SELECTED_RUN_FOLDER_KEY)
            if (savedRunFolder) {
              updates.selectedRunFolder = savedRunFolder
              hasChanges = true
            }
          }

          // Restore currentRunningGroupId
          const savedCurrentGroup = canRestoreLegacyKeys && !currentPresetId
            ? getWorkflowStorageItem(CURRENT_RUNNING_GROUP_ID_KEY)
            : null
          if (savedCurrentGroup) {
            updates.currentRunningGroupId = savedCurrentGroup
            hasChanges = true
          }

          if (hasChanges) {
            set(updates)
          }
        } catch (error) {
          console.error('[WorkflowStore] Failed to restore selection from localStorage:', error)
        }
      },

      setCurrentRunningGroupId: (groupName: string | null) => {
        set({ currentRunningGroupId: groupName })

        // Persist to localStorage
        try {
          if (groupName) {
            setWorkflowStorageItem(CURRENT_RUNNING_GROUP_ID_KEY, groupName)
          } else {
            removeWorkflowStorageItem(CURRENT_RUNNING_GROUP_ID_KEY)
          }
        } catch (error) {
          console.error('[WorkflowStore] Failed to save currentRunningGroupId to localStorage:', error)
        }
      },

      setActivePhase: (phase: string | null) => {
        set({ activePhase: phase })
      },

      setShowChatArea: (show: boolean) => {
        const presetId = useGlobalPresetStore.getState().activePresetIds.workflow ?? get()._currentPresetId
        const currentWorkspacePane = get().showWorkspacePane
        persistWorkflowUIStateForPreset(presetId ?? null, {
          showChatArea: show,
          showWorkspacePane: currentWorkspacePane,
        })
        set(state => ({
          showChatArea: show,
          showWorkspacePane: state.showWorkspacePane,
          chatAreaExpanded: show ? true : state.chatAreaExpanded
        }))
      },

      setShowWorkspacePane: (show: boolean) => {
        const effectiveShow = get().showChatArea ? show : true
        const presetId = useGlobalPresetStore.getState().activePresetIds.workflow ?? get()._currentPresetId
        persistWorkflowUIStateForPreset(presetId ?? null, { showWorkspacePane: effectiveShow })
        set(state => ({
          showWorkspacePane: state.showChatArea ? show : true
        }))
      },

      setFocusedPane: (pane: FocusedPane) => {
        if (get().focusedPane === pane) return
        // Transient — intentionally NOT persisted; every workflow open defaults
        // back to 'preview' (report-large) regardless of last session's focus.
        set({ focusedPane: pane })
      },

      setChatAreaExpanded: (expanded: boolean) => {
        set({ chatAreaExpanded: expanded })
      },

      setWorkflowWorkspaceView: (view: WorkflowWorkspaceView) => {
        const normalizedView = normalizeWorkflowWorkspaceView(view)
        const presetId = useGlobalPresetStore.getState().activePresetIds.workflow ?? get()._currentPresetId
        persistWorkflowUIStateForPreset(presetId ?? null, { workflowWorkspaceView: normalizedView })
        set({ workflowWorkspaceView: normalizedView })
      },

      openWorkspaceView: (view: WorkspaceViewId, target?: string) => {
        const automationTarget = view === 'schedules'
          ? 'schedules'
          : view === 'webhooks'
            ? 'triggers'
            : undefined
        const resolvedView: WorkspaceViewId = automationTarget ? 'workshop' : view
        const resolvedTarget = target?.trim() || automationTarget
        const kind = getWorkspaceView(resolvedView).kind
        if (resolvedTarget) {
          set(state => ({ workspaceViewTarget: { view: resolvedView, target: resolvedTarget, token: (state.workspaceViewTarget?.token ?? 0) + 1 } }))
        }
        if ((kind === 'canvas' || kind === 'preview') && isCanvasView(resolvedView)) {
          const presetId = useGlobalPresetStore.getState().activePresetIds.workflow ?? get()._currentPresetId
          persistWorkflowUIStateForPreset(presetId ?? null, { lastCanvasView: resolvedView })
          set({ lastCanvasView: resolvedView })
        }
        get().setWorkflowWorkspaceView(resolvedView)
        get().setShowWorkspacePane(true)
        // Narrow workflow layouts show one content pane at a time. Selecting
        // any workspace destination should reveal that pane; md+ ignores this
        // focus for visibility and continues to show the split layout.
        get().setFocusedPane('preview')
        useAppStore.getState().setWorkspaceMinimized(resolvedView !== 'files')
      },

      refreshWorkspaceView: (target?: string) => {
        const view = get().workflowWorkspaceView ?? get().lastCanvasView
        if (target && target.trim()) {
          set(state => ({ workspaceViewTarget: { view, target: target.trim(), token: (state.workspaceViewTarget?.token ?? 0) + 1 } }))
        }
        set(state => ({ workspaceViewRefreshToken: state.workspaceViewRefreshToken + 1 }))
        // The report view keeps its HTML in its own state and listens for this.
        window.dispatchEvent(new Event(WORKFLOW_REPORT_REFRESH_EVENT))
      },

      setLayoutDirection: (direction: LayoutDirection) => {
        try {
          setWorkflowStorageItem(LAYOUT_DIRECTION_KEY, direction)
        } catch (error) {
          console.error('[WorkflowStore] Failed to save layout direction to localStorage:', error)
        }
        set({ layoutDirection: direction })
      },

      // Workflow chat tabs
      createWorkflowTab: async (phaseId: string, phaseName: string): Promise<string> => {
        // Generate unique tab ID
        const tabId = `phase_${phaseId}_${Date.now()}`
        
        // Generate new session ID for tab
        const sessionIdForTab = crypto.randomUUID()
        
        // Create tab entry
        const tab: WorkflowChatTab = {
          tabId,
          phaseId,
          phaseName,
          observerId: sessionIdForTab, // Keep for backward compatibility, set to sessionId
          sessionId: sessionIdForTab,
          isActive: false,
          isStreaming: false,
          isCompleted: false,
          createdAt: Date.now()
        }
        
        // Add to store
        set((state) => ({
          workflowChatTabs: {
            ...state.workflowChatTabs,
            [tabId]: tab
          },
          activeWorkflowTabId: tabId
        }))
        
        return tabId
      },

      switchWorkflowTab: (tabId: string) => {
        const state = get()
        if (!state.workflowChatTabs[tabId]) {
          console.warn(`[WorkflowStore] Tab ${tabId} not found`)
          return
        }
        
        set({ activeWorkflowTabId: tabId })
      },

      closeWorkflowTab: async (tabId: string) => {
        const state = get()
        const tab = state.workflowChatTabs[tabId]
        
        if (!tab) {
          console.warn(`[WorkflowStore] Tab ${tabId} not found`)
          return
        }
        
        // If tab is streaming, stop it first
        if (tab.isStreaming && tab.sessionId) {
          try {
            await agentApi.stopSession(tab.sessionId)
            console.log(`[WorkflowStore] Stopped session ${tab.sessionId} for tab ${tabId}`)
          } catch (error) {
            console.error(`[WorkflowStore] Failed to stop session for tab ${tabId}:`, error)
          }
        }
        
        // No observer removal needed - observers are no longer used
        
        // Remove tab from store
        const newTabs = { ...state.workflowChatTabs }
        delete newTabs[tabId]
        
        // If closing active tab, switch to another tab or clear active
        let newActiveTabId = state.activeWorkflowTabId
        if (state.activeWorkflowTabId === tabId) {
          const remainingTabs = Object.values(newTabs)
          if (remainingTabs.length > 0) {
            // Switch to most recently created tab
            const sortedTabs = remainingTabs.sort((a, b) => b.createdAt - a.createdAt)
            newActiveTabId = sortedTabs[0].tabId
          } else {
            newActiveTabId = null
            set({ showChatArea: false })
          }
        }
        
        set({
          workflowChatTabs: newTabs,
          activeWorkflowTabId: newActiveTabId
        })
      },

      getWorkflowTab: (tabId: string) => {
        return get().workflowChatTabs[tabId]
      },

      getActiveWorkflowTab: () => {
        const state = get()
        if (!state.activeWorkflowTabId) {
          return undefined
        }
        return state.workflowChatTabs[state.activeWorkflowTabId]
      },

      getTabsByPhase: (phaseId: string) => {
        const state = get()
        return Object.values(state.workflowChatTabs).filter(tab => tab.phaseId === phaseId)
      },

      setTabStreaming: (tabId: string, isStreaming: boolean) => {
        const state = get()
        const tab = state.workflowChatTabs[tabId]
        if (!tab) {
          console.warn(`[WorkflowStore] Tab ${tabId} not found for setTabStreaming`)
          return
        }
        
        set((state) => ({
          workflowChatTabs: {
            ...state.workflowChatTabs,
            [tabId]: {
              ...state.workflowChatTabs[tabId],
              isStreaming
            }
          }
        }))
      },

      updateTabSessionId: (tabId: string, sessionId: string) => {
        const state = get()
        const tab = state.workflowChatTabs[tabId]
        if (!tab) {
          console.warn(`[WorkflowStore] Tab ${tabId} not found for updateTabSessionId`)
          return
        }
        
        set((state) => ({
          workflowChatTabs: {
            ...state.workflowChatTabs,
            [tabId]: {
              ...state.workflowChatTabs[tabId],
              sessionId
            }
          }
        }))
      },

      setTabCompleted: (tabId: string, isCompleted: boolean) => {
        const state = get()
        const tab = state.workflowChatTabs[tabId]
        if (!tab) {
          console.warn(`[WorkflowStore] Tab ${tabId} not found for setTabCompleted`)
          return
        }
        
        set((state) => ({
          workflowChatTabs: {
            ...state.workflowChatTabs,
            [tabId]: {
              ...state.workflowChatTabs[tabId],
              isCompleted
            }
          }
        }))
      },

      // Computed: Get tab's streaming status (derived from polling and completion)
      getTabStreamingStatus: (tabId: string) => {
        const state = get()
        const tab = state.workflowChatTabs[tabId]
        if (!tab) {
          return false
        }
        
        // If tab is marked as completed, it's not streaming
        if (tab.isCompleted) {
          return false
        }
        
        // Get chat store state to check polling status
        const chatStore = useChatStore.getState()
        
        // Tab is streaming if:
        // 1. Polling is active
        // 2. This tab's observer ID matches the currently polled observer
        // 3. Not manually paused (stored isStreaming !== false)
        const isPolling = chatStore.pollingInterval !== null
        
        // If polling and not completed, it's streaming
        // The stored tab.isStreaming can be used to pause (e.g., human feedback)
        if (isPolling) {
          return tab.isStreaming !== false // Respect manual pause
        }
        
        return false
      },

      // Check if events contain completion events for this tab's observer
      checkTabCompletion: (tabId: string, events: Array<{ type: string }>) => {
        const state = get()
        const tab = state.workflowChatTabs[tabId]
        if (!tab) {
          return false
        }
        
        // Check if any events are completion events
        // For workflow mode: orchestrator_end, request_human_feedback
        // For chat mode: unified_completion, agent_end, conversation_end, etc.
        // 'workflow_end' was deleted in batch 4 (never emitted); 'orchestrator_end'
        // is the real signal (BaseOrchestrator.EmitOrchestratorEnd).
        const completionEventTypes = [
          'orchestrator_end',
          'request_human_feedback',
          'unified_completion',
          'agent_end',
          'conversation_end',
          'conversation_error',
          'agent_error'
        ]
        
        return events.some(event => 
          event.type && completionEventTypes.includes(event.type)
        )
      },

      // Global step override
      setStepOverride: (override: AgentConfigs | null) => {
        set({ stepOverride: override })
      },

      // Workflow Mode Actions
      // workflowMode='plan' is the only value; all workshop modes live under it.
      setWorkflowMode: (_mode: 'plan') => {
        const presetId = useGlobalPresetStore.getState().activePresetIds.workflow
        set(state => {
          const normalizedMode = 'plan' as const
          const rememberedForPreset = presetId ? state.workshopModeByPreset[presetId] : undefined
          const resolvedWorkshopMode: WorkshopMode =
            migrateWorkshopMode(rememberedForPreset ?? state.workshopMode)
          const updated = presetId
            ? {
                ...state.workshopModeByPreset,
                [presetId]: resolvedWorkshopMode,
              }
            : state.workshopModeByPreset
          if (presetId) {
            try {
              setWorkflowStorageItem(WORKSHOP_MODE_BY_PRESET_KEY, JSON.stringify(updated))
            } catch (error) {
              console.error('[WorkflowStore] Failed to save workshopModeByPreset:', error)
            }
          }
          return {
            workflowMode: normalizedMode,
            workshopMode: resolvedWorkshopMode,
            workshopModeByPreset: updated,
          }
        })
      },

      // Load saved settings from localStorage for a preset
      // Uses a single set() call to avoid multiple re-renders
      // Restores selectedRunFolder, selectedGroupIds, currentRunningGroupId from localStorage
      loadSavedSettings: (presetId: string) => {
        if (!presetId) return

        try {
          // Load persisted values from localStorage
          let savedRunFolder: string | null = null
          let savedGroupIds: string[] = []
          let savedCurrentRunningGroupId: string | null = null
          let canRestoreLegacyKeys = false

          try {
            // Load from combined format first
            const groupDataStr = getWorkflowStorageItem(SELECTED_GROUP_IDS_KEY)
            if (groupDataStr) {
              const parsed = JSON.parse(groupDataStr)
              // Handle new format: { groupIds: [...], runFolder: "..." }
              if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
                const storedPresetId = typeof parsed.presetId === 'string' ? parsed.presetId : null
                const presetMatches = !storedPresetId || storedPresetId === presetId
                canRestoreLegacyKeys = !storedPresetId

                if (presetMatches) {
                  if (Array.isArray(parsed.groupIds)) {
                    savedGroupIds = parsed.groupIds
                  }
                  if (parsed.runFolder) {
                    savedRunFolder = parsed.runFolder
                  }
                }
              }
              // Handle old format: just array of groupIds
              else if (Array.isArray(parsed)) {
                canRestoreLegacyKeys = true
                savedGroupIds = parsed
              }
            }

            // Fallback to separate runFolder key if not in combined format
            if (!savedRunFolder && canRestoreLegacyKeys) {
              const runFolderStr = getWorkflowStorageItem(SELECTED_RUN_FOLDER_KEY)
              if (runFolderStr) {
                savedRunFolder = runFolderStr
              }
            }

            const currentGroupStr = canRestoreLegacyKeys
              ? getWorkflowStorageItem(CURRENT_RUNNING_GROUP_ID_KEY)
              : null
            if (currentGroupStr) {
              savedCurrentRunningGroupId = currentGroupStr
            }
          } catch (error) {
            console.error('[WorkflowStore] Failed to load from localStorage:', error)
          }

          // Build state update object - restore from localStorage or use defaults
          // NOTE: activePhase is NOT reset here — it's saved/restored by switchToPreset's
          // per-preset snapshot mechanism. Resetting it here would clobber the restored phase.
          const stateUpdate: Partial<WorkflowStore> = {
            selectedRunFolder: savedRunFolder,
            selectedGroupIds: savedGroupIds,
            currentRunningGroupId: savedCurrentRunningGroupId,
          }

          // Single set() call to avoid multiple re-renders
          set(stateUpdate)
        } catch (error) {
          console.error('[WorkflowStore] Failed to load saved settings:', error)
        }
      },

      // Save current settings to localStorage for a preset
      // NOTE: No longer saves settings to localStorage - removed all persistence
      saveSettings: () => {
        // No-op: Settings are no longer persisted
      },

      // Switch to a new preset - combines reset and load into a single set() call
      // This avoids multiple re-renders that would occur with separate reset + load calls
      switchToPreset: (presetId: string) => {
        if (!presetId) return

        // Check if localStorage has data for the same preset (page reload scenario)
        let storedPresetId: string | null = null
        try {
          const groupDataStr = getWorkflowStorageItem(SELECTED_GROUP_IDS_KEY)
          if (groupDataStr) {
            const parsed = JSON.parse(groupDataStr)
            if (parsed && typeof parsed === 'object' && parsed.presetId) {
              storedPresetId = parsed.presetId
            }
          }
        } catch {
          // Ignore parse errors
        }

        const isSamePreset = storedPresetId === presetId
        const persistedUIState = loadWorkflowUIStateByPreset()[presetId] ?? {}

        try {
          const currentState = get()
          const oldPresetId = currentState._currentPresetId

          // If already on this preset, skip — avoids resetting state when
          // WorkflowModeHandler re-applies the same preset on mount/effect
          if (oldPresetId === presetId) {
            return
          }

          console.log(`%c[WorkflowStore] switchToPreset: ${oldPresetId?.slice(0,8)} → ${presetId?.slice(0,8)}`, 'color: #2196F3; font-weight: bold')
          console.time(`[WorkflowStore] switchToPreset-${presetId?.slice(0,8)}`)
          const restoredWorkshopMode = migrateWorkshopMode(
            currentState.workshopModeByPreset[presetId] ?? currentState.workshopMode
          )

          // Save current flat state into the old preset's slot before switching
          // This preserves all execution state (progress, groups, phase, etc.) so it's
          // restored when the user switches back to this workflow.
          if (oldPresetId && oldPresetId !== presetId) {
            const snapshot = snapshotPresetState(currentState)
            set((state) => ({
              _presetStates: { ...state._presetStates, [oldPresetId]: snapshot }
            }))
          }

          // If switching to the SAME preset (page reload), preserve localStorage
          if (isSamePreset) {
            // Just reset workflow context but preserve selection - localStorage will be restored by loadSavedSettings
            set({
              runFolders: [],
              variablesManifest: null,
              workflowChatTabs: {},
              activeWorkflowTabId: null,
              showChatArea: true,
              showWorkspacePane: true,
              workflowWorkspaceView:
                persistedUIState.workflowWorkspaceView ??
                (loadLegacyWorkspaceViewByPreset()[presetId] ?? 'report'),
              focusedPane: 'preview',
              ...(persistedUIState.lastCanvasView ? { lastCanvasView: persistedUIState.lastCanvasView } : {}),
              workshopMode: restoredWorkshopMode,
              workflowMode: 'plan',
              _currentPresetId: presetId
            } as Partial<WorkflowStore>)
            return
          }

          // Restore saved state for the new preset, or use defaults
          const savedState = currentState._presetStates[presetId]
          const restored = savedState ?? createDefaultPresetState()

          // Apply restored per-preset state to flat fields + reset API-loaded context.
          // _presetStates only survives in-memory; on a fresh session (page reload)
          // it's empty, so fall back to the per-preset workspaceView map persisted
          // in localStorage.
          const persistedWorkspaceView =
            restored.workflowWorkspaceView !== null
              ? restored.workflowWorkspaceView
              : (persistedUIState.workflowWorkspaceView ??
                (loadLegacyWorkspaceViewByPreset()[presetId] ?? 'report'))
          // New adaptive layout: workflows always open with BOTH the chat rail
          // and the preview canvas visible (focus-follows-click sizes them).
          // Force them on so a stale persisted showChatArea:false from the
          // pre-adaptive default can't pin the workflow to chat-only on open.
          const persistedShowChatArea = true
          const persistedShowWorkspacePane = true
          // Always open report-large (laptop view). The chat-focus (mobile)
          // state is transient within a session — never remembered across opens,
          // so a workflow always greets you with the report front-and-centre.
          const persistedFocusedPane: FocusedPane = 'preview'
          // If the user left on Plan or Report, that is the last canvas view by
          // definition; otherwise take the persisted one (if any).
          const restoredLastCanvasView: CanvasViewId | undefined =
            isCanvasView(persistedWorkspaceView)
              ? persistedWorkspaceView
              : persistedUIState.lastCanvasView
          set({
            // Reset workflow context (loaded from API, not per-preset)
            runFolders: [],
            variablesManifest: null,
            // Restore per-preset state
            selectedRunFolder: restored.selectedRunFolder,
            activePhase: restored.activePhase,
            showChatArea: persistedShowChatArea,
            showWorkspacePane: persistedShowWorkspacePane,
            chatAreaExpanded: restored.chatAreaExpanded,
            workflowWorkspaceView: persistedWorkspaceView,
            focusedPane: persistedFocusedPane,
            ...(restoredLastCanvasView ? { lastCanvasView: restoredLastCanvasView } : {}),
            workflowChatTabs: restored.workflowChatTabs,
            activeWorkflowTabId: restored.activeWorkflowTabId,
            selectedGroupIds: restored.selectedGroupIds,
            currentRunningGroupId: restored.currentRunningGroupId,
            workshopMode: restoredWorkshopMode,
            workflowMode: 'plan',
            _currentPresetId: presetId
          } as Partial<WorkflowStore>)

          // Only clear localStorage when there's no saved state (first time visiting this preset)
          if (!savedState) {
            try {
              removeWorkflowStorageItem(SELECTED_GROUP_IDS_KEY)
              removeWorkflowStorageItem(CURRENT_RUNNING_GROUP_ID_KEY)
              removeWorkflowStorageItem(SELECTED_RUN_FOLDER_KEY)
            } catch (error) {
              console.error('[WorkflowStore] Failed to clear group localStorage on preset switch:', error)
            }
          }
          console.timeEnd(`[WorkflowStore] switchToPreset-${presetId?.slice(0,8)}`)
        } catch (error) {
          console.error('[WorkflowStore] Failed to switch preset:', error)
        }
      },

      // Reset execution state (called when switching workflows)
      resetExecutionState: () => {
        // Note: We don't reset showChatArea or activePhase - these persist or are loaded per-preset

        set({
          runFolders: [],
          selectedRunFolder: 'new',
          variablesManifest: null,
          selectedGroupIds: [],
          currentRunningGroupId: null,
          workflowWorkspaceView: null,
          // activePhase is saved/loaded per-preset, don't reset here
          workflowChatTabs: {},
          activeWorkflowTabId: null
        })

        // Clear group-related localStorage when resetting execution state
        try {
          removeWorkflowStorageItem(SELECTED_GROUP_IDS_KEY)
          removeWorkflowStorageItem(CURRENT_RUNNING_GROUP_ID_KEY)
          removeWorkflowStorageItem(SELECTED_RUN_FOLDER_KEY)
        } catch (error) {
          console.error('[WorkflowStore] Failed to clear group localStorage on reset:', error)
        }
      },

    })
)

// Selector hooks for common patterns
export const useWorkflowPhases = () => useWorkflowStore(state => state.phases)
export const useWorkflowPhasesLoading = () => useWorkflowStore(state => state.isLoadingPhases)
export const useWorkflowRunFolders = () => useWorkflowStore(state => state.runFolders)

// Legacy selector removed - use selectedGroupIds array instead
// Group selection is now exclusively managed via checkbox multi-select (selectedGroupIds)

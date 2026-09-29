import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import type { AgentMode } from './types'
import { useModeStore, type ModeCategory } from './useModeStore'
import { getWorkspaceScopedStorageKey } from './useWorkspaceConnectionStore'

interface AppState {
  // Agent configuration
  agentMode: AgentMode
  
  // Chat session state
  currentQuery: string
  selectedPresetId: string | null
  
  // UI state
  workspaceMinimized: boolean
  workspaceMinimizedByMode: Record<'workflow' | 'multi-agent', boolean>
  showWorkflowsOverview: boolean
  showSchedulesOverview: boolean
  /** Admin full pages opened from the top bar (Users & access, Connect an AI agent). */
  adminPage: 'users' | 'mcp' | null
  activityWorkflowPath: string | null
  
  // Code execution mode (for multi-agent mode when no preset is active)
  useCodeExecutionMode: boolean

  // Actions
  setAgentMode: (mode: AgentMode) => void
  
  // Mode category helpers
  getModeCategory: () => ModeCategory
  setModeCategory: (category: ModeCategory) => void
  requiresNewChat: boolean
  clearRequiresNewChat: () => void
  
  // Chat actions
  setCurrentQuery: (query: string) => void
  setSelectedPresetId: (id: string | null) => void
  
  // UI actions
  setWorkspaceMinimized: (minimized: boolean) => void
  setWorkspaceMinimizedForLayout: (minimized: boolean) => void
  setShowWorkflowsOverview: (show: boolean) => void
  setShowSchedulesOverview: (show: boolean) => void
  setAdminPage: (page: 'users' | 'mcp' | null) => void
  setActivityWorkflowPath: (workspacePath: string | null) => void
  setUseCodeExecutionMode: (enabled: boolean) => void
  // Last-used tab settings — inherited by new tabs
  lastSelectedSkills: string[]
  lastBrowserMode: 'none' | 'auto' | 'headless' | 'cdp'
  syncLastTabSettings: (update: Partial<Pick<AppState, 'lastSelectedSkills' | 'lastBrowserMode'>>) => void
}

export const useAppStore = create<AppState>()(
  persist(
    (set, get) => {
      // Sync flag to prevent circular updates
      let isSyncing = false

      return {
        // Initial state
        agentMode: 'multi-agent',
        requiresNewChat: false,
        currentQuery: '',
        selectedPresetId: null,
        workspaceMinimized: false,
        workspaceMinimizedByMode: {
          workflow: false,
          'multi-agent': false,
        },
        showWorkflowsOverview: false,
        showSchedulesOverview: false,
        adminPage: null,
        activityWorkflowPath: null,
        useCodeExecutionMode: true, // Default to enabled
        // Actions
        setAgentMode: (mode) => {
          const currentMode = get().agentMode

          // Only update if mode actually changed
          if (currentMode === mode) {
            return
          }

          set({
            agentMode: mode,
            requiresNewChat: currentMode !== mode
          })

          // Sync ModeStore category when agentMode changes
          // Only sync if not already syncing to prevent circular updates
          if (!isSyncing) {
            isSyncing = true
            const { getModeCategoryFromAgentMode, getAgentModeFromCategory, setModeCategory } = useModeStore.getState()
            const currentCategory = useModeStore.getState().selectedModeCategory

            // Don't override if the current category already maps to the same agent mode.
            // This prevents 'multi-agent' (which maps to 'simple') from being overwritten incorrectly.
            const currentCategoryAgentMode = currentCategory ? getAgentModeFromCategory(currentCategory) : null
            if (currentCategoryAgentMode !== mode) {
              const category = getModeCategoryFromAgentMode(mode)
              if (category && category !== currentCategory) {
                setModeCategory(category)
              }
            }
            isSyncing = false
          }
        },

        // Mode category helpers
        getModeCategory: () => {
          const { getModeCategoryFromAgentMode } = useModeStore.getState()
          return getModeCategoryFromAgentMode(get().agentMode)
        },

        // Simplified: Just delegate to ModeStore, which handles all synchronization
        setModeCategory: (category) => {
          useModeStore.getState().setModeCategory(category)
        },

        clearRequiresNewChat: () => {
          set({ requiresNewChat: false })
        },

        // Chat actions
        setCurrentQuery: (query) => {
          set({ currentQuery: query })
        },

        setSelectedPresetId: (id) => {
          set({ selectedPresetId: id })
        },

        // UI actions
        setWorkspaceMinimized: (minimized) => {
          const currentCategory = useModeStore.getState().selectedModeCategory
          set((state) => {
            const updatedByMode = { ...state.workspaceMinimizedByMode }
            if (currentCategory === 'workflow' || currentCategory === 'multi-agent') {
              updatedByMode[currentCategory] = minimized
            }
            return {
              workspaceMinimized: minimized,
              workspaceMinimizedByMode: updatedByMode,
            }
          })
        },

        setWorkspaceMinimizedForLayout: (minimized) => {
          set({ workspaceMinimized: minimized })
        },

        setShowWorkflowsOverview: (show) => {
          set(show ? { showWorkflowsOverview: true, adminPage: null } : { showWorkflowsOverview: false })
        },

        setShowSchedulesOverview: (show) => {
          set(show ? { showSchedulesOverview: true, adminPage: null } : { showSchedulesOverview: false })
        },

        // One full page at a time: opening an admin page closes the others.
        setAdminPage: (page) => {
          set(page ? { adminPage: page, showWorkflowsOverview: false, showSchedulesOverview: false } : { adminPage: null })
        },

        setActivityWorkflowPath: (workspacePath) => {
          set({ activityWorkflowPath: workspacePath })
        },

        setUseCodeExecutionMode: (enabled) => {
          set({ useCodeExecutionMode: enabled })
        },

        lastSelectedSkills: [],
        lastBrowserMode: 'auto',
        syncLastTabSettings: (update) => {
          set(update)
        },
      }
    },
    {
      name: getWorkspaceScopedStorageKey('app-store'),
      partialize: (state) => ({
        // Only persist user preferences and important state
        agentMode: state.agentMode,
        workspaceMinimized: state.workspaceMinimized,
        workspaceMinimizedByMode: state.workspaceMinimizedByMode,
        showWorkflowsOverview: state.showWorkflowsOverview,
        showSchedulesOverview: state.showSchedulesOverview,
        selectedPresetId: state.selectedPresetId,
        useCodeExecutionMode: state.useCodeExecutionMode,
        lastSelectedSkills: state.lastSelectedSkills,
        lastBrowserMode: state.lastBrowserMode
        // Note: requiresNewChat is not persisted as it's temporary state
        // File context is now mode-specific: multi-agent tabs have their own, workflow uses preset
      }),
      // Drop legacy `delegationMode` persisted from v2 and add per-mode workspace state.
      version: 7,
      migrate: (persistedState: unknown, _version: number) => {
        const state = persistedState as Record<string, unknown>
        delete state.delegationMode
        delete state.lastSelectedSubAgents
        if (state.showWorkflowsOverview === undefined) {
          state.showWorkflowsOverview = false
        }
        if (state.showSchedulesOverview === undefined) {
          state.showSchedulesOverview = false
        }
        if (!state.workspaceMinimizedByMode || typeof state.workspaceMinimizedByMode !== 'object') {
          const legacyWorkspaceMinimized = Boolean(state.workspaceMinimized)
          state.workspaceMinimizedByMode = {
            workflow: legacyWorkspaceMinimized,
            'multi-agent': legacyWorkspaceMinimized,
          }
        }
        if (_version < 5) {
          const workspaceByMode = state.workspaceMinimizedByMode as Record<string, unknown>
          state.workspaceMinimizedByMode = {
            workflow: Boolean(workspaceByMode?.workflow),
            'multi-agent': false,
          }
          state.workspaceMinimized = false
        }
        if (_version < 7) {
          const workspaceByMode = state.workspaceMinimizedByMode as Record<string, unknown>
          state.workspaceMinimizedByMode = {
            workflow: Boolean(workspaceByMode?.workflow),
            'multi-agent': false,
          }
          state.workspaceMinimized = false
        }
        delete state.multiAgentRightPanelView
        return state as unknown as AppState
      }
    }
  )
)

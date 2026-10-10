import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import type { AgentMode } from './types'
import { getWorkspaceScopedStorageKey } from './useWorkspaceConnectionStore'

export type ModeCategory = 'workflow' | 'multi-agent' | null

interface ModeState {
  // Core mode selection
  selectedModeCategory: ModeCategory
  hasCompletedInitialSetup: boolean

  // Preset tracking per category
  lastSelectedPreset: {
    'workflow': string | null
  }

  // Actions
  setModeCategory: (category: ModeCategory) => void
  completeInitialSetup: () => void
  setLastPreset: (category: 'workflow', presetId: string | null) => void
  resetModeSelection: () => void

  // Helpers
  getModeCategoryFromAgentMode: (agentMode: string) => ModeCategory
  getAgentModeFromCategory: (category: ModeCategory) => string
}

export const useModeStore = create<ModeState>()(
    persist(
      (set, get) => {
        // Sync flag to prevent circular updates
        let isSyncing = false

        return {
          // Initial state
          selectedModeCategory: 'workflow',
          hasCompletedInitialSetup: true,
          lastSelectedPreset: {
            'workflow': null,
          },

          // Actions
          setModeCategory: (category) => {
            const normalizedCategory = category
            const currentCategory = get().selectedModeCategory

            // Only update if category actually changed
            if (currentCategory === normalizedCategory) {
              return
            }

            set({ selectedModeCategory: normalizedCategory })

            // On mode switch, clear the workspace activeFolder AND force a root
            // re-fetch. Otherwise the cached files from the previous category
            // (e.g. a workflow scope like "Workflow/customer-login/...") stay in
            // the store, and multi-agent's whitelist filter drops all of them →
            // "No files found".
            import('./useWorkspaceStore').then(({ useWorkspaceStore }) => {
              const ws = useWorkspaceStore.getState()
              ws.setActiveFolder(null)
              ws.fetchFiles(
                undefined,
                normalizedCategory === 'workflow' ? { force: true } : { force: true, maxDepth: 2 }
              ).catch(() => {})
            }).catch(() => {})

            // Automatically sync AppStore's agentMode when category changes
            // Use dynamic import to avoid circular dependency at module level
            if (!isSyncing) {
              isSyncing = true
              import('./useAppStore').then(({ useAppStore }) => {
                const { getAgentModeFromCategory } = get()
                const agentMode = getAgentModeFromCategory(normalizedCategory)
                const appStore = useAppStore.getState()

                if (normalizedCategory === 'workflow' || normalizedCategory === 'multi-agent') {
                  const workspaceByMode = appStore.workspaceMinimizedByMode ?? {
                    workflow: appStore.workspaceMinimized,
                    'multi-agent': appStore.workspaceMinimized,
                  }
                  const nextWorkspaceMinimized = workspaceByMode[normalizedCategory]
                  if (appStore.workspaceMinimized !== nextWorkspaceMinimized) {
                    useAppStore.setState({ workspaceMinimized: nextWorkspaceMinimized })
                  }
                }

                // Only update if agentMode would be different
                if (appStore.agentMode !== agentMode) {
              // Call setAgentMode to sync AppStore
              appStore.setAgentMode(agentMode as AgentMode)
                }
                isSyncing = false
              }).catch(() => {
                isSyncing = false
              })
            }
          },

          completeInitialSetup: () => {
            set({ hasCompletedInitialSetup: true })
          },

          setLastPreset: (category, presetId) => {
            set((state) => ({
              lastSelectedPreset: {
                ...state.lastSelectedPreset,
                [category]: presetId
              }
            }))
          },

          resetModeSelection: () => {
            set({
              selectedModeCategory: 'workflow',
              hasCompletedInitialSetup: true,
              lastSelectedPreset: {
                'workflow': null,
              }
            })
          },

          // Helpers
          getModeCategoryFromAgentMode: (agentMode) => {
            switch (agentMode) {
              case 'simple':
                return 'multi-agent'
              case 'workflow':
                return 'workflow'
              case 'multi-agent':
                return 'multi-agent'
              default:
                return null
            }
          },

          getAgentModeFromCategory: (category) => {
            switch (category) {
              case 'workflow':
                return 'workflow'
              case 'multi-agent':
                return 'simple'
              default:
                return 'simple'
            }
          }
        }
      },
      {
        name: getWorkspaceScopedStorageKey('mode-store'),
        version: 3,
        partialize: (state) => ({
          selectedModeCategory: state.selectedModeCategory,
          hasCompletedInitialSetup: state.hasCompletedInitialSetup,
          lastSelectedPreset: state.lastSelectedPreset
        }),
        migrate: (persistedState: unknown, version: number) => {
          const state = persistedState as Partial<ModeState>
          if (version < 3 && state.selectedModeCategory === 'multi-agent') {
            return { ...state, selectedModeCategory: 'workflow' }
          }
          return state
        }
      }
    )
)

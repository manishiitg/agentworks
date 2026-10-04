import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { secretsApi } from '../api/secrets'

export interface GlobalSecret {
  managed?: boolean
  name: string
}

export interface WorkflowSecret {
  name: string
  encrypted_value?: string
}

interface SecretsState {
  globalSecrets: GlobalSecret[]
  workflowSecretsByPath: Record<string, WorkflowSecret[]>
  // null = no global secrets selected (legacy), string[] = only these names selected
  selectedGlobalSecretNames: string[] | null
  fetchGlobalSecrets: () => Promise<void>
  fetchWorkflowSecrets: (workspacePath: string) => Promise<void>
  addWorkflowSecret: (workspacePath: string, name: string, encryptedValue: string) => Promise<void>
  removeWorkflowSecret: (workspacePath: string, name: string) => Promise<void>
  setSelectedGlobalSecretNames: (names: string[] | null) => void
}


export const useSecretsStore = create<SecretsState>()(
  persist(
    (set) => ({
      globalSecrets: [],
      workflowSecretsByPath: {},
      selectedGlobalSecretNames: [],

      fetchGlobalSecrets: async () => {
        try {
          const result = await secretsApi.getGlobalSecrets()
          set({ globalSecrets: result })
        } catch {
          set({ globalSecrets: [] })
          // Never keep a previous identity's inventory after access fails.
        }
      },

      fetchWorkflowSecrets: async (workspacePath) => {
        const trimmed = workspacePath.trim()
        if (!trimmed) return
        try {
          const result = await secretsApi.listWorkflowSecrets(trimmed)
          set((state) => ({
            workflowSecretsByPath: {
              ...state.workflowSecretsByPath,
              [trimmed]: result,
            },
          }))
        } catch {
          set((state) => ({
            workflowSecretsByPath: {
              ...state.workflowSecretsByPath,
              [trimmed]: [],
            },
          }))
        }
      },

      addWorkflowSecret: async (workspacePath, name, encryptedValue) => {
        const trimmed = workspacePath.trim()
        if (!trimmed) return
        await secretsApi.storeWorkflowSecret(trimmed, name, encryptedValue)
        set((state) => {
          const existing = state.workflowSecretsByPath[trimmed] || []
          const next = existing.some((s) => s.name === name)
            ? existing
            : [...existing, { name, encrypted_value: encryptedValue }].sort((a, b) => a.name.localeCompare(b.name))
          return {
            workflowSecretsByPath: {
              ...state.workflowSecretsByPath,
              [trimmed]: next,
            },
          }
        })
      },

      removeWorkflowSecret: async (workspacePath, name) => {
        const trimmed = workspacePath.trim()
        if (!trimmed) return
        await secretsApi.deleteWorkflowSecret(trimmed, name)
        set((state) => ({
          workflowSecretsByPath: {
            ...state.workflowSecretsByPath,
            [trimmed]: (state.workflowSecretsByPath[trimmed] || []).filter((s) => s.name !== name),
          },
        }))
      },

      setSelectedGlobalSecretNames: (names) => {
        set({ selectedGlobalSecretNames: names })
      },

    }),
    {
      name: 'secrets-store',
      // Only the global selection persists. Secret values live server-side and
      // are refetched on load; names attach per workflow, crew, or chat.
      partialize: (state) => ({
        selectedGlobalSecretNames: state.selectedGlobalSecretNames,
      }),
    }
  )
)

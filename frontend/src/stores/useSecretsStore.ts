import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { secretsApi } from '../api/secrets'

export interface GlobalSecret {
  managed?: boolean
  name: string
}

interface SecretsState {
  globalSecrets: GlobalSecret[]
  // null = no Vault secrets selected (legacy), string[] = only these names selected
  selectedGlobalSecretNames: string[] | null
  fetchGlobalSecrets: () => Promise<void>
  setSelectedGlobalSecretNames: (names: string[] | null) => void
}


export const useSecretsStore = create<SecretsState>()(
  persist(
    (set) => ({
      globalSecrets: [],
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

      setSelectedGlobalSecretNames: (names) => {
        set({ selectedGlobalSecretNames: names })
      },

    }),
    {
      name: 'secrets-store',
      // Only the selection persists. Secret values live in Vault and are
      // refetched on load; names attach per workflow, crew, or chat.
      partialize: (state) => ({
        selectedGlobalSecretNames: state.selectedGlobalSecretNames,
      }),
    }
  )
)

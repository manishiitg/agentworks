import { create } from 'zustand'

export type AppearanceProfile = 'default' | 'clear-dark'
const KEY = 'agentworks-appearance'
const readProfile = (): AppearanceProfile => {
  try { return localStorage.getItem(KEY) === 'clear-dark' ? 'clear-dark' : 'default' } catch { return 'default' }
}

/** Device appearance only: never changes a coding program, model or conversation. */
export const useAppearanceStore = create<{ profile: AppearanceProfile; setProfile: (profile: AppearanceProfile) => void }>(set => ({
  profile: readProfile(),
  setProfile: profile => {
    try { localStorage.setItem(KEY, profile) } catch { /* The current view can still change. */ }
    set({ profile })
  },
}))

export const CLEAR_DARK_FONT = '"AgentWorks SF Mono Terminal", "SF Mono Terminal", "SFMonoTerminal-Regular", "SFMono-Regular", "SF Mono", Menlo, Monaco, Consolas, monospace'

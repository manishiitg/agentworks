import { create } from 'zustand'

export type AppearanceProfile = 'default' | 'clear-dark' | 'clear-light'
const KEY = 'agentworks-appearance'
const readProfile = (): AppearanceProfile => {
  try { const saved = localStorage.getItem(KEY); return saved === 'clear-dark' || saved === 'clear-light' ? saved : 'default' } catch { return 'default' }
}

/** Device appearance only: never changes a coding program, model or conversation. */
export const useAppearanceStore = create<{ profile: AppearanceProfile; setProfile: (profile: AppearanceProfile) => void }>(set => ({
  profile: readProfile(),
  setProfile: profile => {
    try { localStorage.setItem(KEY, profile) } catch { /* The current view can still change. */ }
    set({ profile })
  },
}))

export const CLEAR_MONO_FONT = '"AgentWorks SF Mono Terminal", "SF Mono Terminal", "SFMonoTerminal-Regular", "SFMono-Regular", "SF Mono", Menlo, Monaco, Consolas, monospace'

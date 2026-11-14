import { create } from 'zustand'

export type AppearanceProfile = 'dark' | 'light'
const KEY = 'agentworks-appearance'
const readProfile = (): AppearanceProfile => {
  try { const saved = localStorage.getItem(KEY); return saved === 'light' ? 'light' : 'dark' } catch { return 'dark' }
}

/** Device appearance only: never changes a coding program, model or conversation. */
export const useAppearanceStore = create<{ profile: AppearanceProfile; setProfile: (profile: AppearanceProfile) => void }>(set => ({
  profile: readProfile(),
  setProfile: profile => {
    try { localStorage.setItem(KEY, profile) } catch { /* The current view can still change. */ }
    set({ profile })
  },
}))

export const APPLE_MONO_FONT = '"AgentWorks SF Mono Terminal", "SF Mono Terminal", "SFMonoTerminal-Regular", "SFMono-Regular", "SF Mono", Menlo, Monaco, Consolas, monospace'

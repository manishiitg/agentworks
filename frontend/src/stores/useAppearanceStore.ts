import { create } from 'zustand'
import api from '../services/api'
import { defaultAppearance, validateTheme, type AppearanceDocument, type AppearanceProfile, type AppearanceTheme } from './appearanceTheme'
export { APPLE_MONO_FONT, codeFontFamily } from './appearanceTheme'
export type { AppearanceProfile } from './appearanceTheme'

type Operation = 'read' | 'apply' | 'save' | 'delete' | 'reset'
type State = AppearanceDocument & {
  profile: AppearanceProfile; preview: AppearanceTheme | null; scope: string; loaded: boolean; saving: boolean; error: string
  setProfile: (profile: AppearanceProfile) => void
  setPreview: (theme: AppearanceTheme | null) => void
  load: (scope: string) => Promise<void>
  change: (operation: Operation, theme?: AppearanceTheme, name?: string) => Promise<boolean>
  resetScope: (scope: string) => void
}
export const useAppearanceStore = create<State>((set, get) => ({
  current: defaultAppearance(), saved: [], profile: 'dark', preview: null, scope: '', loaded: false, saving: false, error: '',
  resetScope: scope => set({ scope, current: defaultAppearance(), saved: [], preview: null, profile: 'dark', loaded: false, saving: false, error: '' }),
  setPreview: preview => set({ preview, profile: (preview ?? get().current).mode, error: '' }),
  setProfile: profile => { void get().change('apply', { ...(get().preview ?? get().current), mode: profile }) },
  load: async scope => {
    try {
      const { data } = await api.get<AppearanceDocument>('/api/appearance', { skipSessionContext: true })
      if (get().scope !== scope || get().preview || get().saving) return
      const current = validateTheme(data.current)
      const saved = data.saved.map(validateTheme)
      set({ current, saved, profile: current.mode, loaded: true, error: '' })
    } catch { if (get().scope === scope) set({ error: 'Could not load your appearance. The server may need an update; try again.', loaded: false }) }
  },
  change: async (operation, theme, name) => {
    if (get().saving) return false
    const scope = get().scope
    if (theme) set({ preview: theme, profile: theme.mode })
    set({ saving: true, error: '' })
    try {
      const { data } = await api.put<AppearanceDocument>('/api/appearance', { operation, ...(theme ? { theme } : {}), ...(name ? { name } : {}) }, { skipSessionContext: true })
      if (get().scope !== scope) return false
      const current = validateTheme(data.current)
      set({ current, saved: data.saved.map(validateTheme), profile: current.mode, preview: null, loaded: true, saving: false })
      return true
    } catch {
      if (get().scope === scope) set({ saving: false, error: 'Appearance was not saved. Your preview is still visible; retry or discard it.' })
      return false
    }
  },
}))

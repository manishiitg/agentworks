import { create } from 'zustand'
import type { BackgroundScene } from '../components/appearance/backgroundScenes'
import api from '../services/api'
import { defaultAppearance, validateTheme, type AppearanceDocument, type AppearanceProfile, type AppearanceTheme } from './appearanceTheme'
export { APPLE_MONO_FONT, codeFontFamily } from './appearanceTheme'
export type { AppearanceProfile } from './appearanceTheme'

let requestVersion = 0

/** The server's document with any theme that no longer validates left out: one bad saved theme must not hide the rest. */
function readDocument(data: AppearanceDocument): AppearanceDocument {
  const valid = (value: unknown): AppearanceTheme[] => { try { return [validateTheme(value)] } catch { return [] } }
  return { current: valid(data.current)[0] ?? defaultAppearance(), saved: (Array.isArray(data.saved) ? data.saved : []).flatMap(valid) }
}

type Operation = 'read' | 'apply' | 'save' | 'delete' | 'reset'
type State = AppearanceDocument & {
  backgroundScene: BackgroundScene; profile: AppearanceProfile; preview: AppearanceTheme | null; scope: string; loaded: boolean; saving: boolean; error: string
  setProfile: (profile: AppearanceProfile) => void
  setPreview: (theme: AppearanceTheme | null) => void
  load: (scope: string) => Promise<void>
  change: (operation: Operation, theme?: AppearanceTheme, name?: string) => Promise<boolean>
  resetScope: (scope: string) => void
}
export const useAppearanceStore = create<State>((set, get) => ({
  current: defaultAppearance(), saved: [], backgroundScene: 'none', profile: 'dark', preview: null, scope: '', loaded: false, saving: false, error: '',
  resetScope: scope => { requestVersion++; set({ backgroundScene: 'none', scope, current: defaultAppearance(), saved: [], preview: null, profile: 'dark', loaded: false, saving: false, error: '' }) },
  setPreview: preview => set({ preview, profile: (preview ?? get().current).mode, error: '' }),
  setProfile: profile => {
    const preview = get().preview
    if (preview) { get().setPreview({ ...preview, mode: profile }); return }
    void get().change('apply', { ...get().current, mode: profile })
  },
  load: async scope => {
    if (get().preview || get().saving) return
    const version = ++requestVersion
    try {
      const { data } = await api.get<AppearanceDocument>('/api/appearance', { skipSessionContext: true })
      if (get().scope !== scope || requestVersion !== version || get().preview || get().saving) return
      const { current, saved } = readDocument(data)
      set({ current, saved, profile: current.mode, loaded: true, error: '' })
    } catch { if (get().scope === scope && requestVersion === version) set({ error: 'Could not load your appearance. The server may need an update; try again.', loaded: false }) }
  },
  change: async (operation, theme, name) => {
    if (get().saving) return false
    const scope = get().scope
    const version = ++requestVersion
    if (theme) set({ preview: theme, profile: theme.mode })
    set({ saving: true, error: '' })
    try {
      const { data } = await api.put<AppearanceDocument>('/api/appearance', { operation, ...(theme ? { theme, replace: true } : {}), ...(name ? { name } : {}) }, { skipSessionContext: true })
      if (get().scope !== scope || requestVersion !== version) return false
      const { current, saved } = readDocument(data)
      set({ current, saved, profile: current.mode, preview: null, loaded: true, saving: false })
      return true
    } catch {
      // The look goes back to the saved one, so later changes (a teammate's agent's, or a retry) are not blocked by a stale preview.
      if (get().scope === scope && requestVersion === version) set({ saving: false, preview: null, profile: get().current.mode, error: 'Appearance was not saved, so your previous look is back. Try again.' })
      return false
    }
  },
}))

import { create } from 'zustand'
import api from '../services/api'
import { defaultAppearance, readTheme, type AppearanceDocument, type AppearanceRead, type AppearanceProfile, type AppearanceTheme } from './appearanceTheme'
export { APPLE_MONO_FONT, codeFontFamily } from './appearanceTheme'
export type { AppearanceProfile } from './appearanceTheme'

let requestVersion = 0

/** The server's document as sent: the server decides what a valid theme is. */
export function readDocument(data: AppearanceRead): AppearanceDocument & { preview: AppearanceTheme | null } {
  const saved = (Array.isArray(data.saved) ? data.saved : []).map(readTheme).filter((theme): theme is AppearanceTheme => !!theme)
  return { current: readTheme(data.current) ?? defaultAppearance(), saved, guide: data.guide, preview: readTheme(data.preview) }
}

/** What the running background reports (#3085): kind, state and a measured frame rate. */
export type AppearanceBackgroundStatus = { kind: 'script' | 'shader' | 'video' | 'image' | 'none'; state: 'running' | 'throttled' | 'stopped' | 'paused' | 'error'; fps?: number; error?: string }
/** A look shown only on this page (the theme preview page), never saved. */
export type AppearanceDisplay = { theme: AppearanceTheme; mode: AppearanceProfile }
/** The document is large (saved themes with CSS); a refocus within this time reuses what is loaded. A live change still reloads. */
const FRESH_MS = 60_000

type Operation = 'read' | 'apply' | 'save' | 'delete' | 'reset'
type State = AppearanceDocument & {
  profile: AppearanceProfile; preview: AppearanceTheme | null; display: AppearanceDisplay | null; background: AppearanceBackgroundStatus | null; scope: string; loaded: boolean; loadedAt: number; saving: boolean; error: string
  setProfile: (profile: AppearanceProfile) => void
  setPreview: (theme: AppearanceTheme | null) => void
  setDisplay: (display: AppearanceDisplay | null) => void
  setBackground: (background: AppearanceBackgroundStatus | null) => void
  load: (scope: string, options?: { force?: boolean }) => Promise<void>
  change: (operation: Operation, theme?: Partial<AppearanceTheme>, name?: string, replace?: boolean) => Promise<boolean>
  resetScope: (scope: string) => void
}
/** The look on screen: this page's own display, then an unsaved change, then the saved look. */
export const shownTheme = (state: Pick<State, 'display' | 'preview' | 'current'>) => state.display?.theme ?? state.preview ?? state.current
export const shownMode = (state: Pick<State, 'display' | 'profile'>) => state.display?.mode ?? state.profile

export const useAppearanceStore = create<State>((set, get) => ({
  current: defaultAppearance(), saved: [], profile: 'dark', preview: null, display: null, background: null, scope: '', loaded: false, loadedAt: 0, saving: false, error: '',
  resetScope: scope => { requestVersion++; set({ scope, current: defaultAppearance(), saved: [], preview: null, display: null, profile: 'dark', loaded: false, loadedAt: 0, saving: false, error: '', guide: undefined }) },
  setPreview: preview => set({ preview, profile: (preview ?? get().current).mode, error: '' }),
  setDisplay: display => set({ display }),
  setBackground: background => {
    const now = get().background
    if (JSON.stringify(now) !== JSON.stringify(background)) set({ background })
  },
  setProfile: profile => {
    const preview = get().preview
    if (preview) { get().setPreview({ ...preview, mode: profile }); return }
    void get().change('apply', { ...get().current, mode: profile })
  },
  load: async (scope, options) => {
    if (get().preview || get().saving) return
    if (!options?.force && get().loaded && Date.now() - get().loadedAt < FRESH_MS) return
    const version = ++requestVersion
    try {
      const { data } = await api.get<AppearanceDocument>('/api/appearance', { skipSessionContext: true })
      if (get().scope !== scope || requestVersion !== version || get().preview || get().saving) return
      const { current, saved, guide } = readDocument(data)
      set({ current, saved, guide, profile: current.mode, loaded: true, loadedAt: Date.now(), error: '' })
    } catch { if (get().scope === scope && requestVersion === version) set({ error: 'Could not load your appearance. The server may need an update; try again.', loaded: false }) }
  },
  change: async (operation, theme, name, replace = true) => {
    if (get().saving) return false
    const scope = get().scope
    const version = ++requestVersion
    if (theme) {
      const preview = { ...(replace ? defaultAppearance() : get().current), ...theme }
      set({ preview, profile: preview.mode })
    }
    set({ saving: true, error: '' })
    try {
      const { data } = await api.put<AppearanceDocument>('/api/appearance', { operation, ...(theme ? { theme, replace } : {}), ...(name ? { name } : {}) }, { skipSessionContext: true })
      if (get().scope !== scope || requestVersion !== version) return false
      const { current, saved, guide } = readDocument(data)
      set({ current, saved, guide, profile: current.mode, preview: null, loaded: true, loadedAt: Date.now(), saving: false })
      return true
    } catch {
      // The look goes back to the saved one, so later changes (a teammate's agent's, or a retry) are not blocked by a stale preview.
      if (get().scope === scope && requestVersion === version) set({ saving: false, preview: null, profile: get().current.mode, error: 'Appearance was not saved, so your previous look is back. Try again.' })
      return false
    }
  },
}))

import { create } from 'zustand'
import { createJSONStorage, persist } from 'zustand/middleware'

export const PROJECT_MENU_RECENT_LIMIT = 10
export const projectMenuHistoryScope = (server: string, user: string, product: string, project: string) => JSON.stringify([server, user, product, project])

/** Presentation history only. The current destination inventory decides access. */
export const useProjectMenuHistoryStore = create<{
  recent: Record<string, string[]>
  visit: (scope: string, destination: string) => void
}>()(persist(set => ({
  recent: {},
  visit: (scope, destination) => set(state => {
    if (!scope || !destination || state.recent[scope]?.[0] === destination) return state
    return { recent: { ...state.recent, [scope]: [destination, ...(state.recent[scope] || []).filter(id => id !== destination)].slice(0, PROJECT_MENU_RECENT_LIMIT) } }
  }),
}), {
  name: 'project-menu-history-v1',
  storage: createJSONStorage(() => ({
    getItem: key => { try { return window.localStorage.getItem(key) } catch { return null } },
    setItem: (key, value) => { try { window.localStorage.setItem(key, value) } catch { /* History is optional. */ } },
    removeItem: key => { try { window.localStorage.removeItem(key) } catch { /* History is optional. */ } },
  })),
  partialize: state => ({ recent: state.recent }),
}))

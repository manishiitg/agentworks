import { create } from 'zustand'
import { createJSONStorage, persist } from 'zustand/middleware'

export const PROJECT_MENU_RECENT_LIMIT = 10
export const projectMenuHistoryScope = (server: string, user: string, product: string, project: string) => JSON.stringify([server, user, product, project])

/** The local account owns cross-server navigation history, including after a renderer reload. */
export function connectedWorkHistoryScope(): string {
  let owner = ''
  try { owner = JSON.parse(localStorage.getItem('auth-storage') || '{}').state?.user?.id || '' } catch { /* No saved owner yet. */ }
  return owner ? projectMenuHistoryScope('local', owner, 'connected-work', 'sidebar') : ''
}

/** Presentation history only. The current destination inventory decides access. */
export const useProjectMenuHistoryStore = create<{
  recent: Record<string, string[]>
  startHere: Record<string, string[]>
  pinned: Record<string, string[]>
  setPinned: (scope: string, destination: string, pinned: boolean) => void
  movePinned: (scope: string, destination: string, target: string) => void
  seedStartHere: (scope: string, destinations: string[]) => void
  visit: (scope: string, destination: string) => void
}>()(persist(set => ({
  recent: {},
  startHere: {},
  pinned: {},
  setPinned: (scope, destination, pinned) => set(state => {
    if (!scope || !destination) return state
    const previous = state.pinned[scope] || []
    if (previous.includes(destination) === pinned) return state
    return { pinned: { ...state.pinned, [scope]: pinned ? [...previous, destination] : previous.filter(id => id !== destination) } }
  }),
  movePinned: (scope, destination, target) => set(state => {
    const previous = state.pinned[scope] || []
    const from = previous.indexOf(destination)
    const to = previous.indexOf(target)
    if (!scope || from < 0 || to < 0 || from === to) return state
    const ordered = [...previous]
    ;[ordered[from], ordered[to]] = [ordered[to], ordered[from]]
    return { pinned: { ...state.pinned, [scope]: ordered } }
  }),
  seedStartHere: (scope, destinations) => set(state => {
    if (!scope || Object.hasOwn(state.startHere, scope)) return state
    return { startHere: { ...state.startHere, [scope]: [...new Set(destinations)].filter(id => id && !state.recent[scope]?.includes(id)).slice(0, PROJECT_MENU_RECENT_LIMIT) } }
  }),
  visit: (scope, destination) => set(state => {
    if (!scope || !destination || state.recent[scope]?.[0] === destination) return state
    return {
      recent: { ...state.recent, [scope]: [destination, ...(state.recent[scope] || []).filter(id => id !== destination)].slice(0, PROJECT_MENU_RECENT_LIMIT) },
      startHere: state.startHere[scope]?.includes(destination) ? { ...state.startHere, [scope]: state.startHere[scope].filter(id => id !== destination) } : state.startHere,
    }
  }),
}), {
  name: 'project-menu-history-v1',
  storage: createJSONStorage(() => ({
    getItem: key => { try { return window.localStorage.getItem(key) } catch { return null } },
    setItem: (key, value) => { try { window.localStorage.setItem(key, value) } catch { /* History is optional. */ } },
    removeItem: key => { try { window.localStorage.removeItem(key) } catch { /* History is optional. */ } },
  })),
  partialize: state => ({ recent: state.recent, startHere: state.startHere, pinned: state.pinned }),
}))

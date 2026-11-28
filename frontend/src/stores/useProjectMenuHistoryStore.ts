import { create } from 'zustand'
import { createJSONStorage, persist } from 'zustand/middleware'

export const PROJECT_MENU_RECENT_LIMIT = 10
export const projectMenuHistoryScope = (server: string, user: string, product: string, project: string) => JSON.stringify([server, user, product, project])

export interface SidebarSection {
  id: string
  name: string
  items: string[]
  collapsed?: boolean
}

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
  sections: Record<string, SidebarSection[]>
  setPinned: (scope: string, destination: string, pinned: boolean) => void
  movePinned: (scope: string, destination: string, target: string) => void
  createSection: (scope: string, name: string) => string
  renameSection: (scope: string, sectionId: string, name: string) => void
  deleteSection: (scope: string, sectionId: string) => void
  toggleSectionCollapsed: (scope: string, sectionId: string) => void
  moveSection: (scope: string, sectionId: string, offset: -1 | 1) => void
  assignProjectToSection: (scope: string, destination: string, sectionId: string | null) => void
  seedStartHere: (scope: string, destinations: string[]) => void
  visit: (scope: string, destination: string) => void
}>()(persist(set => ({
  recent: {},
  startHere: {},
  pinned: {},
  sections: {},
  setPinned: (scope, destination, pinned) => set(state => {
    if (!scope || !destination) return state
    const previous = state.pinned[scope] || []
    const nextSections = pinned && state.sections[scope]?.some(sec => sec.items.includes(destination))
      ? { ...state.sections, [scope]: state.sections[scope].map(sec => sec.items.includes(destination) ? { ...sec, items: sec.items.filter(id => id !== destination) } : sec) }
      : state.sections
    if (previous.includes(destination) === pinned && nextSections === state.sections) return state
    return {
      pinned: { ...state.pinned, [scope]: pinned ? (previous.includes(destination) ? previous : [...previous, destination]) : previous.filter(id => id !== destination) },
      sections: nextSections,
    }
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
  createSection: (scope, name) => {
    const trimmed = name.trim()
    if (!scope || !trimmed) return ''
    const id = `sec-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 6)}`
    set(state => ({
      sections: { ...state.sections, [scope]: [...(state.sections[scope] || []), { id, name: trimmed, items: [], collapsed: false }] },
    }))
    return id
  },
  renameSection: (scope, sectionId, name) => set(state => {
    const trimmed = name.trim()
    const list = state.sections[scope]
    if (!scope || !trimmed || !list) return state
    return { sections: { ...state.sections, [scope]: list.map(sec => sec.id === sectionId ? { ...sec, name: trimmed } : sec) } }
  }),
  deleteSection: (scope, sectionId) => set(state => {
    const list = state.sections[scope]
    if (!scope || !list) return state
    return { sections: { ...state.sections, [scope]: list.filter(sec => sec.id !== sectionId) } }
  }),
  toggleSectionCollapsed: (scope, sectionId) => set(state => {
    const list = state.sections[scope]
    if (!scope || !list) return state
    return { sections: { ...state.sections, [scope]: list.map(sec => sec.id === sectionId ? { ...sec, collapsed: !sec.collapsed } : sec) } }
  }),
  moveSection: (scope, sectionId, offset) => set(state => {
    const list = state.sections[scope]
    if (!scope || !list) return state
    const from = list.findIndex(sec => sec.id === sectionId)
    const to = from + offset
    if (from < 0 || to < 0 || to >= list.length) return state
    const ordered = [...list]
    ;[ordered[from], ordered[to]] = [ordered[to], ordered[from]]
    return { sections: { ...state.sections, [scope]: ordered } }
  }),
  assignProjectToSection: (scope, destination, sectionId) => set(state => {
    if (!scope || !destination) return state
    const list = state.sections[scope] || []
    const cleaned = list.map(sec => sec.items.includes(destination) ? { ...sec, items: sec.items.filter(id => id !== destination) } : sec)
    const nextList = sectionId ? cleaned.map(sec => sec.id === sectionId ? { ...sec, items: [...sec.items, destination] } : sec) : cleaned
    const nextPinned = sectionId && (state.pinned[scope] || []).includes(destination)
      ? { ...state.pinned, [scope]: (state.pinned[scope] || []).filter(id => id !== destination) }
      : state.pinned
    return { sections: { ...state.sections, [scope]: nextList }, pinned: nextPinned }
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
  partialize: state => ({ recent: state.recent, startHere: state.startHere, pinned: state.pinned, sections: state.sections }),
}))

import { create } from 'zustand'
import { createJSONStorage, persist } from 'zustand/middleware'

// Presentation preferences only. Never used to grant access or select a chat.
export const sidebarExpansionKey = (scope: string, ...parts: string[]) => JSON.stringify([scope, ...parts])

export const useSidebarExpansionStore = create<{
  expanded: Record<string, boolean>
  connectionNames: Record<string, string>
  renameConnection: (id: string, name: string) => void
  toggle: (key: string, initiallyExpanded?: boolean) => void
}>()(persist((set) => ({
  expanded: {},
  connectionNames: {},
  renameConnection: (id, name) => set(state => ({ connectionNames: { ...state.connectionNames, [id]: name.trim().slice(0, 24) } })),
  toggle: (key, initiallyExpanded = false) => set(state => ({ expanded: { ...state.expanded, [key]: !(state.expanded[key] ?? initiallyExpanded) } })),
}), {
  name: 'sidebar-expansion-v1',
  storage: createJSONStorage(() => ({
    getItem: key => { try { return window.localStorage.getItem(key) } catch { return null } },
    setItem: (key, value) => { try { window.localStorage.setItem(key, value) } catch { /* Keep the in-memory choice when storage is unavailable. */ } },
    removeItem: key => { try { window.localStorage.removeItem(key) } catch { /* Storage is optional. */ } },
  })),
  partialize: state => ({ expanded: state.expanded, connectionNames: state.connectionNames }),
}))

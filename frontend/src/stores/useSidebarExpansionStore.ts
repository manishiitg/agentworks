import { create } from 'zustand'
import { createJSONStorage, persist } from 'zustand/middleware'

// Presentation preferences only. Never used to grant access or select a chat.
export const sidebarExpansionKey = (scope: string, ...parts: string[]) => JSON.stringify([scope, ...parts])

export const useSidebarExpansionStore = create<{
  expanded: Record<string, boolean>
  toggle: (key: string) => void
}>()(persist((set) => ({
  expanded: {},
  toggle: (key) => set(state => ({ expanded: { ...state.expanded, [key]: !state.expanded[key] } })),
}), {
  name: 'sidebar-expansion-v1',
  storage: createJSONStorage(() => ({
    getItem: key => { try { return window.localStorage.getItem(key) } catch { return null } },
    setItem: (key, value) => { try { window.localStorage.setItem(key, value) } catch { /* Keep the in-memory choice when storage is unavailable. */ } },
    removeItem: key => { try { window.localStorage.removeItem(key) } catch { /* Storage is optional. */ } },
  })),
  partialize: state => ({ expanded: state.expanded }),
}))

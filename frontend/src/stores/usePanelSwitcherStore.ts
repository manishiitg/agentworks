import { useEffect, useRef } from 'react'
import { create } from 'zustand'
import type { ProductSurface } from '../products/productSurfaceConfig'

export interface PanelSwitcherPanel { id: string; label: string; group?: string }
interface PanelSwitcherEntry { panels: PanelSwitcherPanel[]; open: (id: string) => void }

interface PanelSwitcherState {
  /** Panels of each product's right-hand toolbar, registered while it is mounted. */
  entries: Partial<Record<ProductSurface, PanelSwitcherEntry>>
  register: (surface: ProductSurface, entry: PanelSwitcherEntry) => void
  unregister: (surface: ProductSurface, open: PanelSwitcherEntry['open']) => void
  /** The toolbar is hidden to an icon in every product (owner, 2026-10-07). */
  toolbarMinimized: boolean
  setToolbarMinimized: (minimized: boolean) => void
}

const MINIMIZED_KEY = 'workspace-toolbar-minimized'
const readMinimized = () => { try { return localStorage.getItem(MINIMIZED_KEY) === '1' } catch { return false } }

export const usePanelSwitcherStore = create<PanelSwitcherState>(set => ({
  entries: {},
  register: (surface, entry) => set(state => ({ entries: { ...state.entries, [surface]: entry } })),
  // Only the registration that is still current removes itself.
  unregister: (surface, open) => set(state => {
    if (state.entries[surface]?.open !== open) return state
    const entries = { ...state.entries }
    delete entries[surface]
    return { entries }
  }),
  toolbarMinimized: readMinimized(),
  setToolbarMinimized: minimized => {
    try { localStorage.setItem(MINIMIZED_KEY, minimized ? '1' : '0') } catch { /* per-viewer convenience only */ }
    set({ toolbarMinimized: minimized })
  },
}))

/** Registers a product toolbar's panels for the ⌘/Ctrl+J panel search. */
export function useRegisterPanelSwitcher(surface: ProductSurface | null | undefined, panels: PanelSwitcherPanel[], open: (id: string) => void) {
  const openRef = useRef(open)
  openRef.current = open
  const key = panels.map(panel => `${panel.id}:${panel.label}:${panel.group || ''}`).join('|')
  useEffect(() => {
    if (!surface) return
    const stableOpen = (id: string) => openRef.current(id)
    usePanelSwitcherStore.getState().register(surface, { panels, open: stableOpen })
    return () => usePanelSwitcherStore.getState().unregister(surface, stableOpen)
    // panels are captured by their content key
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [surface, key])
}

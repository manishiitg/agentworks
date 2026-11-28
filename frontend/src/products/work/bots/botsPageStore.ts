import { create } from 'zustand'

/** The Bots page is open (the left menu's Bots row, #4545), optionally on one bot. It closes when another place opens. */
export const useBotsPageStore = create<{ open: boolean; selected: string | null; setOpen: (open: boolean) => void; show: (bot?: string) => void }>(set => ({
  open: false,
  selected: null,
  setOpen: open => set({ open }),
  show: bot => set({ open: true, selected: bot ?? null }),
}))

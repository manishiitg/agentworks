import { create } from 'zustand'

/** The Bots page is open (the left menu's Bots row, #4545). It closes when another place opens. */
export const useBotsPageStore = create<{ open: boolean; setOpen: (open: boolean) => void }>(set => ({ open: false, setOpen: open => set({ open }) }))

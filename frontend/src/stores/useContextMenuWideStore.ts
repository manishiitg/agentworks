import { create } from 'zustand'

/** The project menu card grows while it shows Access (owner, 3 Nov: "same this just gets bigger"); screen-only. */
export const useContextMenuWideStore = create<{ wide: boolean; setWide: (wide: boolean) => void }>(set => ({
  wide: false,
  setWide: wide => set({ wide }),
}))

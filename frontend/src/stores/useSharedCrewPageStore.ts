import { create } from 'zustand'

/** The Crew someone shared agents with you from (by its workspace id), shown in place of the page; screen-only. */
export const useSharedCrewPageStore = create<{ workspaceId: string | null; open: (workspaceId: string | null) => void }>(set => ({
  workspaceId: null,
  open: workspaceId => set({ workspaceId }),
}))

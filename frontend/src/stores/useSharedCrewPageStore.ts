import { create } from 'zustand'

/** The Crew someone shared agents with you from (by its workspace id), shown in place of the page, and the agent open
 * on it (null = its first agent); screen-only. */
export const useSharedCrewPageStore = create<{ workspaceId: string | null; chatId: string | null; open: (workspaceId: string | null, chatId?: string | null) => void }>(set => ({
  workspaceId: null,
  chatId: null,
  open: (workspaceId, chatId = null) => set({ workspaceId, chatId: workspaceId ? chatId : null }),
}))

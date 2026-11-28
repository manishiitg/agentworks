import { create } from 'zustand'

/** Where a bot chat was opened from: the project it was added to (#4545). */
export type BotChatFrom = { projectId: string; title?: string; /** A project on this Mac. */ mac?: boolean }

/**
 * The Bots page (#4545), over the workspace. Without `chat` it is the company's directory of shared agents (the left
 * menu's Bots row). With `chat` it is your own private chat with one bot, opened from a project it was added to (that
 * project's agent list or right menu). It closes when another place opens.
 */
export const useBotsPageStore = create<{
  open: boolean; chat: string | null; from: BotChatFrom | null
  setOpen: (open: boolean) => void; show: () => void; openChat: (bot: string, from: BotChatFrom | null) => void
}>(set => ({
  open: false, chat: null, from: null,
  setOpen: open => set(open ? { open } : { open, chat: null, from: null }),
  show: () => set({ open: true, chat: null, from: null }),
  openChat: (bot, from) => set({ open: true, chat: bot, from }),
}))

import { create } from 'zustand'

/** Team invites waiting for an answer (owner, 15 Nov): their count badges the closed left-menu button, and minimized
 * invites stay in the left menu only. Kept in memory, so a restart shows the popup again; a new invite always does. */
export const useTeamInviteNotice = create<{ count: number; minimized: Record<string, true> }>(() => ({ count: 0, minimized: {} }))

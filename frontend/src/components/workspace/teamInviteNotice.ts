import { create } from 'zustand'

// The always-mounted app-shell observer supplies the pending count. Minimize
// lives with that observer, scoped to the current account/network and launch.
export const useTeamInviteNotice = create<{ count: number }>(() => ({ count: 0 }))

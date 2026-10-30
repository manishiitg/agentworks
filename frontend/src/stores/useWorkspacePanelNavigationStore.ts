import { create } from 'zustand'
import { useAuthStore } from './useAuthStore'
import { useWorkspaceConnectionStore } from './useWorkspaceConnectionStore'

export interface WorkspacePanelBackAction {
  owner: symbol
  scope: string
  label: string
  onBack: () => void
}

type DraftRequest = Parameters<typeof import('../utils/workspacePaneChat').openWorkspacePaneChatWithDraft>[0]
type WithoutMessage<T> = T extends unknown ? Omit<T, 'message'> : never
export type WorkspacePanelChatTarget = WithoutMessage<DraftRequest>

export function useWorkspacePanelScope(workspacePath: string | null | undefined, view: string | undefined) {
  const account = useAuthStore(state => state.user?.id ?? null)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const origin = useWorkspaceConnectionStore(state => state.profiles.find(profile => profile.id === state.activeWorkspaceId)?.apiBaseUrl ?? '')
  return workspacePath && view ? JSON.stringify([server, origin, account, workspacePath.replace(/\/+$/, ''), view]) : ''
}

/** Ephemeral UI navigation only. It never changes chat state or permissions. */
export const useWorkspacePanelNavigationStore = create<{
  actions: WorkspacePanelBackAction[]
  register: (action: WorkspacePanelBackAction) => () => void
}>(set => ({
  actions: [],
  register: action => {
    set(state => ({ actions: [...state.actions.filter(item => item.owner !== action.owner), action] }))
    return () => set(state => ({ actions: state.actions.filter(item => item.owner !== action.owner) }))
  },
}))

export function panelBackAction<T extends { scope: string }>(actions: T[], scope: string): T | undefined {
  for (let index = actions.length - 1; index >= 0; index--) {
    if (actions[index].scope === scope) return actions[index]
  }
  return undefined
}

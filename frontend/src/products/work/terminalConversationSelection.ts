import { create } from 'zustand'
import { captureChatIdentity } from '../../utils/chatIdentity'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { openAppProduct } from '../../utils/appNavigation'
import type { TeamTarget } from './teamApi'

export type TerminalConversationSelection = {
  projectId: string; target: TeamTarget; agent: { id: string; name: string; provider?: string; provider_name?: string; address?: string }; owner: boolean
  // app: a teammate's app agent whose chat is hidden; it opens here too, so every teammate agent opens in the main chat.
  kind: 'terminal' | 'app'
  identity: number; workspace: string
}
// Navigation only, never a writable app chat or a persisted copy of native text.
export const useTerminalConversationSelection = create<{ selection?: TerminalConversationSelection }>(() => ({}))
export function closeTerminalConversation() { useTerminalConversationSelection.setState({ selection: undefined }) }
export function openTerminalConversation(projectId: string, target: TeamTarget, agent: { id: string; name: string; provider?: string; provider_name?: string; address?: string }, owner = false, kind: 'terminal' | 'app' = 'terminal') {
  openAppProduct({ surface: 'work', id: projectId })
  useTerminalConversationSelection.setState({ selection: { projectId, target, agent, owner, kind, identity: captureChatIdentity(), workspace: getRawActiveWorkspaceId() } })
  window.dispatchEvent(new Event('close-app-sidebar'))
  console.info('[TEAM_AGENT_CLICK]', { project: projectId, agent: agent.id, outcome: kind === 'app' ? 'hidden_chat_opened' : 'terminal_chat_opened' })
}

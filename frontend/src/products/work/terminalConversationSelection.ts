import { create } from 'zustand'
import { captureChatIdentity } from '../../utils/chatIdentity'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { openAppProduct } from '../../utils/appNavigation'
import type { TeamTarget } from './teamApi'

export type TerminalConversationSelection = {
  projectId: string; target: TeamTarget; agent: { id: string; name: string; provider?: string; provider_name?: string; address?: string }; owner: boolean
  identity: number; workspace: string
}
// Navigation only, never a writable app chat or a persisted copy of native text.
export const useTerminalConversationSelection = create<{ selection?: TerminalConversationSelection }>(() => ({}))
export function closeTerminalConversation() { useTerminalConversationSelection.setState({ selection: undefined }) }
export function openTerminalConversation(projectId: string, target: TeamTarget, agent: { id: string; name: string; provider?: string; provider_name?: string; address?: string }, owner = false) {
  openAppProduct({ surface: 'work', id: projectId })
  useTerminalConversationSelection.setState({ selection: { projectId, target, agent, owner, identity: captureChatIdentity(), workspace: getRawActiveWorkspaceId() } })
  window.dispatchEvent(new Event('close-app-sidebar'))
  console.info('[TEAM_AGENT_CLICK]', { project: projectId, agent: agent.id, outcome: 'terminal_chat_opened' })
}

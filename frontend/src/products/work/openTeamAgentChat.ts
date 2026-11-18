import { useChatStore } from '../../stores/useChatStore'
import { activateTab } from '../../utils/activateTab'
import { registerRemoteSession } from '../../utils/remoteSessions'
import { hydrateTabEvents } from '../../utils/sessionRestore'
import { teamAgentMyChat, type TeamAgentChat, type TeamTarget } from './teamApi'

export type TeamAgentChatOpen = { program?: string; canChangeProgram?: boolean }

/**
 * Opens a chat with the Team agent in the normal chat screen (#3084). Without a chat it opens the viewer's own one
 * ("My chat", from the server's my-chat route), which they write in; a teammate's chat (from the server's list of the
 * Team agent's chats) opens to read, with "Only <name> writes in it". The server decides who may open which; on a Mac
 * the reads go to the Team's server through the saved connection.
 */
export async function openTeamAgentChat(projectId: string, target: TeamTarget, chat?: TeamAgentChat, info: TeamAgentChatOpen = {}) {
  const store = useChatStore.getState()
  const tabs = Object.values(store.chatTabs)
  const main = tabs.find(row => row.metadata?.agentProfileProjectId === projectId && row.metadata?.agentProfileIsMainChat && !row.metadata?.isViewOnly)
  if (!main) { store.addToast('Open one of your agents in this Crew first.', 'info'); return }
  const mine = !chat || !!chat.you
  let session = chat?.session_id || ''
  let workspace: string | undefined
  try {
    if (mine) ({ session_id: session, workspace } = await teamAgentMyChat(target))
  } catch (error) {
    store.addToast(error instanceof Error ? error.message : 'Could not open the Team agent. Try again.', 'error')
    return
  }
  if (!session) return
  if (target.connection) registerRemoteSession(session, target.connection)
  console.info('[TEAM_AGENT_CHAT]', { project: projectId, event: 'opened', mine, session })
  const existing = tabs.find(row => row.sessionId === session && row.metadata?.teamAgentChat)
  const teamAgentChat = { project: target.project, connection: target.connection, mine, program: info.program, canChangeProgram: info.canChangeProgram }
  const tabId = existing?.tabId || await store.createChatTab(mine ? 'Team agent' : `${chat!.person}'s chat with the Team agent`, {
    ...main.metadata,
    agentProfileIsMainChat: false,
    agentProfileBuilder: false,
    agentProfileConversationKey: `${projectId}:history:${session}`,
    agentProfileConversationId: undefined,
    agentProfileCanOpenModels: false,
    // Kept in the tab strip like other opened views; "My chat" still has its chat box (teamAgentChat.mine).
    isViewOnly: true,
    viewOnlyWriter: mine ? undefined : chat!.person,
    teamAgentChat,
    readOnlyRestoredAt: Date.now(),
    userInteractiveContinuation: false,
  }, session)
  if (existing) store.setTabMetadata(tabId, { teamAgentChat })
  store.setTabCanSteer(tabId, false)
  await hydrateTabEvents(session, { workspacePath: workspace, fallbackToChatHistory: true, preferChatHistory: true }).catch(() => undefined)
  activateTab(tabId)
}

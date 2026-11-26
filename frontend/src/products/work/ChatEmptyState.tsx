import { useAuthStore } from '../../stores/useAuthStore'
import { useChatStore } from '../../stores/useChatStore'
import { teamAgentFor, teamAgentForTab, useTeamTab } from './teamApi'
import { ProjectWelcome, useProjectWelcome } from './ProjectWelcome'
import { CHAT_INSERT_QUOTE_EVENT, type InsertQuoteDetail } from '../../components/chat/ReplyQuote'

/** Puts a starter's request in this chat's message box, ready to finish and send. */
export function startInChat(tabId: string, text: string): boolean {
  const detail: InsertQuoteDetail = { tabId, text, plain: true }
  window.dispatchEvent(new CustomEvent(CHAT_INSERT_QUOTE_EVENT, { detail }))
  return Boolean(detail.handled)
}

/** The server's lines for a teammate's chat: private replaces it; "" for the viewer's own tabs. */
export function useChatShareLines(tabId?: string | null) {
  const tab = useChatStore(state => (tabId ? state.chatTabs[tabId] : undefined))
  const viewer = useAuthStore(state => state.user?.id)
  // Own schedule/run history is non-writable too; it is not a teammate chat.
  const watching = tab?.metadata?.agentProfileTabCanWrite === false && (!viewer || tab.metadata.agentProfileTabOwnerID !== viewer)
  const team = useTeamTab(watching ? tab?.metadata?.agentProfileProjectId : undefined)
  const agent = watching ? teamAgentFor(team.view?.agents, tab?.metadata?.agentProfileConversationKey) : undefined
  return { private: !watching ? '' : team.error ? 'Could not check access to this chat. Refresh the Team and try again.' : !team.view ? 'Checking access to this chat…' : !team.view.team.on || !agent?.in_team ? 'This agent’s chat is not available to you now.' : agent.chat_private_line || '' }
}

/** A new chat's light empty state (owner, 3 Nov): the tab's name and one line, plus, for your own agent In team, one
 * line saying the Team sees it (from the Team tab's server reading, the same words in the Mac app and on the website).
 * No buttons, pictures or examples unless the project has a welcome page (WELCOME.md); the first message replaces it. */
export function ChatEmptyState({ tabId }: { tabId?: string | null }) {
  const tab = useChatStore(state => (tabId ? state.chatTabs[tabId] : undefined))
  const main = tab?.metadata?.agentProfileIsMainChat !== false
  const own = tab?.metadata?.agentProfileTabCanWrite !== false
  const team = useTeamTab(tab?.metadata?.agentProfileProjectId)
  const agent = own ? teamAgentForTab(team, tab?.metadata?.agentProfileConversationKey, main, own) : undefined
  const shared = Boolean(team.view?.team.on && agent?.in_team)
  // The project's welcome page, when it has one, replaces the line in your own new chats (owner, 25 Nov).
  const welcome = useProjectWelcome(own ? tab?.metadata?.agentProfileProjectId : undefined)
  if (welcome && tabId) return <div className="flex flex-1 flex-col overflow-y-auto">
    <ProjectWelcome page={welcome} onStart={text => startInChat(tabId, text)} />
    {shared && <p className="pb-4 text-center text-xs text-muted-foreground">Shared with team · people in this project's Team can watch this agent</p>}
  </div>
  return <div data-theme-surface="empty" className="flex flex-1 flex-col items-center justify-center gap-1 p-6 text-center text-sm text-muted-foreground">
    {!main && tab?.name && <p className="font-medium text-foreground/70">{tab.name}</p>}
    <p>Send a message to start.</p>
    {shared && <p className="text-xs">Shared with team · people in this project's Team can watch this agent</p>}
  </div>
}

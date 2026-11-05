import { useChatStore } from '../../stores/useChatStore'
import { teamAgentForTab, useTeamTab } from './teamApi'

/** A new chat's light empty state (owner, 3 Nov): the tab's name and one line, plus, for your own agent In team, one
 * line saying the Team sees it (from the Team tab's server reading, the same words in the Mac app and on the website).
 * No buttons, pictures or examples; the first message replaces it. */
export function ChatEmptyState({ tabId }: { tabId?: string | null }) {
  const tab = useChatStore(state => (tabId ? state.chatTabs[tabId] : undefined))
  const main = tab?.metadata?.agentProfileIsMainChat !== false
  const own = tab?.metadata?.agentProfileTabCanWrite !== false
  const team = useTeamTab(tab?.metadata?.agentProfileProjectId)
  const agent = own ? teamAgentForTab(team, tab?.metadata?.agentProfileConversationKey, main, own) : undefined
  const shared = Boolean(team.view?.team.on && agent?.in_team)
  return <div className="flex flex-1 flex-col items-center justify-center gap-1 p-6 text-center text-sm text-muted-foreground">
    {!main && tab?.name && <p className="font-medium text-foreground/70">{tab.name}</p>}
    <p>Send a message to start.</p>
    {shared && <p className="text-xs">In team · people in this Crew's Team can watch this agent</p>}
  </div>
}

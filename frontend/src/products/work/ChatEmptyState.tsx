import { useChatStore } from '../../stores/useChatStore'
import { useMacTabShareLine } from './YourComputerPanel'

/** A new chat's light empty state (owner, 3 Nov): the tab's name and one line, plus who it is shared with. No buttons,
 * pictures or examples; the first message replaces it. Main shows only the line. */
export function ChatEmptyState({ tabId }: { tabId?: string | null }) {
  const tab = useChatStore(state => (tabId ? state.chatTabs[tabId] : undefined))
  const main = tab?.metadata?.agentProfileIsMainChat !== false
  const shared = useMacTabShareLine(tab?.metadata?.agentProfileProjectId, main ? undefined : tab?.metadata?.agentProfileConversationKey)
  return <div className="flex flex-1 flex-col items-center justify-center gap-1 p-6 text-center text-sm text-muted-foreground">
    {!main && tab?.name && <p className="font-medium text-foreground/70">{tab.name}</p>}
    <p>Send a message to start.</p>
    {shared && <p className="text-xs">{shared} · they see only their own messages</p>}
  </div>
}

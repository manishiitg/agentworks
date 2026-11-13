import { isOwnRecentAgent } from '../../utils/recentAgentVisits'
import { useChatStore } from '../../stores/useChatStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { connectedWorkHistoryScope, useProjectMenuHistoryStore } from '../../stores/useProjectMenuHistoryStore'
import { openAgentTab } from '../../utils/openAgentTab'
import { AgentMark } from '../chat/AgentIcon'
import { useAgentMenuState } from '../chat/agentMenuState'

function RecentAgent({ id, name, crew, selected }: { id: string; name: string; crew: string; selected: boolean }) {
  const state = useAgentMenuState(id)
  return <button type="button" title={`${name} · ${crew}`} aria-current={selected ? 'page' : undefined}
    onClick={() => { openAgentTab(id); window.dispatchEvent(new Event('close-app-sidebar')) }}
    className={`flex h-8 w-full min-w-0 items-center gap-2 rounded-lg px-3 text-left text-[13px] ${selected ? 'bg-foreground/[0.06] text-foreground' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}>
    <span className="truncate">{name}</span><span className="min-w-0 flex-1 truncate text-[11px] text-muted-foreground/70">· {crew}</span><AgentMark state={state} />
  </button>
}

/** Personal navigation IDs only; names always come from this account's current chat cache.
 * Only explicit agent selection records a visit, never streaming or schedule activity. */
export function RecentAgentChats() {
  const scope = connectedWorkHistoryScope()
  const recent = useProjectMenuHistoryStore(state => state.recentAgents[scope])
  const tabs = useChatStore(state => state.chatTabs)
  const active = useChatStore(state => state.activeTabId)
  const user = useAuthStore(state => state.user?.id)
  const visible = (recent || []).flatMap(id => {
    const tab = tabs[id], meta = tab?.metadata
    if (!isOwnRecentAgent(meta, user)) return []
    return [tab]
  }).slice(0, 8)
  return <section aria-label="Recent chats" className="mb-3 shrink-0">
    <h2 className="px-3 pb-1 pt-2 text-[11px] font-medium text-muted-foreground">Recent chats</h2>
    {visible.map(tab => <RecentAgent key={tab.tabId} id={tab.tabId} name={tab.metadata?.agentProfileConversationKey === tab.metadata?.agentProfileProjectId ? 'Main' : tab.name} crew={tab.metadata?.agentProfileProjectTitle || 'Crew'} selected={tab.tabId === active} />)}
    {!visible.length && <p className="px-3 py-1 text-xs text-muted-foreground/70">Chats you open will appear here.</p>}
  </section>
}

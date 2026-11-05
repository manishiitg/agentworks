import { useEffect, useRef, useState } from 'react'
import { ChevronRight, Eye, MoreHorizontal } from 'lucide-react'
import { AgentActionsMenu, AgentHoverCard, useAgentsViewStore, useCrewAgentsStore, type CrewAgentRow } from '../chat/AgentCard'
import { AGENT_KEY, AgentMark, AgentSharedMark } from '../chat/AgentIcon'
import { useAgentMenuState } from '../chat/agentMenuState'
import { useChatStore } from '../../stores/useChatStore'
import { activateTab } from '../../utils/activateTab'
import { OPEN_TEAM_BOARD_EVENT } from '../chat/ProjectWorkspaceTitle'
import { teamAgentStatus, useTeamAgents } from '../../products/work/useTeamAgents'

// The phone drawer closes once something is opened from it.
const closeDrawer = () => window.dispatchEvent(new Event('close-app-sidebar'))
const openTeamBoard = (projectId: string) => { window.dispatchEvent(new CustomEvent(OPEN_TEAM_BOARD_EVENT, { detail: { projectId } })); closeDrawer() }
const openAgent = (tabId: string) => { activateTab(tabId); closeDrawer() }

const personKey = (name: string) => name.trim().toLowerCase().split('@')[0]

/** The open Crew's agents in the app's left menu (owner, 4 Nov, "Left menu", the default): one row each with its
 * name (no icon, owner 5 Nov) and a mark at the end only when needed, the agent card on hover, and "…" to manage it. "+" is in the top line. */
export function SidebarCrewAgents({ projectId }: { projectId: string }) {
  const view = useAgentsViewStore(state => state.view)
  const crew = useCrewAgentsStore()
  const activeTabId = useChatStore(state => state.activeTabId)
  const [showHidden, setShowHidden] = useState(false)
  const [dragging, setDragging] = useState<string | null>(null)
  const peopleStatus = useTeamAgents(projectId)
  const tabs = useChatStore(state => state.chatTabs)
  const teamPeople = peopleStatus.filter(person => !person.you).map(person => person.name)
  // Every agent is listed, the open one highlighted, so the menu matches the top line (owner, 4 Nov: "show that on left also").
  const total = crew.agents.length + crew.closed.length
  if (view === 'tabs' || crew.projectId !== projectId || !crew.agents.length) return null
  // Hidden agents (unpinned, as on the tab bar) sit at the end; the open one always stays in view.
  const shown = crew.agents.filter(agent => agent.pinned !== false || agent.tabId === activeTabId)
  const hidden = crew.agents.filter(agent => agent.pinned === false && agent.tabId !== activeTabId)
  const row = (agent: CrewAgentRow) => <AgentRow key={agent.tabId} agent={agent} status={teamAgentStatus(peopleStatus, tabs[agent.tabId]?.metadata?.agentProfileTabOwnerID, tabs[agent.tabId]?.metadata?.agentProfileConversationKey)} active={agent.tabId === activeTabId} dragging={dragging} onDrag={setDragging} />
  // A shared room (owner, 5 Nov): with other people's agents here, the list is grouped by person, yours under "You".
  const people = [...new Set(shown.map(agent => agent.owner || ''))]
  // A Team member with nothing shared here still has a heading, so the team sees who is in it.
  const quiet = teamPeople.filter(name => !people.some(person => person && personKey(person) === personKey(name)))
  const grouped = people.length > 1 || quiet.length > 0
  return <div role="list" aria-label="Agents" className="ml-6 space-y-0.5 border-l border-border pb-1 pl-1.5">
    {grouped ? people.map(person => <div key={person || 'you'} role="group" aria-label={person ? `${person}'s agents` : 'Your agents'}>
      <p className="px-2 pb-0.5 pt-1.5 text-[11px] text-muted-foreground">{person || 'You'}</p>
      {shown.filter(agent => (agent.owner || '') === person).map(row)}
    </div>) : shown.map(row)}
    {quiet.map(name => <div key={`quiet:${name}`} role="group" aria-label={`${name}'s agents`}>
      <p className="px-2 pb-0.5 pt-1.5 text-[11px] text-muted-foreground">{name}</p>
      <p className="px-2 py-0.5 text-[11px] text-muted-foreground/70">No shared agents</p>
    </div>)}
    {hidden.length > 0 && <button type="button" aria-expanded={showHidden} onClick={() => setShowHidden(value => !value)}
      className="flex w-full items-center gap-1.5 rounded-md px-2 py-1 text-left text-xs text-muted-foreground hover:bg-muted hover:text-foreground max-md:min-h-10">
      <ChevronRight aria-hidden="true" className={`h-3 w-3 transition-transform ${showHidden ? 'rotate-90' : ''}`} />Hidden agents ({hidden.length})
    </button>}
    {showHidden && hidden.map(row)}
    <button type="button" onClick={() => openTeamBoard(projectId)}
      className="flex w-full items-center gap-1.5 rounded-md px-2 py-1 text-left text-xs text-muted-foreground hover:bg-muted hover:text-foreground max-md:min-h-10">All agents ({total})</button>
  </div>
}

function AgentRow({ agent, status, active, dragging, onDrag }: { agent: CrewAgentRow; status: string; active: boolean; dragging: string | null; onDrag: (key: string | null) => void }) {
  const state = useAgentMenuState(agent.tabId, agent.activity)
  const [menu, setMenu] = useState(false)
  const [over, setOver] = useState(false)
  const box = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!menu) return
    const close = (event: MouseEvent) => { if (!box.current?.contains(event.target as Node)) setMenu(false) }
    document.addEventListener('mousedown', close)
    return () => document.removeEventListener('mousedown', close)
  }, [menu])
  const actions = agent.actions
  const key = actions?.orderKey
  const hasActions = !!actions && Object.entries(actions).some(([name, value]) => typeof value === 'function' && name !== 'dropHere')
  // Drag a row onto another to change the order, saved the same way as the tab bar's.
  return <div ref={box} className={`group relative rounded-md ${over && dragging && dragging !== key ? 'ring-1 ring-inset ring-primary' : ''}`}
    draggable={!!key} onDragStart={key ? event => { event.dataTransfer.effectAllowed = 'move'; onDrag(key) } : undefined} onDragEnd={() => { onDrag(null); setOver(false) }}
    onDragOver={dragging && key ? event => { event.preventDefault(); setOver(true) } : undefined} onDragLeave={() => setOver(false)}
    onDrop={dragging && key ? event => { event.preventDefault(); setOver(false); if (dragging !== key) actions?.dropHere?.(dragging); onDrag(null) } : undefined}>
    <AgentHoverCard info={agent.info} shortcut={agent.shortcut}>
      <button type="button" role="listitem" aria-current={active ? 'page' : undefined} onClick={() => agent.open ? agent.open() : openAgent(agent.tabId)}
        className={`flex w-full min-w-0 items-center gap-2 rounded-md py-1 pl-2 pr-7 text-left text-[13px] max-md:min-h-10 max-md:pr-11 ${active ? 'bg-foreground/[0.06] font-medium text-foreground' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}>
        <span className="min-w-0 truncate">{agent.name}</span>
        <AgentSharedMark shared={agent.info?.sharedWith} />
        {status && <span className="shrink-0 text-[10px] text-muted-foreground">{status}</span>}
        {agent.owner && <Eye role="img" aria-label="Watching: only its owner types here" className="h-3 w-3 shrink-0 text-muted-foreground" />}
        <span className="flex-1" />
        {agent.shortcut && <span className="hidden shrink-0 text-[10px] text-muted-foreground group-hover:inline">{AGENT_KEY}{agent.shortcut}</span>}
        <AgentMark state={state} asking={agent.asking} />
      </button>
    </AgentHoverCard>
    {hasActions && <button type="button" aria-label={`More for ${agent.name}`} aria-expanded={menu} onClick={() => setMenu(value => !value)}
      className={`absolute right-1 top-1/2 h-6 w-6 -translate-y-1/2 place-items-center rounded text-muted-foreground hover:bg-muted hover:text-foreground max-md:h-9 max-md:w-9 ${menu ? 'grid' : 'hidden group-hover:grid group-focus-within:grid max-md:grid [@media(hover:none)]:grid'}`}>
      <MoreHorizontal aria-hidden="true" className="h-3.5 w-3.5" />
    </button>}
    {menu && actions && <div className="absolute right-0 top-full z-50 mt-1 w-44 rounded-xl border border-border bg-popover p-1 shadow-lg">
      <AgentActionsMenu name={agent.name} actions={actions} pinned={agent.pinned !== false} onDone={() => setMenu(false)} />
    </div>}
  </div>
}

import { useEffect, useRef, useState } from 'react'
import { ChevronRight, MoreHorizontal } from 'lucide-react'
import { AgentActionsMenu, AgentHoverCard, useCrewAgentsStore, type CrewAgentRow } from '../chat/AgentCard'
import { AGENT_KEY, AgentMark, AgentSetupMark, PersonPresenceDot } from '../chat/AgentIcon'
import { useAgentMenuState } from '../chat/agentMenuState'
import { useChatStore } from '../../stores/useChatStore'
import { openAgentTab } from '../../utils/openAgentTab'
import { teamAgentFor, useTeamTab } from '../../products/work/teamApi'
import { useAuthStore } from '../../stores/useAuthStore'

// The phone drawer closes once something is opened from it.
const closeDrawer = () => window.dispatchEvent(new Event('close-app-sidebar'))
const openAgent = (tabId: string) => { openAgentTab(tabId); closeDrawer() }

/** The open Crew's agents in the app's left menu: one row each with its
 * name (no icon, owner 5 Nov) and a mark at the end only when needed, the agent card on hover, and "…" to manage it. "+" is in the top line. */
export function SidebarCrewAgents({ projectId }: { projectId: string }) {
  const crew = useCrewAgentsStore()
  const activeTabId = useChatStore(state => state.activeTabId)
  const [showHidden, setShowHidden] = useState(false)
  const [showClosed, setShowClosed] = useState(false)
  const [dragging, setDragging] = useState<string | null>(null)
  // The Team tab's one server reading: each agent's availability and every person, by account, with their name.
  const team = useTeamTab(projectId).view
  const me = useAuthStore(state => state.user?.id)
  const tabs = useChatStore(state => state.chatTabs)
  const teamPeople = (team?.team.on ? team.people : []).filter(person => person.user_id !== me && person.note !== 'invited')
  if (crew.projectId !== projectId || !crew.agents.length) return null
  // Hidden agents (unpinned, as on the tab bar) sit at the end; the open one always stays in view.
  const shown = crew.agents.filter(agent => agent.pinned !== false || agent.tabId === activeTabId)
  const hidden = crew.agents.filter(agent => agent.pinned === false && agent.tabId !== activeTabId)
  const row = (agent: CrewAgentRow) => <AgentRow key={agent.tabId} agent={agent} status={agent.ownerId ? teamAgentFor(team?.agents, tabs[agent.tabId]?.metadata?.agentProfileConversationKey)?.status_label || '' : ''} active={agent.tabId === activeTabId} dragging={dragging} onDrag={setDragging} />
  // A shared room (owner, 5 Nov): with other people's agents here, the list is grouped by person (by account), yours
  // under "You", each heading the server's display name.
  const people = [...new Set(shown.map(agent => agent.ownerId || ''))]
  const nameOf = (id: string) => teamPeople.find(person => person.user_id === id)?.name || shown.find(agent => agent.ownerId === id)?.owner || 'Someone'
  const presenceOf = (id: string) => teamPeople.find(person => person.user_id === id)?.presence
  // A Team member with nothing shared here still has a heading, so the team sees who is in it.
  const quiet = teamPeople.filter(person => !people.includes(person.user_id))
  const grouped = people.length > 1 || quiet.length > 0
  return <div role="list" aria-label="Agents" className="ml-6 space-y-0.5 border-l border-border pb-1 pl-1.5">
    {grouped ? people.map(person => <div key={person || 'you'} role="group" aria-label={person ? `${nameOf(person)}'s agents` : 'Your agents'}>
      <p title={presenceOf(person)?.line} className="flex items-center gap-1.5 px-2 pb-0.5 pt-1.5 text-[11px] text-muted-foreground"><PersonPresenceDot presence={presenceOf(person)} />{person ? nameOf(person) : 'You'}</p>
      {shown.filter(agent => (agent.ownerId || '') === person).map(row)}
    </div>) : shown.map(row)}
    {quiet.map(({ user_id: id, name, presence }) => <div key={`quiet:${id}`} role="group" aria-label={`${name}'s agents`}>
      <p title={presence?.line} className="flex items-center gap-1.5 px-2 pb-0.5 pt-1.5 text-[11px] text-muted-foreground"><PersonPresenceDot presence={presence} />{name}</p>
      <p className="px-2 py-0.5 text-[11px] text-muted-foreground/70">No shared agents</p>
    </div>)}
    {hidden.length > 0 && <button type="button" aria-expanded={showHidden} onClick={() => setShowHidden(value => !value)}
      className="flex w-full items-center gap-1.5 rounded-md px-2 py-1 text-left text-xs text-muted-foreground hover:bg-muted hover:text-foreground max-md:min-h-10">
      <ChevronRight aria-hidden="true" className={`h-3 w-3 transition-transform ${showHidden ? 'rotate-90' : ''}`} />Hidden agents ({hidden.length})
    </button>}
    {showHidden && hidden.map(row)}
    {/* Closed agents (no tab here) open again from this list. */}
    {crew.closed.length > 0 && <button type="button" aria-expanded={showClosed} onClick={() => setShowClosed(value => !value)}
      className="flex w-full items-center gap-1.5 rounded-md px-2 py-1 text-left text-xs text-muted-foreground hover:bg-muted hover:text-foreground max-md:min-h-10">
      <ChevronRight aria-hidden="true" className={`h-3 w-3 transition-transform ${showClosed ? 'rotate-90' : ''}`} />Closed agents ({crew.closed.length})
    </button>}
    {showClosed && crew.closed.map(agent => <button key={agent.key} type="button" role="listitem" title={agent.purpose || undefined} onClick={() => { void agent.open(); closeDrawer() }}
      className="flex w-full min-w-0 items-center rounded-md py-1 pl-2 pr-7 text-left text-[13px] text-muted-foreground hover:bg-muted hover:text-foreground max-md:min-h-10">
      <span className="min-w-0 truncate">{agent.name}</span>
    </button>)}
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
      <button type="button" role="listitem" aria-current={active ? 'page' : undefined} onClick={() => openAgent(agent.tabId)}
        className={`flex w-full min-w-0 items-center gap-2 rounded-md py-1 pl-2 pr-7 text-left text-[13px] focus:outline-none focus-visible:ring-2 focus-visible:ring-ring max-md:min-h-10 max-md:pr-11 ${active ? 'bg-foreground/[0.06] font-medium text-foreground' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}>
        <span className="min-w-0 truncate">{agent.name}</span>
        <AgentSetupMark setup={agent.info?.setup} />
        {status && <span className="shrink-0 text-[10px] text-muted-foreground">{status}</span>}
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

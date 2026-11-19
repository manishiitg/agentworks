import { AgentInTeamMark, mostInTeam } from '../chat/AgentInTeamMark'
import { OwnTerminalAgents } from '../../products/work/OwnTerminalAgents'
import { useEffect, useRef, useState } from 'react'
import { ChevronRight, MoreHorizontal, GripVertical, MailOpen } from 'lucide-react'
import { AgentActionsMenu, AgentHoverCard, useCrewAgentsStore, type CrewAgentRow, type CrewClosedAgent } from '../chat/AgentCard'
import { AgentMark, AgentPlaceMark, AgentSetupMark, shortAgo } from '../chat/AgentIcon'
import { useAgentMenuState } from '../chat/agentMenuState'
import { useChatStore } from '../../stores/useChatStore'
import { openAgentTab } from '../../utils/openAgentTab'
import { teamAgentFor, useTeamTab } from '../../products/work/teamApi'

import { sidebarAgentDisclosureClass, sidebarAgentListClass, sidebarAgentRowClass } from './sidebarAgentLayout'

// The phone drawer closes once something is opened from it.
const closeDrawer = () => window.dispatchEvent(new Event('close-app-sidebar'))
const openAgent = (tabId: string) => { openAgentTab(tabId); closeDrawer() }

/** One shared list for the open Crew's sidebar and Agents popup: one row each with its name, left-aligned under the project
 * heading (no icon, owner 5 and 18 Nov; PLAT-746 left menu v2), its live state dot at the end, the agent card on hover, and "…"
 * to manage it, with creation in the project actions menu. */
export function SidebarCrewAgents({ projectId, popup = false, reordering = false, onSelect, onClosedOpened }: { projectId: string; popup?: boolean; reordering?: boolean; onSelect?: (tabId: string) => void; onClosedOpened?: () => void }) {
  const crew = useCrewAgentsStore()
  const activeTabId = useChatStore(state => state.activeTabId)
  const [showClosed, setShowClosed] = useState(false)
  const [dragging, setDragging] = useState<string | null>(null)
  // The Team tab's one server reading: each agent's availability and every person, by account, with their name.
  const team = useTeamTab(projectId).view
  const tabs = useChatStore(state => state.chatTabs)
  if (crew.projectId !== projectId || !crew.agents.length) return <OwnTerminalAgents projectId={projectId} />
  const shown = crew.agents.filter(agent => !agent.ownerId)
  const closed = crew.closed
  // Mark only the odd one out (owner, 18 Nov): a lock on private agents when most are in the team.
  const own = [...shown, ...closed]
  const mostIn = mostInTeam(own.map(agent => agent.inTeam))
  const anyPrivate = own.some(agent => !agent.inTeam)
  const closedRow = (agent: CrewClosedAgent) => <button key={agent.key} type="button" role="listitem" title={agent.purpose || undefined} onClick={() => { void agent.open(); closeDrawer(); onClosedOpened?.() }}
    className={`${sidebarAgentRowClass} gap-2 text-muted-foreground hover:bg-muted hover:text-foreground`}>
    <span className="min-w-0 truncate">{agent.name}</span><AgentInTeamMark on={agent.inTeam} mostIn={mostIn}/>
  </button>
  const row = (agent: CrewAgentRow) => <AgentRow key={agent.tabId} agent={agent} status={agent.ownerId ? teamAgentFor(team?.agents, tabs[agent.tabId]?.metadata?.agentProfileConversationKey)?.status_label || '' : ''} mostIn={mostIn} active={agent.tabId === activeTabId} dragging={dragging} onDrag={setDragging} popup={popup} reordering={reordering} onSelect={onSelect || openAgent} />
  return <div role="list" aria-label="Agents" className={popup ? 'space-y-0.5 pb-1' : sidebarAgentListClass}>
    {shown.map(row)}
    {/* Closed agents (no tab here) open again from this list. */}
    {closed.length > 0 && <button type="button" aria-expanded={showClosed} onClick={() => setShowClosed(value => !value)}
      className={sidebarAgentDisclosureClass}>
      <ChevronRight aria-hidden="true" className={`h-3 w-3 transition-transform ${showClosed ? 'rotate-90' : ''}`} />Closed agents ({closed.length})
    </button>}
    {showClosed && closed.map(closedRow)}
    <OwnTerminalAgents projectId={projectId} nested mostIn={mostIn} />
    {mostIn && !popup && <p className="px-2 pt-1 text-[11px] text-muted-foreground/80">{anyPrivate ? 'In team unless marked with a lock' : 'All in team'}</p>}
  </div>
}

function AgentRow({ agent, mostIn, status, active, dragging, onDrag, popup, reordering, onSelect }: { mostIn: boolean; popup: boolean; reordering: boolean; onSelect: (tabId: string) => void; agent: CrewAgentRow; status: string; active: boolean; dragging: string | null; onDrag: (key: string | null) => void }) {
  const state = useAgentMenuState(agent.tabId, agent.activity)
  // An idle row shows when it was last active, from the active-session list the app already holds (nothing new is read).
  const lastActivity = useChatStore(store => {
    const sessionId = store.chatTabs[agent.tabId]?.sessionId
    return sessionId ? store.activeSessionsCache.find(session => session.session_id === sessionId)?.last_activity : undefined
  })
  const ago = !state && !agent.asking && !status ? shortAgo(lastActivity) : ''
  const markUnread = useChatStore(store => store.markTabUnread)
  // Only a quiet or failed agent can go back on the to-read list; working, needs-you and unread rows already show a mark.
  const canMarkUnread = state === null || state === 'failed'
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
  const hasActions = canMarkUnread || Boolean(actions && Object.entries(actions).some(([name, value]) => typeof value === 'function' && name !== 'dropHere'))
  // Drag a row onto another to change the order, saved the same way as the tab bar's.
  return <div ref={box} data-theme-row="agent" data-theme-agent-state={state || "quiet"} data-theme-agent-selected={active ? "true" : "false"} className={`group relative rounded-md ${over && dragging && dragging !== key ? 'border-t-2 border-primary bg-primary/10' : ''}`}
    draggable={!!key} onDragStart={key ? event => { event.dataTransfer.setData('application/x-agentworks-chat-key', key); event.dataTransfer.setData('text/plain', key); event.dataTransfer.effectAllowed = 'move'; setMenu(false); onDrag(key) } : undefined} onDragEnd={() => { onDrag(null); setOver(false) }}
    onDragOver={dragging && key ? event => { event.preventDefault(); event.dataTransfer.dropEffect = 'move'; setOver(true) } : undefined} onDragLeave={() => setOver(false)}
    onDrop={dragging && key ? event => { event.preventDefault(); event.stopPropagation(); setOver(false); if (dragging !== key) actions?.dropHere?.(dragging); onDrag(null) } : undefined}>
      <button type="button" role="listitem" aria-current={active ? 'page' : undefined} onClick={() => onSelect(agent.tabId)}
        className={`${sidebarAgentRowClass} gap-2 focus:outline-none focus-visible:ring-2 focus-visible:ring-ring max-md:pr-11 ${canMarkUnread ? 'md:group-hover:pr-14 md:group-focus-within:pr-14' : ''} ${active ? 'bg-foreground/[0.06] font-medium text-foreground' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}>
        {reordering && agent.actions?.orderKey && <GripVertical aria-hidden="true" className="h-3 w-3 shrink-0 cursor-grab text-muted-foreground" />}
        <AgentHoverCard info={agent.info} shortcut={agent.shortcut}><span className="block min-w-0 truncate">{agent.name}</span></AgentHoverCard>
        <AgentInTeamMark on={agent.inTeam} mostIn={mostIn}/>
        <AgentSetupMark setup={agent.info?.setup && { ...agent.info.setup, reach: '' }} />
        {/* Where it runs (owner, 15 Nov); terminal sessions keep their program mark. */}
        <AgentPlaceMark place={agent.place} />
        {status && <span className="shrink-0 text-[10px] text-muted-foreground">{status}</span>}
        <span className="flex-1" />
        {ago && <span className="shrink-0 text-[11px] text-muted-foreground group-hover:hidden">{ago}</span>}
        <AgentMark state={state} asking={agent.asking} dot />
      </button>
    {/* Owner, 18 Nov: put an opened agent back on the to-read list; the green dot shows until it is opened again. */}
    {canMarkUnread && <button type="button" aria-label={`Mark ${agent.name} as unread`} title="Mark as unread" onClick={() => markUnread(agent.tabId)}
      className={`absolute ${hasActions ? 'right-7' : 'right-1'} ${popup ? 'top-0.5' : 'top-1/2 -translate-y-1/2'} hidden h-6 w-6 place-items-center rounded text-muted-foreground hover:bg-muted hover:text-foreground group-hover:grid group-focus-within:grid max-md:hidden`}>
      <MailOpen aria-hidden="true" className="h-3 w-3" strokeWidth={1.75} />
    </button>}
    {hasActions && <button type="button" aria-label={`More for ${agent.name}`} aria-expanded={menu} onClick={() => setMenu(value => !value)}
      className={`absolute right-1 ${popup ? 'top-0.5' : 'top-1/2 -translate-y-1/2'} h-6 w-6 place-items-center rounded text-muted-foreground hover:bg-muted hover:text-foreground max-md:h-9 max-md:w-9 ${menu ? 'grid' : 'hidden group-hover:grid group-focus-within:grid max-md:grid [@media(hover:none)]:grid'}`}>
      <MoreHorizontal aria-hidden="true" className="h-3.5 w-3.5" />
    </button>}
    {/* Inside the popup, actions scroll with the list rather than clipping at its edge. */}
    {menu && hasActions && <div className={`${popup ? 'relative mx-1 my-1' : 'absolute right-0 top-full z-50 mt-1 w-44'} rounded-xl border border-border bg-popover p-1 shadow-lg`}>
      <AgentActionsMenu name={agent.name} actions={actions || {}} onMarkUnread={canMarkUnread ? () => markUnread(agent.tabId) : undefined} onDone={() => setMenu(false)} />
    </div>}
  </div>
}

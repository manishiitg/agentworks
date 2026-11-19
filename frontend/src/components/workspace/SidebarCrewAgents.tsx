import { AgentInTeamMark, mostInTeam } from '../chat/AgentInTeamMark'
import { OwnTerminalAgents } from '../../products/work/OwnTerminalAgents'
import { useEffect, useRef, useState, type DragEvent, type ReactNode } from 'react'
import { ChevronRight, MoreHorizontal, GripVertical } from 'lucide-react'
import { AgentActionsMenu, useCrewAgentsStore, type CrewAgentRow, type CrewClosedAgent } from '../chat/AgentCard'
import { AgentMark, AgentPlaceMark, AgentSetupMark, shortAgo } from '../chat/AgentIcon'
import { useAgentMenuState } from '../chat/agentMenuState'
import { useChatStore } from '../../stores/useChatStore'
import { openAgentTab } from '../../utils/openAgentTab'
import { teamAgentFor, useTeamTab } from '../../products/work/teamApi'
import { moveAgentFeature, useCrewFeatures, type Feature } from '../../products/work/featuresApi'

import { sidebarAgentDisclosureClass, sidebarAgentListClass, sidebarAgentRowClass } from './sidebarAgentLayout'

// The phone drawer closes once something is opened from it.
const closeDrawer = () => window.dispatchEvent(new Event('close-app-sidebar'))
const openAgent = (tabId: string) => { openAgentTab(tabId); closeDrawer() }

/** Shared by the loaded and live lists so opening an agent never changes the disclosure. */
export function SidebarHiddenAgents({ count, expanded, onToggle, children }: { count: number; expanded: boolean; onToggle: () => void; children: ReactNode }) {
  if (!count) return null
  return <>
    <button type="button" aria-expanded={expanded} onClick={onToggle} className={sidebarAgentDisclosureClass}>
      <ChevronRight aria-hidden="true" className={`h-3 w-3 transition-transform ${expanded ? 'rotate-90' : ''}`} />Hidden agents · {count}
    </button>
    {expanded && <div role="group" aria-label="Hidden agents">{children}</div>}
  </>
}

/** The sidebar's own agents in their saved order, with features and two folded lists. */
export function SidebarCrewAgents({ projectId, popup = false, reordering = false, onSelect, onClosedOpened, hiddenExpansion }: { projectId: string; popup?: boolean; reordering?: boolean; onSelect?: (tabId: string) => void; onClosedOpened?: () => void; hiddenExpansion?: [boolean, (value: boolean) => void] }) {
  const crew = useCrewAgentsStore()
  const activeTabId = useChatStore(state => state.activeTabId)
  const localHiddenExpansion = useState(false)
  const [showHidden, setShowHidden] = hiddenExpansion ?? localHiddenExpansion
  const [showClosed, setShowClosed] = useState(false)
  const [dragging, setDragging] = useState<string | null>(null)
  // The feature group the dragged row came from ('' for agents in no feature), and the drop target being hovered.
  const [dragFrom, setDragFrom] = useState('')
  const [dropOn, setDropOn] = useState('')
  // The Team tab's one server reading: each agent's availability and every person, by account, with their name.
  const team = useTeamTab(projectId).view
  const tabs = useChatStore(state => state.chatTabs)
  const { target, features, forTab, chatKeyFor, agentIdFor } = useCrewFeatures(projectId)
  if (crew.projectId !== projectId || !crew.agents.length) return <OwnTerminalAgents projectId={projectId} />
  const ownAgents = crew.agents.filter(agent => !agent.ownerId)
  const shown = ownAgents.filter(agent => !agent.sidebarHidden)
  // Keep the server order even when opening a hidden agent replaces its closed row with a live one.
  const hidden = [...ownAgents, ...crew.closed].filter(agent => agent.sidebarHidden)
    .sort((a, b) => (a.sidebarOrder ?? Infinity) - (b.sidebarOrder ?? Infinity))
  // Agents in a feature move under it (#3210): agents in no feature first, then each feature with its agents. An
  // agent in two features shows under both.
  const inFeatures = new Map(shown.map(agent => [agent.tabId, forTab(agent.tabId, agent.main)]))
  const plain = shown.filter(agent => !inFeatures.get(agent.tabId)?.length)
  const groups = features.map(feature => ({ feature, agents: shown.filter(agent => inFeatures.get(agent.tabId)?.some(item => item.id === feature.id)) })).filter(group => group.agents.length || dragging)
  const closed = crew.closed.filter(agent => !agent.sidebarHidden)
  // Mark only the odd one out (owner, 18 Nov): a lock on private agents when most are in the team.
  const own = [...ownAgents, ...crew.closed]
  const mostIn = mostInTeam(own.map(agent => agent.inTeam))
  const anyPrivate = own.some(agent => !agent.inTeam)
  const closedRow = (agent: CrewClosedAgent) => <button key={agent.key} type="button" role="listitem" title={agent.purpose || undefined} onClick={() => { void agent.open(); closeDrawer(); onClosedOpened?.() }}
    className={`${sidebarAgentRowClass} gap-2 text-muted-foreground hover:bg-muted hover:text-foreground`}>
    <span className="min-w-0 truncate">{agent.name}</span><AgentInTeamMark on={agent.inTeam} mostIn={mostIn}/>
  </button>
  const row = (agent: CrewAgentRow, group = '') => <AgentRow key={`${group}:${agent.tabId}`} agent={agent} status={agent.ownerId ? teamAgentFor(team?.agents, tabs[agent.tabId]?.metadata?.agentProfileConversationKey)?.status_label || '' : ''} mostIn={mostIn} active={agent.tabId === activeTabId} dragging={dragging} onDrag={key => { setDragging(key); setDragFrom(key ? group : ''); if (!key) setDropOn('') }} popup={popup} reordering={reordering} onSelect={onSelect || openAgent} />
  // Drag an agent onto a feature's header to put it in that feature, or onto "take out" to leave the one it came from
  // (#3210; owner, 19 Nov). The row order drag stays as it was.
  const draggedAgent = dragging ? shown.find(agent => agent.actions?.orderKey === dragging) : undefined
  const fromFeature = features.find(feature => feature.id === dragFrom)
  const move = (feature: Feature, join: boolean) => {
    const agent = draggedAgent, chat = agent ? chatKeyFor(agent.tabId, agent.main) : ''
    setDragging(null); setDropOn('')
    if (!agent || !target || !chat) return
    void moveAgentFeature(target, { chatKey: chat, agentId: agentIdFor(agent.tabId, agent.main), name: agent.name }, join ? feature.name : feature.id, join)
      .catch(error => useChatStore.getState().addToast(error instanceof Error ? error.message : 'Could not change the feature. Try again.', 'error'))
  }
  const dropZone = (id: string, accepts: boolean, drop: () => void) => accepts ? {
    onDragOver: (event: DragEvent) => { event.preventDefault(); event.dataTransfer.dropEffect = 'move'; setDropOn(id) },
    onDragLeave: () => setDropOn(value => value === id ? '' : value),
    onDrop: (event: DragEvent) => { event.preventDefault(); event.stopPropagation(); drop() },
  } : {}
  return <div role="list" aria-label="Agents" className={popup ? 'space-y-0.5 pb-1' : sidebarAgentListClass}>
    {draggedAgent && fromFeature && <div {...dropZone('out', true, () => move(fromFeature, false))}
      className={`rounded-md border border-dashed px-2 py-1 text-[11px] ${dropOn === 'out' ? 'border-primary bg-primary/10 text-foreground' : 'border-border text-muted-foreground'}`}>
      Drop here to take {draggedAgent.name} out of ◈ {fromFeature.name}
    </div>}
    {plain.map(agent => row(agent))}
    {groups.map(({ feature, agents }) => <div key={feature.id} role="group" aria-label={`Feature ${feature.name}`} className="pt-1">
      <div title={feature.link ? `${feature.name} · ${feature.link}` : feature.name} {...dropZone(feature.id, !!draggedAgent && !agents.includes(draggedAgent), () => move(feature, true))}
        className={`flex h-6 min-w-0 items-center gap-1.5 rounded-md px-2 text-[11px] font-medium text-muted-foreground/90 ${dropOn === feature.id ? 'bg-primary/10 ring-1 ring-primary' : ''}`}>
        <span aria-hidden="true" className="text-primary">◈</span><span className="min-w-0 truncate">{feature.name}</span>
        {feature.open_tasks > 0 && <span className="shrink-0 text-[10px] font-normal text-muted-foreground/70">{feature.open_tasks} open</span>}
      </div>
      {agents.map(agent => row(agent, feature.id))}
    </div>)}
    {/* Closed agents (no tab here) open again from this list. */}
    {closed.length > 0 && <button type="button" aria-expanded={showClosed} onClick={() => setShowClosed(value => !value)}
      className={sidebarAgentDisclosureClass}>
      <ChevronRight aria-hidden="true" className={`h-3 w-3 transition-transform ${showClosed ? 'rotate-90' : ''}`} />Closed agents ({closed.length})
    </button>}
    {showClosed && closed.map(closedRow)}
    <SidebarHiddenAgents count={hidden.length} expanded={showHidden} onToggle={() => setShowHidden(!showHidden)}>
      {showHidden && hidden.map(agent => 'tabId' in agent ? row(agent) : closedRow(agent))}
    </SidebarHiddenAgents>
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
  const hasActions = canMarkUnread || Boolean(actions && (actions.fork || actions.editorWorkspace || actions.terminal || actions.rename || actions.moveUp || actions.moveDown || actions.setSidebarHidden))
  // Drag a row onto another to change the order, saved the same way as the tab bar's.
  return <div ref={box} data-theme-row="agent" data-theme-agent-state={state || "quiet"} data-theme-agent-selected={active ? "true" : "false"} className={`group relative rounded-md ${over && dragging && dragging !== key ? 'border-t-2 border-primary bg-primary/10' : ''}`}
    draggable={!!key} onDragStart={key ? event => { event.dataTransfer.setData('application/x-agentworks-chat-key', key); event.dataTransfer.setData('text/plain', key); event.dataTransfer.effectAllowed = 'move'; setMenu(false); onDrag(key) } : undefined} onDragEnd={() => { onDrag(null); setOver(false) }}
    onDragOver={dragging && key ? event => { event.preventDefault(); event.dataTransfer.dropEffect = 'move'; setOver(true) } : undefined} onDragLeave={() => setOver(false)}
    onDrop={dragging && key ? event => { event.preventDefault(); event.stopPropagation(); setOver(false); if (dragging !== key) actions?.dropHere?.(dragging); onDrag(null) } : undefined}>
      <button type="button" role="listitem" aria-current={active ? 'page' : undefined} onClick={() => onSelect(agent.tabId)}
        className={`${sidebarAgentRowClass} gap-2 focus:outline-none focus-visible:ring-2 focus-visible:ring-ring max-md:pr-11 ${active ? 'bg-foreground/[0.06] font-medium text-foreground' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}>
        {reordering && agent.actions?.orderKey && <GripVertical aria-hidden="true" className="h-3 w-3 shrink-0 cursor-grab text-muted-foreground" />}
        <span className="block min-w-0 truncate">{agent.name}</span>
        <AgentInTeamMark on={agent.inTeam} mostIn={mostIn}/>
        <AgentSetupMark setup={agent.info?.setup && { ...agent.info.setup, reach: '' }} />
        {/* Where it runs (owner, 15 Nov); terminal sessions keep their program mark. */}
        <AgentPlaceMark place={agent.place} />
        {status && <span className="shrink-0 text-[10px] text-muted-foreground">{status}</span>}
        <span className="flex-1" />
        {ago && <span className="shrink-0 text-[11px] text-muted-foreground group-hover:hidden">{ago}</span>}
        <AgentMark state={state} asking={agent.asking} dot />
      </button>
    {hasActions && <button type="button" aria-label={`More for ${agent.name}`} aria-expanded={menu} onClick={() => setMenu(value => !value)}
      className={`absolute right-1 ${popup ? 'top-0.5' : 'top-1/2 -translate-y-1/2'} h-6 w-6 place-items-center rounded text-muted-foreground hover:bg-muted hover:text-foreground max-md:h-9 max-md:w-9 ${menu ? 'grid' : 'hidden group-hover:grid group-focus-within:grid max-md:grid [@media(hover:none)]:grid'}`}>
      <MoreHorizontal aria-hidden="true" className="h-3.5 w-3.5" />
    </button>}
    {/* Inside the popup, actions scroll with the list rather than clipping at its edge. */}
    {menu && hasActions && <div className={`${popup ? 'relative mx-1 my-1' : 'absolute right-0 top-full z-50 mt-1 w-44'} rounded-xl border border-border bg-popover p-1 shadow-lg`}>
      <AgentActionsMenu name={agent.name} actions={actions || {}} sidebarHidden={agent.sidebarHidden} onMarkUnread={canMarkUnread ? () => markUnread(agent.tabId) : undefined} onDone={() => setMenu(false)} />
    </div>}
  </div>
}

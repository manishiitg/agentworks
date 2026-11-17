import { CodingCliIcon } from '../chat/CodingCliIcon'
import { AgentInTeamMark } from '../chat/AgentInTeamMark'
import { OwnTerminalAgents } from '../../products/work/OwnTerminalAgents'
import { useEffect, useRef, useState } from 'react'
import { ChevronRight, MoreHorizontal, GripVertical } from 'lucide-react'
import { AgentActionsMenu, AgentHoverCard, useCrewAgentsStore, type CrewAgentRow, type CrewClosedAgent } from '../chat/AgentCard'
import { AGENT_KEY, AgentMark, AgentPlaceMark, AgentSetupMark } from '../chat/AgentIcon'
import { useAgentMenuState } from '../chat/agentMenuState'
import { useChatStore } from '../../stores/useChatStore'
import { useLLMStore } from '../../stores/useLLMStore'
import { codingProviderName } from '../../utils/codingProviderName'
import { openAgentTab } from '../../utils/openAgentTab'
import { teamAgentFor, useTeamTab } from '../../products/work/teamApi'

import { sidebarAgentDisclosureClass, sidebarAgentListClass, sidebarAgentRowClass } from './sidebarAgentLayout'

// The phone drawer closes once something is opened from it.
const closeDrawer = () => window.dispatchEvent(new Event('close-app-sidebar'))
const openAgent = (tabId: string) => { openAgentTab(tabId); closeDrawer() }

/** One shared list for the open Crew's sidebar and Agents popup: one row each with its
 * name (no icon, owner 5 Nov) and a mark at the end only when needed, the agent card on hover, and "…" to manage it, with creation in the project actions menu. */
export function SidebarCrewAgents({ projectId, popup = false, reordering = false, onSelect, onClosedOpened }: { projectId: string; popup?: boolean; reordering?: boolean; onSelect?: (tabId: string) => void; onClosedOpened?: () => void }) {
  const crew = useCrewAgentsStore()
  const activeTabId = useChatStore(state => state.activeTabId)
  const [showHidden, setShowHidden] = useState(false)
  const [showClosed, setShowClosed] = useState(false)
  const [dragging, setDragging] = useState<string | null>(null)
  // The Team tab's one server reading: each agent's availability and every person, by account, with their name.
  const team = useTeamTab(projectId).view
  const tabs = useChatStore(state => state.chatTabs)
  if (crew.projectId !== projectId || !crew.agents.length) return <OwnTerminalAgents projectId={projectId} />
  // Selection changes the open chat, not the saved Hidden placement.
  const shown = crew.agents.filter(agent => !agent.ownerId && agent.pinned !== false)
  const hidden = crew.agents.filter(agent => !agent.ownerId && agent.pinned === false)
  const hiddenClosed = crew.closed.filter(agent => agent.pinned === false)
  const closed = crew.closed.filter(agent => agent.pinned !== false)
  const hiddenCount = hidden.length + hiddenClosed.length
  const closedRow = (agent: CrewClosedAgent) => <button key={agent.key} type="button" role="listitem" title={agent.purpose || undefined} onClick={() => { void agent.open(); closeDrawer(); onClosedOpened?.() }}
    className={`${sidebarAgentRowClass} text-muted-foreground hover:bg-muted hover:text-foreground`}>
    <span className="min-w-0 truncate">{agent.name}</span><AgentInTeamMark on={agent.inTeam}/>
  </button>
  const row = (agent: CrewAgentRow) => <AgentRow key={agent.tabId} agent={agent} status={agent.ownerId ? teamAgentFor(team?.agents, tabs[agent.tabId]?.metadata?.agentProfileConversationKey)?.status_label || '' : ''} active={agent.tabId === activeTabId} dragging={dragging} onDrag={setDragging} popup={popup} reordering={reordering} onSelect={onSelect || openAgent} />
  return <div role="list" aria-label="Agents" className={popup ? 'space-y-0.5 pb-1' : sidebarAgentListClass}>
    {shown.map(row)}
    {hiddenCount > 0 && <button type="button" aria-label={`${hiddenCount} hidden ${hiddenCount === 1 ? 'agent' : 'agents'}`} aria-expanded={showHidden} onClick={() => setShowHidden(value => !value)}
      className={sidebarAgentDisclosureClass}>
      <ChevronRight aria-hidden="true" className={`h-3 w-3 transition-transform ${showHidden ? 'rotate-90' : ''}`} />+{hiddenCount} hidden
    </button>}
    {showHidden && <>{hidden.map(row)}{hiddenClosed.map(closedRow)}</>}
    {/* Closed agents (no tab here) open again from this list. */}
    {closed.length > 0 && <button type="button" aria-expanded={showClosed} onClick={() => setShowClosed(value => !value)}
      className={sidebarAgentDisclosureClass}>
      <ChevronRight aria-hidden="true" className={`h-3 w-3 transition-transform ${showClosed ? 'rotate-90' : ''}`} />Closed agents ({closed.length})
    </button>}
    {showClosed && closed.map(closedRow)}
    <OwnTerminalAgents projectId={projectId} nested />
  </div>
}

/** Same server-resolved runtime as the agent hover card, never the Crew default. */
function AgentCliMark({ tabId }: { tabId: string }) {
  const provider = useChatStore(state => {
    const meta = state.chatTabs[tabId]?.metadata
    return meta?.agentProfileEngine || meta?.agentProfileChatProvider
  })
  return <AgentCliIcon provider={provider} />
}

export function AgentCliIcon({ provider }: { provider?: string }) {
  const manifest = useLLMStore(state => state.providerManifest.find(item => item.id === provider))
  if (!provider) return null
  const name = manifest ? codingProviderName(manifest) : provider
  return <CodingCliIcon provider={provider} label={name} className="hidden shrink-0 items-center group-hover:inline-flex group-focus-within:inline-flex" />
}

function AgentRow({ agent, status, active, dragging, onDrag, popup, reordering, onSelect }: { popup: boolean; reordering: boolean; onSelect: (tabId: string) => void; agent: CrewAgentRow; status: string; active: boolean; dragging: string | null; onDrag: (key: string | null) => void }) {
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
  return <div ref={box} data-theme-row="agent" data-theme-agent-state={state || "quiet"} data-theme-agent-selected={active ? "true" : "false"} className={`group relative rounded-md ${over && dragging && dragging !== key ? 'border-t-2 border-primary bg-primary/10' : ''}`}
    draggable={!!key} onDragStart={key ? event => { event.dataTransfer.setData('application/x-agentworks-chat-key', key); event.dataTransfer.setData('text/plain', key); event.dataTransfer.effectAllowed = 'move'; setMenu(false); onDrag(key) } : undefined} onDragEnd={() => { onDrag(null); setOver(false) }}
    onDragOver={dragging && key ? event => { event.preventDefault(); event.dataTransfer.dropEffect = 'move'; setOver(true) } : undefined} onDragLeave={() => setOver(false)}
    onDrop={dragging && key ? event => { event.preventDefault(); event.stopPropagation(); setOver(false); if (dragging !== key) actions?.dropHere?.(dragging); onDrag(null) } : undefined}>
      <button type="button" role="listitem" aria-current={active ? 'page' : undefined} onClick={() => onSelect(agent.tabId)}
        className={`${sidebarAgentRowClass} gap-2 focus:outline-none focus-visible:ring-2 focus-visible:ring-ring max-md:pr-11 ${active ? 'bg-foreground/[0.06] font-medium text-foreground' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}>
        {reordering && agent.actions?.orderKey && <GripVertical aria-hidden="true" className="h-3 w-3 shrink-0 cursor-grab text-muted-foreground" />}
        <AgentHoverCard info={agent.info} shortcut={agent.shortcut}><span className="block min-w-0 truncate">{agent.name}</span></AgentHoverCard>
        <AgentInTeamMark on={agent.inTeam}/>
        <AgentSetupMark setup={agent.info?.setup && { ...agent.info.setup, reach: '' }} />
        {/* Where it runs (owner, 15 Nov); terminal sessions keep their program mark. */}
        <AgentPlaceMark place={agent.place} />
        {status && <span className="shrink-0 text-[10px] text-muted-foreground">{status}</span>}
        <span className="flex-1" />
        <AgentCliMark tabId={agent.tabId} />
        {agent.shortcut && <span className="hidden shrink-0 text-[10px] text-muted-foreground group-hover:inline">{AGENT_KEY}{agent.shortcut}</span>}
        <AgentMark state={state} asking={agent.asking} />
      </button>
    {hasActions && <button type="button" aria-label={`More for ${agent.name}`} aria-expanded={menu} onClick={() => setMenu(value => !value)}
      className={`absolute right-1 ${popup ? 'top-0.5' : 'top-1/2 -translate-y-1/2'} h-6 w-6 place-items-center rounded text-muted-foreground hover:bg-muted hover:text-foreground max-md:h-9 max-md:w-9 ${menu ? 'grid' : 'hidden group-hover:grid group-focus-within:grid max-md:grid [@media(hover:none)]:grid'}`}>
      <MoreHorizontal aria-hidden="true" className="h-3.5 w-3.5" />
    </button>}
    {/* Inside the popup, actions scroll with the list rather than clipping at its edge. */}
    {menu && actions && <div className={`${popup ? 'relative mx-1 my-1' : 'absolute right-0 top-full z-50 mt-1 w-44'} rounded-xl border border-border bg-popover p-1 shadow-lg`}>
      {actions && <AgentActionsMenu name={agent.name} actions={actions} pinned={agent.pinned !== false} onDone={() => setMenu(false)} />}
    </div>}
  </div>
}

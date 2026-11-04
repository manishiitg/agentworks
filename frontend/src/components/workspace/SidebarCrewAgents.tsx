import { useEffect, useRef, useState } from 'react'
import { ChevronRight, MoreHorizontal } from 'lucide-react'
import { AgentActionsMenu, AgentHoverCard, useAgentsViewStore, useCrewAgentsStore, type CrewAgentRow } from '../chat/AgentCard'
import { AGENT_KEY, AgentIcon, AgentMark } from '../chat/AgentIcon'
import { useAgentMenuState } from '../chat/agentMenuState'
import { useChatStore } from '../../stores/useChatStore'
import { activateTab } from '../../utils/activateTab'
import { OPEN_TEAM_BOARD_EVENT } from '../chat/ProjectWorkspaceTitle'

const openTeamBoard = (projectId: string) => window.dispatchEvent(new CustomEvent(OPEN_TEAM_BOARD_EVENT, { detail: { projectId } }))

/** The open Crew's agents in the app's left menu (owner, 4 Nov, "Left menu", the default): one row each with its icon,
 * name and a mark at the end only when needed, the agent card on hover, and "…" to manage it. "+" is in the top line. */
export function SidebarCrewAgents({ projectId }: { projectId: string }) {
  const view = useAgentsViewStore(state => state.view)
  const crew = useCrewAgentsStore()
  const activeTabId = useChatStore(state => state.activeTabId)
  const [showHidden, setShowHidden] = useState(false)
  const [dragging, setDragging] = useState<string | null>(null)
  // The open agent is in the top line, so it is not listed again here (owner, 4 Nov); its ⌘number stays its own.
  const others = crew.agents.filter(agent => agent.tabId !== activeTabId)
  const total = crew.agents.length + crew.closed.length
  if (view === 'tabs' || crew.projectId !== projectId || total <= crew.agents.length - others.length) return null
  // Hidden agents (unpinned, as on the tab bar) sit at the end.
  const shown = others.filter(agent => agent.pinned !== false)
  const hidden = others.filter(agent => agent.pinned === false)
  const row = (agent: CrewAgentRow) => <AgentRow key={agent.tabId} agent={agent} active={agent.tabId === activeTabId} dragging={dragging} onDrag={setDragging} />
  return <div role="list" aria-label="Agents" className="ml-6 space-y-0.5 border-l border-border pb-1 pl-1.5">
    {shown.map(row)}
    {hidden.length > 0 && <button type="button" aria-expanded={showHidden} onClick={() => setShowHidden(value => !value)}
      className="flex w-full items-center gap-1.5 rounded-md px-2 py-1 text-left text-xs text-muted-foreground hover:bg-muted hover:text-foreground">
      <ChevronRight aria-hidden="true" className={`h-3 w-3 transition-transform ${showHidden ? 'rotate-90' : ''}`} />Hidden agents ({hidden.length})
    </button>}
    {showHidden && hidden.map(row)}
    <button type="button" onClick={() => openTeamBoard(projectId)}
      className="flex w-full items-center gap-1.5 rounded-md px-2 py-1 text-left text-xs text-muted-foreground hover:bg-muted hover:text-foreground">All agents ({total})</button>
  </div>
}

function AgentRow({ agent, active, dragging, onDrag }: { agent: CrewAgentRow; active: boolean; dragging: string | null; onDrag: (key: string | null) => void }) {
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
      <button type="button" role="listitem" aria-current={active ? 'page' : undefined} onClick={() => activateTab(agent.tabId)}
        className={`flex w-full min-w-0 items-center gap-2 rounded-md py-1 pl-2 pr-7 text-left text-[13px] ${active ? 'bg-foreground/[0.06] font-medium text-foreground' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}>
        <AgentIcon name={agent.name} icon={agent.info?.icon} active={active} shared={agent.info?.sharedWith} />
        <span className="min-w-0 flex-1 truncate">{agent.name}</span>
        {agent.shortcut && <span className="hidden shrink-0 text-[10px] text-muted-foreground group-hover:inline">{AGENT_KEY}{agent.shortcut}</span>}
        <AgentMark state={state} asking={agent.asking} />
      </button>
    </AgentHoverCard>
    {hasActions && <button type="button" aria-label={`More for ${agent.name}`} aria-expanded={menu} onClick={() => setMenu(value => !value)}
      className={`absolute right-1 top-1/2 h-6 w-6 -translate-y-1/2 place-items-center rounded text-muted-foreground hover:bg-muted hover:text-foreground ${menu ? 'grid' : 'hidden group-hover:grid group-focus-within:grid'}`}>
      <MoreHorizontal aria-hidden="true" className="h-3.5 w-3.5" />
    </button>}
    {menu && actions && <div className="absolute right-0 top-full z-50 mt-1 w-44 rounded-xl border border-border bg-popover p-1 shadow-lg">
      <AgentActionsMenu name={agent.name} actions={actions} pinned={agent.pinned !== false} onDone={() => setMenu(false)} />
    </div>}
  </div>
}

/** The collapsed sidebar (icon rail) keeps the open Crew's agents (owner, 4 Nov): one icon per agent with its mark.
 * Shown only while one of that Crew's agents is open. */
export function RailCrewAgents() {
  const crew = useCrewAgentsStore()
  const activeTabId = useChatStore(state => state.activeTabId)
  const projectId = crew.projectId
  const others = crew.agents.filter(agent => agent.tabId !== activeTabId)
  if (!projectId || others.length === crew.agents.length || (!others.length && !crew.closed.length)) return null
  return <div role="list" aria-label="Agents" className="mt-2 flex min-h-0 flex-col items-center gap-1 overflow-y-auto border-t border-border/70 pt-2 [scrollbar-width:none]">
    {others.map(agent => <RailAgent key={agent.tabId} agent={agent} active={false} />)}
    <button type="button" title="All agents" aria-label="All agents" onClick={() => openTeamBoard(projectId)}
      className="grid h-8 w-8 place-items-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground">
      <MoreHorizontal aria-hidden="true" className="h-4 w-4" />
    </button>
  </div>
}

function RailAgent({ agent, active }: { agent: CrewAgentRow; active: boolean }) {
  const state = useAgentMenuState(agent.tabId, agent.activity)
  // Every agent the same box; the open one highlighted. Hover shows its card to the right of the rail.
  return <AgentHoverCard info={agent.info} side="right" actions={agent.actions} pinned={agent.pinned !== false} shortcut={agent.shortcut}>
    <button type="button" role="listitem" aria-current={active ? 'page' : undefined} title={agent.info ? undefined : agent.name} aria-label={agent.name} onClick={() => activateTab(agent.tabId)}
      className="relative grid h-8 w-8 place-items-center rounded-md hover:opacity-90">
      <AgentIcon name={agent.name} icon={agent.info?.icon} size="md" active={active} shared={agent.info?.sharedWith} />
      <span className="absolute -right-0.5 -top-0.5"><AgentMark state={state} asking={agent.asking} /></span>
    </button>
  </AgentHoverCard>
}

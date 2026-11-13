import { useCallback, useEffect, useRef, useState } from 'react'
import { ChevronRight, MoreHorizontal, Terminal, Plus, Pencil, X } from 'lucide-react'
import { AgentActionsMenu, AgentHoverCard, useCrewAgentsStore, type CrewAgentRow, type CrewClosedAgent } from '../chat/AgentCard'
import { AGENT_KEY, AgentMark, AgentSetupMark } from '../chat/AgentIcon'
import { useAgentMenuState, type AgentMenuState } from '../chat/agentMenuState'
import { useChatStore } from '../../stores/useChatStore'
import { useLLMStore } from '../../stores/useLLMStore'
import { codingProviderName } from '../../utils/codingProviderName'
import { openAgentTab } from '../../utils/openAgentTab'
import { teamAgentFor, useTeamTab } from '../../products/work/teamApi'

import { useAgentGroups } from './MenuGroups'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import type { MenuAgentGroup } from '../../services/llm-config-api'

// The phone drawer closes once something is opened from it.
const closeDrawer = () => window.dispatchEvent(new Event('close-app-sidebar'))
const openAgent = (tabId: string) => { openAgentTab(tabId); closeDrawer() }

/** The open Crew's agents in the app's left menu: one row each with its
 * name (no icon, owner 5 Nov) and a mark at the end only when needed, the agent card on hover, and "…" to manage it, with creation in the project actions menu. */
export function SidebarCrewAgents({ projectId }: { projectId: string }) {
  const crew = useCrewAgentsStore()
  const grouping = useAgentGroups(JSON.stringify([getRawActiveWorkspaceId(), projectId]))
  const [newGroup, setNewGroup] = useState<string | null>(null)
  const activeTabId = useChatStore(state => state.activeTabId)
  const [showHidden, setShowHidden] = useState(false)
  const [showClosed, setShowClosed] = useState(false)
  const [dragging, setDragging] = useState<string | null>(null)
  // The Team tab's one server reading: each agent's availability and every person, by account, with their name.
  const team = useTeamTab(projectId).view
  const tabs = useChatStore(state => state.chatTabs)
  if (crew.projectId !== projectId || !crew.agents.length) return null
  // Hidden agents (unpinned, as on the tab bar) sit at the end; the open one always stays in view.
  const shown = crew.agents.filter(agent => !agent.ownerId && (agent.pinned !== false || agent.tabId === activeTabId))
  const hidden = crew.agents.filter(agent => !agent.ownerId && agent.pinned === false && agent.tabId !== activeTabId)
  const hiddenClosed = crew.closed.filter(agent => agent.pinned === false)
  const closed = crew.closed.filter(agent => agent.pinned !== false)
  const ungroupedClosed = closed.filter(agent => !grouping.groups.some(group => group.agents.includes(agent.key)))
  const hiddenCount = hidden.length + hiddenClosed.length
  const closedRow = (agent: CrewClosedAgent) => <button key={agent.key} type="button" role="listitem" title={agent.purpose || undefined} onClick={() => { void agent.open(); closeDrawer() }}
    className="flex w-full min-w-0 items-center rounded-md py-1 pl-2 pr-7 text-left text-[13px] text-muted-foreground hover:bg-muted hover:text-foreground max-md:min-h-10">
    <span className="min-w-0 truncate">{agent.name}</span>
  </button>
  const row = (agent: CrewAgentRow) => <AgentRow key={agent.tabId} agent={agent} status={agent.ownerId ? teamAgentFor(team?.agents, tabs[agent.tabId]?.metadata?.agentProfileConversationKey)?.status_label || '' : ''} active={agent.tabId === activeTabId} dragging={dragging} onDrag={setDragging} groups={grouping.groups} groupKey={tabs[agent.tabId]?.metadata?.agentProfileConversationKey || ''} onMove={grouping.move} />
  return <div role="list" aria-label="Agents" className="ml-6 space-y-0.5 border-l border-border pb-1 pl-1.5">
    {shown.filter(agent => !grouping.groups.some(group => group.agents.includes(tabs[agent.tabId]?.metadata?.agentProfileConversationKey || ''))).map(row)}
    {grouping.groups.map(group => {
      const members = shown.filter(agent => group.agents.includes(tabs[agent.tabId]?.metadata?.agentProfileConversationKey || ''))
      const groupedClosed = closed.filter(agent => group.agents.includes(agent.key))
      const probes = crew.agents.filter(agent => !agent.ownerId && group.agents.includes(tabs[agent.tabId]?.metadata?.agentProfileConversationKey || ''))
      return <AgentGroup key={group.id} group={group} agents={probes} count={members.length + groupedClosed.length} onToggle={() => void grouping.toggle(group.id)} onRename={name => void grouping.rename(group.id, name)} onRemove={() => { if (window.confirm(`Delete “${group.name}”? Its agents will stay in this Crew.`)) void grouping.remove(group.id) }}>
        {members.map(row)}{groupedClosed.map(closedRow)}
      </AgentGroup>
    })}
    {grouping.loaded && (newGroup !== null ? <form className="flex gap-1 px-2 py-1" onSubmit={event => { event.preventDefault(); if (newGroup.trim()) { void grouping.create(newGroup.trim()); setNewGroup(null) } }}>
      <input autoFocus aria-label="Group name" maxLength={40} placeholder="Group name" className="min-w-0 flex-1 rounded border border-border bg-background px-1 text-xs" value={newGroup} onChange={event => setNewGroup(event.target.value)} onKeyDown={event => { if (event.key === 'Escape') setNewGroup(null) }} />
      <button type="submit" className="text-xs">Add</button><button type="button" aria-label="Cancel new group" onClick={() => setNewGroup(null)}><X className="h-3 w-3" /></button>
    </form> : <button type="button" onClick={() => setNewGroup('')} className="flex items-center gap-1 px-2 py-1 text-xs text-muted-foreground hover:text-foreground"><Plus className="h-3 w-3" />New group</button>)}
    {grouping.error && <p role="alert" className="px-2 text-xs text-destructive">{grouping.error}</p>}
    {hiddenCount > 0 && <button type="button" aria-expanded={showHidden} onClick={() => setShowHidden(value => !value)}
      className="flex w-full items-center gap-1.5 rounded-md px-2 py-1 text-left text-xs text-muted-foreground hover:bg-muted hover:text-foreground max-md:min-h-10">
      <ChevronRight aria-hidden="true" className={`h-3 w-3 transition-transform ${showHidden ? 'rotate-90' : ''}`} />+{hiddenCount} hidden {hiddenCount === 1 ? 'agent' : 'agents'}
    </button>}
    {showHidden && <>{hidden.map(row)}{hiddenClosed.map(closedRow)}</>}
    {/* Closed agents (no tab here) open again from this list. */}
    {ungroupedClosed.length > 0 && <button type="button" aria-expanded={showClosed} onClick={() => setShowClosed(value => !value)}
      className="flex w-full items-center gap-1.5 rounded-md px-2 py-1 text-left text-xs text-muted-foreground hover:bg-muted hover:text-foreground max-md:min-h-10">
      <ChevronRight aria-hidden="true" className={`h-3 w-3 transition-transform ${showClosed ? 'rotate-90' : ''}`} />Closed agents ({ungroupedClosed.length})
    </button>}
    {showClosed && ungroupedClosed.map(closedRow)}
  </div>
}

const cliIcons: Record<string, string> = {
  'claude-code': 'claude.ico', 'codex-cli': 'codex.png', 'cursor-cli': 'cursor.svg',
  'pi-cli': 'pi.svg', 'muse-cli': 'muse.ico', 'agy-cli': 'antigravity.svg',
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
  return <span title={name} aria-label={name} className="hidden shrink-0 items-center group-hover:inline-flex group-focus-within:inline-flex">
    {cliIcons[provider] ? <img src={`/provider-icons/${cliIcons[provider]}`} alt="" className="h-3.5 w-3.5 object-contain" /> : <Terminal aria-hidden="true" className="h-3.5 w-3.5" />}
  </span>
}

function AgentRow({ agent, status, active, dragging, onDrag, groups, groupKey, onMove }: { groups: MenuAgentGroup[]; groupKey: string; onMove: (key: string, id: string) => Promise<void>; agent: CrewAgentRow; status: string; active: boolean; dragging: string | null; onDrag: (key: string | null) => void }) {
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
  return <div ref={box} className={`group relative rounded-md ${over && dragging && dragging !== key ? 'border-t-2 border-primary bg-primary/10' : ''}`}
    draggable={!!key} onDragStart={key ? event => { event.dataTransfer.setData('application/x-agentworks-chat-key', key); event.dataTransfer.setData('text/plain', key); event.dataTransfer.effectAllowed = 'move'; setMenu(false); onDrag(key) } : undefined} onDragEnd={() => { onDrag(null); setOver(false) }}
    onDragOver={dragging && key ? event => { event.preventDefault(); event.dataTransfer.dropEffect = 'move'; setOver(true) } : undefined} onDragLeave={() => setOver(false)}
    onDrop={dragging && key ? event => { event.preventDefault(); event.stopPropagation(); setOver(false); if (dragging !== key) actions?.dropHere?.(dragging); onDrag(null) } : undefined}>
      <button type="button" role="listitem" aria-current={active ? 'page' : undefined} onClick={() => openAgent(agent.tabId)}
        className={`flex w-full min-w-0 items-center gap-2 rounded-md py-1 pl-2 pr-7 text-left text-[13px] focus:outline-none focus-visible:ring-2 focus-visible:ring-ring max-md:min-h-10 max-md:pr-11 ${active ? 'bg-foreground/[0.06] font-medium text-foreground' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}>
        <AgentHoverCard info={agent.info} shortcut={agent.shortcut}><span className="block min-w-0 truncate">{agent.name}</span></AgentHoverCard>
        <AgentSetupMark setup={agent.info?.setup} />
        {status && <span className="shrink-0 text-[10px] text-muted-foreground">{status}</span>}
        <span className="flex-1" />
        <AgentCliMark tabId={agent.tabId} />
        {agent.shortcut && <span className="hidden shrink-0 text-[10px] text-muted-foreground group-hover:inline">{AGENT_KEY}{agent.shortcut}</span>}
        <AgentMark state={state} asking={agent.asking} />
      </button>
    {hasActions && <button type="button" aria-label={`More for ${agent.name}`} aria-expanded={menu} onClick={() => setMenu(value => !value)}
      className={`absolute right-1 top-1/2 h-6 w-6 -translate-y-1/2 place-items-center rounded text-muted-foreground hover:bg-muted hover:text-foreground max-md:h-9 max-md:w-9 ${menu ? 'grid' : 'hidden group-hover:grid group-focus-within:grid max-md:grid [@media(hover:none)]:grid'}`}>
      <MoreHorizontal aria-hidden="true" className="h-3.5 w-3.5" />
    </button>}
    {menu && actions && <div className="absolute right-0 top-full z-50 mt-1 w-44 rounded-xl border border-border bg-popover p-1 shadow-lg">
      {groupKey && groups.length > 0 && <label className="block px-2 py-1 text-xs text-muted-foreground">Group
        <select aria-label={`Group for ${agent.name}`} className="mt-1 w-full rounded border border-border bg-popover p-1 text-foreground" value={groups.find(group => group.agents.includes(groupKey))?.id || ''} onChange={event => { void onMove(groupKey, event.target.value); setMenu(false) }}>
          <option value="">No group</option>{groups.map(group => <option key={group.id} value={group.id}>{group.name}</option>)}
        </select>
      </label>}
      <AgentActionsMenu name={agent.name} actions={actions} pinned={agent.pinned !== false} onDone={() => setMenu(false)} />
    </div>}
  </div>
}

/** Reuse each agent's existing status projection, including unread completion;
 * grouping never infers completion from its own stream/timer rules. */
function GroupStatusProbe({ agent, report }: { agent: CrewAgentRow; report: (id: string, state: AgentMenuState) => void }) {
  const state = useAgentMenuState(agent.tabId, agent.activity)
  useEffect(() => { report(agent.tabId, state) }, [agent.tabId, state, report])
  return null
}

function AgentGroup({ group, agents, count, onToggle, onRename, onRemove, children }: {
  group: MenuAgentGroup; agents: CrewAgentRow[]; count: number; onToggle: () => void; onRename: (name: string) => void; onRemove: () => void; children: React.ReactNode
}) {
  const [editing, setEditing] = useState(false)
  const [name, setName] = useState(group.name)
  const [marks, setMarks] = useState<Record<string, AgentMenuState>>({})
  const report = useCallback((id: string, state: AgentMenuState) => setMarks(previous => previous[id] === state ? previous : { ...previous, [id]: state }), [])
  const states = agents.map(agent => marks[agent.tabId])
  const groupStates = (['waiting', 'running', 'failed', 'done'] as const).filter(value => states.includes(value))
  return <div className="min-w-0">
    {agents.map(agent => <GroupStatusProbe key={agent.tabId} agent={agent} report={report} />)}
    <div className="group flex min-w-0 items-center gap-1">
      {editing ? <form className="flex min-w-0 flex-1 gap-1 px-2" onSubmit={event => { event.preventDefault(); if (name.trim()) { onRename(name.trim()); setEditing(false) } }}>
        <input autoFocus aria-label="Rename group" maxLength={40} value={name} onChange={event => setName(event.target.value)} onKeyDown={event => { if (event.key === 'Escape') setEditing(false) }} className="min-w-0 flex-1 rounded border border-border bg-background px-1 text-xs" /><button className="text-xs" type="submit">Save</button>
      </form> : <button type="button" aria-expanded={!group.collapsed} onClick={onToggle} className="flex min-w-0 flex-1 items-center gap-1.5 rounded px-2 py-1 text-left text-xs text-muted-foreground hover:text-foreground">
        <ChevronRight className={`h-3 w-3 shrink-0 ${group.collapsed ? '' : 'rotate-90'}`} /><span className="min-w-0 truncate">{group.name}</span><span className="text-[10px]">{count}</span>
        {group.collapsed && <span className="ml-auto flex shrink-0 items-center gap-1">{groupStates.map(state => <AgentMark key={state} state={state} />)}</span>}
      </button>}
      <button type="button" aria-label={`Rename ${group.name}`} onClick={() => { setName(group.name); setEditing(true) }} className="rounded p-1 text-muted-foreground opacity-0 hover:text-foreground group-hover:opacity-100 group-focus-within:opacity-100 max-md:opacity-100"><Pencil className="h-3 w-3" /></button>
      <button type="button" aria-label={`Delete group ${group.name}`} onClick={onRemove} className="rounded p-1 text-muted-foreground opacity-0 hover:text-foreground group-hover:opacity-100 group-focus-within:opacity-100 max-md:opacity-100"><X className="h-3 w-3" /></button>
    </div>
    {!group.collapsed && <div className="pl-3">{children}</div>}
  </div>
}

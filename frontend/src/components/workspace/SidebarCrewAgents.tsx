import { AgentInTeamMark, mostInTeam } from '../chat/AgentInTeamMark'
import { OwnTerminalAgents } from '../../products/work/OwnTerminalAgents'
import { useEffect, useRef, useState, type DragEvent, type ReactNode } from 'react'
import { ChevronRight, MoreHorizontal, GripVertical, Search, X } from 'lucide-react'
import { AgentActionsMenu, useCrewAgentsStore, type CrewAgentRow, type CrewClosedAgent } from '../chat/AgentCard'
import { AgentMark, AgentPlaceMark, AgentSetupMark, shortAgo } from '../chat/AgentIcon'
import { useAgentMenuState } from '../chat/agentMenuState'
import { useChatStore } from '../../stores/useChatStore'
import { openAgentTab } from '../../utils/openAgentTab'
import { teamAgentFor, useTeamTab } from '../../products/work/teamApi'
import { openTeammateAgent } from '../../products/work/openTeammateAgent'
import { groupNames, useAgentGroups, useAgentGroupsStore } from './agentGroups'

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

/** Folded groups and fork trees, per Crew, in this browser (how the menu looks; nothing about the agents). */
function useFolds(projectId: string): [Set<string>, (key: string) => void] {
  const storageKey = `agentworks.sidebar.folds.${projectId}`
  const [folds, setFolds] = useState<Set<string>>(() => {
    try { return new Set(JSON.parse(window.localStorage.getItem(storageKey) || '[]') as string[]) } catch { return new Set() }
  })
  const toggle = (key: string) => setFolds(current => {
    const next = new Set(current)
    if (next.has(key)) next.delete(key); else next.add(key)
    try { window.localStorage.setItem(storageKey, JSON.stringify([...next])) } catch { /* the fold lasts until reload */ }
    return next
  })
  return [folds, toggle]
}

/** One dot for a folded group or tree when anything inside works, waits or has a new reply (each member's own mark). */
function MemberMark({ tabId, activity }: { tabId: string; activity?: 'running' | 'waiting' | null }) {
  const state = useAgentMenuState(tabId, activity)
  return state ? <AgentMark state={state} dot /> : null
}
function FoldedMarks({ agents }: { agents: CrewAgentRow[] }) {
  return <span className="flex shrink-0 [&>*+*]:hidden">{agents.map(agent => <MemberMark key={agent.tabId} tabId={agent.tabId} activity={agent.activity} />)}</span>
}

// Fold arrows sit at the right end of the row, like the project heading's, so rows keep the usual left edge and
// nothing hangs outside the menu (owner, 19 Nov).
const chevronCell = 'grid h-[30px] w-5 shrink-0 place-items-center rounded text-muted-foreground hover:bg-muted hover:text-foreground max-md:min-h-10'
const foldArrow = (open: boolean) => <ChevronRight aria-hidden="true" className={`h-3 w-3 shrink-0 transition-transform ${open ? 'rotate-90' : ''}`} />
const menuItem = 'flex w-full items-center gap-2 rounded-md px-2 py-1 text-left text-xs max-md:min-h-10 max-md:text-sm hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
const nameInput = 'w-full min-w-0 rounded-md border border-primary bg-background px-2 py-1 text-xs max-md:text-base'

/** The sidebar's own agents in their saved order, with two folded lists. Your own groups come first (#3288), and a fork
 * sits under the agent it was forked from. */
export function SidebarCrewAgents({ projectId, popup = false, reordering = false, onSelect, onClosedOpened, hiddenExpansion }: { projectId: string; popup?: boolean; reordering?: boolean; onSelect?: (tabId: string) => void; onClosedOpened?: () => void; hiddenExpansion?: [boolean, (value: boolean) => void] }) {
  const crew = useCrewAgentsStore()
  const activeTabId = useChatStore(state => state.activeTabId)
  const localHiddenExpansion = useState(false)
  const [showHidden, setShowHidden] = hiddenExpansion ?? localHiddenExpansion
  const [showClosed, setShowClosed] = useState(false)
  const [dragging, setDragging] = useState<string | null>(null)
  const [dropOn, setDropOn] = useState('')
  const groups = useAgentGroups(projectId)
  const [folds, toggleFold] = useFolds(projectId)
  // An inline name box: a new group for an agent, or a rename; error is the server's refusal.
  const [naming, setNaming] = useState<{ kind: 'new' | 'rename'; key?: string; group?: string; draft: string; error: string } | null>(null)
  const [picking, setPicking] = useState('')
  const [groupMenu, setGroupMenu] = useState('')
  const [failed, setFailed] = useState('')
  // The Team tab's one server reading: each agent's availability and every person, by account, with their name.
  const team = useTeamTab(projectId).view
  const tabs = useChatStore(state => state.chatTabs)
  if (crew.projectId !== projectId || !crew.agents.length) return <OwnTerminalAgents projectId={projectId} />
  const ownAgents = crew.agents.filter(agent => !agent.ownerId)
  const shown = ownAgents.filter(agent => !agent.sidebarHidden)
  // Keep the server order even when opening a hidden agent replaces its closed row with a live one.
  const hidden = [...ownAgents, ...crew.closed].filter(agent => agent.sidebarHidden)
    .sort((a, b) => (a.sidebarOrder ?? Infinity) - (b.sidebarOrder ?? Infinity))
  const closed = crew.closed.filter(agent => !agent.sidebarHidden)
  // Mark only the odd one out (owner, 18 Nov): a lock on private agents when most are in the team.
  const own = [...ownAgents, ...crew.closed]
  const mostIn = mostInTeam(own.map(agent => agent.inTeam))
  const anyPrivate = own.some(agent => !agent.inTeam)

  const edit = async (change: Parameters<ReturnType<typeof useAgentGroupsStore.getState>['edit']>[1]) => {
    setFailed('')
    try { await useAgentGroupsStore.getState().edit(projectId, change); return '' }
    catch (cause) { const message = cause instanceof Error ? cause.message : 'Couldn’t save this group change. Try again.'; setFailed(message); return message }
  }
  // A fork hangs under its parent when the parent is in this list; a chain that never ends at a root stays flat.
  const byKey = new Map(shown.filter(agent => agent.key).map(agent => [agent.key!, agent]))
  const parentOf = (agent: CrewAgentRow) => {
    const parent = agent.forkedFrom && agent.forkedFrom !== agent.key ? byKey.get(agent.forkedFrom) : undefined
    if (!parent) return undefined
    const seen = new Set([agent.key])
    for (let up: CrewAgentRow | undefined = parent; up; up = up.forkedFrom ? byKey.get(up.forkedFrom) : undefined) {
      if (seen.has(up.key)) return undefined
      seen.add(up.key)
    }
    return parent.key
  }
  const forks = new Map<string, CrewAgentRow[]>()
  const roots: CrewAgentRow[] = []
  for (const agent of shown) {
    const parent = parentOf(agent)
    if (parent) forks.set(parent, [...(forks.get(parent) || []), agent])
    else roots.push(agent)
  }
  const below = (agent: CrewAgentRow): CrewAgentRow[] => (forks.get(agent.key || '') || []).flatMap(child => [child, ...below(child)])
  // Groups hold your own agents and teammates' agents shared with the Team; a key no list knows was unshared.
  const teammates = crew.agents.filter(agent => agent.ownerId && agent.key)
  const known = new Set([...crew.agents.map(agent => agent.key), ...crew.closed.map(agent => agent.key)])
  const names = groupNames(groups)
  const groupOf = (agent: CrewAgentRow) => agent.key ? groups[agent.key] : undefined
  const grouped = new Set(Object.keys(groups))

  const status = (agent: CrewAgentRow) => agent.ownerId ? teamAgentFor(team?.agents, tabs[agent.tabId]?.metadata?.agentProfileConversationKey)?.status_label || '' : ''
  const moveMenu = (agent: CrewAgentRow) => {
    if (!agent.key) return undefined
    const current = groupOf(agent)
    return (done: () => void) => <div role="group" aria-label="Move to group" className="mb-0.5 border-b border-border pb-0.5">
      <p className="px-2 pt-1 text-[11px] text-muted-foreground">Move to group</p>
      {names.filter(name => name !== current).map(name => <button key={name} type="button" role="menuitem" className={menuItem} onClick={() => { void edit({ op: 'move', key: agent.key, group: name }); done() }}>{name}</button>)}
      {!agent.ownerId && <button type="button" role="menuitem" className={menuItem} onClick={() => { setNaming({ kind: 'new', key: agent.key, draft: '', error: '' }); done() }}>New group…</button>}
      {current && <button type="button" role="menuitem" className={menuItem} onClick={() => { void edit({ op: 'move', key: agent.key, group: '' }); done() }}>Take out of {current}</button>}
    </div>
  }
  const row = (agent: CrewAgentRow, isRoot: boolean) => <AgentRow key={agent.tabId} agent={agent} status={status(agent)} mostIn={mostIn} active={agent.tabId === activeTabId}
    dragging={dragging} onDrag={key => { setDragging(key); setDropOn('') }} popup={popup} reordering={reordering}
    person={agent.ownerId ? (agent.owner || 'Teammate') : undefined}
    groupMenu={isRoot ? moveMenu(agent) : undefined}
    onSelect={agent.ownerId && agent.key ? () => { openTeammateAgent(projectId, agent.key!, agent.ownerId); closeDrawer() } : onSelect || openAgent} />
  // An agent and the forks under it; the arrow folds its forks (with a count and a dot while folded).
  const tree = (agent: CrewAgentRow, depth = 0): ReactNode => {
    const children = forks.get(agent.key || '') || []
    const foldKey = `agent:${agent.key}`
    const folded = folds.has(foldKey)
    return <div key={agent.tabId} role="group" aria-label={children.length ? `${agent.name} and its forks` : undefined}>
      <div className="flex min-w-0 items-center">
        <div className="min-w-0 flex-1">{row(agent, depth === 0)}</div>
        {folded && children.length > 0 && <span className="flex shrink-0 items-center gap-1 text-[11px] text-muted-foreground">{below(agent).length} fork{below(agent).length > 1 ? 's' : ''}<FoldedMarks agents={below(agent)} /></span>}
        {children.length > 0 && <button type="button" aria-label={`${folded ? 'Show' : 'Hide'} forks of ${agent.name}`} aria-expanded={!folded} onClick={() => toggleFold(foldKey)} className={chevronCell}>{foldArrow(!folded)}</button>}
      </div>
      {!folded && children.length > 0 && <div className="ml-2 border-l border-border pl-1">{children.map(child => tree(child, depth + 1))}</div>}
    </div>
  }
  const dropZone = (id: string, drop: (key: string) => void) => ({
    onDragOver: (event: DragEvent) => { if (!dragging) return; event.preventDefault(); event.dataTransfer.dropEffect = 'move'; setDropOn(id) },
    onDragLeave: () => setDropOn(current => current === id ? '' : current),
    onDrop: (event: DragEvent) => { event.preventDefault(); const key = dragging; setDragging(null); setDropOn(''); if (key) drop(key) },
  })
  const nameBox = (label: string, save: (name: string) => Promise<string>) => naming && <div className="px-1 py-0.5">
    <input autoFocus aria-label={label} value={naming.draft} placeholder="Group name" className={nameInput}
      onChange={event => setNaming({ ...naming, draft: event.target.value, error: '' })}
      onKeyDown={async event => {
        if (event.key === 'Escape') { event.stopPropagation(); setNaming(null) }
        if (event.key === 'Enter') {
          const error = await save(naming.draft)
          if (error) setNaming(current => current && { ...current, error }); else setNaming(null)
        }
      }} onBlur={() => setNaming(current => current?.error ? current : null)} />
    {naming.error && <p role="alert" className="px-1 pt-0.5 text-[11px] text-destructive">{naming.error}</p>}
  </div>
  const groupBlock = (name: string) => {
    const foldKey = `group:${name}`
    const folded = folds.has(foldKey)
    const members = roots.filter(agent => groupOf(agent) === name)
    const theirs = teammates.filter(agent => groupOf(agent) === name)
    const gone = Object.entries(groups).filter(([key, group]) => group === name && !known.has(key)).map(([key]) => key)
    const inside = [...members.flatMap(agent => [agent, ...below(agent)]), ...theirs]
    const count = members.length + theirs.length
    const renaming = naming?.kind === 'rename' && naming.group === name
    const addable = teammates.filter(agent => groupOf(agent) !== name)
    return <div key={name} role="group" aria-label={`Group ${name}`}>
      {renaming ? nameBox(`New name for ${name}`, draft => edit({ op: 'rename', group: name, name: draft }))
        : <div className={`group relative flex min-w-0 items-center rounded-md ${dropOn === foldKey ? 'bg-primary/10 ring-1 ring-primary' : ''}`} {...dropZone(foldKey, key => { void edit({ op: 'move', key, group: name }) })}>
          <button type="button" aria-expanded={!folded} onClick={() => toggleFold(foldKey)} className={`${sidebarAgentRowClass} gap-1.5 text-xs font-semibold text-muted-foreground hover:bg-muted hover:text-foreground`}>
            <span className="min-w-0 truncate text-foreground">{name}</span>
            <span className="shrink-0 font-normal">{count}</span>
            <span className="flex-1" />
            {folded && <FoldedMarks agents={inside} />}
            {foldArrow(!folded)}
          </button>
          <button type="button" aria-label={`More for group ${name}`} aria-expanded={groupMenu === name} onClick={() => setGroupMenu(current => current === name ? '' : name)}
            className={`absolute right-1 top-1/2 h-6 w-6 -translate-y-1/2 place-items-center rounded text-muted-foreground hover:bg-muted hover:text-foreground max-md:h-9 max-md:w-9 ${groupMenu === name ? 'grid' : 'hidden group-hover:grid group-focus-within:grid max-md:grid [@media(hover:none)]:grid'}`}>
            <MoreHorizontal aria-hidden="true" className="h-3.5 w-3.5" />
          </button>
          {groupMenu === name && <div role="menu" aria-label={`Group ${name} actions`} className="absolute right-0 top-full z-50 mt-1 w-52 space-y-0.5 rounded-xl border border-border bg-popover p-1 shadow-lg">
            <button type="button" role="menuitem" className={menuItem} onClick={() => { setNaming({ kind: 'rename', group: name, draft: name, error: '' }); setGroupMenu('') }}>Rename</button>
            <button type="button" role="menuitem" className={menuItem} onClick={() => { setPicking(name); setGroupMenu('') }}>Add a teammate’s agent…</button>
            <button type="button" role="menuitem" className={`${menuItem} text-destructive`} onClick={() => { void edit({ op: 'ungroup', group: name }); setGroupMenu('') }}>Ungroup</button>
          </div>}
          {picking === name && <TeammateAgentPicker group={name} agents={addable} onPick={agent => { void edit({ op: 'move', key: agent.key, group: name }); setPicking('') }} onClose={() => setPicking('')} />}
        </div>}
      {!folded && <div role="list" aria-label={name} className="ml-2 border-l border-dashed border-border pl-1">
        {members.map(agent => tree(agent))}
        {theirs.map(agent => <div key={agent.tabId}>{row(agent, true)}</div>)}
        {gone.map(key => <div key={key} className="mx-1 my-0.5 rounded-md border border-border px-2 py-1 text-[11px] text-muted-foreground">
          An agent in this group is no longer shared with you, so it can’t be opened.{' '}
          <button type="button" className="font-medium text-foreground underline" onClick={() => { void edit({ op: 'move', key, group: '' }) }}>Remove from group</button>
        </div>)}
        {!count && !gone.length && <p className="px-2 py-1 text-[11px] text-muted-foreground">Drag an agent here.</p>}
      </div>}
    </div>
  }
  const draggedGrouped = dragging ? grouped.has(dragging) : false
  return <div role="list" aria-label="Agents" className={popup ? 'space-y-0.5 pb-1' : sidebarAgentListClass}>
    {names.map(groupBlock)}
    {naming?.kind === 'new' && nameBox('New group name', draft => edit({ op: 'new', key: naming.key, name: draft }))}
    {roots.filter(agent => !groupOf(agent)).map(agent => tree(agent))}
    {draggedGrouped && <div className={`mx-1 my-1 rounded-md border border-dashed px-2 py-2 text-center text-[11px] text-muted-foreground ${dropOn === 'out' ? 'border-primary bg-primary/10' : 'border-border'}`}
      {...dropZone('out', key => { void edit({ op: 'move', key, group: '' }) })}>Drop here to take it out of the group</div>}
    {failed && !naming && <p role="alert" className="px-2 py-1 text-[11px] text-destructive">{failed}</p>}
    {/* Closed agents (no tab here) open again from this list. */}
    {closed.length > 0 && <button type="button" aria-expanded={showClosed} onClick={() => setShowClosed(value => !value)}
      className={sidebarAgentDisclosureClass}>
      <ChevronRight aria-hidden="true" className={`h-3 w-3 transition-transform ${showClosed ? 'rotate-90' : ''}`} />Closed agents ({closed.length})
    </button>}
    {showClosed && closed.map(closedRow)}
    <SidebarHiddenAgents count={hidden.length} expanded={showHidden} onToggle={() => setShowHidden(!showHidden)}>
      {showHidden && hidden.map(agent => 'tabId' in agent ? row(agent, false) : closedRow(agent))}
    </SidebarHiddenAgents>
    <OwnTerminalAgents projectId={projectId} nested mostIn={mostIn} />
    {mostIn && !popup && <p className="px-2 pt-1 text-[11px] text-muted-foreground/80">{anyPrivate ? 'In team unless marked with a lock' : 'All in team'}</p>}
  </div>

  function closedRow(agent: CrewClosedAgent) {
    return <button key={agent.key} type="button" role="listitem" title={agent.purpose || undefined} onClick={() => { void agent.open(); closeDrawer(); onClosedOpened?.() }}
      className={`${sidebarAgentRowClass} gap-2 text-muted-foreground hover:bg-muted hover:text-foreground`}>
      <span className="min-w-0 truncate">{agent.name}</span><AgentInTeamMark on={agent.inTeam} mostIn={mostIn}/>
    </button>
  }
}

/** A teammate's initial, like the Team panel's person mark. */
function PersonInitial({ name }: { name: string }) {
  return <span aria-hidden="true" title={`${name}'s agent`} className="grid h-4 w-4 shrink-0 place-items-center rounded-full bg-primary text-[9px] font-bold text-primary-foreground">{name.trim().charAt(0).toUpperCase() || '?'}</span>
}

function AgentRow({ agent, mostIn, status, active, dragging, onDrag, popup, reordering, onSelect, person, groupMenu }: { mostIn: boolean; popup: boolean; reordering: boolean; onSelect: (tabId: string) => void; agent: CrewAgentRow; status: string; active: boolean; dragging: string | null; onDrag: (key: string | null) => void; person?: string; groupMenu?: (done: () => void) => ReactNode }) {
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
  // A teammate's grouped agent drags by its key too, so it can leave or change groups (#3288).
  const key = actions?.orderKey || (person ? agent.key : undefined)
  const hasActions = canMarkUnread || Boolean(groupMenu) || Boolean(actions && (actions.fork || actions.editorWorkspace || actions.terminal || actions.rename || actions.moveUp || actions.moveDown || actions.setSidebarHidden))
  // Drag a row onto another to change the order, saved the same way as the tab bar's.
  return <div ref={box} data-theme-row="agent" data-theme-agent-state={state || "quiet"} data-theme-agent-selected={active ? "true" : "false"} className={`group relative rounded-md ${over && dragging && dragging !== key ? 'border-t-2 border-primary bg-primary/10' : ''}`}
    draggable={!!key} onDragStart={key ? event => { event.dataTransfer.setData('application/x-agentworks-chat-key', key); event.dataTransfer.setData('text/plain', key); event.dataTransfer.effectAllowed = 'move'; setMenu(false); onDrag(key) } : undefined} onDragEnd={() => { onDrag(null); setOver(false) }}
    onDragOver={dragging && key && actions?.dropHere ? event => { event.preventDefault(); event.dataTransfer.dropEffect = 'move'; setOver(true) } : undefined} onDragLeave={() => setOver(false)}
    onDrop={dragging && key && actions?.dropHere ? event => { event.preventDefault(); event.stopPropagation(); setOver(false); if (dragging !== key) actions?.dropHere?.(dragging); onDrag(null) } : undefined}>
      <button type="button" role="listitem" aria-current={active ? 'page' : undefined} onClick={() => onSelect(agent.tabId)}
        className={`${sidebarAgentRowClass} gap-2 focus:outline-none focus-visible:ring-2 focus-visible:ring-ring max-md:pr-11 ${active ? 'bg-foreground/[0.06] font-medium text-foreground' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}>
        {reordering && agent.actions?.orderKey && <GripVertical aria-hidden="true" className="h-3 w-3 shrink-0 cursor-grab text-muted-foreground" />}
        {person && <PersonInitial name={person} />}
        <span className="block min-w-0 truncate">{agent.name}</span>
        {!agent.ownerId && <AgentInTeamMark on={agent.inTeam} mostIn={mostIn}/>}
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
      {groupMenu?.(() => setMenu(false))}
      <AgentActionsMenu name={agent.name} actions={actions || {}} sidebarHidden={agent.sidebarHidden} onMarkUnread={canMarkUnread ? () => markUnread(agent.tabId) : undefined} onDone={() => setMenu(false)} />
    </div>}
  </div>
}

/** "Add a teammate's agent…": a popover under the group, closed by a click outside, Escape or ×. Agents are listed
 * by teammate, with a search box once the list is long. Adding only puts the agent in your menu (#3288). */
function TeammateAgentPicker({ group, agents, onPick, onClose }: { group: string; agents: CrewAgentRow[]; onPick: (agent: CrewAgentRow) => void; onClose: () => void }) {
  const box = useRef<HTMLDivElement>(null)
  const [query, setQuery] = useState('')
  useEffect(() => {
    const outside = (event: MouseEvent) => { if (!box.current?.contains(event.target as Node)) onClose() }
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') onClose() }
    document.addEventListener('mousedown', outside)
    document.addEventListener('keydown', escape)
    return () => { document.removeEventListener('mousedown', outside); document.removeEventListener('keydown', escape) }
  }, [onClose])
  const q = query.trim().toLowerCase()
  const shown = agents.filter(agent => !q || `${agent.name} ${agent.owner || ''}`.toLowerCase().includes(q))
  const byOwner = new Map<string, CrewAgentRow[]>()
  for (const agent of shown) byOwner.set(agent.owner || 'Teammate', [...(byOwner.get(agent.owner || 'Teammate') || []), agent])
  return <div ref={box} role="dialog" aria-label={`Add a teammate's agent to ${group}`} className="absolute left-0 right-0 top-full z-50 mt-1 flex max-h-80 flex-col rounded-xl border border-border bg-popover text-xs shadow-lg">
    <div className="flex items-center gap-1 border-b border-border px-2 py-1.5">
      <span className="min-w-0 flex-1 truncate font-medium text-foreground">Add to {group}</span>
      <button type="button" aria-label="Close" onClick={onClose} className="grid h-6 w-6 place-items-center rounded text-muted-foreground hover:bg-muted hover:text-foreground"><X aria-hidden="true" className="h-3.5 w-3.5" /></button>
    </div>
    {agents.length > 6 && <label className="mx-1.5 mt-1.5 flex items-center gap-1.5 rounded-md border border-border bg-background px-2 py-1">
      <Search aria-hidden="true" className="h-3 w-3 shrink-0 text-muted-foreground" />
      <input autoFocus value={query} onChange={event => setQuery(event.target.value)} placeholder="Find an agent or teammate" aria-label="Find an agent or teammate" className="min-w-0 flex-1 bg-transparent text-xs outline-none" />
    </label>}
    <div className="min-h-0 flex-1 overflow-y-auto p-1">
      {!agents.length ? <p className="px-1 py-1 text-muted-foreground">No teammate shares an agent with the Team yet.</p>
        : !shown.length ? <p className="px-1 py-1 text-muted-foreground">No agent matches.</p>
        : [...byOwner].map(([owner, list]) => <div key={owner} role="group" aria-label={owner}>
          <p className="flex items-center gap-1.5 px-2 pb-0.5 pt-1.5 text-[11px] font-medium text-muted-foreground"><PersonInitial name={owner} />{owner}</p>
          {list.map(agent => <button key={agent.tabId} type="button" className={`${menuItem} pl-8`} onClick={() => onPick(agent)}>
            <span className="min-w-0 flex-1 truncate text-foreground">{agent.name}</span>
          </button>)}
        </div>)}
    </div>
    <p className="border-t border-border px-2 py-1 text-[10px] text-muted-foreground">Only adds it to your menu; its sharing doesn’t change.</p>
  </div>
}

/** A project's agents when it is not the open one: the same groups and fork tree as the live list, without the
 * editing (that happens once it is open), so switching projects does not reshape the menu (#3288). */
export function SidebarAgentLayout<T>({ projectId, rows: all, keyOf, forkedFromOf, teammate, render }: { projectId?: string; rows: T[]; keyOf: (row: T) => string; forkedFromOf: (row: T) => string | undefined; teammate: (row: T) => boolean; render: (row: T) => ReactNode }) {
  const groups = useAgentGroups(projectId)
  const [folds, toggleFold] = useFolds(projectId || '')
  // A teammate's agent shows only in one of your groups, as in the live list.
  const rows = all.filter(row => !teammate(row) || groups[keyOf(row)])
  const byKey = new Map(rows.map(row => [keyOf(row), row]))
  // A fork hangs under its parent when the parent is listed; a chain that never ends at a root stays flat.
  const parentOf = (row: T) => {
    const first = forkedFromOf(row)
    if (!first || first === keyOf(row) || !byKey.has(first)) return undefined
    const seen = new Set([keyOf(row)])
    for (let up: string | undefined = first; up;) {
      if (seen.has(up)) return undefined
      seen.add(up)
      const next = byKey.get(up)
      up = next ? forkedFromOf(next) : undefined
    }
    return first
  }
  const forks = new Map<string, T[]>()
  const roots: T[] = []
  for (const row of rows) {
    const parent = parentOf(row)
    if (parent) forks.set(parent, [...(forks.get(parent) || []), row]); else roots.push(row)
  }
  const below = (row: T): T[] => (forks.get(keyOf(row)) || []).flatMap(child => [child, ...below(child)])
  const tree = (row: T): ReactNode => {
    const children = forks.get(keyOf(row)) || []
    const foldKey = `agent:${keyOf(row)}`
    const folded = folds.has(foldKey)
    return <div key={keyOf(row)}>
      <div className="flex min-w-0 items-center">
        <div className="min-w-0 flex-1">{render(row)}</div>
        {folded && children.length > 0 && <span className="shrink-0 text-[11px] text-muted-foreground">{below(row).length} fork{below(row).length > 1 ? 's' : ''}</span>}
        {children.length > 0 && <button type="button" aria-label={`${folded ? 'Show' : 'Hide'} forks`} aria-expanded={!folded} onClick={() => toggleFold(foldKey)} className={chevronCell}>{foldArrow(!folded)}</button>}
      </div>
      {!folded && children.length > 0 && <div className="ml-2 border-l border-border pl-1">{children.map(tree)}</div>}
    </div>
  }
  const groupOf = (row: T) => groups[keyOf(row)]
  return <>
    {groupNames(groups).map(name => {
      const members = roots.filter(row => groupOf(row) === name)
      if (!members.length) return null
      const folded = folds.has(`group:${name}`)
      return <div key={name} role="group" aria-label={`Group ${name}`}>
        <button type="button" aria-expanded={!folded} onClick={() => toggleFold(`group:${name}`)} className={`${sidebarAgentRowClass} gap-1.5 text-xs font-semibold text-muted-foreground hover:bg-muted hover:text-foreground`}>
          <span className="min-w-0 truncate text-foreground">{name}</span><span className="shrink-0 font-normal">{members.length}</span>
          <span className="flex-1" />{foldArrow(!folded)}
        </button>
        {!folded && <div className="ml-2 border-l border-dashed border-border pl-1">{members.map(tree)}</div>}
      </div>
    })}
    {roots.filter(row => !groupOf(row)).map(tree)}
  </>
}

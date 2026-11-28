import { isLocalDeployment } from '../../products/productSurfaceConfig'
import { OwnTerminalAgents } from '../../products/work/OwnTerminalAgents'
import { Fragment, useEffect, useRef, useState, type DragEvent, type ReactNode } from 'react'
import { ChevronRight, MoreHorizontal, Search, X } from 'lucide-react'
import { AgentActionsMenu, useCrewAgentsStore, type CrewAgentRow, type CrewClosedAgent } from '../chat/AgentCard'
import { AgentMark, latestTime } from '../chat/AgentIcon'
import { useAgentMenuState } from '../chat/agentMenuState'
import { useChatStore } from '../../stores/useChatStore'
import { useShallow } from 'zustand/react/shallow'
import { openAgentTab } from '../../utils/openAgentTab'
import { teamAgentFor, useTeamTab } from '../../products/work/teamApi'
import { openTeammateAgent } from '../../products/work/openTeammateAgent'
import { groupNames, useAgentGroups, useAgentGroupsStore } from './agentGroups'
import { moveSidebarRow, sidebarAgentId, sidebarGroupId, sidebarRowsInOrder, useSidebarAgentOrder } from './sidebarAgentOrder'

import { sidebarAgentDisclosureClass, sidebarAgentListClass, sidebarGroupHeaderClass } from './sidebarAgentLayout'
import { AgentRowLine, PersonInitial, agentRowThemeProps } from './SidebarAgentRowLine'

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
function useFolds(projectId: string): [Set<string>, (key: string) => void, (key: string) => void] {
  const storageKey = `agentworks.sidebar.folds.${projectId}`
  const [folds, setFolds] = useState<Set<string>>(() => {
    try { return new Set(JSON.parse(window.localStorage.getItem(storageKey) || '[]') as string[]) } catch { return new Set() }
  })
  const persist = (next: Set<string>) => {
    try { window.localStorage.setItem(storageKey, JSON.stringify([...next])) } catch { /* the fold lasts until reload */ }
  }
  const toggle = (key: string) => setFolds(current => {
    const next = new Set(current)
    if (next.has(key)) next.delete(key); else next.add(key)
    persist(next)
    return next
  })
  const unfold = (key: string) => setFolds(current => {
    if (!current.has(key)) return current
    const next = new Set(current)
    next.delete(key)
    persist(next)
    return next
  })
  return [folds, toggle, unfold]
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

/** Personal groups and ungrouped agents share an order; forks stay under their agents. */
export function SidebarCrewAgents({ projectId, popup = false, reordering = false, onSelect, onClosedOpened, hiddenExpansion }: { projectId: string; popup?: boolean; reordering?: boolean; onSelect?: (tabId: string) => void; onClosedOpened?: () => void; hiddenExpansion?: [boolean, (value: boolean) => void] }) {
  const crew = useCrewAgentsStore()
  const activeTabId = useChatStore(state => state.activeTabId)
  const localHiddenExpansion = useState(false)
  const [showHidden, setShowHidden] = hiddenExpansion ?? localHiddenExpansion
  const [showClosed, setShowClosed] = useState(false)
  const [showTeammates, setShowTeammates] = useState(false)
  const [dragging, setDragging] = useState<string | null>(null)
  const [draggingGroup, setDraggingGroup] = useState<string | null>(null)
  const [dropOn, setDropOn] = useState('')
  const groups = useAgentGroups(projectId)
  const { order, save: saveOrder } = useSidebarAgentOrder(projectId)
  const [folds, toggleFold, unfold] = useFolds(projectId)
  // An inline name box: a new group for an agent, or a rename; error is the server's refusal.
  const [naming, setNaming] = useState<{ kind: 'new' | 'rename'; key?: string; group?: string; draft: string; error: string } | null>(null)
  const [picking, setPicking] = useState('')
  const [groupMenu, setGroupMenu] = useState('')
  const [failed, setFailed] = useState('')
  const [forkFailed, setForkFailed] = useState<{ message: string; retry: () => void } | null>(null)
  // The Team tab's one server reading: each agent's availability and every person, by account, with their name.
  const team = useTeamTab(projectId).view
  // Select only teammate conversation keys so composer draft edits on chatTabs never re-render the sidebar.
  const teammateKeys = useChatStore(useShallow(state => {
    const keys: Record<string, string | undefined> = {}
    for (const agent of crew.agents) {
      if (agent.ownerId) keys[agent.tabId] = state.chatTabs[agent.tabId]?.metadata?.agentProfileConversationKey
    }
    return keys
  }))
  // The roster can be incomplete. An absent row does not tell us that sharing
  // changed; keep the saved group so the row can reappear with the next list.
  if (crew.projectId !== projectId || !crew.agents.length) return <OwnTerminalAgents projectId={projectId} />
  const ownAgents = crew.agents.filter(agent => !agent.ownerId)
  // A teammate's agent with no tab open here still shows in its group, as in a project that is not open:
  // switching projects closes its tab, and it used to vanish into Closed agents.
  const ownClosed = crew.closed.filter(agent => !agent.owner)
  const theirClosed = crew.closed.filter(agent => agent.owner)
  const shown = ownAgents.filter(agent => !agent.sidebarHidden)
  // Keep the server order even when opening a hidden agent replaces its closed row with a live one.
  const hidden = [...ownAgents, ...ownClosed].filter(agent => agent.sidebarHidden)
    .sort((a, b) => (a.sidebarOrder ?? Infinity) - (b.sidebarOrder ?? Infinity))
  const closed = ownClosed.filter(agent => !agent.sidebarHidden)

  const edit = async (change: Parameters<ReturnType<typeof useAgentGroupsStore.getState>['edit']>[1]) => {
    setFailed('')
    try {
      await useAgentGroupsStore.getState().edit(projectId, change)
      if (change.group && (change.op === 'rename' || change.op === 'ungroup') && order.includes(sidebarGroupId(change.group))) {
        const old = sidebarGroupId(change.group)
        saveOrder(order.flatMap(id => id !== old ? [id] : change.op === 'rename' && change.name ? [sidebarGroupId(change.name.trim())] : []))
      }
      return ''
    }
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
  // The agent a listed fork hangs under, when it is drawn under it.
  const nestedParent = (key?: string | null) => { const agent = key ? byKey.get(key) : undefined; const parent = agent && parentOf(agent); return parent ? byKey.get(parent) : undefined }
  const forkError = (cause: unknown, fallback: string) => (cause as { response?: { data?: { error?: string } } })?.response?.data?.error || fallback
  // Moving a fork out makes it an agent of its own; its agent can then no longer delete it (#3500). The server decides.
  const moveOut = async (agent: CrewAgentRow, parent: CrewAgentRow): Promise<boolean> => {
    const save = agent.actions?.forkParent
    if (!save || !agent.key || !parent.key) return false
    setForkFailed(null)
    try {
      await save('')
      return true
    } catch (cause) {
      setForkFailed({ message: forkError(cause, `Couldn’t move ${agent.name} out. It’s still under ${parent.name}.`), retry: () => { void moveOut(agent, parent) } })
      return false
    }
  }
  // #4152: check whether a dragged or selected chat can be nested under target (same project, owned, non-Main, no loop).
  const canNestUnder = (fromKey: string, target: CrewAgentRow): { ok: boolean; reason?: string } => {
    const from = byKey.get(fromKey)
    if (!from || !target.key || from.key === target.key) return { ok: false }
    if (from.ownerId || target.ownerId) return { ok: false }
    if (from.main || !from.actions?.forkParent) return { ok: false }
    if (nestedParent(from.key)?.key === target.key) return { ok: false }
    if (below(from).some(descendant => descendant.key === target.key)) return { ok: false, reason: 'Own sub-chat' }
    return { ok: true }
  }
  const nestUnder = async (agent: CrewAgentRow, target: CrewAgentRow): Promise<boolean> => {
    const save = agent.actions?.forkParent
    if (!save || !agent.key || !target.key) return false
    const check = canNestUnder(agent.key, target)
    if (!check.ok) {
      if (check.reason) setForkFailed({ message: `${agent.name} can’t go under ${target.name} — it’s one of its own sub-chats.`, retry: () => { setForkFailed(null) } })
      return false
    }
    setForkFailed(null)
    try {
      await save(target.key)
      unfold(`agent:${target.key}`)
      return true
    } catch (cause) {
      setForkFailed({ message: forkError(cause, `Couldn’t move ${agent.name} under ${target.name}.`), retry: () => { void nestUnder(agent, target) } })
      return false
    }
  }
  // A fork dragged out of its agent's list moves out first, then lands where it was dropped.
  const dropOut = async (key: string, then: () => unknown) => {
    const agent = byKey.get(key)
    const parent = nestedParent(key)
    if (agent && parent && !(await moveOut(agent, parent))) return
    await then()
  }
  const forkMenu = (agent: CrewAgentRow) => {
    const parent = nestedParent(agent.key)
    if (!parent || !agent.actions?.forkParent) return undefined
    return (done: () => void) => <div role="group" aria-label="Fork" className="mb-0.5 border-b border-border pb-0.5">
      <button type="button" role="menuitem" className={menuItem} onClick={() => { void moveOut(agent, parent); done() }}>Move out of {parent.name}</button>
    </div>
  }
  const teammates = crew.agents.filter(agent => agent.ownerId && agent.key)
  const names = groupNames(groups)
  const groupOf = (agent: CrewAgentRow) => agent.key ? groups[agent.key] : undefined
  const grouped = new Set(Object.keys(groups))
  const mixed = names.length > 0
  const rootId = (agent: CrewAgentRow) => agent.key ? sidebarAgentId(agent.key) : `tab:${agent.tabId}`
  const top = sidebarRowsInOrder<string | CrewAgentRow>([
    ...names.map(name => ({ id: sidebarGroupId(name), value: name })),
    ...roots.filter(agent => !groupOf(agent)).map(agent => ({ id: rootId(agent), value: agent })),
  ], mixed ? order : [])
  const topIds = top.map(item => item.id)
  const draggedRoot = roots.find(agent => agent.actions?.orderKey === dragging)
  const draggedTop = draggingGroup !== null ? sidebarGroupId(draggingGroup) : draggedRoot ? rootId(draggedRoot) : null
  const reorderTop = (id: string, before: string | null, visible = topIds) => {
    if (!saveOrder(moveSidebarRow(order, visible, id, before))) setFailed('Couldn’t save this sidebar order. Try again.')
    else setFailed('')
  }
  const moveTop = (id: string, direction: -1 | 1) => {
    const index = topIds.indexOf(id)
    if (index < 0 || !topIds[index + direction]) return
    reorderTop(id, topIds[index + (direction === -1 ? -1 : 2)] ?? null)
  }

  const status = (agent: CrewAgentRow) => agent.ownerId ? teamAgentFor(team?.agents, teammateKeys[agent.tabId])?.status_label || '' : ''
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
  // Dropping a fork onto a row outside its agent's list moves it out too; among its siblings it only reorders.
  const dropTarget = (agent: CrewAgentRow): CrewAgentRow => {
    const id = rootId(agent)
    const position = mixed && agent.actions?.orderKey ? topIds.indexOf(id) : -1
    const moves = position < 0 ? {} : {
      moveUp: position > 0 ? () => moveTop(id, -1) : undefined,
      moveDown: position + 1 < top.length ? () => moveTop(id, 1) : undefined,
    }
    const reorder = agent.actions?.dropHere
    if (!reorder && position < 0) return agent
    return { ...agent, actions: { ...agent.actions, ...moves, dropHere: from => {
      const source = roots.find(row => row.actions?.orderKey === from && !groupOf(row))
      if (source && position >= 0) { reorderTop(rootId(source), id); return }
      if (!reorder) return
      const parent = nestedParent(from)
      if (!parent || parent.key === agent.key || nestedParent(agent.key)?.key === parent.key) { reorder(from); return }
      void dropOut(from, () => reorder(from))
    } } }
  }
  const row = (agent: CrewAgentRow, isRoot: boolean) => {
    const nestTargets = !agent.ownerId && !agent.main && agent.key && agent.actions?.forkParent
      ? shown.filter(candidate => canNestUnder(agent.key!, candidate).ok)
      : undefined
    return <AgentRow key={agent.tabId} agent={dropTarget(agent)} status={status(agent)} active={agent.tabId === activeTabId}
      dragging={dragging} onDrag={key => { setDragging(key); setDropOn('') }} popup={popup} reordering={reordering}
      person={agent.ownerId ? (agent.owner || 'Teammate') : undefined}
      groupMenu={isRoot ? moveMenu(agent) : forkMenu(agent)}
      canNest={agent.key ? fromKey => canNestUnder(fromKey, agent) : undefined}
      onNest={agent.key ? fromKey => { const source = byKey.get(fromKey); if (source) void nestUnder(source, agent) } : undefined}
      nestTargets={nestTargets}
      onNestUnder={target => { void nestUnder(agent, target) }}
      onSelect={agent.ownerId && agent.key ? () => { openTeammateAgent(projectId, agent.key!, agent.ownerId); closeDrawer() } : onSelect || openAgent} />
  }
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
  const between = (before: string | null) => {
    if (!mixed || !draggedTop || draggedTop === before) return null
    const id = 'between:' + (before ?? 'end')
    // Zero-height overlay so appearing on dragstart never shifts rows below out from under the cursor (#4152).
    return <div className="relative !mt-0 h-0">
      <div data-sidebar-drop-before={before ?? ''} title="Drop here to reorder"
        className={`absolute inset-x-1 -top-1 z-20 h-2 rounded ${dropOn === id ? 'bg-primary' : 'bg-border/40'}`}
        onDragOver={event => { event.preventDefault(); event.dataTransfer.dropEffect = 'move'; setDropOn(id) }}
        onDragLeave={() => setDropOn(current => current === id ? '' : current)}
        onDrop={event => {
          event.preventDefault()
          if (draggedRoot?.key && groupOf(draggedRoot)) {
            void edit({ op: 'move', key: draggedRoot.key, group: '' }).then(error => {
              if (!error) reorderTop(draggedTop, before, [...topIds, draggedTop])
            })
          } else reorderTop(draggedTop, before)
          setDragging(null); setDraggingGroup(null); setDropOn('')
        }} />
    </div>
  }
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
    const theirsClosed = theirClosed.filter(agent => groups[agent.key] === name)
    const inside = [...members.flatMap(agent => [agent, ...below(agent)]), ...theirs]
    const count = members.length + theirs.length + theirsClosed.length
    const renaming = naming?.kind === 'rename' && naming.group === name
    const addable = teammates.filter(agent => groupOf(agent) !== name)
    return <div key={name} role="group" aria-label={`Group ${name}`}>
      {renaming ? nameBox(`New name for ${name}`, draft => edit({ op: 'rename', group: name, name: draft }))
        : <div draggable onDragStart={event => {
          event.dataTransfer.setData('text/plain', name); event.dataTransfer.effectAllowed = 'move'
          setDragging(null); setDraggingGroup(name); setDropOn(''); setGroupMenu('')
        }} onDragEnd={() => { setDraggingGroup(null); setDropOn('') }}
          className={`group relative flex min-w-0 items-center rounded-md ${dropOn === foldKey ? 'bg-primary/10 ring-1 ring-primary' : ''}`} {...dropZone(foldKey, key => { void dropOut(key, () => edit({ op: 'move', key, group: name })) })}>
          <button type="button" aria-expanded={!folded} onClick={() => toggleFold(foldKey)} className={sidebarGroupHeaderClass}>
            {/* A group is a heading inside its project: small grey capitals, never bold like the project name (#3369). */}
            <span className="min-w-0 truncate">{name}</span>
            <span className="shrink-0 font-normal tracking-normal">{count}</span>
            <span className="flex-1" />
            {folded && <FoldedMarks agents={inside} />}
            {foldArrow(!folded)}
          </button>
          <button type="button" aria-label={`More for group ${name}`} aria-expanded={groupMenu === name} onClick={() => setGroupMenu(current => current === name ? '' : name)}
            className={`absolute right-1 top-1/2 h-6 w-6 -translate-y-1/2 place-items-center rounded text-muted-foreground hover:bg-muted hover:text-foreground max-md:h-9 max-md:w-9 ${groupMenu === name ? 'grid' : 'hidden group-hover:grid group-focus-within:grid max-md:grid [@media(hover:none)]:grid'}`}>
            <MoreHorizontal aria-hidden="true" className="h-3.5 w-3.5" />
          </button>
          {groupMenu === name && <div role="menu" aria-label={`Group ${name} actions`} className="absolute right-0 top-full z-50 mt-1 w-52 space-y-0.5 rounded-xl border border-border bg-popover p-1 shadow-lg">
            {topIds.indexOf(foldKey) > 0 && <button type="button" role="menuitem" className={menuItem} onClick={() => { moveTop(foldKey, -1); setGroupMenu('') }}>Move up</button>}
            {topIds.indexOf(foldKey) < topIds.length - 1 && <button type="button" role="menuitem" className={menuItem} onClick={() => { moveTop(foldKey, 1); setGroupMenu('') }}>Move down</button>}
            <button type="button" role="menuitem" className={menuItem} onClick={() => { setNaming({ kind: 'rename', group: name, draft: name, error: '' }); setGroupMenu('') }}>Rename</button>
            <button type="button" role="menuitem" className={menuItem} onClick={() => { setPicking(name); setGroupMenu('') }}>Add a teammate’s agent…</button>
            <button type="button" role="menuitem" className={`${menuItem} text-destructive`} onClick={() => { void edit({ op: 'ungroup', group: name }); setGroupMenu('') }}>Ungroup</button>
          </div>}
          {picking === name && <TeammateAgentPicker group={name} agents={addable} onPick={agent => { void edit({ op: 'move', key: agent.key, group: name }); setPicking('') }} onClose={() => setPicking('')} />}
        </div>}
      {!folded && <div role="list" aria-label={name} className="ml-2 border-l border-dashed border-border pl-1">
        {members.map(agent => tree(agent))}
        {theirs.map(agent => <div key={agent.tabId}>{row(agent, true)}</div>)}
        {theirsClosed.map(closedRow)}
        {!count && <p className="px-2 py-1 text-[11px] text-muted-foreground">Drag an agent here.</p>}
      </div>}
    </div>
  }
  const draggedGrouped = dragging ? grouped.has(dragging) : false
  const draggedParent = dragging && byKey.get(dragging)?.actions?.forkParent ? nestedParent(dragging) : undefined
  const ungroupedTeammates = isLocalDeployment() ? [] : teammates.filter(agent => !groupOf(agent))
  const ungroupedTheirClosed = isLocalDeployment() ? [] : theirClosed.filter(agent => !groups[agent.key])
  const teammateCount = ungroupedTeammates.length + ungroupedTheirClosed.length
  return <div role="list" aria-label="Agents" className={popup ? 'space-y-0.5 pb-1' : sidebarAgentListClass}>
    {naming?.kind === 'new' && nameBox('New group name', draft => edit({ op: 'new', key: naming.key, name: draft }))}
    {top.map(({ id, value }) => <Fragment key={id}>{between(id)}{typeof value === 'string' ? groupBlock(value) : tree(value)}</Fragment>)}
    {between(null)}
    {draggedGrouped && <div className={`mx-1 my-1 rounded-md border border-dashed px-2 py-2 text-center text-[11px] text-muted-foreground ${dropOn === 'out' ? 'border-primary bg-primary/10' : 'border-border'}`}
      {...dropZone('out', key => { void edit({ op: 'move', key, group: '' }) })}>Drop here to take it out of the group</div>}
    {draggedParent && <div className={`mx-1 my-1 rounded-md border border-dashed px-2 py-2 text-center text-[11px] text-muted-foreground ${dropOn === 'fork-out' ? 'border-primary bg-primary/10' : 'border-border'}`}
      {...dropZone('fork-out', key => { void dropOut(key, () => undefined) })}>Drop here to move it out of {draggedParent.name}</div>}
    {failed && !naming && <p role="alert" className="px-2 py-1 text-[11px] text-destructive">{failed}</p>}
    {forkFailed && <p role="alert" className="px-2 py-1 text-[11px] text-destructive">{forkFailed.message}{' '}
      <button type="button" className="font-medium underline" onClick={forkFailed.retry}>Try again</button></p>}
    {/* An online project keeps other people's ungrouped chats behind a toggle (owner, 26 Nov),
        so they don't flood the left menu. A local Mac project keeps Team members' agents in groups and the Team view. */}
    {teammateCount > 0 && <button type="button" aria-expanded={showTeammates} onClick={() => setShowTeammates(value => !value)}
      className={sidebarAgentDisclosureClass}>
      <ChevronRight aria-hidden="true" className={`h-3 w-3 transition-transform ${showTeammates ? 'rotate-90' : ''}`} />Teammates’ chats ({teammateCount})
    </button>}
    {showTeammates && ungroupedTeammates.map(agent => <div key={agent.tabId}>{row(agent, true)}</div>)}
    {showTeammates && ungroupedTheirClosed.map(closedRow)}
    {/* Closed agents (no tab here) open again from this list. */}
    {closed.length > 0 && <button type="button" aria-expanded={showClosed} onClick={() => setShowClosed(value => !value)}
      className={sidebarAgentDisclosureClass}>
      <ChevronRight aria-hidden="true" className={`h-3 w-3 transition-transform ${showClosed ? 'rotate-90' : ''}`} />Closed agents ({closed.length})
    </button>}
    {showClosed && closed.map(closedRow)}
    <SidebarHiddenAgents count={hidden.length} expanded={showHidden} onToggle={() => setShowHidden(!showHidden)}>
      {showHidden && hidden.map(agent => 'tabId' in agent ? row(agent, false) : closedRow(agent))}
    </SidebarHiddenAgents>
    <OwnTerminalAgents projectId={projectId} nested />
  </div>

  function closedRow(agent: CrewClosedAgent) {
    return <div key={agent.key} {...agentRowThemeProps(null, false)} className="group relative rounded-md">
      <AgentRowLine name={agent.name} title={agent.purpose} person={agent.owner} inTeam={agent.owner ? undefined : { on: agent.inTeam, share: agent.teamShare }} state={null} onClick={() => { void agent.open(); closeDrawer(); onClosedOpened?.() }} />
    </div>
  }
}


type NestCheck = { ok: boolean; reason?: string }
type RowDropMode = 'before' | 'nest' | 'refuse' | null

function rowDropMode(clientY: number, rect: { top: number; height: number }, canReorder: boolean, nest: NestCheck): RowDropMode {
  const inTopEdge = rect.height > 0 && (clientY - rect.top) / rect.height < 0.3
  if (inTopEdge && canReorder) return 'before'
  if (nest.ok) return 'nest'
  if (nest.reason) return 'refuse'
  return canReorder ? 'before' : null
}

function AgentRow({ agent, status, active, dragging, onDrag, popup, reordering, onSelect, person, groupMenu, canNest, onNest, nestTargets, onNestUnder }: { popup: boolean; reordering: boolean; onSelect: (tabId: string) => void; agent: CrewAgentRow; status: string; active: boolean; dragging: string | null; onDrag: (key: string | null) => void; person?: string; groupMenu?: (done: () => void) => ReactNode; canNest?: (fromKey: string) => NestCheck; onNest?: (fromKey: string) => void; nestTargets?: CrewAgentRow[]; onNestUnder?: (target: CrewAgentRow) => void }) {
  const state = useAgentMenuState(agent.tabId, agent.activity)
  // An idle row shows when it was last active, from the active-session list the app already holds (nothing new is read).
  const lastActivity = useChatStore(store => {
    const sessionId = store.chatTabs[agent.tabId]?.sessionId
    return sessionId ? store.activeSessionsCache.find(session => session.session_id === sessionId)?.last_activity : undefined
  })
  // After a restart no session is live yet: the saved last message keeps the time (#3406).
  const lastAt = !state && !agent.asking && !status ? latestTime(lastActivity, agent.lastMessageAt) : undefined
  // The program the chat runs now, as the agent card shows it.
  const provider = useChatStore(store => { const meta = store.chatTabs[agent.tabId]?.metadata; return meta?.agentProfileEngine || meta?.agentProfileChatProvider })
  const markUnread = useChatStore(store => store.markTabUnread)
  // Only a quiet or failed agent can go back on the to-read list; working, needs-you and unread rows already show a mark.
  const canMarkUnread = state === null || state === 'failed'
  const [menu, setMenu] = useState(false)
  const [pickingParent, setPickingParent] = useState(false)
  const [dropState, setDropState] = useState<{ mode: RowDropMode; reason?: string }>({ mode: null })
  const box = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!menu) { setPickingParent(false); return }
    const close = (event: MouseEvent) => { if (!box.current?.contains(event.target as Node)) setMenu(false) }
    document.addEventListener('mousedown', close)
    return () => document.removeEventListener('mousedown', close)
  }, [menu])
  const actions = agent.actions
  // A teammate's grouped agent drags by its key too, so it can leave or change groups (#3288).
  const dragKey = actions?.orderKey || (person ? agent.key : undefined)
  // Main has agent.key without orderKey, so other chats can still drop onto Main to nest under it (#4152).
  const targetKey = agent.key || dragKey
  const nestCheck = dragging && targetKey && dragging !== targetKey && canNest ? canNest(dragging) : { ok: false }
  const canDrop = Boolean(dragging && targetKey && dragging !== targetKey && (actions?.dropHere || nestCheck.ok || nestCheck.reason))
  const hasNestMenu = Boolean(nestTargets?.length && onNestUnder)
  const hasActions = canMarkUnread || Boolean(groupMenu) || hasNestMenu || Boolean(agent.info?.onRemove) || Boolean(actions && (actions.fork || actions.rename || actions.moveUp || actions.moveDown || actions.setSidebarHidden))
  const dropClass = !dragging || dragging === targetKey || !dropState.mode ? ''
    : dropState.mode === 'before' ? 'border-t-2 border-primary bg-primary/10'
    : dropState.mode === 'nest' ? 'ring-1 ring-primary bg-primary/10'
    : 'ring-1 ring-destructive bg-destructive/10'
  // Top edge reorders above this row; middle/body nests the dragged chat under it (#4152).
  return <div ref={box} data-sidebar-agent-row={targetKey} {...agentRowThemeProps(state, active)} className={`group relative rounded-md ${dropClass}`}
    draggable={!!dragKey} onDragStart={dragKey ? event => { event.dataTransfer.setData('application/x-agentworks-chat-key', dragKey); event.dataTransfer.setData('text/plain', dragKey); event.dataTransfer.effectAllowed = 'move'; setMenu(false); onDrag(dragKey) } : undefined} onDragEnd={() => { onDrag(null); setDropState({ mode: null }) }}
    onDragOver={canDrop ? event => {
      event.preventDefault()
      event.dataTransfer.dropEffect = 'move'
      const mode = rowDropMode(event.clientY, event.currentTarget.getBoundingClientRect(), Boolean(actions?.dropHere), nestCheck)
      setDropState(current => current.mode === mode && current.reason === nestCheck.reason ? current : { mode, reason: nestCheck.reason })
    } : undefined}
    onDragLeave={event => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDropState({ mode: null }) }}
    onDrop={canDrop ? event => {
      event.preventDefault()
      event.stopPropagation()
      const mode = dropState.mode || rowDropMode(event.clientY, event.currentTarget.getBoundingClientRect(), Boolean(actions?.dropHere), nestCheck)
      setDropState({ mode: null })
      if (dragging && dragging !== targetKey) {
        if (mode === 'nest' || mode === 'refuse') onNest?.(dragging)
        else if (mode === 'before') actions?.dropHere?.(dragging)
      }
      onDrag(null)
    } : undefined}>
      <AgentRowLine name={agent.name} person={person} inTeam={agent.ownerId ? undefined : { on: agent.inTeam, share: agent.teamShare }} setup={agent.info?.setup && { ...agent.info.setup, reach: '' }}
        place={agent.place} status={status} lastAt={lastAt} provider={provider} state={state} asking={agent.asking} active={active} grip={reordering && !!agent.actions?.orderKey} onClick={() => onSelect(agent.tabId)} />
    {dragging && dragging !== targetKey && (dropState.mode === 'nest' || dropState.mode === 'refuse') && <span
      className={`pointer-events-none absolute right-1.5 top-1/2 -translate-y-1/2 rounded-full px-1.5 py-0.5 text-[10px] font-semibold ${dropState.mode === 'nest' ? 'bg-primary text-primary-foreground' : 'bg-destructive text-destructive-foreground'}`}>
      {dropState.mode === 'nest' ? 'Make sub-chat' : (dropState.reason || 'Can’t drop')}
    </span>}
    {hasActions && <button type="button" aria-label={`More for ${agent.name}`} aria-expanded={menu} onClick={() => setMenu(value => !value)}
      className={`absolute right-1 ${popup ? 'top-0.5' : 'top-1/2 -translate-y-1/2'} h-6 w-6 place-items-center rounded text-muted-foreground hover:bg-muted hover:text-foreground max-md:h-9 max-md:w-9 ${menu ? 'grid' : 'hidden group-hover:grid group-focus-within:grid max-md:grid [@media(hover:none)]:grid'}`}>
      <MoreHorizontal aria-hidden="true" className="h-3.5 w-3.5" />
    </button>}
    {/* Inside the popup, actions scroll with the list rather than clipping at its edge. */}
    {menu && hasActions && <div className={`${popup ? 'relative mx-1 my-1' : 'absolute right-0 top-full z-50 mt-1 w-48'} rounded-xl border border-border bg-popover p-1 shadow-lg`}>
      {pickingParent && hasNestMenu ? <div role="group" aria-label={`Make ${agent.name} a sub-chat under`}>
        <p className="px-2 pt-1 text-[11px] text-muted-foreground">Make sub-chat under…</p>
        <div className="max-h-52 overflow-y-auto">
          {nestTargets!.map(target => <button key={target.tabId} type="button" role="menuitem" className={menuItem} onClick={() => { onNestUnder!(target); setMenu(false) }}>
            <span className="min-w-0 flex-1 truncate">{target.name}</span>
          </button>)}
        </div>
        <div className="mt-0.5 border-t border-border pt-0.5">
          <button type="button" role="menuitem" className={menuItem} onClick={() => setPickingParent(false)}>← Back</button>
        </div>
      </div> : <>
        {groupMenu?.(() => setMenu(false))}
        {hasNestMenu && <div className="mb-0.5 border-b border-border pb-0.5">
          <button type="button" role="menuitem" className={`${menuItem} justify-between`} onClick={() => setPickingParent(true)}>
            <span>Move under…</span><ChevronRight aria-hidden="true" className="h-3 w-3 shrink-0 text-muted-foreground" />
          </button>
        </div>}
        <AgentActionsMenu name={agent.name} actions={actions || {}} sidebarHidden={agent.sidebarHidden} onMarkUnread={canMarkUnread ? () => markUnread(agent.tabId) : undefined} onRemove={agent.info?.onRemove} onDone={() => setMenu(false)} />
      </>}
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
          {list.map(agent => agent.messageOnly
            // Agent only (owner, 28 Nov): its person shares the agent, not its chat, so it can't sit in your menu.
            ? <button key={agent.tabId} type="button" disabled aria-disabled="true" title={`${owner} shares this agent, not its chat. Ask it from the Team panel.`} className={`${menuItem} pl-8 cursor-not-allowed opacity-50 hover:bg-transparent`}>
              <span className="min-w-0 flex-1 truncate text-foreground">{agent.name}</span>
              <span className="shrink-0 rounded-full bg-muted px-1.5 py-0.5 text-[10px] font-medium leading-none text-muted-foreground">Agent only</span>
            </button>
            : <button key={agent.tabId} type="button" className={`${menuItem} pl-8`} onClick={() => onPick(agent)}>
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
  const { order } = useSidebarAgentOrder(projectId)
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
  const names = groupNames(groups)
  return <>
    {sidebarRowsInOrder<{ kind: 'group'; name: string } | { kind: 'agent'; row: T }>([
      ...names.map(name => ({ id: sidebarGroupId(name), value: { kind: 'group' as const, name } })),
      ...roots.filter(row => !groupOf(row)).map(row => ({ id: sidebarAgentId(keyOf(row)), value: { kind: 'agent' as const, row } })),
    ], names.length ? order : []).map(({ value }) => {
      if (value.kind === 'agent') return tree(value.row)
      const { name } = value
      const members = roots.filter(row => groupOf(row) === name)
      const folded = folds.has(`group:${name}`)
      return <div key={name} role="group" aria-label={`Group ${name}`}>
        <button type="button" aria-expanded={!folded} onClick={() => toggleFold(`group:${name}`)} className={sidebarGroupHeaderClass}>
          <span className="min-w-0 truncate">{name}</span><span className="shrink-0 font-normal tracking-normal">{members.length}</span>
          <span className="flex-1" />{foldArrow(!folded)}
        </button>
        {!folded && <div className="ml-2 border-l border-dashed border-border pl-1">{members.map(tree)}
          {!members.length && <p className="px-2 py-1 text-[11px] text-muted-foreground">Open the project to add agents here.</p>}
        </div>}
      </div>
    })}
  </>
}

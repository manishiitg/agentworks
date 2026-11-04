import { useEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { ArrowDown, ArrowUp, ChevronDown, Eye, EyeOff, Info, Pencil, Trash2 } from 'lucide-react'
import { create } from 'zustand'
import { AGENT_KEY, AgentIcon, AgentMark } from './AgentIcon'
import { useAgentMenuState } from './agentMenuState'
import { activateTab } from '../../utils/activateTab'
import { useMobileViewport } from '../../hooks/useMobileViewport'

// A Crew agent's details (owner, 4 Nov, tab option B): the tab strip shows names only; hovering a name (~400 ms, or a
// long press on touch) opens one card, and the open tab's purpose sits in one line at the top of its chat.
export interface AgentCardInfo {
  name: string
  /** Its emoji, once it has one; the first letter stands in until then. */
  icon?: string
  purpose?: string
  /** "tester-b", "everyone in the company"; undefined while the agent is not shared. */
  sharedWith?: string
  canChange?: boolean
  lastAsked?: { who: string; when: string; active: boolean; open: () => void }
  onSetPurpose: () => void
  onSetIcon?: () => void
  /** Removes the agent for good (its chat is deleted); only for its owner, never Main. */
  onRemove?: () => void
  /** Opens Access at this agent; only where sharing can be set from here. */
  onSharing?: () => void
}

/** The open tab's agent, for the purpose line above its chat; the tab strip sets it. */
export const useOpenAgentStore = create<{ tabId: string | null; info: AgentCardInfo | null; set: (tabId: string | null, info: AgentCardInfo | null) => void }>(set => ({
  tabId: null,
  info: null,
  set: (tabId, info) => set({ tabId, info }),
}))

/** One Crew's agents as its tab strip knows them, for the agent list in the app's left menu. */
/** What the person may do with one agent, from the tab strip's own handlers; a missing one is not theirs to do. */
export interface CrewAgentActions {
  /** The saved order's key, for dragging rows. */
  orderKey?: string
  renameDraft?: string
  rename?: (name: string) => Promise<unknown>
  moveUp?: () => void
  moveDown?: () => void
  /** Moves the dragged agent (by its order key) to this one's place. */
  dropHere?: (fromKey: string) => void
  setPinned?: (pinned: boolean) => void
  /** Removes it for good (its chat is deleted). */
  remove?: () => void
}
/** pinned: shown in the list (and on the tab bar); the rest sit under "Hidden agents". */
export interface CrewAgentRow { tabId: string; name: string; main: boolean; pinned?: boolean; /** Its Cmd/Ctrl+number, in the left menu's order. */ shortcut?: number; asking?: boolean; info?: AgentCardInfo; activity?: 'running' | 'waiting' | null; actions?: CrewAgentActions }
/** An agent the server lists for the Crew that has no open tab here; open() opens it again and switches to it. */
export interface CrewClosedAgent { key: string; name: string; purpose?: string; icon?: string; open: () => Promise<unknown> }
export const useCrewAgentsStore = create<{ projectId: string | null; name: string; agents: CrewAgentRow[]; closed: CrewClosedAgent[]; canAdd: boolean; set: (projectId: string, name: string, agents: CrewAgentRow[], canAdd: boolean, closed?: CrewClosedAgent[]) => void }>(set => ({
  projectId: null,
  name: '',
  agents: [],
  closed: [],
  canAdd: false,
  set: (projectId, name, agents, canAdd, closed = []) => set({ projectId, name, agents, canAdd, closed }),
}))

/** The person's choice of how agents are shown ("menu" or "tabs"), once it has loaded from the server. */
export const useAgentsViewStore = create<{ view: 'menu' | 'tabs' | null; set: (view: 'menu' | 'tabs') => void }>(set => ({
  view: null,
  set: view => set({ view }),
}))

export function AgentHoverCard({ info, children, side = 'below', clickToOpen = false, actions, pinned, shortcut }: { info?: AgentCardInfo; children: ReactNode; side?: 'below' | 'right'; clickToOpen?: boolean; actions?: CrewAgentActions; pinned?: boolean; shortcut?: number }) {
  const anchor = useRef<HTMLSpanElement>(null)
  const box = useRef<HTMLDivElement>(null)
  const [at, setAt] = useState<{ top: number; left: number } | null>(null)
  const [removing, setRemoving] = useState(false)
  const timer = useRef<number | undefined>(undefined)
  // On a phone the card is a bottom sheet over a backdrop; the backdrop closes it with its own click, so the tap does
  // not fall through to what is underneath.
  const sheet = useMobileViewport()
  const backdrop = useRef<HTMLDivElement>(null)
  const pressed = useRef(false)
  useEffect(() => {
    if (!at) return
    const close = (event: MouseEvent | TouchEvent) => { const target = event.target as Node; if (!box.current?.contains(target) && !anchor.current?.contains(target) && !backdrop.current?.contains(target)) setAt(null) }
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') setAt(null) }
    document.addEventListener('mousedown', close)
    document.addEventListener('touchstart', close)
    document.addEventListener('keydown', escape)
    return () => { document.removeEventListener('mousedown', close); document.removeEventListener('touchstart', close); document.removeEventListener('keydown', escape) }
  }, [at])
  useEffect(() => () => window.clearTimeout(timer.current), [])
  if (!info) return <>{children}</>
  const open = () => {
    const rect = anchor.current?.getBoundingClientRect()
    if (!rect) return
    // The collapsed rail opens the card beside it; everywhere else below the name.
    if (side === 'right') setAt({ top: Math.max(8, Math.min(rect.top - 8, window.innerHeight - 240)), left: rect.right + 8 })
    else setAt({ top: rect.bottom + 8, left: Math.max(8, Math.min(rect.left - 8, window.innerWidth - 336)) })
  }
  const later = (ms: number, action: () => void) => { window.clearTimeout(timer.current); timer.current = window.setTimeout(action, ms) }
  const finish = (action: () => void) => { setAt(null); action() }
  return <>
    <span ref={anchor} className={side === 'right' ? 'block' : 'min-w-0'}
      onPointerEnter={event => { if (event.pointerType === 'mouse') later(400, open) }} onPointerLeave={event => { if (event.pointerType === 'mouse') later(200, () => setAt(null)) }}
      onTouchStart={() => { pressed.current = false; later(500, () => { pressed.current = true; open() }) }} onTouchEnd={() => { if (!at) window.clearTimeout(timer.current) }}
      onTouchMove={() => { if (!at) window.clearTimeout(timer.current) }}
      /* The click that ends a long press only opened the card; it does not also open the agent. */
      onClickCapture={event => { if (pressed.current) { pressed.current = false; event.preventDefault(); event.stopPropagation() } }}
      onClick={clickToOpen ? () => { window.clearTimeout(timer.current); if (at) setAt(null); else open() } : undefined}>
      {children}
    </span>
    {at && createPortal(<>{sheet && <div ref={backdrop} aria-hidden="true" className="fixed inset-0 z-[100] bg-black/40" onClick={event => { event.stopPropagation(); setAt(null) }} />}
    <div ref={box} role="dialog" aria-label={info.name} style={sheet ? undefined : at} onClick={event => event.stopPropagation()}
      onPointerEnter={() => window.clearTimeout(timer.current)} onPointerLeave={event => { if (event.pointerType === 'mouse') later(200, () => setAt(null)) }}
      className={sheet
        ? 'fixed inset-x-0 bottom-0 z-[100] max-h-[80dvh] overflow-y-auto overscroll-contain rounded-t-2xl border-t border-border bg-popover p-4 pb-[calc(1rem+env(safe-area-inset-bottom))] text-sm shadow-lg'
        : 'fixed z-[100] w-80 rounded-2xl border border-border bg-popover p-3 text-xs shadow-lg'}>
      <p className="flex items-center gap-2 text-sm font-semibold"><span className="min-w-0 flex-1 truncate">{info.name}</span>{shortcut && <kbd className="rounded border border-border px-1 font-sans text-[10px] font-normal text-muted-foreground">{AGENT_KEY}{shortcut}</kbd>}</p>
      <p className={`mb-2 mt-1 ${info.purpose ? 'text-muted-foreground' : 'text-muted-foreground/70'}`}>{info.purpose || 'No purpose yet. Others see it when you share this agent.'}</p>
      <div className="flex justify-between gap-3 border-t border-border py-1.5"><span>Shared with</span><span className="min-w-0 truncate text-muted-foreground">{info.sharedWith || 'Only you'}</span></div>
      {info.sharedWith && <div className="flex justify-between gap-3 border-t border-border py-1.5"><span>For them it</span><span className="text-muted-foreground">{info.canChange ? 'can make changes' : 'answers only'}</span></div>}
      {info.lastAsked && <div className="flex justify-between gap-3 border-t border-border py-1.5"><span>Last asked</span>
        <button type="button" onClick={() => finish(info.lastAsked!.open)} className="min-w-0 truncate text-primary hover:underline">{info.lastAsked.who} · {info.lastAsked.when}</button></div>}
      <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 max-md:[&>button]:min-h-10">
        <button type="button" onClick={() => finish(info.onSetPurpose)} className="text-primary hover:underline">{info.purpose ? 'Change purpose' : 'Set purpose'}</button>
        {info.onSetIcon && <button type="button" onClick={() => finish(info.onSetIcon!)} className="text-primary hover:underline">{info.icon ? 'Change icon' : 'Set icon'}</button>}
        {info.onSharing && <button type="button" onClick={() => finish(info.onSharing!)} className="text-primary hover:underline">{info.sharedWith ? 'Sharing…' : 'Share…'}</button>}
      </div>
      {info.onRemove && <div className="mt-2 flex items-center gap-3 border-t border-border pt-1.5">
        {removing ? <>
          <span className="text-muted-foreground">Remove {info.name}? Its chat is deleted.</span>
          <button type="button" onClick={() => { setRemoving(false); finish(info.onRemove!) }} className="text-destructive hover:underline">Remove</button>
          <button type="button" onClick={() => setRemoving(false)} className="text-muted-foreground hover:underline">Keep</button>
        </> : <button type="button" onClick={() => setRemoving(true)} className="text-muted-foreground hover:text-destructive hover:underline max-md:min-h-10">Remove agent…</button>}
      </div>}
      {actions && <div className="mt-2 border-t border-border pt-1.5"><AgentActionsMenu name={info.name} actions={actions} pinned={pinned} onDone={() => setAt(null)} /></div>}
      {!sheet && <p className="mt-2 border-t border-border pt-1.5 text-[11px] text-muted-foreground">{AGENT_KEY}← {AGENT_KEY}→ switch agents</p>}
    </div></>, document.body)}
  </>
}

/** The top line of a Crew's chat (owner, 4 Nov): "<Crew name> › <agent name> ▾ ⓘ", no icon. The name opens a list of all the Crew's agents, so they can be
 * reached with the left menu closed (owner, 4 Nov: "show it in header"). The Crew name opens its
 * team board; ⓘ opens the agent's card (purpose, sharing, ✎). Purpose text is not shown inline. */
export function AgentHeaderLine({ tabId, crewName, projectId }: { tabId?: string | null; crewName: string; projectId?: string }) {
  const open = useOpenAgentStore()
  const row = useCrewAgentsStore(state => (tabId ? state.agents.find(agent => agent.tabId === tabId) : undefined))
  const info = open.tabId && open.tabId === tabId ? open.info : null
  const name = row?.name || info?.name
  return <div className="mr-2 flex h-10 min-w-0 shrink items-center gap-1.5 px-1 text-sm md:h-8" data-agent-header>
    {projectId
      ? <button type="button" data-project-title title={crewName} aria-label={`${crewName}: see all agents`}
          onClick={() => window.dispatchEvent(new CustomEvent('agentworks:open-team-board', { detail: { projectId } }))}
          className="min-w-0 max-w-28 truncate rounded-md px-1 font-semibold text-muted-foreground hover:bg-muted md:max-w-40 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">{crewName}</button>
      : <span data-project-title title={crewName} className="min-w-0 max-w-28 truncate px-1 md:max-w-40 font-semibold text-muted-foreground">{crewName}</span>}
    {name && <>
      <span aria-hidden="true" className="shrink-0 text-muted-foreground/60">›</span>
      <AgentPicker name={name} tabId={tabId} projectId={projectId} />
      {info && <span className="shrink-0"><AgentHoverCard info={info} clickToOpen shortcut={row?.shortcut} actions={row?.actions} pinned={row?.pinned !== false}>
        <button type="button" aria-label={`About ${name}`} className="grid h-5 w-5 place-items-center max-md:h-8 max-md:w-8 rounded-full text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><Info aria-hidden="true" className="h-3.5 w-3.5" /></button>
      </AgentHoverCard></span>}
    </>}
  </div>
}

/** One agent's actions, the same as the tab strip's (owner, 4 Nov): rename, move up or down, hide from or show in the
 * list (owner, 4 Nov: Close = Hide). Used by the left menu's "…" and the agent card in the collapsed rail. */
export function AgentActionsMenu({ name, actions, pinned, onDone }: { name: string; actions: CrewAgentActions; pinned?: boolean; onDone: () => void }) {
  const [draft, setDraft] = useState<string | null>(null)
  const [removing, setRemoving] = useState(false)
  const item = 'flex w-full items-center gap-2 rounded-md px-2 py-1 text-left text-xs max-md:min-h-10 max-md:text-sm hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
  const run = (action: () => void) => { action(); onDone() }
  if (draft !== null) return <input autoFocus aria-label={`New name for ${name}`} value={draft} onChange={event => setDraft(event.target.value)}
    onKeyDown={event => {
      if (event.key === 'Escape') { event.stopPropagation(); setDraft(null) }
      if (event.key === 'Enter' && draft.trim()) { void actions.rename?.(draft.trim()); onDone() }
    }}
    onBlur={() => setDraft(null)} className="w-full rounded-md border border-border bg-background px-2 py-1 text-xs max-md:text-base" />
  return <div role="menu" aria-label={`${name} actions`} className="space-y-0.5">
    {actions.rename && <button type="button" role="menuitem" className={item} onClick={() => setDraft(actions.renameDraft || name)}><Pencil aria-hidden="true" className="h-3.5 w-3.5" />Rename</button>}
    {actions.moveUp && <button type="button" role="menuitem" className={item} onClick={() => run(actions.moveUp!)}><ArrowUp aria-hidden="true" className="h-3.5 w-3.5" />Move up</button>}
    {actions.moveDown && <button type="button" role="menuitem" className={item} onClick={() => run(actions.moveDown!)}><ArrowDown aria-hidden="true" className="h-3.5 w-3.5" />Move down</button>}
    {actions.setPinned && (pinned
      ? <button type="button" role="menuitem" className={item} onClick={() => run(() => actions.setPinned!(false))}><EyeOff aria-hidden="true" className="h-3.5 w-3.5" />Hide from list</button>
      : <button type="button" role="menuitem" className={item} onClick={() => run(() => actions.setPinned!(true))}><Eye aria-hidden="true" className="h-3.5 w-3.5" />Show in list</button>)}
    {actions.remove && (removing
      ? <div className="border-t border-border px-2 pt-1.5 text-xs"><p className="text-muted-foreground">Remove {name}? Its chat is deleted.</p>
          <div className="mt-1 flex gap-3"><button type="button" className="text-destructive hover:underline" onClick={() => run(actions.remove!)}>Remove</button><button type="button" className="text-muted-foreground hover:underline" onClick={() => setRemoving(false)}>Keep</button></div></div>
      : <button type="button" role="menuitem" className={`${item} border-t border-border text-destructive`} onClick={() => setRemoving(true)}><Trash2 aria-hidden="true" className="h-3.5 w-3.5" />Remove agent…</button>)}
  </div>
}

/** The agent name in the top line, as a button: a list of every agent of the Crew (icon, name, status dot), the open one
 * highlighted, and "All agents" for the team board (owner, 4 Nov). */
function AgentPicker({ name, tabId, projectId }: { name: string; tabId?: string | null; projectId?: string }) {
  const agents = useCrewAgentsStore(state => state.agents)
  const total = useCrewAgentsStore(state => state.agents.length + state.closed.length)
  const [open, setOpen] = useState(false)
  const [at, setAt] = useState<{ left: number; top: number } | null>(null)
  const box = useRef<HTMLDivElement>(null)
  const list = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const close = (event: MouseEvent) => { if (!box.current?.contains(event.target as Node) && !list.current?.contains(event.target as Node)) setOpen(false) }
    const esc = (event: KeyboardEvent) => { if (event.key === 'Escape') setOpen(false) }
    document.addEventListener('mousedown', close)
    document.addEventListener('keydown', esc)
    return () => { document.removeEventListener('mousedown', close); document.removeEventListener('keydown', esc) }
  }, [open])
  if (agents.length < 2 && !projectId) return <span className="min-w-0 max-w-48 truncate font-semibold">{name}</span>
  return <div ref={box} className="relative min-w-0">
    <button type="button" aria-haspopup="listbox" aria-expanded={open} onClick={() => {
      const rect = box.current?.getBoundingClientRect()
      if (rect) setAt({ left: Math.max(8, Math.min(rect.left, window.innerWidth - 264)), top: rect.bottom + 4 })
      setOpen(value => !value)
    }}
      className="flex min-w-0 max-w-56 items-center gap-1 rounded-md px-1 font-semibold hover:bg-muted max-md:min-h-8 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
      <span className="min-w-0 truncate">{name}</span><ChevronDown aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
    </button>
    {open && at && createPortal(<div ref={list} role="listbox" aria-label="Agents" style={{ left: at.left, top: at.top }} className="fixed z-[100] max-h-[60vh] w-64 overflow-y-auto rounded-xl border border-border bg-popover p-1 font-normal shadow-lg">
      {agents.map(agent => <AgentPickerRow key={agent.tabId} agent={agent} active={agent.tabId === tabId} onPick={() => { setOpen(false); activateTab(agent.tabId) }} />)}
      {projectId && <button type="button" onClick={() => { setOpen(false); window.dispatchEvent(new CustomEvent('agentworks:open-team-board', { detail: { projectId } })) }}
        className="mt-0.5 flex w-full items-center rounded-md border-t border-border px-2 py-1.5 text-left text-xs text-muted-foreground hover:bg-muted hover:text-foreground max-md:min-h-10">All agents ({total})</button>}
    </div>, document.body)}
  </div>
}

function AgentPickerRow({ agent, active, onPick }: { agent: CrewAgentRow; active: boolean; onPick: () => void }) {
  const state = useAgentMenuState(agent.tabId, agent.activity)
  return <button type="button" role="option" aria-selected={active} onClick={onPick}
    className={`flex w-full min-w-0 items-center gap-2 rounded-md px-2 py-1.5 text-left text-[13px] max-md:min-h-10 ${active ? 'bg-foreground/[0.06] font-medium text-foreground' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}>
    <AgentIcon name={agent.name} icon={agent.info?.icon} active={active} shared={agent.info?.sharedWith} />
    <span className="min-w-0 flex-1 truncate">{agent.name}</span>
    {agent.shortcut && <span className="shrink-0 text-[10px] text-muted-foreground">{AGENT_KEY}{agent.shortcut}</span>}
    <AgentMark state={state} asking={agent.asking} />
  </button>
}

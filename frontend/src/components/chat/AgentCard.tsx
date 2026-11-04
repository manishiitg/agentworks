import { useEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { ArrowDown, ArrowUp, Eye, EyeOff, Info, Pencil, X } from 'lucide-react'
import { create } from 'zustand'
import { AGENT_KEY, AgentIcon } from './AgentIcon'

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
  close?: () => void
}
/** pinned: shown in the list (and on the tab bar); the rest sit under "Hidden agents". */
export interface CrewAgentRow { tabId: string; name: string; main: boolean; pinned?: boolean; /** Its Cmd/Ctrl+number, in the left menu's order. */ shortcut?: number; asking?: boolean; info?: AgentCardInfo; activity?: 'running' | 'waiting' | null; actions?: CrewAgentActions }
export const useCrewAgentsStore = create<{ projectId: string | null; name: string; agents: CrewAgentRow[]; canAdd: boolean; set: (projectId: string, name: string, agents: CrewAgentRow[], canAdd: boolean) => void }>(set => ({
  projectId: null,
  name: '',
  agents: [],
  canAdd: false,
  set: (projectId, name, agents, canAdd) => set({ projectId, name, agents, canAdd }),
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
  const timer = useRef<number | undefined>(undefined)
  useEffect(() => {
    if (!at) return
    const close = (event: MouseEvent | TouchEvent) => { if (!box.current?.contains(event.target as Node) && !anchor.current?.contains(event.target as Node)) setAt(null) }
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
      onMouseEnter={() => later(400, open)} onMouseLeave={() => later(200, () => setAt(null))}
      onTouchStart={() => later(500, open)} onTouchEnd={() => { if (!at) window.clearTimeout(timer.current) }}
      onClick={clickToOpen ? () => { window.clearTimeout(timer.current); if (at) setAt(null); else open() } : undefined}>
      {children}
    </span>
    {at && createPortal(<div ref={box} role="dialog" aria-label={info.name} style={at} onClick={event => event.stopPropagation()}
      onMouseEnter={() => window.clearTimeout(timer.current)} onMouseLeave={() => later(200, () => setAt(null))}
      className="fixed z-[100] w-80 rounded-2xl border border-border bg-popover p-3 text-xs shadow-lg">
      <p className="flex items-center gap-2 text-sm font-semibold"><span className="min-w-0 flex-1 truncate">{info.name}</span>{shortcut && <kbd className="rounded border border-border px-1 font-sans text-[10px] font-normal text-muted-foreground">{AGENT_KEY}{shortcut}</kbd>}</p>
      <p className={`mb-2 mt-1 ${info.purpose ? 'text-muted-foreground' : 'text-muted-foreground/70'}`}>{info.purpose || 'No purpose yet. Others see it when you share this agent.'}</p>
      <div className="flex justify-between gap-3 border-t border-border py-1.5"><span>Shared with</span><span className="min-w-0 truncate text-muted-foreground">{info.sharedWith || 'Only you'}</span></div>
      {info.sharedWith && <div className="flex justify-between gap-3 border-t border-border py-1.5"><span>For them it</span><span className="text-muted-foreground">{info.canChange ? 'can make changes' : 'answers only'}</span></div>}
      {info.lastAsked && <div className="flex justify-between gap-3 border-t border-border py-1.5"><span>Last asked</span>
        <button type="button" onClick={() => finish(info.lastAsked!.open)} className="min-w-0 truncate text-primary hover:underline">{info.lastAsked.who} · {info.lastAsked.when}</button></div>}
      <div className="mt-2 flex gap-4">
        <button type="button" onClick={() => finish(info.onSetPurpose)} className="text-primary hover:underline">{info.purpose ? 'Change purpose' : 'Set purpose'}</button>
        {info.onSetIcon && <button type="button" onClick={() => finish(info.onSetIcon!)} className="text-primary hover:underline">{info.icon ? 'Change icon' : 'Set icon'}</button>}
        {info.onSharing && <button type="button" onClick={() => finish(info.onSharing!)} className="text-primary hover:underline">{info.sharedWith ? 'Sharing…' : 'Share…'}</button>}
      </div>
      {actions && <div className="mt-2 border-t border-border pt-1.5"><AgentActionsMenu name={info.name} actions={actions} pinned={pinned} onDone={() => setAt(null)} /></div>}
      <p className="mt-2 border-t border-border pt-1.5 text-[11px] text-muted-foreground">{AGENT_KEY}← {AGENT_KEY}→ switch agents</p>
    </div>, document.body)}
  </>
}

/** The top line of a Crew's chat (owner, 4 Nov): "<Crew name> › <icon> <agent name> ⓘ". The Crew name opens its
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
          className="max-w-40 truncate rounded-md px-1 font-semibold text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">{crewName}</button>
      : <span data-project-title title={crewName} className="max-w-40 truncate px-1 font-semibold text-muted-foreground">{crewName}</span>}
    {name && <>
      <span aria-hidden="true" className="text-muted-foreground/60">›</span>
      <AgentIcon name={name} icon={info?.icon} active shared={info?.sharedWith} />
      <span className="min-w-0 max-w-48 truncate font-semibold">{name}</span>
      {info && <AgentHoverCard info={info} clickToOpen shortcut={row?.shortcut} actions={row?.actions} pinned={row?.pinned !== false}>
        <button type="button" aria-label={`About ${name}`} className="grid h-5 w-5 place-items-center rounded-full text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><Info aria-hidden="true" className="h-3.5 w-3.5" /></button>
      </AgentHoverCard>}
    </>}
  </div>
}

/** One agent's actions, the same as the tab strip's (owner, 4 Nov): rename, move up or down, hide from or show in the
 * list, and close. Used by the left menu's "…" and the agent card in the collapsed rail. */
export function AgentActionsMenu({ name, actions, pinned, onDone }: { name: string; actions: CrewAgentActions; pinned?: boolean; onDone: () => void }) {
  const [draft, setDraft] = useState<string | null>(null)
  const item = 'flex w-full items-center gap-2 rounded-md px-2 py-1 text-left text-xs hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
  const run = (action: () => void) => { action(); onDone() }
  if (draft !== null) return <input autoFocus aria-label={`New name for ${name}`} value={draft} onChange={event => setDraft(event.target.value)}
    onKeyDown={event => {
      if (event.key === 'Escape') { event.stopPropagation(); setDraft(null) }
      if (event.key === 'Enter' && draft.trim()) { void actions.rename?.(draft.trim()); onDone() }
    }}
    onBlur={() => setDraft(null)} className="w-full rounded-md border border-border bg-background px-2 py-1 text-xs" />
  return <div role="menu" aria-label={`${name} actions`} className="space-y-0.5">
    {actions.rename && <button type="button" role="menuitem" className={item} onClick={() => setDraft(actions.renameDraft || name)}><Pencil aria-hidden="true" className="h-3.5 w-3.5" />Rename</button>}
    {actions.moveUp && <button type="button" role="menuitem" className={item} onClick={() => run(actions.moveUp!)}><ArrowUp aria-hidden="true" className="h-3.5 w-3.5" />Move up</button>}
    {actions.moveDown && <button type="button" role="menuitem" className={item} onClick={() => run(actions.moveDown!)}><ArrowDown aria-hidden="true" className="h-3.5 w-3.5" />Move down</button>}
    {actions.setPinned && (pinned
      ? <button type="button" role="menuitem" className={item} onClick={() => run(() => actions.setPinned!(false))}><EyeOff aria-hidden="true" className="h-3.5 w-3.5" />Hide from list</button>
      : <button type="button" role="menuitem" className={item} onClick={() => run(() => actions.setPinned!(true))}><Eye aria-hidden="true" className="h-3.5 w-3.5" />Show in list</button>)}
    {actions.close && <button type="button" role="menuitem" className={item} onClick={() => run(actions.close!)}><X aria-hidden="true" className="h-3.5 w-3.5" />Close</button>}
  </div>
}

import { HeaderAgentSharing } from '../../products/work/ShareCrew'
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { ArrowDown, ArrowUp, ChevronDown, Clock, Eye, EyeOff, GitFork, Loader2, MailOpen, Pencil, SquareTerminal, Trash2, X } from 'lucide-react'
import { create } from 'zustand'
import { useChatStore } from '../../stores/useChatStore'
import { AGENT_KEY, AgentPlaceMark, enabledAgentAutomations } from './AgentIcon'
import { AgentDetailSection } from './AgentDetailSection'
import { CodingCliIcon } from './CodingCliIcon'
import { useLLMStore } from '../../stores/useLLMStore'
import { codingProviderName } from '../../utils/codingProviderName'
import type { TeamTabSetup } from '../../products/work/teamApi'
import { useMobileViewport } from '../../hooks/useMobileViewport'

// A Crew agent's details (owner, 4 Nov, tab option B): the tab strip shows names only; hovering a name deliberately (1 second, or a
// long press on touch) opens one card, and the open tab's purpose sits in one line at the top of its chat.
export interface AgentCardInfo {
  name: string
  sharing?: { projectId: string; agentKey: string }
  examples?: string[]; purpose?: string
  /** "tester-b", "everyone in the company"; undefined while the agent is not shared. */
  sharedWith?: string
  canChange?: boolean
  /** Shared with chosen people only (the server row's audience). */
  sharedChosen?: boolean
  lastAsked?: { who: string; when: string; active: boolean; open: () => void }
  /** Only on your own agents; a teammate's agent is theirs to change. */
  onSetPurpose?: () => void
  /** Only on your own agents: whether it has its own standing instructions (undefined when not known) and how to edit them. */
  instructions?: { set?: boolean; onEdit: () => void }
  /** A teammate's agent: whose it is (it is shared with the team). */
  owner?: string
  /** A teammate's agent: the server's line about their Mac, under Now. */
  presence?: string
  /** Removes the agent for good (its chat is deleted); only for its owner, never Main. */
  onRemove?: () => void
  /** Opens Access at this agent; only where sharing can be set from here. */
  onSharing?: () => void
  /** Its tab, for the Now row (the same state as the mark left of its name); activity is a Crew Main's own. */
  tabId?: string
  activity?: 'running' | 'waiting' | null
  /** Its setup icon and rows, from the server (crew_tab_setup.go). */
  setup?: TeamTabSetup
  /** Sends the server's Automations… message to this agent's own chat; only on your own agents. */
  onAutomations?: () => void
  onAddAutomation?: () => void
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
  forkTo?: () => void
  fork?: () => void | Promise<void>
  terminal?: () => void | Promise<void>
  /** The saved order's key, for dragging rows. */
  orderKey?: string
  renameDraft?: string
  rename?: (name: string) => Promise<unknown>
  moveUp?: () => void
  moveDown?: () => void
  /** Moves the dragged agent (by its order key) to this one's place. */
  dropHere?: (fromKey: string) => void
  setSidebarHidden?: (hidden: boolean) => void
  /** A fork: hang it under that agent's key, or '' to move it out to its own place (#3504). */
  forkParent?: (parentKey: string) => Promise<void>
}
export interface CrewAgentRow { /** Its conversation key, for groups (#3288). */ key?: string; /** When its chat last had a message (saved history), for "2m" before any session is live (#3406). */ lastMessageAt?: string; /** The conversation key of the agent it was forked from. */ forkedFrom?: string; sidebarHidden?: boolean; sidebarOrder?: number; inTeam?:boolean; tabId: string; name: string; main: boolean; /** Where it runs, as the server lists it: 'server' shows a cloud in the left menu. */ place?: 'mac' | 'server'; /** Whose agent it is when it is someone else's (a Team or co-owner Crew), by account and with the server's display name; absent for your own. */ ownerId?: string; owner?: string; /** Its Cmd/Ctrl+number, in the left menu's order. */ shortcut?: number; asking?: boolean; info?: AgentCardInfo; activity?: 'running' | 'waiting' | null; actions?: CrewAgentActions }
/** An agent the server lists for the Crew that has no open tab here; open() opens it again and switches to it. */
export interface CrewClosedAgent { /** A teammate's agent: their name. */ owner?: string; forkedFrom?: string; sidebarHidden?: boolean; sidebarOrder?: number; inTeam?:boolean; key: string; name: string; examples?: string[]; purpose?: string; open: () => Promise<unknown> }
export const useCrewAgentsStore = create<{ projectId: string | null; name: string; agents: CrewAgentRow[]; closed: CrewClosedAgent[]; canAdd: boolean; canImportChat: boolean; canDiscoverSkills: boolean; set: (projectId: string, name: string, agents: CrewAgentRow[], canAdd: boolean, closed?: CrewClosedAgent[], imports?: { chat: boolean; skills: boolean }) => void }>(set => ({
  projectId: null,
  name: '',
  agents: [],
  closed: [],
  canAdd: false,
  canImportChat: false,
  canDiscoverSkills: false,
  set: (projectId, name, agents, canAdd, closed = [], imports) => set({ projectId, name, agents, canAdd, closed, canImportChat: !!imports?.chat, canDiscoverSkills: !!imports?.skills }),
}))

// The online copy answers teammates while this Mac sleeps; always on for shared agents (owner, 16 Nov).
function AgentOnlineCopyStatus({ setup }: { setup?: TeamTabSetup }) {
 const copy = setup?.online_copy
 if (!copy) return null
 return <div className="flex justify-between gap-3 border-t border-border py-2"><span className="text-muted-foreground">When Mac sleeps</span><span>{copy.state === 'ready' ? `Online copy answers${copy.synced_at ? ` · synced ${Math.max(0, Math.floor((Date.now() - Date.parse(copy.synced_at)) / 60000))}m ago` : ''}` : 'Online copy preparing'}</span></div>
}

let agentHoverCooldownUntil = 0

export function AgentHoverCard({ info, children, side = 'below', clickToOpen = false, actions, shortcut, onRename, automationShortcut = false }: { info?: AgentCardInfo; children: ReactNode | ((detailsOpen: boolean) => ReactNode); side?: 'below' | 'right'; clickToOpen?: boolean; actions?: CrewAgentActions; shortcut?: number; onRename?: () => void; automationShortcut?: boolean }) {
  const [automationsOpen, setAutomationsOpen] = useState(false)
  const anchor = useRef<HTMLSpanElement>(null)
  const box = useRef<HTMLDivElement>(null)
  const [at, setAt] = useState<{ top: number; left: number; view: 'details' | 'automations' } | null>(null)
  const trigger = useRef<HTMLButtonElement | null>(null)
  const timer = useRef<number | undefined>(undefined)
  const activeTabId = useChatStore(state => state.activeTabId)
  const dismiss = useCallback(() => {
    window.clearTimeout(timer.current)
    agentHoverCooldownUntil = Date.now() + 3000
    setAt(null)
  }, [])
  useEffect(() => { dismiss() }, [activeTabId, dismiss])
  useEffect(() => {
    const scroll = (event: Event) => {
      // Only a scroll that moves the agent name matters; the chat transcript auto-scrolling while a reply streams does not.
      const target = event.target as Node
      if (!box.current?.contains(target) && anchor.current && target.contains(anchor.current)) dismiss()
    }
    window.addEventListener('scroll', scroll, true)
    return () => window.removeEventListener('scroll', scroll, true)
  }, [dismiss])
  // On a phone the card is a bottom sheet over a backdrop; the backdrop closes it with its own click, so the tap does
  // not fall through to what is underneath.
  const sheet = useMobileViewport()
  const backdrop = useRef<HTMLDivElement>(null)
  const pressed = useRef(false)
  useEffect(() => {
    if (!at) return
    if (clickToOpen) box.current?.focus()
    const close = (event: MouseEvent | TouchEvent) => { const target = event.target as Node; if (!box.current?.contains(target) && !anchor.current?.contains(target) && !backdrop.current?.contains(target)) dismiss() }
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') { dismiss(); trigger.current?.focus() } }
    document.addEventListener('mousedown', close)
    document.addEventListener('touchstart', close)
    document.addEventListener('keydown', escape)
    return () => { document.removeEventListener('mousedown', close); document.removeEventListener('touchstart', close); document.removeEventListener('keydown', escape) }
  }, [at, clickToOpen, dismiss])
  useEffect(() => () => window.clearTimeout(timer.current), [])
  const content = typeof children === 'function' ? children(at?.view === 'details') : children
  if (!info) return <>{content}</>
  const enabled = enabledAgentAutomations(info.setup)
  const open = (view: 'details' | 'automations' = 'details') => {
    const rect = anchor.current?.getBoundingClientRect()
    if (!rect) return
    setAutomationsOpen(false)
    // The collapsed rail opens the card beside it; everywhere else below the name.
    if (side === 'right') setAt({ top: Math.max(8, Math.min(rect.top - 8, window.innerHeight - 240)), left: rect.right + 8, view })
    else setAt({ top: rect.bottom + 8, left: Math.max(8, Math.min(rect.left - 8, window.innerWidth - 448)), view })
  }
  const later = (ms: number, action: () => void) => { window.clearTimeout(timer.current); timer.current = window.setTimeout(action, ms) }
  const finish = (action: () => void) => { dismiss(); action() }
  const close = (label: string) => <button type="button" aria-label={label} onClick={() => { dismiss(); trigger.current?.focus() }}
    className="grid h-6 w-6 shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring max-md:h-10 max-md:w-10"><X aria-hidden="true" className="h-3.5 w-3.5" /></button>
  const addAutomation = info.onAddAutomation && <button type="button" className="text-primary hover:underline" onClick={() => finish(info.onAddAutomation!)}>✦ Ask this agent to add an automation</button>
  const setupDetails = <>
      <div className="mb-2 mt-1 flex min-w-0 items-center gap-2 text-muted-foreground"><p className="min-w-0 flex-1 truncate" title={info.purpose}>{info.purpose || 'Add a purpose'}</p>{info.onSetPurpose && <button type="button" onClick={() => finish(info.onSetPurpose!)} className="shrink-0 text-primary hover:underline">{info.purpose ? 'Edit' : 'Set up'}</button>}</div>
      {info.instructions && <div className="flex justify-between gap-3 border-t border-border py-1.5"><span>Instructions</span><span className="flex min-w-0 items-center gap-2 text-muted-foreground"><span className="truncate">{info.instructions.set === undefined ? 'Its own rules' : info.instructions.set ? 'Has its own' : 'None yet'}</span><button type="button" onClick={() => finish(info.instructions!.onEdit)} className="shrink-0 text-primary hover:underline">{info.instructions.set === false ? 'Set up' : 'Edit'}</button></span></div>}
      {!!info.examples?.length && <p className="mb-2 text-muted-foreground">Ask me to: {info.examples.join(' · ')}</p>}
  </>
  return <>
    <span ref={anchor} className={side === 'right' ? 'block' : 'inline-flex min-w-0 items-center gap-1.5'}
      onPointerEnter={event => { if (!clickToOpen && event.pointerType === 'mouse' && Date.now() >= agentHoverCooldownUntil) later(1000, () => { if (Date.now() >= agentHoverCooldownUntil) open() }) }} onPointerLeave={event => { if (!clickToOpen && event.pointerType === 'mouse') { window.clearTimeout(timer.current); if (at) later(200, dismiss) } }}
      onTouchStart={() => { if (clickToOpen) return; pressed.current = false; later(500, () => { pressed.current = true; open() }) }} onTouchEnd={() => { if (!at) window.clearTimeout(timer.current) }}
      onTouchMove={() => { if (!at) window.clearTimeout(timer.current) }}
      /* The click that ends a long press only opened the card; it does not also open the agent. */
      onClickCapture={event => { if (pressed.current) { pressed.current = false; event.preventDefault(); event.stopPropagation() } else if (!clickToOpen) dismiss() }}
      onClick={clickToOpen ? event => {
        window.clearTimeout(timer.current)
        trigger.current = (event.target as Element).closest('button')
        if (at?.view === 'details') dismiss(); else open()
      } : undefined}>
      {content}
      {automationShortcut && enabled.length > 0 && <button type="button" aria-label={`${enabled.length} enabled automation${enabled.length === 1 ? '' : 's'}`} aria-expanded={at?.view === 'automations'} aria-haspopup="dialog"
        title={enabled.map(item => `${item.kind}: ${item.name} · ${item.line}`).join('\n')}
        className="inline-flex h-7 shrink-0 items-center gap-1 rounded-md bg-primary/10 px-1.5 text-xs font-medium text-primary hover:bg-primary/15 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring max-md:h-10 max-md:px-2"
        onClick={event => {
          event.stopPropagation()
          trigger.current = event.currentTarget
          if (at?.view === 'automations') dismiss(); else open('automations')
        }}><Clock aria-hidden="true" className="h-3.5 w-3.5" />{enabled.length}</button>}
    </span>
    {at && createPortal(<>{sheet && <div ref={backdrop} aria-hidden="true" className="fixed inset-0 z-[100] bg-black/40" onClick={event => { event.stopPropagation(); dismiss() }} />}
    <div ref={box} tabIndex={clickToOpen ? -1 : undefined} role="dialog" aria-label={at.view === 'automations' ? `Automations for ${info.name}` : info.name} style={sheet ? undefined : { top: at.top, left: at.left }} onClick={event => event.stopPropagation()}
      onPointerEnter={() => window.clearTimeout(timer.current)} onPointerLeave={event => { if (!clickToOpen && event.pointerType === 'mouse') later(200, dismiss) }}
      className={sheet
        ? 'fixed inset-x-0 bottom-0 z-[100] max-h-[80dvh] overflow-y-auto overscroll-contain rounded-t-2xl border-t border-border bg-popover p-4 pb-[calc(1rem+env(safe-area-inset-bottom))] text-sm shadow-lg'
        : 'fixed z-[100] max-h-[80vh] w-[min(28rem,calc(100vw-16px))] overflow-y-auto rounded-2xl border border-border bg-popover p-3 text-xs shadow-lg'}>
      {at.view === 'automations' ? <>
        <div className="flex items-center justify-between gap-2">
          <p className="text-sm font-semibold">Automations</p>
          {close('Close automations')}
        </div>
        <p className="mb-2 mt-1 text-muted-foreground">Enabled for {info.name}</p>
        <AgentAutomationList items={enabled} empty="No enabled automations." />
      </> : <>
      <div className="flex items-center gap-2 text-sm font-semibold"><p className="min-w-0 flex-1 truncate">{info.name}</p>{shortcut && <kbd className="rounded border border-border px-1 font-sans text-[10px] font-normal text-muted-foreground">{AGENT_KEY}{shortcut}</kbd>}{clickToOpen && close('Close agent details')}</div>
      <AgentModelRow tabId={info.tabId} compact={clickToOpen} />
      {clickToOpen ? <AgentDetailSection title="Setup" value={info.purpose || 'Add a purpose'}>{setupDetails}</AgentDetailSection> : setupDetails}
      {info.sharing && <HeaderAgentSharing {...info.sharing} status={!info.owner ? <AgentOnlineCopyStatus setup={info.setup} /> : undefined} />}
      {info.owner ? <div className="flex justify-between gap-3 border-t border-border py-1.5"><span>Whose</span><span className="min-w-0 truncate text-muted-foreground">{info.owner}'s · shared with the team</span></div>
        : !info.sharing && <div className="flex justify-between gap-3 border-t border-border py-1.5"><span>Shared with</span><span className="min-w-0 truncate text-muted-foreground">{info.sharedWith || 'Only you'}</span></div>}
      {!info.sharing && info.sharedWith && <div className="flex justify-between gap-3 border-t border-border py-1.5"><span>For them it</span><span className="text-muted-foreground">{info.canChange ? 'can make changes' : 'answers only'}</span></div>}
      {info.lastAsked && <div className="flex justify-between gap-3 border-t border-border py-1.5"><span>Last asked</span>
        <button type="button" onClick={() => finish(info.lastAsked!.open)} className="min-w-0 truncate text-primary hover:underline">{info.lastAsked.who} · {info.lastAsked.when}</button></div>}
      {info.setup?.agentbridge_line && <div className="flex justify-between gap-3 border-t border-border py-1.5"><span>AgentBridge</span><span className="text-emerald-500">{info.setup.agentbridge_line}</span></div>}
      {!info.owner && !info.sharing && <AgentOnlineCopyStatus setup={info.setup} />}
      {(info.setup || info.onAutomations || info.onAddAutomation) && (clickToOpen
        ? <AgentDetailSection title="Automations" value={enabled.length ? `${enabled.length} enabled` : info.setup?.automations.length ? 'None enabled' : 'None'}>
          {info.setup?.next_automation && <p className="text-muted-foreground">Next: {info.setup.next_automation}</p>}
          <AgentAutomationList items={info.setup?.automations ?? []} empty="No automations yet." />
          {addAutomation}
        </AgentDetailSection>
        : <div className="space-y-1 border-t border-border py-2">
          <div className="flex items-center justify-between gap-2"><span>Automations</span><button type="button" aria-expanded={automationsOpen} onClick={() => setAutomationsOpen(!automationsOpen)} className="text-primary hover:underline">{automationsOpen ? 'Close' : 'Open ›'}</button></div>
          {automationsOpen && <AgentAutomationList items={info.setup?.automations ?? []} empty="No automations yet." />}
          {addAutomation}
        </div>)}
      {(actions?.forkTo || actions?.terminal || onRename || (!info.sharing && info.onSharing)) && <div className="flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-border pt-2 max-md:[&>button]:min-h-10">
        {actions?.forkTo && <button type="button" onClick={() => finish(actions.forkTo!)} className="inline-flex items-center gap-1.5 rounded-md py-1 text-left hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><GitFork aria-hidden className="h-3.5 w-3.5" />Send a copy to a teammate…</button>}
        {/* Resume this agent's chat in your own Claude Code: in the agent's top menu (owner, 20 Nov), not the left menu. */}
        {actions?.terminal && <button type="button" onClick={() => finish(actions.terminal!)} className="inline-flex items-center gap-1.5 rounded-md py-1 text-left hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><SquareTerminal aria-hidden className="h-3.5 w-3.5" />Continue in my terminal</button>}
        {onRename && <button type="button" onClick={() => finish(onRename)} className="inline-flex items-center gap-1 text-primary hover:underline"><Pencil aria-hidden="true" className="h-3 w-3" />Rename</button>}
        {!info.sharing && info.onSharing && <button type="button" onClick={() => finish(info.onSharing!)} className="text-primary hover:underline">{info.sharedWith ? 'Sharing…' : 'Share…'}</button>}
      </div>}
      </>}
    </div></>, document.body)}
  </>
}

function AgentAutomationList({ items, empty }: { items: TeamTabSetup['automations']; empty: string }) {
  return <ul className="space-y-1 py-1 text-muted-foreground">{items.map((item, index) => <li key={index}>{item.name} · {item.kind} · {item.line}</li>)}{!items.length && <li>{empty}</li>}</ul>
}

/** The Crew header shows only the current agent; switching and ordering stay in the sidebar. */
export function AgentHeaderLine({ agent: row, crewName, onOpenIdentity, onReturn }: { agent?: CrewAgentRow; crewName: string; onOpenIdentity?: () => void; onReturn?: () => void }) {
  // Other mounted Crews also project the sidebar/open-agent stores. The header
  // must use this pane's own row and actions, not whichever projection ran last.
  const info = row?.info
  const name = row?.name
  return <div className="mr-2 flex h-10 min-w-0 shrink items-center gap-1.5 px-1 text-sm md:h-8" data-agent-header>
    {onOpenIdentity ? <button type="button" data-project-title onClick={onOpenIdentity} title="Identity & instructions"
      aria-label={`Identity & instructions for ${crewName}`} className="min-w-0 max-w-28 truncate rounded-sm px-1 font-semibold text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring md:max-w-40">{crewName}</button>
      : <span data-project-title title={crewName} className="min-w-0 max-w-28 truncate px-1 md:max-w-40 font-semibold text-muted-foreground">{crewName}</span>}
    {name && <>
      <span aria-hidden="true" className="shrink-0 text-muted-foreground/60">›</span>
      <AgentPlaceMark place={row?.place} />
      {/* Watching a teammate's agent: your own agent's name takes you back to it. */}
      {onReturn ? <button type="button" onClick={onReturn} title={`Back to your ${name}`} aria-label={`Back to your ${name}`}
        className="block min-w-0 max-w-48 truncate rounded-sm font-semibold text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">{name}</button>
      : info ? <AgentHoverCard info={info} clickToOpen automationShortcut shortcut={row?.shortcut} actions={row?.actions}>
        {open => <button type="button" aria-label={`About ${name}`} title={name} aria-expanded={open} aria-haspopup="dialog"
          className="inline-flex min-w-0 max-w-48 items-center gap-1 rounded-md px-1.5 py-1 font-semibold hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring max-md:min-h-10">
          <span className="min-w-0 truncate">{name}</span><ChevronDown aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
        </button>}
      </AgentHoverCard> : <span title={name} className="min-w-0 max-w-48 truncate font-semibold">{name}</span>}
    </>}
  </div>
}

/** Actions for an agent in the left menu. */
/** The left menu's "…" for one agent. "Remove agent…" sits here, last, not in the agent's top menu (owner, 20 Nov);
 * it opens the same "Remove … for good?" step as before. */
export function AgentActionsMenu({ name, actions, sidebarHidden, onMarkUnread, onRemove, onDone }: { name: string; actions: CrewAgentActions; sidebarHidden?: boolean; onMarkUnread?: () => void; onRemove?: () => void; onDone: () => void }) {
  const [draft, setDraft] = useState<string | null>(null)
  const [forking, setForking] = useState(false)
  const item = 'flex w-full items-center gap-2 rounded-md px-2 py-1 text-left text-xs max-md:min-h-10 max-md:text-sm hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
  const run = (action: () => void) => { action(); onDone() }
  if (draft !== null) return <input autoFocus aria-label={`New name for ${name}`} value={draft} onChange={event => setDraft(event.target.value)}
    onKeyDown={event => {
      if (event.key === 'Escape') { event.stopPropagation(); setDraft(null) }
      if (event.key === 'Enter' && draft.trim()) { void actions.rename?.(draft.trim()); onDone() }
    }}
    onBlur={() => setDraft(null)} className="w-full rounded-md border border-border bg-background px-2 py-1 text-xs max-md:text-base" />
  return <div role="menu" aria-label={`${name} actions`} className="space-y-0.5">
    {onMarkUnread && <button type="button" role="menuitem" className={item} onClick={() => run(onMarkUnread)}><MailOpen aria-hidden="true" className="h-3.5 w-3.5" />Mark as unread</button>}
    {actions.fork && <button type="button" role="menuitem" disabled={forking} aria-busy={forking} className={`${item} disabled:cursor-wait disabled:opacity-70`} onClick={async () => {
      if (forking) return
      setForking(true)
      try { await actions.fork!() } finally { setForking(false); onDone() }
    }}>{forking ? <Loader2 aria-hidden="true" className="h-3.5 w-3.5 animate-spin" /> : <GitFork aria-hidden="true" className="h-3.5 w-3.5" />}{forking ? 'Forking chat…' : 'Fork chat'}</button>}
    {forking && <p role="status" className="px-2 py-1 text-[11px] text-muted-foreground">Copying conversation history. Your new chat will open when ready.</p>}
    {actions.rename && <button type="button" role="menuitem" className={item} onClick={() => setDraft(actions.renameDraft || name)}><Pencil aria-hidden="true" className="h-3.5 w-3.5" />Rename</button>}
    {actions.moveUp && <button type="button" role="menuitem" className={item} onClick={() => run(actions.moveUp!)}><ArrowUp aria-hidden="true" className="h-3.5 w-3.5" />Move up</button>}
    {actions.moveDown && <button type="button" role="menuitem" className={item} onClick={() => run(actions.moveDown!)}><ArrowDown aria-hidden="true" className="h-3.5 w-3.5" />Move down</button>}
    {actions.setSidebarHidden && <button type="button" role="menuitem" className={item} onClick={() => run(() => actions.setSidebarHidden!(!sidebarHidden))}>
      {sidebarHidden ? <><Eye aria-hidden="true" className="h-3.5 w-3.5" />Show in sidebar</> : <><EyeOff aria-hidden="true" className="h-3.5 w-3.5" />Hide from sidebar</>}
    </button>}
    {onRemove && <button type="button" role="menuitem" className={`${item} text-destructive`} onClick={() => run(onRemove)}><Trash2 aria-hidden="true" className="h-3.5 w-3.5" />Remove agent…</button>}
  </div>
}

/** Which program and model the agent answers with (owner, 18 Nov: the left menu no longer shows the program logo on
 * hover). The same server label the composer shows; nothing when the tab has not loaded it. */
function AgentModelRow({ tabId, compact }: { tabId?: string; compact?: boolean }) {
  const meta = useChatStore(state => tabId ? state.chatTabs[tabId]?.metadata : undefined)
  // The program the chat runs now (the chat box's), before the one it was saved with.
  const provider = meta?.agentProfileEngine || meta?.agentProfileChatProvider
  const manifest = useLLMStore(state => state.providerManifest.find(item => item.id === provider))
  const program = provider ? (manifest ? codingProviderName(manifest) : provider) : ''
  const label = meta?.agentProfileChatModelLabel || program
  if (!label) return null
  return <div className={compact ? "mb-2 flex text-xs" : "flex justify-between gap-3 border-t border-border py-1.5"}>{!compact && <span>Model</span>}
    <span className="flex min-w-0 items-center gap-1.5 text-muted-foreground"><CodingCliIcon provider={provider} label={program} /><span className="truncate" title={label}>{label}</span></span></div>
}

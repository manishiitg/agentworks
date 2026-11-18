import { HeaderAgentSharing } from '../../products/work/ShareCrew'
import { OpenInEditorAction } from '../workspace/OpenInEditor'
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { ArrowDown, ArrowUp, Eye, EyeOff, GitFork, Loader2, Pencil, Trash2 } from 'lucide-react'
import { create } from 'zustand'
import { useChatStore } from '../../stores/useChatStore'
import { AGENT_KEY, AgentPlaceMark } from './AgentIcon'
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
  editorWorkspace?: string
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
  setPinned?: (pinned: boolean) => void
  /** Removes it for good (its chat is deleted). */
  remove?: () => void
}
/** pinned: shown in the list (and on the tab bar); the rest sit under "Hidden agents". */
export interface CrewAgentRow { inTeam?:boolean; tabId: string; name: string; main: boolean; /** Where it runs, as the server lists it: 'server' shows a cloud in the left menu. */ place?: 'mac' | 'server'; pinned?: boolean; /** Whose agent it is when it is someone else's (a Team or co-owner Crew), by account and with the server's display name; absent for your own. */ ownerId?: string; owner?: string; /** Its Cmd/Ctrl+number, in the left menu's order. */ shortcut?: number; asking?: boolean; info?: AgentCardInfo; activity?: 'running' | 'waiting' | null; actions?: CrewAgentActions }
/** An agent the server lists for the Crew that has no open tab here; open() opens it again and switches to it. */
export interface CrewClosedAgent { inTeam?:boolean; key: string; name: string; examples?: string[]; purpose?: string; pinned?: boolean; open: () => Promise<unknown> }
export const useCrewAgentsStore = create<{ projectId: string | null; name: string; agents: CrewAgentRow[]; closed: CrewClosedAgent[]; canAdd: boolean; set: (projectId: string, name: string, agents: CrewAgentRow[], canAdd: boolean, closed?: CrewClosedAgent[]) => void }>(set => ({
  projectId: null,
  name: '',
  agents: [],
  closed: [],
  canAdd: false,
  set: (projectId, name, agents, canAdd, closed = []) => set({ projectId, name, agents, canAdd, closed }),
}))

// The online copy answers teammates while this Mac sleeps; always on for shared agents (owner, 16 Nov).
function AgentOnlineCopyStatus({ setup }: { setup?: TeamTabSetup }) {
 const copy = setup?.online_copy
 if (!copy) return null
 return <div className="flex justify-between gap-3 border-t border-border py-2"><span className="text-muted-foreground">When Mac sleeps</span><span>{copy.state === 'ready' ? `Online copy answers${copy.synced_at ? ` · synced ${Math.max(0, Math.floor((Date.now() - Date.parse(copy.synced_at)) / 60000))}m ago` : ''}` : 'Online copy preparing'}</span></div>
}

let agentHoverCooldownUntil = 0

export function AgentHoverCard({ info, children, side = 'below', clickToOpen = false, actions, pinned, shortcut, onRename }: { info?: AgentCardInfo; children: ReactNode; side?: 'below' | 'right'; clickToOpen?: boolean; actions?: CrewAgentActions; pinned?: boolean; shortcut?: number; onRename?: () => void }) {
  const [automationsOpen, setAutomationsOpen] = useState(false)
  const anchor = useRef<HTMLSpanElement>(null)
  const box = useRef<HTMLDivElement>(null)
  const [at, setAt] = useState<{ top: number; left: number } | null>(null)
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
    const close = (event: MouseEvent | TouchEvent) => { const target = event.target as Node; if (!box.current?.contains(target) && !anchor.current?.contains(target) && !backdrop.current?.contains(target)) dismiss() }
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') dismiss() }
    document.addEventListener('mousedown', close)
    document.addEventListener('touchstart', close)
    document.addEventListener('keydown', escape)
    return () => { document.removeEventListener('mousedown', close); document.removeEventListener('touchstart', close); document.removeEventListener('keydown', escape) }
  }, [at, dismiss])
  useEffect(() => () => window.clearTimeout(timer.current), [])
  if (!info) return <>{children}</>
  const open = () => {
    const rect = anchor.current?.getBoundingClientRect()
    if (!rect) return
    // The collapsed rail opens the card beside it; everywhere else below the name.
    if (side === 'right') setAt({ top: Math.max(8, Math.min(rect.top - 8, window.innerHeight - 240)), left: rect.right + 8 })
    else setAt({ top: rect.bottom + 8, left: Math.max(8, Math.min(rect.left - 8, window.innerWidth - 448)) })
  }
  const later = (ms: number, action: () => void) => { window.clearTimeout(timer.current); timer.current = window.setTimeout(action, ms) }
  const finish = (action: () => void) => { dismiss(); action() }
  return <>
    <span ref={anchor} className={side === 'right' ? 'block' : 'min-w-0'}
      onPointerEnter={event => { if (event.pointerType === 'mouse' && Date.now() >= agentHoverCooldownUntil) later(1000, () => { if (Date.now() >= agentHoverCooldownUntil) open() }) }} onPointerLeave={event => { if (event.pointerType === 'mouse') { window.clearTimeout(timer.current); if (at) later(200, dismiss) } }}
      onTouchStart={() => { pressed.current = false; later(500, () => { pressed.current = true; open() }) }} onTouchEnd={() => { if (!at) window.clearTimeout(timer.current) }}
      onTouchMove={() => { if (!at) window.clearTimeout(timer.current) }}
      /* The click that ends a long press only opened the card; it does not also open the agent. */
      onClickCapture={event => { if (pressed.current) { pressed.current = false; event.preventDefault(); event.stopPropagation() } else if (!clickToOpen) dismiss() }}
      onClick={clickToOpen ? () => { window.clearTimeout(timer.current); if (at) dismiss(); else open() } : undefined}>
      {children}
    </span>
    {at && createPortal(<>{sheet && <div ref={backdrop} aria-hidden="true" className="fixed inset-0 z-[100] bg-black/40" onClick={event => { event.stopPropagation(); dismiss() }} />}
    <div ref={box} role="dialog" aria-label={info.name} style={sheet ? undefined : at} onClick={event => event.stopPropagation()}
      onPointerEnter={() => window.clearTimeout(timer.current)} onPointerLeave={event => { if (!clickToOpen && event.pointerType === 'mouse') later(200, dismiss) }}
      className={sheet
        ? 'fixed inset-x-0 bottom-0 z-[100] max-h-[80dvh] overflow-y-auto overscroll-contain rounded-t-2xl border-t border-border bg-popover p-4 pb-[calc(1rem+env(safe-area-inset-bottom))] text-sm shadow-lg'
        : 'fixed z-[100] max-h-[80vh] w-[min(28rem,calc(100vw-16px))] overflow-y-auto rounded-2xl border border-border bg-popover p-3 text-xs shadow-lg'}>
      <p className="flex items-center gap-2 text-sm font-semibold"><span className="min-w-0 flex-1 truncate">{info.name}</span>{shortcut && <kbd className="rounded border border-border px-1 font-sans text-[10px] font-normal text-muted-foreground">{AGENT_KEY}{shortcut}</kbd>}</p>
      <div className="mb-2 mt-1 flex min-w-0 items-center gap-2 text-muted-foreground"><p className="min-w-0 flex-1 truncate" title={info.purpose}>{info.purpose || 'Add a purpose'}</p>{info.onSetPurpose && <button type="button" onClick={() => finish(info.onSetPurpose!)} className="shrink-0 text-primary hover:underline">{info.purpose ? 'Edit' : 'Set up'}</button>}</div>
      {info.instructions && <div className="flex justify-between gap-3 border-t border-border py-1.5"><span>Instructions</span><span className="flex min-w-0 items-center gap-2 text-muted-foreground"><span className="truncate">{info.instructions.set === undefined ? 'Its own rules' : info.instructions.set ? 'Has its own' : 'None yet'}</span><button type="button" onClick={() => finish(info.instructions!.onEdit)} className="shrink-0 text-primary hover:underline">{info.instructions.set === false ? 'Set up' : 'Edit'}</button></span></div>}
      <AgentModelRow tabId={info.tabId} />
      {!!info.examples?.length && <p className="mb-2 text-muted-foreground">Ask me to: {info.examples.join(' · ')}</p>}
      {info.sharing && <HeaderAgentSharing {...info.sharing} />}
      {info.owner ? <div className="flex justify-between gap-3 border-t border-border py-1.5"><span>Whose</span><span className="min-w-0 truncate text-muted-foreground">{info.owner}'s · shared with the team</span></div>
        : !info.sharing && <div className="flex justify-between gap-3 border-t border-border py-1.5"><span>Shared with</span><span className="min-w-0 truncate text-muted-foreground">{info.sharedWith || 'Only you'}</span></div>}
      {!info.sharing && info.sharedWith && <div className="flex justify-between gap-3 border-t border-border py-1.5"><span>For them it</span><span className="text-muted-foreground">{info.canChange ? 'can make changes' : 'answers only'}</span></div>}
      {info.lastAsked && <div className="flex justify-between gap-3 border-t border-border py-1.5"><span>Last asked</span>
        <button type="button" onClick={() => finish(info.lastAsked!.open)} className="min-w-0 truncate text-primary hover:underline">{info.lastAsked.who} · {info.lastAsked.when}</button></div>}
      {info.setup?.agentbridge_line && <div className="flex justify-between gap-3 border-t border-border py-1.5"><span>AgentBridge</span><span className="text-emerald-500">{info.setup.agentbridge_line}</span></div>}
      {!info.owner && <AgentOnlineCopyStatus setup={info.setup} />}
      {(info.setup || info.onAutomations) && <div className="space-y-1 border-t border-border py-2"><div className="flex items-center gap-2"><span>Automations</span><span className="min-w-0 flex-1 truncate text-muted-foreground">{info.setup?.automations.length ?? 0}{info.setup?.next_automation ? ` · next: ${info.setup.next_automation}` : ''}</span><button type="button" aria-expanded={automationsOpen} onClick={() => setAutomationsOpen(!automationsOpen)} className="shrink-0 text-primary hover:underline">{automationsOpen ? 'Close' : 'Open ›'}</button></div>
        {automationsOpen && <ul className="space-y-1 py-1 text-muted-foreground">{info.setup?.automations.map((item, index) => <li key={index}>{item.name} · {item.kind} · {item.line}</li>)}{!info.setup?.automations.length && <li>No automations yet.</li>}</ul>}
        {info.onAddAutomation && <button type="button" className="text-primary hover:underline" onClick={() => finish(info.onAddAutomation!)}>✦ Ask this agent to add an automation</button>}</div>}
      {actions?.forkTo && <div className="border-t border-border py-2">
        <button type="button" onClick={() => finish(actions.forkTo!)} className="flex w-full items-center gap-2 rounded-md py-1 text-left hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><GitFork aria-hidden className="h-3.5 w-3.5" />Send a copy to a teammate…</button>
        <p className="mt-1 text-[11px] text-muted-foreground">They accept their own copy. Your original stays here.</p>
      </div>}
      <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 max-md:[&>button]:min-h-10">
        {onRename && <button type="button" onClick={() => finish(onRename)} className="inline-flex items-center gap-1 text-primary hover:underline"><Pencil aria-hidden="true" className="h-3 w-3" />Rename</button>}
        {!info.sharing && info.onSharing && <button type="button" onClick={() => finish(info.onSharing!)} className="text-primary hover:underline">{info.sharedWith ? 'Sharing…' : 'Share…'}</button>}
      </div>
      {info.onRemove && <div className="mt-2 border-t border-border pt-1.5">
        <button type="button" onClick={() => confirmRemoveAgent(info.name, () => finish(info.onRemove!))} className="text-muted-foreground hover:text-destructive hover:underline max-md:min-h-10">Remove agent…</button>
      </div>}
    </div></>, document.body)}
  </>
}

/** The top line of a Crew's chat (owner, 4 Nov): "<Crew name> › <agent name> ⓘ". ⓘ opens the agent's card
 * (purpose, sharing, ✎). Purpose text is not shown inline. */
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
      : <AgentHoverCard info={info || undefined} clickToOpen shortcut={row?.shortcut} actions={row?.actions} pinned={row?.pinned !== false}>
        <button type="button" aria-label={`About ${name}`} className="block min-w-0 max-w-48 truncate rounded-sm font-semibold focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">{name}</button>
      </AgentHoverCard>}
    </>}
  </div>
}

/** One explicit popup, shared by the agent card and its actions menu. */
function confirmRemoveAgent(_name: string, remove: () => void) {
  // The shared close flow opens the server-owned dependency preview.
  remove()
}

/** One agent's actions, the same as the tab strip's (owner, 4 Nov): rename, move up or down, hide from or show in the
 * list (owner, 4 Nov: Close = Hide). Used by the left menu's "…" and the agent card in the collapsed rail. */
export function AgentActionsMenu({ name, actions, pinned, onDone }: { name: string; actions: CrewAgentActions; pinned?: boolean; onDone: () => void }) {
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
    {actions.forkTo && <button type="button" role="menuitem" className={item} onClick={() => { actions.forkTo!(); onDone() }}><GitFork aria-hidden="true" className="h-3.5 w-3.5" />Send a copy to a teammate…</button>}
    {actions.fork && <button type="button" role="menuitem" disabled={forking} aria-busy={forking} className={`${item} disabled:cursor-wait disabled:opacity-70`} onClick={async () => {
      if (forking) return
      setForking(true)
      try { await actions.fork!() } finally { setForking(false); onDone() }
    }}>{forking ? <Loader2 aria-hidden="true" className="h-3.5 w-3.5 animate-spin" /> : <GitFork aria-hidden="true" className="h-3.5 w-3.5" />}{forking ? 'Forking chat…' : 'Fork chat'}</button>}
    {actions.editorWorkspace && <OpenInEditorAction workspacePath={actions.editorWorkspace} menu />}
    {actions.terminal && <button type="button" role="menuitem" className={item} onClick={() => { void actions.terminal!(); onDone() }}>Continue in my terminal</button>}
    {forking && <p role="status" className="px-2 py-1 text-[11px] text-muted-foreground">Copying conversation history. Your new chat will open when ready.</p>}
    {actions.rename && <button type="button" role="menuitem" className={item} onClick={() => setDraft(actions.renameDraft || name)}><Pencil aria-hidden="true" className="h-3.5 w-3.5" />Rename</button>}
    {actions.moveUp && <button type="button" role="menuitem" className={item} onClick={() => run(actions.moveUp!)}><ArrowUp aria-hidden="true" className="h-3.5 w-3.5" />Move up</button>}
    {actions.moveDown && <button type="button" role="menuitem" className={item} onClick={() => run(actions.moveDown!)}><ArrowDown aria-hidden="true" className="h-3.5 w-3.5" />Move down</button>}
    {actions.setPinned && (pinned
      ? <button type="button" role="menuitem" className={item} onClick={() => run(() => actions.setPinned!(false))}><EyeOff aria-hidden="true" className="h-3.5 w-3.5" />Hide from list</button>
      : <button type="button" role="menuitem" className={item} onClick={() => run(() => actions.setPinned!(true))}><Eye aria-hidden="true" className="h-3.5 w-3.5" />Show in list</button>)}
    {actions.remove && <button type="button" role="menuitem" className={`${item} border-t border-border text-destructive`} onClick={() => confirmRemoveAgent(name, () => run(actions.remove!))}><Trash2 aria-hidden="true" className="h-3.5 w-3.5" />Remove agent…</button>}
  </div>
}

/** Which program and model the agent answers with (owner, 18 Nov: the left menu no longer shows the program logo on
 * hover). The same server label the composer shows; nothing when the tab has not loaded it. */
function AgentModelRow({ tabId }: { tabId?: string }) {
  const meta = useChatStore(state => tabId ? state.chatTabs[tabId]?.metadata : undefined)
  // The program the chat runs now (the chat box's), before the one it was saved with.
  const provider = meta?.agentProfileEngine || meta?.agentProfileChatProvider
  const manifest = useLLMStore(state => state.providerManifest.find(item => item.id === provider))
  const program = provider ? (manifest ? codingProviderName(manifest) : provider) : ''
  const label = meta?.agentProfileChatModelLabel || program
  if (!label) return null
  return <div className="flex justify-between gap-3 border-t border-border py-1.5"><span>Model</span>
    <span className="flex min-w-0 items-center gap-1.5 text-muted-foreground"><CodingCliIcon provider={provider} label={program} /><span className="truncate" title={label}>{label}</span></span></div>
}

import { HeaderAgentSharing } from '../../products/work/ShareCrew'
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { ArrowDown, ArrowUp, ChevronDown, Clock, Cloud, Eye, EyeOff, GitFork, Loader2, MailOpen, Pencil, SquareTerminal, Trash2, Webhook, Workflow, X } from 'lucide-react'
import { create } from 'zustand'
import { useChatStore } from '../../stores/useChatStore'
import { AGENT_KEY, AgentPlaceMark, enabledAgentAutomations } from './AgentIcon'
import { AgentShareIcon, describeAgentShare, type AgentTeamShare } from './AgentInTeamMark'
import { AgentDetailSection } from './AgentDetailSection'
import { AgentMessageFrom, CodingAppIcons } from './AgentMessageFrom'
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
  /** Shared with chosen people only (the server row's audience). */
  sharedChosen?: boolean
  /** Structured Team sharing state for the header pill (only present when shared with the Team). */
  teamShare?: AgentTeamShare
  lastAsked?: { who: string; when: string; active: boolean; open: () => void }
  /** Only on your own agents; a teammate's agent is theirs to change. */
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
  /** What to say in Claude Code, Codex or another agent to message this one; only on your own agents. */
  messageLine?: string
  messageCopy?: string
  messageOnline?: boolean
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
  /** Continue online (#4520): move this Mac chat to an agent in the Team's online project. */
  continueOnline?: () => void
  fork?: () => unknown
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
export interface CrewAgentRow { /** Its conversation key, for groups (#3288). */ key?: string; /** A teammate's agent shared as Agent only: it can be asked, not opened or added to a group. */ messageOnly?: boolean; /** When its chat last had a message (saved history), for "2m" before any session is live (#3406). */ lastMessageAt?: string; /** The conversation key of the agent it was forked from. */ forkedFrom?: string; sidebarHidden?: boolean; sidebarOrder?: number; inTeam?:boolean; teamShare?: AgentTeamShare; tabId: string; name: string; main: boolean; /** Where it runs, as the server lists it: 'server' shows a cloud in the left menu. */ place?: 'mac' | 'server'; /** Whose agent it is when it is someone else's (a Team or co-owner Crew), by account and with the server's display name; absent for your own. */ ownerId?: string; owner?: string; /** Its Cmd/Ctrl+number, in the left menu's order. */ shortcut?: number; asking?: boolean; info?: AgentCardInfo; activity?: 'running' | 'waiting' | null; actions?: CrewAgentActions }
/** An agent the server lists for the Crew that has no open tab here; open() opens it again and switches to it. */
export interface CrewClosedAgent { /** A teammate's agent: their name. */ owner?: string; forkedFrom?: string; sidebarHidden?: boolean; sidebarOrder?: number; inTeam?:boolean; teamShare?: AgentTeamShare; key: string; name: string; examples?: string[]; purpose?: string; open: () => Promise<unknown> }
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

// What teammates get while this agent is offline (#3194): the server says it; their agents read its chat any time, or wait for it.
function AgentOnlineCopyStatus({ setup }: { setup?: TeamTabSetup }) {
 const line = setup?.when_offline?.line
 if (!line) return null
 return <p className="border-t border-border py-2 text-muted-foreground">{line}</p>
}

let agentHoverCooldownUntil = 0

export function AgentHoverCard({ info, children, side = 'below', clickToOpen = false, actions, sidebarHidden, shortcut, onRename, automationShortcut = false, shareShortcut = false }: { info?: AgentCardInfo; children: ReactNode | ((detailsOpen: boolean) => ReactNode); side?: 'below' | 'right'; clickToOpen?: boolean; actions?: CrewAgentActions; sidebarHidden?: boolean; shortcut?: number; onRename?: () => void; automationShortcut?: boolean; shareShortcut?: boolean }) {
  const [automationsOpen, setAutomationsOpen] = useState(false)
  const [renameDraft, setRenameDraft] = useState<string | null>(null)
  const [forking, setForking] = useState(false)
  const anchor = useRef<HTMLSpanElement>(null)
  const box = useRef<HTMLDivElement>(null)
  const [at, setAt] = useState<{ top: number; left: number; view: 'details' | 'automations' | 'sharing' } | null>(null)
  const trigger = useRef<HTMLButtonElement | null>(null)
  const timer = useRef<number | undefined>(undefined)
  const activeTabId = useChatStore(state => state.activeTabId)
  const dismiss = useCallback(() => {
    window.clearTimeout(timer.current)
    agentHoverCooldownUntil = Date.now() + 3000
    setRenameDraft(null)
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
  const shareInfo = shareShortcut && !info.owner ? describeAgentShare(info.teamShare) : null
  const open = (view: 'details' | 'automations' | 'sharing' = 'details') => {
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
  // Setup is the agent's own AGENTS.md. Its purpose is for the Team, so it is set in the Team sharing menu (owner,
  // 28 Nov); a teammate's agent still shows its purpose here, read-only, to say what to ask it.
  const shared = Boolean(info.owner)
  const instructionsState = info.instructions?.set === undefined ? '' : info.instructions.set ? 'has its own' : 'none yet'
  // The Setup row leads with the agent's own AGENTS.md, shared or not (owner, 28 Nov); its purpose is inside.
  const setupValue = instructionsState ? `AGENTS.md · ${instructionsState}` : 'AGENTS.md'
  const setupDetails = <>
      {info.instructions && <div className="flex items-start justify-between gap-3 py-1.5"><span className="min-w-0"><span className="block">AGENTS.md</span><span className="block text-[11px] text-muted-foreground">This agent's own instructions: standing rules it follows in every chat.</span></span><button type="button" onClick={() => finish(info.instructions!.onEdit)} className="shrink-0 text-primary hover:underline">{info.instructions.set === false ? 'Set up' : 'Edit'}</button></div>}
      {shared && <div className={`flex min-w-0 items-start gap-2 py-1.5 ${info.instructions ? 'border-t border-border' : ''}`}><span className="min-w-0 flex-1"><span className="block">Purpose <span className="text-[11px] text-muted-foreground">for your Team</span></span><span className="block truncate text-muted-foreground" title={info.purpose}>{info.purpose || 'Add a purpose'}</span></span></div>}
      {shared && !!info.examples?.length && <p className="mb-2 text-muted-foreground">Ask me to: {info.examples.join(' · ')}</p>}
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
      {shareInfo && <button type="button" aria-label={shareInfo.tooltip} aria-expanded={at?.view === 'sharing'} aria-haspopup="dialog"
        title={`${shareInfo.tooltip} — click to edit`}
        className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring max-md:h-10 max-md:w-10"
        onClick={event => {
          event.stopPropagation()
          trigger.current = event.currentTarget
          if (at?.view === 'sharing') dismiss()
          else if (info.sharing) open('sharing')
          else info.onSharing?.()
        }}>
        <AgentShareIcon info={shareInfo} className="h-3.5 w-3.5 shrink-0" />
      </button>}
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
    <div ref={box} tabIndex={clickToOpen ? -1 : undefined} role="dialog" aria-label={at.view === 'automations' ? `Automations for ${info.name}` : at.view === 'sharing' ? `Sharing for ${info.name}` : info.name} style={sheet ? undefined : { top: at.top, left: at.left }} onClick={event => event.stopPropagation()}
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
      </> : at.view === 'sharing' && info.sharing ? <>
        <div className="mb-1 flex items-center justify-between gap-2">
          <p className="min-w-0 flex-1 truncate text-sm font-semibold">Sharing</p>
          {close('Close sharing')}
        </div>
        <HeaderAgentSharing {...info.sharing} defaultOpen bare status={!info.owner ? <AgentOnlineCopyStatus setup={info.setup} /> : undefined} />
        {actions?.forkTo && <div className="mt-2 flex flex-wrap items-center border-t border-border pt-2 max-md:[&>button]:min-h-10">
          <button type="button" onClick={() => finish(actions.forkTo!)} className="inline-flex items-center gap-1.5 rounded-md py-1 text-left hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><GitFork aria-hidden className="h-3.5 w-3.5" />Send a copy to a teammate…</button>
        </div>}
      </> : <>
      {(!clickToOpen || sheet) && <div className="flex items-center gap-2 text-sm font-semibold"><p className="min-w-0 flex-1 truncate">{info.name}</p>{shortcut && <kbd className="rounded border border-border px-1 font-sans text-[10px] font-normal text-muted-foreground">{AGENT_KEY}{shortcut}</kbd>}{clickToOpen && close('Close agent details')}</div>}
      {!clickToOpen && <AgentModelRow tabId={info.tabId} />}
      {clickToOpen ? <AgentDetailSection title="Setup" value={setupValue}>{setupDetails}</AgentDetailSection> : setupDetails}
      {clickToOpen && info.messageLine && <AgentDetailSection title="Message it from another agent" aside={info.messageOnline ? undefined : <CodingAppIcons />}><AgentMessageFrom line={info.messageLine} copyText={info.messageCopy} online={info.messageOnline} /></AgentDetailSection>}
      {!shareShortcut && info.sharing && <HeaderAgentSharing {...info.sharing} status={!info.owner ? <AgentOnlineCopyStatus setup={info.setup} /> : undefined} />}
      {info.owner ? <div className="flex justify-between gap-3 border-t border-border py-1.5"><span>Whose</span><span className="min-w-0 truncate text-muted-foreground">{info.owner}'s · shared with the team</span></div>
        : !info.sharing && <div className="flex justify-between gap-3 border-t border-border py-1.5"><span>Shared with</span><span className="min-w-0 truncate text-muted-foreground">{info.sharedWith || 'Only you'}</span></div>}
      {info.lastAsked && <div className="flex justify-between gap-3 border-t border-border py-1.5"><span>Last asked</span>
        <button type="button" onClick={() => finish(info.lastAsked!.open)} className="min-w-0 truncate text-primary hover:underline">{info.lastAsked.who} · {info.lastAsked.when}</button></div>}
      {info.setup?.agentbridge_line && <div className="flex justify-between gap-3 border-t border-border py-1.5"><span>AgentBridge</span><span className="text-emerald-500">{info.setup.agentbridge_line}</span></div>}
      {!info.owner && !info.sharing && <AgentOnlineCopyStatus setup={info.setup} />}
      {(info.setup || info.onAutomations || info.onAddAutomation) && (clickToOpen
        ? <AgentDetailSection title="Automations" value={automationsSummary(info.setup?.automations ?? [])}>
          {info.setup?.next_automation && <p className="text-muted-foreground">Next: {info.setup.next_automation}</p>}
          <AgentAutomationList items={info.setup?.automations ?? []} empty="Nothing starts this agent on its own yet." explain />
          {addAutomation}
        </AgentDetailSection>
        : <div className="space-y-1 border-t border-border py-2">
          <div className="flex items-center justify-between gap-2"><span>Automations</span><button type="button" aria-expanded={automationsOpen} onClick={() => setAutomationsOpen(!automationsOpen)} className="text-primary hover:underline">{automationsOpen ? 'Close' : 'Open ›'}</button></div>
          {automationsOpen && <AgentAutomationList items={info.setup?.automations ?? []} empty="Nothing starts this agent on its own yet." explain />}
          {addAutomation}
        </div>)}
      {((!shareShortcut && actions?.forkTo) || actions?.continueOnline || actions?.terminal || onRename || (clickToOpen && (actions?.fork || actions?.rename || actions?.setSidebarHidden || info.onRemove)) || (shareShortcut && !shareInfo && info.sharing) || (!info.sharing && info.onSharing)) && (() => {
        const menuRow = 'flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs text-foreground/90 hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-wait disabled:opacity-70 max-md:min-h-10 max-md:text-sm'
        const hasUpperAction = Boolean((!shareShortcut && actions?.forkTo) || actions?.continueOnline || (clickToOpen && actions?.fork) || actions?.terminal || onRename || (clickToOpen && actions?.rename) || (clickToOpen && actions?.setSidebarHidden) || (shareShortcut && !shareInfo && info.sharing) || (!info.sharing && info.onSharing))
        return <div className={clickToOpen ? 'mt-1 flex flex-col gap-0.5 border-t border-border pt-1.5' : 'flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-border pt-2 max-md:[&>button]:min-h-10'}>
        {renameDraft !== null ? <input autoFocus aria-label={`New name for ${info.name}`} value={renameDraft} onChange={event => setRenameDraft(event.target.value)}
          onKeyDown={event => {
            if (event.key === 'Escape') { event.stopPropagation(); setRenameDraft(null) }
            if (event.key === 'Enter' && renameDraft.trim()) { void actions?.rename?.(renameDraft.trim()); dismiss() }
          }}
          onBlur={() => setRenameDraft(null)} className="w-full rounded-md border border-border bg-background px-2 py-1 text-xs max-md:text-base" /> : <>
          {!shareShortcut && actions?.forkTo && <button type="button" onClick={() => finish(actions.forkTo!)} className={clickToOpen ? menuRow : 'inline-flex items-center gap-1.5 rounded-md py-1 text-left hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'}><GitFork aria-hidden className={`h-3.5 w-3.5 shrink-0 ${clickToOpen ? 'text-muted-foreground' : ''}`} />Send a copy to a teammate…</button>}
          {clickToOpen && actions?.fork && <button type="button" disabled={forking} aria-busy={forking} onClick={async () => {
            if (forking) return
            setForking(true)
            try { await actions.fork!() } finally { setForking(false); dismiss() }
          }} className={menuRow}>
            {forking ? <Loader2 aria-hidden="true" className="h-3.5 w-3.5 shrink-0 animate-spin text-muted-foreground" /> : <GitFork aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />}{forking ? 'Forking chat…' : 'Fork chat'}
          </button>}
          {actions?.continueOnline && <button type="button" onClick={() => finish(actions.continueOnline!)} className={clickToOpen ? menuRow : 'inline-flex items-center gap-1.5 rounded-md py-1 text-left hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'}><Cloud aria-hidden className={`h-3.5 w-3.5 shrink-0 ${clickToOpen ? 'text-muted-foreground' : ''}`} />Continue online…</button>}
          {/* Resume this agent's chat in your own Claude Code: in the agent's top menu (owner, 20 Nov), not the left menu. */}
          {actions?.terminal && <button type="button" onClick={() => finish(actions.terminal!)} className={clickToOpen ? menuRow : 'inline-flex items-center gap-1.5 rounded-md py-1 text-left hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'}><SquareTerminal aria-hidden className={`h-3.5 w-3.5 shrink-0 ${clickToOpen ? 'text-muted-foreground' : ''}`} />Continue in my terminal</button>}
          {(onRename || (clickToOpen && actions?.rename)) && <button type="button" onClick={() => onRename ? finish(onRename) : setRenameDraft(actions?.renameDraft || info.name)} className={clickToOpen ? menuRow : 'inline-flex items-center gap-1 rounded-md py-1 text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'}><Pencil aria-hidden="true" className={`h-3.5 w-3.5 shrink-0 ${clickToOpen ? 'text-muted-foreground' : ''}`} />Rename</button>}
          {clickToOpen && actions?.setSidebarHidden && <button type="button" onClick={() => finish(() => actions.setSidebarHidden!(!sidebarHidden))} className={menuRow}>
            {sidebarHidden ? <><Eye aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />Show in sidebar</> : <><EyeOff aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />Hide from sidebar</>}
          </button>}
          {shareShortcut && !shareInfo && info.sharing && <button type="button" onClick={() => open('sharing')} className={clickToOpen ? menuRow : 'text-primary hover:underline'}>Share…</button>}
          {!info.sharing && info.onSharing && <button type="button" onClick={() => finish(info.onSharing!)} className={clickToOpen ? menuRow : 'text-primary hover:underline'}>{info.sharedWith ? 'Sharing…' : 'Share…'}</button>}
          {clickToOpen && info.onRemove && <>
            {hasUpperAction && <div className="my-1 border-t border-border" />}
            <button type="button" onClick={() => finish(info.onRemove!)} className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs text-destructive hover:bg-destructive/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring max-md:min-h-10 max-md:text-sm"><Trash2 aria-hidden="true" className="h-3.5 w-3.5 shrink-0" />Remove agent…</button>
          </>}
        </>}
      </div>})()}
      </>}
    </div></>, document.body)}
  </>
}

// What starts an agent on its own, by kind (owner, 28 Nov). The server lists them; each agent shows only the kinds it has.
const AUTOMATION_KINDS = [
  { kind: 'Schedule', plural: 'Schedules', Icon: Clock, tone: 'bg-blue-500/10 text-blue-600 dark:text-blue-400', can: <>run on a <b>schedule</b> (every morning, every hour)</> },
  { kind: 'Watcher', plural: 'Watchers', Icon: Eye, tone: 'bg-amber-500/10 text-amber-600 dark:text-amber-400', can: <><b>watch</b> for something (a new email, a new PR)</> },
  { kind: 'Webhook', plural: 'Webhooks', Icon: Webhook, tone: 'bg-purple-500/10 text-purple-600 dark:text-purple-400', can: <>start when a <b>webhook</b> is called</> },
  { kind: 'Workflow', plural: 'Workflows', Icon: Workflow, tone: 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400', can: <>run a <b>workflow</b> built for this project</> },
] as const

/** "1 schedule · 2 workflows", or None. */
export function automationsSummary(items: TeamTabSetup['automations']) {
  const parts = AUTOMATION_KINDS.map(({ kind }) => [kind, items.filter(item => item.kind === kind).length] as const).filter(([, count]) => count)
  return parts.length ? parts.map(([kind, count]) => `${count} ${kind.toLowerCase()}${count === 1 ? '' : 's'}`).join(' · ') : 'None'
}

function AgentAutomationList({ items, empty, explain = false }: { items: TeamTabSetup['automations']; empty: string; explain?: boolean }) {
  if (!items.length) return <div className="py-1 text-muted-foreground"><p>{empty}</p>
    {explain && <><p className="mt-1">It can:</p><ul className="mt-1 space-y-1">{AUTOMATION_KINDS.map(({ kind, Icon, tone, can }) => <li key={kind} className="flex items-center gap-2"><span className={`grid h-5 w-5 shrink-0 place-items-center rounded-md ${tone}`}><Icon aria-hidden className="h-3 w-3" /></span><span>{can}</span></li>)}</ul></>}
  </div>
  return <div className="py-1">{AUTOMATION_KINDS.map(({ kind, plural, Icon, tone }) => {
    const list = items.filter(item => item.kind === kind)
    if (!list.length) return null
    return <section key={kind} aria-label={plural} className="mt-1.5 first:mt-0">
      <p className="flex items-center gap-2 font-medium"><span className={`grid h-5 w-5 shrink-0 place-items-center rounded-md ${tone}`}><Icon aria-hidden className="h-3 w-3" /></span>{plural}<span className="font-normal text-muted-foreground">{list.length}</span></p>
      <ul className="mt-0.5 space-y-0.5">{list.map((item, index) => <li key={index} className="flex min-w-0 items-center gap-2 pl-7">
        <span aria-label={item.on ? 'on' : 'not running on its own'} className={`h-1.5 w-1.5 shrink-0 rounded-full ${item.on ? 'bg-emerald-500' : 'bg-muted-foreground/40'}`} />
        <span className="shrink-0 font-medium">{item.name}</span><span className="min-w-0 truncate text-muted-foreground" title={item.line}>{item.line}</span>
      </li>)}</ul>
    </section>
  })}</div>
}

/** The Crew header shows only the current agent; switching and ordering stay in the sidebar. */
export function AgentHeaderLine({ agent: row, crewName, onOpenIdentity, onReturn }: { agent?: CrewAgentRow; crewName: string; onOpenIdentity?: () => void; onReturn?: () => void }) {
  // Other mounted Crews also project the sidebar/open-agent stores. The header
  // must use this pane's own row and actions, not whichever projection ran last.
  const info = row?.info
  const name = row?.name
  const canRename = Boolean(row?.actions?.rename && name)
  const [headerRename, setHeaderRename] = useState<string | null>(null)
  const skipRenameBlur = useRef(false)
  useEffect(() => { setHeaderRename(null) }, [row?.tabId])
  const commitHeaderRename = (draft: string) => {
    const next = draft.trim()
    const current = (row?.actions?.renameDraft || name || '').trim()
    setHeaderRename(null)
    if (next && next !== current) void row?.actions?.rename?.(next)
  }
  return <div className="mr-0.5 flex h-10 min-w-0 shrink items-center gap-1.5 px-1 text-sm md:h-8" data-agent-header>
    {onOpenIdentity ? <button type="button" data-project-title onClick={onOpenIdentity} title="Identity & instructions"
      aria-label={`Identity & instructions for ${crewName}`} className="min-w-0 max-w-28 truncate rounded-sm px-1 font-semibold text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring md:max-w-40">{crewName}</button>
      : <span data-project-title title={crewName} className="min-w-0 max-w-28 truncate px-1 md:max-w-40 font-semibold text-muted-foreground">{crewName}</span>}
    {name && <>
      <span aria-hidden="true" className="shrink-0 text-muted-foreground/60">›</span>
      <AgentPlaceMark place={row?.place} />
      {/* Watching a teammate's agent: your own agent's name takes you back to it. */}
      {onReturn ? <button type="button" onClick={onReturn} title={`Back to your ${name}`} aria-label={`Back to your ${name}`}
        className="block min-w-0 max-w-48 sm:max-w-xs md:max-w-[28rem] lg:max-w-[36rem] truncate rounded-sm font-semibold text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">{name}</button>
      : info ? <AgentHoverCard info={info} clickToOpen automationShortcut shareShortcut shortcut={row?.shortcut} actions={row?.actions} sidebarHidden={row?.sidebarHidden}>
        {open => canRename ? <span className="inline-flex min-w-0 items-center gap-0.5">
          {headerRename !== null ? <input autoFocus aria-label={`Rename ${name}`} value={headerRename} onClick={event => event.stopPropagation()}
            onChange={event => setHeaderRename(event.target.value)}
            onKeyDown={event => {
              if (event.key === 'Escape') { event.stopPropagation(); skipRenameBlur.current = true; setHeaderRename(null) }
              if (event.key === 'Enter') {
                event.preventDefault(); event.stopPropagation(); skipRenameBlur.current = true
                const next = headerRename.trim()
                setHeaderRename(null)
                if (next) void row?.actions?.rename?.(next)
              }
            }}
            onBlur={() => { if (skipRenameBlur.current) { skipRenameBlur.current = false; return } commitHeaderRename(headerRename) }}
            className="w-40 min-w-0 max-w-[20rem] rounded-md border border-border bg-background px-1.5 py-0.5 text-sm font-semibold text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring max-md:text-base" />
          : <button type="button" aria-label={`Rename ${name}`} title="Click to rename"
              onClick={event => { event.stopPropagation(); skipRenameBlur.current = false; setHeaderRename(row?.actions?.renameDraft || name) }}
              className="min-w-0 max-w-48 sm:max-w-xs md:max-w-[28rem] lg:max-w-[36rem] truncate rounded-md px-1.5 py-1 font-semibold hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring max-md:min-h-10">{name}</button>}
          <button type="button" aria-label={`About ${name}`} title={`Options for ${name}`} aria-expanded={open} aria-haspopup="dialog"
            className="inline-flex h-7 w-6 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring max-md:h-10 max-md:w-9">
            <ChevronDown aria-hidden="true" className="h-3.5 w-3.5 shrink-0" />
          </button>
        </span> : <button type="button" aria-label={`About ${name}`} title={name} aria-expanded={open} aria-haspopup="dialog"
          className="inline-flex min-w-0 max-w-48 sm:max-w-xs md:max-w-[28rem] lg:max-w-[36rem] items-center gap-1 rounded-md px-1.5 py-1 font-semibold hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring max-md:min-h-10">
          <span className="min-w-0 truncate">{name}</span><ChevronDown aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
        </button>}
      </AgentHoverCard> : <span title={name} className="min-w-0 max-w-48 sm:max-w-xs md:max-w-[28rem] lg:max-w-[36rem] truncate font-semibold">{name}</span>}
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

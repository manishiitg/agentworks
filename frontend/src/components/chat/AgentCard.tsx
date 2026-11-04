import { useEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { Pencil } from 'lucide-react'
import { create } from 'zustand'

// A Crew agent's details (owner, 4 Nov, tab option B): the tab strip shows names only; hovering a name (~400 ms, or a
// long press on touch) opens one card, and the open tab's purpose sits in one line at the top of its chat.
export interface AgentCardInfo {
  name: string
  purpose?: string
  /** "tester-b", "everyone in the company"; undefined while the agent is not shared. */
  sharedWith?: string
  canChange?: boolean
  lastAsked?: { who: string; when: string; active: boolean; open: () => void }
  onSetPurpose: () => void
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
export interface CrewAgentRow { tabId: string; name: string; main: boolean; asking?: boolean; info?: AgentCardInfo; activity?: 'running' | 'waiting' | null }
export const useCrewAgentsStore = create<{ projectId: string | null; agents: CrewAgentRow[]; canAdd: boolean; set: (projectId: string, agents: CrewAgentRow[], canAdd: boolean) => void }>(set => ({
  projectId: null,
  agents: [],
  canAdd: false,
  set: (projectId, agents, canAdd) => set({ projectId, agents, canAdd }),
}))

/** The person's choice of how agents are shown ("menu" or "tabs"), once it has loaded from the server. */
export const useAgentsViewStore = create<{ view: 'menu' | 'tabs' | null; set: (view: 'menu' | 'tabs') => void }>(set => ({
  view: null,
  set: view => set({ view }),
}))

export function AgentHoverCard({ info, children }: { info?: AgentCardInfo; children: ReactNode }) {
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
    if (rect) setAt({ top: rect.bottom + 8, left: Math.max(8, Math.min(rect.left - 8, window.innerWidth - 336)) })
  }
  const later = (ms: number, action: () => void) => { window.clearTimeout(timer.current); timer.current = window.setTimeout(action, ms) }
  const finish = (action: () => void) => { setAt(null); action() }
  return <>
    <span ref={anchor} className="min-w-0"
      onMouseEnter={() => later(400, open)} onMouseLeave={() => later(200, () => setAt(null))}
      onTouchStart={() => later(500, open)} onTouchEnd={() => { if (!at) window.clearTimeout(timer.current) }}>
      {children}
    </span>
    {at && createPortal(<div ref={box} role="dialog" aria-label={info.name} style={at} onClick={event => event.stopPropagation()}
      onMouseEnter={() => window.clearTimeout(timer.current)} onMouseLeave={() => later(200, () => setAt(null))}
      className="fixed z-[100] w-80 rounded-2xl border border-border bg-popover p-3 text-xs shadow-lg">
      <p className="text-sm font-semibold">{info.name}</p>
      <p className={`mb-2 mt-1 ${info.purpose ? 'text-muted-foreground' : 'text-muted-foreground/70'}`}>{info.purpose || 'No purpose yet. Others see it when you share this agent.'}</p>
      <div className="flex justify-between gap-3 border-t border-border py-1.5"><span>Shared with</span><span className="min-w-0 truncate text-muted-foreground">{info.sharedWith || 'Only you'}</span></div>
      {info.sharedWith && <div className="flex justify-between gap-3 border-t border-border py-1.5"><span>For them it</span><span className="text-muted-foreground">{info.canChange ? 'can make changes' : 'answers only'}</span></div>}
      {info.lastAsked && <div className="flex justify-between gap-3 border-t border-border py-1.5"><span>Last asked</span>
        <button type="button" onClick={() => finish(info.lastAsked!.open)} className="min-w-0 truncate text-primary hover:underline">{info.lastAsked.who} · {info.lastAsked.when}</button></div>}
      <div className="mt-2 flex gap-4">
        <button type="button" onClick={() => finish(info.onSetPurpose)} className="text-primary hover:underline">{info.purpose ? 'Change purpose' : 'Set purpose'}</button>
        {info.onSharing && <button type="button" onClick={() => finish(info.onSharing!)} className="text-primary hover:underline">{info.sharedWith ? 'Sharing…' : 'Share…'}</button>}
      </div>
    </div>, document.body)}
  </>
}

/** One muted line at the top of the open tab's chat: its purpose (two lines with "more" when long), who it is shared
 * with (opens Access at this agent) and ✎ to change the purpose. */
export function AgentPurposeLine({ tabId }: { tabId?: string | null }) {
  const open = useOpenAgentStore()
  const [more, setMore] = useState(false)
  const info = open.tabId && open.tabId === tabId ? open.info : null
  if (!info || (!info.purpose && !info.sharedWith)) return null
  return <div className="flex shrink-0 items-start gap-2 border-b border-border px-4 py-1.5 text-xs text-muted-foreground">
    <p className={`min-w-0 flex-1 ${more ? 'whitespace-pre-wrap' : 'truncate'}`}>
      {info.purpose || 'No purpose yet'}
      {info.sharedWith && <> · {info.onSharing ? <button type="button" onClick={info.onSharing} className="hover:text-foreground hover:underline">Shared with {info.sharedWith}</button> : <>Shared with {info.sharedWith}</>}</>}
    </p>
    {(info.purpose?.length || 0) > 90 && <button type="button" onClick={() => setMore(value => !value)} className="shrink-0 text-primary hover:underline">{more ? 'less' : 'more'}</button>}
    <button type="button" aria-label={info.purpose ? 'Change purpose' : 'Set purpose'} title={info.purpose ? 'Change purpose' : 'Set purpose'} onClick={info.onSetPurpose} className="grid h-5 w-5 shrink-0 place-items-center rounded hover:bg-muted hover:text-foreground"><Pencil aria-hidden="true" className="h-3 w-3" /></button>
  </div>
}

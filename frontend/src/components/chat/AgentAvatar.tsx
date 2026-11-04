import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Users } from 'lucide-react'

// The agent's face on its tab (owner, 4 Nov, tab design A): its first letter on a colour taken from its name. Every
// state is a small mark on it: a ring while it works (amber when it needs you, red when it failed), a green dot while
// someone else is asking it, a people badge when it is shared. Hover or click opens one card about the agent.
const AVATAR_COLOURS = ['#9db7ff', '#8fd6b4', '#f0c27a', '#f2a3b3', '#b9a4f5', '#7fd0e0', '#d6c78f']

export function agentAvatarColour(name: string) {
  let hash = 0
  for (const char of name) hash = (hash * 31 + char.charCodeAt(0)) >>> 0
  return AVATAR_COLOURS[hash % AVATAR_COLOURS.length]
}

export type AgentAvatarStatus = 'waiting' | 'busy' | 'failed' | 'completed' | 'ready'

export interface AgentAvatarCard {
  purpose?: string
  /** "tester-b", "everyone in the company"; undefined while the agent is not shared. */
  sharedWith?: string
  canChange?: boolean
  lastAsked?: { who: string; when: string; active: boolean; open: () => void }
  onSetPurpose: () => void
  /** Opens Access at this agent; only where sharing can be set from here. */
  onSharing?: () => void
}

const RING: Partial<Record<AgentAvatarStatus, string>> = {
  busy: 'border-t-[hsl(var(--info))] motion-safe:animate-spin',
  waiting: 'border-[hsl(var(--warning))] motion-safe:animate-pulse',
  failed: 'border-[hsl(var(--destructive))]',
  completed: 'border-[hsl(var(--success))]',
}
const STATUS_WORD: Partial<Record<AgentAvatarStatus, string>> = { busy: 'Working', waiting: 'Needs you', failed: 'Failed', completed: 'Completed — open to mark as seen' }

export function AgentAvatar({ name, status, asking, shared, card }: { name: string; status: AgentAvatarStatus; asking?: boolean; shared?: string; card?: AgentAvatarCard }) {
  const face = useRef<HTMLButtonElement>(null)
  const box = useRef<HTMLDivElement>(null)
  const [at, setAt] = useState<{ top: number; left: number } | null>(null)
  const [pinned, setPinned] = useState(false)
  const leave = useRef<number | undefined>(undefined)
  useEffect(() => {
    if (!at) return
    const close = (event: MouseEvent) => { if (!box.current?.contains(event.target as Node) && !face.current?.contains(event.target as Node)) { setAt(null); setPinned(false) } }
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') { setAt(null); setPinned(false) } }
    document.addEventListener('mousedown', close)
    document.addEventListener('keydown', escape)
    return () => { document.removeEventListener('mousedown', close); document.removeEventListener('keydown', escape) }
  }, [at])
  const show = () => {
    window.clearTimeout(leave.current)
    const rect = face.current?.getBoundingClientRect()
    if (card && rect) setAt({ top: rect.bottom + 8, left: Math.max(8, Math.min(rect.left - 8, window.innerWidth - 336)) })
  }
  const hide = () => { if (!pinned) leave.current = window.setTimeout(() => setAt(null), 180) }
  const marks = [STATUS_WORD[status], asking ? 'Someone is asking it now' : '', shared ? `Shared with ${shared}` : ''].filter(Boolean).join(' · ')
  const letter = (name.trim()[0] || '?').toUpperCase()
  const finish = (action: () => void) => { setAt(null); setPinned(false); action() }
  return <>
    <button ref={face} type="button" data-agent-avatar aria-label={[name, marks].filter(Boolean).join(' · ')} aria-expanded={card ? Boolean(at) : undefined}
      title={card ? undefined : [name, marks].filter(Boolean).join(' · ')}
      onMouseEnter={show} onMouseLeave={hide} onFocus={show}
      onClick={event => { event.stopPropagation(); if (!card) return; if (pinned) { setAt(null); setPinned(false) } else { show(); setPinned(true) } }}
      onKeyDown={event => { if (event.key === 'Enter' || event.key === ' ') event.stopPropagation() }}
      style={{ background: agentAvatarColour(name) }}
      className="relative grid h-6 w-6 shrink-0 place-items-center rounded-full text-[11px] font-bold text-neutral-900 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
      {letter}
      {RING[status] && <span aria-hidden="true" className={`pointer-events-none absolute -inset-[3px] rounded-full border-2 ${status === 'busy' ? 'border-transparent' : ''} ${RING[status]}`} />}
      {asking && <span aria-hidden="true" className="absolute -right-0.5 -top-0.5 h-2 w-2 rounded-full border border-background bg-emerald-500 motion-safe:animate-pulse" />}
      {shared && <span aria-hidden="true" className="absolute -bottom-1 -right-1 grid h-3.5 w-3.5 place-items-center rounded-full border border-background bg-muted text-foreground"><Users className="h-2 w-2" /></span>}
    </button>
    {card && at && createPortal(<div ref={box} role="dialog" aria-label={name} style={at} onClick={event => event.stopPropagation()}
      onMouseEnter={() => window.clearTimeout(leave.current)} onMouseLeave={hide}
      className="fixed z-[100] w-80 rounded-2xl border border-border bg-popover p-3 text-xs shadow-lg">
      <p className="text-sm font-semibold">{name}</p>
      <p className={`mb-2 mt-1 ${card.purpose ? 'text-muted-foreground' : 'text-muted-foreground/70'}`}>{card.purpose || 'No purpose yet. Others see it when you share this agent.'}</p>
      <div className="flex justify-between gap-3 border-t border-border py-1.5"><span>Shared with</span><span className="min-w-0 truncate text-muted-foreground">{card.sharedWith || 'Only you'}</span></div>
      {card.sharedWith && <div className="flex justify-between gap-3 border-t border-border py-1.5"><span>For them it</span><span className="text-muted-foreground">{card.canChange ? 'can make changes' : 'answers only'}</span></div>}
      {card.lastAsked && <div className="flex justify-between gap-3 border-t border-border py-1.5"><span>Last asked</span>
        <button type="button" onClick={() => finish(card.lastAsked!.open)} className="min-w-0 truncate text-primary hover:underline">{card.lastAsked.who} · {card.lastAsked.when}</button></div>}
      <div className="mt-2 flex gap-4">
        <button type="button" onClick={() => finish(card.onSetPurpose)} className="text-primary hover:underline">{card.purpose ? 'Change purpose' : 'Set purpose'}</button>
        {card.onSharing && <button type="button" onClick={() => finish(card.onSharing!)} className="text-primary hover:underline">{card.sharedWith ? 'Sharing…' : 'Share…'}</button>}
      </div>
    </div>, document.body)}
  </>
}

import { useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Loader2, Users } from 'lucide-react'

export type SharedPerson = { name: string; state: 'working' | 'waiting' | 'online' | 'offline' }

/** A shared project's people icon: a dot when anyone else is online, and on hover who shared it and each person's
 * state, as the server sends them (owner, 10 Nov). */
export function SharedPeopleIcon({ sharedLine, people }: { sharedLine?: string; people: SharedPerson[] }) {
  const icon = useRef<HTMLSpanElement>(null)
  const [at, setAt] = useState<{ top: number; left: number } | null>(null)
  const anyoneOn = people.some(person => person.state !== 'offline')
  const label = [sharedLine, ...people.map(person => `${person.name} · ${person.state}`)].filter(Boolean).join(', ')
  const show = () => {
    const rect = icon.current?.getBoundingClientRect()
    if (rect) setAt({ top: Math.min(rect.bottom + 6, window.innerHeight - 40 - people.length * 24), left: Math.max(8, Math.min(rect.left - 8, window.innerWidth - 248)) })
  }
  return <>
    <span ref={icon} role="img" aria-label={label} onMouseEnter={show} onMouseLeave={() => setAt(null)} onFocus={show} onBlur={() => setAt(null)} tabIndex={-1}
      className="relative ml-1.5 inline-flex align-[-1px] text-muted-foreground">
      <Users aria-hidden="true" className="h-3 w-3" />
      {people.length > 0 && <span aria-hidden="true" className={`absolute -bottom-0.5 -right-0.5 h-1.5 w-1.5 rounded-full ring-1 ring-background ${anyoneOn ? 'bg-emerald-500' : 'bg-muted-foreground/50'}`} />}
    </span>
    {at && createPortal(<div role="tooltip" style={at} className="pointer-events-none fixed z-[100] w-60 rounded-lg border border-border bg-popover p-2 text-xs text-popover-foreground shadow-lg">
      {sharedLine && <p className="mb-1 truncate text-muted-foreground">{sharedLine}</p>}
      {people.map(person => <div key={person.name} className="flex items-center gap-2 py-0.5">
        <span aria-hidden="true" className={`h-1.5 w-1.5 shrink-0 rounded-full ${person.state === 'offline' ? 'bg-muted-foreground/50' : 'bg-emerald-500'}`} />
        <span className="min-w-0 flex-1 truncate">{person.name}</span>
        <span className="shrink-0 text-muted-foreground">{person.state}</span>
        {person.state === 'working' && <Loader2 aria-hidden="true" className="h-3 w-3 shrink-0 animate-spin text-[hsl(var(--info))]" />}
      </div>)}
      {!people.length && !sharedLine && <p className="text-muted-foreground">Shared with you</p>}
    </div>, document.body)}
  </>
}

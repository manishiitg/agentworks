import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Info } from 'lucide-react'

/** A quiet purpose icon on the owner's idle tab (owner, 3 Nov: the pens on every tab were distracting): a faint info
 * icon when the agent has a purpose, otherwise shown only while hovering the tab. It opens a small card with the purpose
 * and Change purpose / Set purpose, which puts the request in the tab's chat box. */
export function AgentPurposeButton({ name, purpose, onSetPurpose }: { name: string; purpose?: string; onSetPurpose: () => void }) {
  const button = useRef<HTMLButtonElement>(null)
  const card = useRef<HTMLDivElement>(null)
  const [at, setAt] = useState<{ top: number; left: number } | null>(null)
  useEffect(() => {
    if (!at) return
    const close = (event: MouseEvent) => { if (!card.current?.contains(event.target as Node) && !button.current?.contains(event.target as Node)) setAt(null) }
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') setAt(null) }
    document.addEventListener('mousedown', close)
    document.addEventListener('keydown', escape)
    return () => { document.removeEventListener('mousedown', close); document.removeEventListener('keydown', escape) }
  }, [at])
  const toggle = () => {
    const rect = button.current?.getBoundingClientRect()
    setAt(at || !rect ? null : { top: rect.bottom + 6, left: Math.max(8, Math.min(rect.left - 8, window.innerWidth - 288)) })
  }
  return <>
    <button ref={button} type="button" data-agent-purpose aria-label={purpose ? `${name}'s purpose` : `Set ${name}'s purpose`} aria-expanded={Boolean(at)}
      onClick={event => { event.stopPropagation(); toggle() }}
      onKeyDown={event => { if (event.key === 'Enter' || event.key === ' ') event.stopPropagation() }}
      className={`grid h-3.5 w-3.5 place-items-center rounded text-muted-foreground/60 hover:text-foreground focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${purpose || at ? '' : 'opacity-0 group-hover:opacity-100'}`}>
      <Info aria-hidden="true" className="h-3 w-3" />
    </button>
    {at && createPortal(<div ref={card} role="dialog" aria-label={`${name}'s purpose`} style={at} onClick={event => event.stopPropagation()}
      className="fixed z-[100] w-72 space-y-2 rounded-2xl border border-border bg-popover p-3 text-xs shadow-lg">
      <p className="font-medium text-muted-foreground">Purpose</p>
      <p className={purpose ? 'text-sm text-foreground' : 'text-muted-foreground'}>{purpose || 'No purpose yet. Others see it when you share this agent.'}</p>
      <button type="button" onClick={() => { setAt(null); onSetPurpose() }} className="rounded-full border border-border px-3 py-1 text-xs hover:bg-muted">{purpose ? 'Change purpose' : 'Set purpose'}</button>
    </div>, document.body)}
  </>
}

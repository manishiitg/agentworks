import { useEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { ArrowDown, ArrowUp, MoreHorizontal, Plus } from 'lucide-react'

/** Reorders personal navigation shortcuts, never the Crew or its agents. */
export function PinnedCrewOrderMenu({ name, onMoveUp, onMoveDown, onNewAgent, groupsMenu }: { name: string; onMoveUp?: () => void; onMoveDown?: () => void; onNewAgent?: () => void; groupsMenu?: ReactNode }) {
  const trigger = useRef<HTMLButtonElement>(null)
  const panel = useRef<HTMLDivElement>(null)
  const [position, setPosition] = useState<{ top: number; left: number } | null>(null)
  const close = () => { setPosition(null); trigger.current?.focus() }
  useEffect(() => {
    if (!position) return
    Array.from(panel.current?.querySelectorAll<HTMLElement>('button:not(:disabled), summary') || []).find(item => item.getClientRects().length > 0)?.focus()
    const outside = (event: MouseEvent) => {
      const target = event.target as Node
      if (!panel.current?.contains(target) && !trigger.current?.contains(target)) setPosition(null)
    }
    const escape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { event.preventDefault(); setPosition(null); trigger.current?.focus() }
    }
    const dismiss = () => setPosition(null)
    document.addEventListener('mousedown', outside)
    document.addEventListener('keydown', escape)
    window.addEventListener('resize', dismiss)
    return () => {
      document.removeEventListener('mousedown', outside)
      document.removeEventListener('keydown', escape)
      window.removeEventListener('resize', dismiss)
    }
  }, [position])
  return <>
    <button ref={trigger} type="button" title="Project actions" aria-label={`Actions for ${name}`} aria-haspopup="dialog" aria-expanded={!!position}
      className="mr-1 flex h-8 w-7 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring md:opacity-0 md:group-hover/crew:opacity-100 md:group-focus-within/crew:opacity-100"
      onClick={() => {
        if (position) { setPosition(null); return }
        const rect = trigger.current?.getBoundingClientRect()
        if (rect) setPosition({ top: Math.max(8, Math.min(rect.bottom + 4, window.innerHeight - 280)), left: Math.max(8, Math.min(rect.right - 208, window.innerWidth - 216)) })
      }}><MoreHorizontal aria-hidden="true" className="h-3.5 w-3.5" /></button>
    {position && createPortal(<div ref={panel} role="dialog" aria-label={`Actions for ${name}`} style={position} className="fixed z-[100] w-52 max-h-[calc(100dvh-16px)] overflow-y-auto rounded-lg border border-border bg-popover p-1 text-popover-foreground shadow-lg"
      onBlur={event => { if (event.relatedTarget && !event.currentTarget.contains(event.relatedTarget as Node) && event.relatedTarget !== trigger.current) setPosition(null) }}>
      {onNewAgent && <button type="button" onClick={() => { close(); onNewAgent() }} className="flex w-full items-center gap-2 rounded-md px-2 py-2 text-left text-xs hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring"><Plus aria-hidden="true" className="h-3.5 w-3.5" />New agent</button>}
      {groupsMenu}
      {(onMoveUp || onMoveDown) && <div className="my-1 border-t border-border" />}
      {(onMoveUp || onMoveDown) && <>
      <button type="button" disabled={!onMoveUp} onClick={() => { onMoveUp?.(); close() }} className="flex w-full items-center gap-2 rounded-md px-2 py-2 text-left text-xs hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-40"><ArrowUp aria-hidden="true" className="h-3.5 w-3.5" />Move up</button>
      <button type="button" disabled={!onMoveDown} onClick={() => { onMoveDown?.(); close() }} className="flex w-full items-center gap-2 rounded-md px-2 py-2 text-left text-xs hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-40"><ArrowDown aria-hidden="true" className="h-3.5 w-3.5" />Move down</button>
      </>}
    </div>, document.body)}
  </>
}

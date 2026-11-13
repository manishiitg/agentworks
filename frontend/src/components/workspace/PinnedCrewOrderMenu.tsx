import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { ArrowDown, ArrowUp, MoreHorizontal, Plus, Pin, PinOff, Trash2 } from 'lucide-react'

/** Reorders personal navigation shortcuts, never the Crew or its agents. */
export function PinnedCrewOrderMenu({ name, pinned, onTogglePin, onMoveUp, onMoveDown, onNewAgent, groupsMenu, onRemoveOnlineCopy }: { name: string; pinned?: boolean; onTogglePin?: () => void; onMoveUp?: () => void; onMoveDown?: () => void; onNewAgent?: () => void; groupsMenu?: ReactNode; onRemoveOnlineCopy?: () => void }) {
  const trigger = useRef<HTMLButtonElement>(null)
  const panel = useRef<HTMLDivElement>(null)
  const [position, setPosition] = useState<{ top: number; left: number } | null>(null)
  const [confirmRemove, setConfirmRemove] = useState(false)
  const open = Boolean(position)
  useLayoutEffect(() => {
    if (!open) return
    const rect = trigger.current?.getBoundingClientRect()
    const height = panel.current?.getBoundingClientRect().height || 0
    if (!rect) return
    const top = Math.max(8, rect.bottom + 4 + height <= window.innerHeight - 8 ? rect.bottom + 4 : rect.top - height - 4)
    const left = Math.max(8, Math.min(rect.right - 208, window.innerWidth - 216))
    setPosition(previous => previous?.top === top && previous.left === left ? previous : { top, left })
  }, [open, confirmRemove, groupsMenu])
  const close = () => { setPosition(null); setConfirmRemove(false); trigger.current?.focus() }
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
    const scroll = (event: Event) => { if (!(event.target instanceof Node) || !panel.current?.contains(event.target)) dismiss() }
    window.addEventListener('scroll', scroll, true)
    return () => {
      document.removeEventListener('mousedown', outside)
      document.removeEventListener('keydown', escape)
      window.removeEventListener('resize', dismiss)
      window.removeEventListener('scroll', scroll, true)
    }
  }, [position])
  return <>
    <button ref={trigger} type="button" title="Project actions" aria-label={`Actions for ${name}`} aria-haspopup="dialog" aria-expanded={!!position}
      className="mr-1 flex h-8 w-7 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring md:opacity-0 md:group-hover/crew:opacity-100 md:group-focus-within/crew:opacity-100"
      onClick={() => {
        if (position) { setPosition(null); return }
        setConfirmRemove(false)
        const rect = trigger.current?.getBoundingClientRect()
        if (rect) setPosition({ top: rect.bottom + 4, left: Math.max(8, Math.min(rect.right - 208, window.innerWidth - 216)) })
      }}><MoreHorizontal aria-hidden="true" className="h-3.5 w-3.5" /></button>
    {position && createPortal(<div ref={panel} role="dialog" aria-label={`Actions for ${name}`} style={position} className="fixed z-[100] w-52 max-h-[calc(100dvh-16px)] overflow-y-auto rounded-lg border border-border bg-popover p-1 text-popover-foreground shadow-lg"
      onBlur={event => { if (event.relatedTarget && !event.currentTarget.contains(event.relatedTarget as Node) && event.relatedTarget !== trigger.current) setPosition(null) }}>
      {onNewAgent && <button type="button" onClick={() => { close(); onNewAgent() }} className="flex w-full items-center gap-2 rounded-md px-2 py-2 text-left text-xs hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring"><Plus aria-hidden="true" className="h-3.5 w-3.5" />New agent</button>}
      {onTogglePin && <button type="button" onClick={() => { onTogglePin(); close() }} className="flex w-full items-center gap-2 rounded-md px-2 py-2 text-left text-xs hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring">{pinned ? <PinOff aria-hidden="true" className="h-3.5 w-3.5" /> : <Pin aria-hidden="true" className="h-3.5 w-3.5" />}{pinned ? 'Unpin Crew' : 'Pin Crew'}</button>}
      {groupsMenu}
      {onRemoveOnlineCopy && !confirmRemove && <button type="button" onClick={() => setConfirmRemove(true)} className="flex w-full items-center gap-2 rounded-md px-2 py-2 text-left text-xs hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring"><Trash2 aria-hidden="true" className="h-3.5 w-3.5" />Remove online copy</button>}
      {onRemoveOnlineCopy && confirmRemove && <div className="px-2 py-1">
        <p className="px-1 pb-1.5 text-xs text-muted-foreground">Remove this online copy? Your Mac Crew stays on your Mac.</p>
        <div className="flex gap-1">
          <button type="button" onClick={() => { onRemoveOnlineCopy(); close() }} className="flex-1 rounded-md bg-destructive px-2 py-1.5 text-left text-xs text-destructive-foreground hover:opacity-90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">Remove</button>
          <button type="button" onClick={() => setConfirmRemove(false)} className="flex-1 rounded-md border px-2 py-1.5 text-left text-xs hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">Cancel</button>
        </div>
      </div>}
      {(onMoveUp || onMoveDown) && <div className="my-1 border-t border-border" />}
      {(onMoveUp || onMoveDown) && <>
      <button type="button" disabled={!onMoveUp} onClick={() => { onMoveUp?.(); close() }} className="flex w-full items-center gap-2 rounded-md px-2 py-2 text-left text-xs hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-40"><ArrowUp aria-hidden="true" className="h-3.5 w-3.5" />Move up</button>
      <button type="button" disabled={!onMoveDown} onClick={() => { onMoveDown?.(); close() }} className="flex w-full items-center gap-2 rounded-md px-2 py-2 text-left text-xs hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-40"><ArrowDown aria-hidden="true" className="h-3.5 w-3.5" />Move down</button>
      </>}
    </div>, document.body)}
  </>
}

import { useEffect, useRef, useState } from 'react'
import { X, type LucideIcon } from 'lucide-react'

// A feature that is not set up yet (#3780, #3838): one row with its one next step.
// That button opens a small card saying in plain words what it is, with Set up
// (which starts it) and Hide; Hide moves the row into Connections.
export function SetupPrompt({ icon: Icon, title, line, action, onAction, what, points, onHide }: {
  icon: LucideIcon
  title: string
  line: string
  action: string
  onAction: () => void
  what: string
  points: string[]
  onHide: () => void
}) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const outside = (event: MouseEvent) => { if (ref.current && !ref.current.contains(event.target as Node)) setOpen(false) }
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') setOpen(false) }
    document.addEventListener('mousedown', outside)
    document.addEventListener('keydown', escape)
    return () => { document.removeEventListener('mousedown', outside); document.removeEventListener('keydown', escape) }
  }, [open])
  const ring = 'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
  return <div ref={ref} className="px-1 pb-1">
    <div className="flex min-h-8 items-center gap-2 text-xs text-foreground/85">
      <Icon aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-muted-foreground"/>
      <span className="min-w-0 flex-1 truncate">{title}</span>
      <button type="button" onClick={() => setOpen(value => !value)} aria-expanded={open} aria-haspopup="dialog" className={`shrink-0 rounded-full border border-border px-2 py-0.5 text-[10px] font-medium text-primary hover:bg-background/40 ${open ? 'bg-background/40' : ''} ${ring}`}>{action}</button>
    </div>
    <p className="truncate pl-[22px] text-[10px] text-muted-foreground">{line}</p>
    {open && <div role="dialog" aria-label={title} className="mt-1 rounded-lg border border-border bg-popover px-3 py-2 text-[11px] text-popover-foreground shadow-lg">
      <div className="flex items-start gap-2">
        <p className="min-w-0 flex-1 font-medium">{title}</p>
        <button type="button" onClick={() => setOpen(false)} aria-label="Close" className={`flex h-4 w-4 shrink-0 items-center justify-center rounded text-muted-foreground hover:text-foreground ${ring}`}><X aria-hidden="true" className="h-3 w-3"/></button>
      </div>
      <p className="mt-0.5 text-muted-foreground">{what}</p>
      <ul className="mt-1 list-disc space-y-0.5 pl-4">{points.map(point => <li key={point}>{point}</li>)}</ul>
      <div className="mt-2 flex items-center gap-1.5">
        <button type="button" onClick={() => { setOpen(false); onAction() }} className={`rounded-md bg-primary px-2.5 py-1 font-medium text-primary-foreground hover:bg-primary/90 ${ring}`}>{action}</button>
        <button type="button" onClick={() => { setOpen(false); onHide() }} className={`rounded-md border border-border px-2.5 py-1 hover:bg-background/40 ${ring}`}>Hide</button>
      </div>
      <p className="mt-1.5 text-[10px] text-muted-foreground">Hidden? It stays in Connections, ready when you are.</p>
    </div>}
  </div>
}

// Hiding is a view preference for this project on this device.
export function useSetupPromptHidden(key: string) {
  const [hidden, setHidden] = useState(() => { try { return localStorage.getItem(key) === '1' } catch { return false } })
  useEffect(() => { try { setHidden(localStorage.getItem(key) === '1') } catch { setHidden(false) } }, [key])
  const hide = () => { setHidden(true); try { localStorage.setItem(key, '1') } catch { /* hidden for this visit only */ } }
  return [hidden, hide] as const
}

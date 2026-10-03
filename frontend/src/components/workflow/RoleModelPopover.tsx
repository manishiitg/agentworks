import { useCallback, useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode } from 'react'
import { ChevronDown } from 'lucide-react'

const FOCUSABLE = 'button:not([disabled]), select:not([disabled]), [href], [tabindex]:not([tabindex="-1"])'

/**
 * A small popover opened by one summary button. Focus moves into it on open,
 * Tab stays inside it, Escape or an outside click closes it and focus returns
 * to the button.
 */
export function RoleModelPopover({ label, summary, children }: { label: string; summary: string; children: ReactNode }) {
  const [open, setOpen] = useState(false)
  const buttonRef = useRef<HTMLButtonElement>(null)
  const panelRef = useRef<HTMLDivElement>(null)
  const rootRef = useRef<HTMLDivElement>(null)
  const panelId = useId()

  const close = useCallback(() => {
    setOpen(false)
    buttonRef.current?.focus()
  }, [])

  useEffect(() => {
    if (!open) return
    panelRef.current?.querySelector<HTMLElement>(FOCUSABLE)?.focus()
    const onPointer = (event: MouseEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onPointer)
    return () => document.removeEventListener('mousedown', onPointer)
  }, [open])

  const onKeyDown = (event: KeyboardEvent) => {
    if (!open) return
    if (event.key === 'Escape') {
      event.stopPropagation()
      close()
      return
    }
    if (event.key !== 'Tab') return
    const items = Array.from(panelRef.current?.querySelectorAll<HTMLElement>(FOCUSABLE) ?? [])
    if (items.length === 0) return
    const first = items[0]
    const last = items[items.length - 1]
    const active = document.activeElement
    if (event.shiftKey && (active === first || active === buttonRef.current)) { event.preventDefault(); last.focus() }
    else if (!event.shiftKey && active === last) { event.preventDefault(); first.focus() }
  }

  return (
    <div ref={rootRef} className="relative min-w-0" onKeyDown={onKeyDown}>
      <button
        ref={buttonRef}
        type="button"
        aria-label={`${label}: ${summary}. Change model`}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-controls={open ? panelId : undefined}
        onClick={() => setOpen(value => !value)}
        className="flex h-8 w-full min-w-0 items-center justify-between gap-2 rounded-md border border-border bg-background px-2.5 text-left text-xs text-foreground transition-colors hover:bg-muted/50 focus:border-primary focus:outline-none"
      >
        <span className="min-w-0 truncate">{summary}</span>
        <ChevronDown className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden />
      </button>
      {open && (
        <div
          ref={panelRef}
          id={panelId}
          role="dialog"
          aria-label={`${label} model`}
          className="absolute right-0 z-30 mt-1 w-[min(24rem,calc(100vw-2rem))] space-y-3 rounded-lg border border-border bg-popover p-3 text-popover-foreground shadow-lg"
        >
          {children}
        </div>
      )}
    </div>
  )
}

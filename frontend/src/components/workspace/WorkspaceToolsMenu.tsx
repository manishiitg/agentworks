import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Ellipsis } from 'lucide-react'

/** Existing workspace views only; app setup lives in the workspace picker. */
export function WorkspaceToolsMenu({ children }: { children: (close: () => void) => ReactNode }) {
  const [open, setOpen] = useState(false)
  const root = useRef<HTMLElement>(null)
  const trigger = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    if (!open) return
    const outside = (event: MouseEvent) => {
      if (!root.current?.contains(event.target as Node)) setOpen(false)
    }
    const select = (event: MouseEvent) => {
      if (event.target instanceof Element && root.current?.contains(event.target) && event.target.closest('[data-product-navigation-action]')) setOpen(false)
    }
    const key = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { setOpen(false); trigger.current?.focus() }
      if (!(event.target instanceof Element) || !root.current?.contains(event.target) || !event.target.closest('[role="menu"]')) return
      if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return
      event.preventDefault()
      const items = Array.from(root.current.querySelectorAll<HTMLButtonElement>('[role="menuitem"]:not(:disabled)'))
      const current = items.indexOf(document.activeElement as HTMLButtonElement)
      const next = event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : (current + (event.key === 'ArrowUp' ? -1 : 1) + items.length) % items.length
      items[next]?.focus()
    }
    document.addEventListener('mousedown', outside)
    document.addEventListener('keydown', key)
    document.addEventListener('click', select)
    return () => { document.removeEventListener('mousedown', outside); document.removeEventListener('keydown', key); document.removeEventListener('click', select) }
  }, [open])
  return <nav ref={root} aria-label="Workspace tools" className="relative inline-flex shrink-0 items-center">
    <button ref={trigger} type="button" aria-label="More workspace tools" title="More options" aria-haspopup="menu" aria-expanded={open}
      onClick={() => setOpen(value => !value)}
      onKeyDown={event => {
        if (event.key === 'ArrowDown') {
          event.preventDefault(); setOpen(true)
          requestAnimationFrame(() => root.current?.querySelector<HTMLButtonElement>('[role="menuitem"]')?.focus())
        }
      }}
      className="grid h-8 w-8 place-items-center rounded-lg text-muted-foreground hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
      <Ellipsis className="h-4 w-4" aria-hidden="true" />
    </button>
    <div hidden={!open} role="menu" aria-label="More workspace tools"
      className="absolute right-0 top-full z-50 mt-2 max-h-[75vh] w-60 max-w-[calc(100vw-24px)] overflow-auto rounded-xl border border-border bg-popover p-1 shadow-xl">
      {children(() => setOpen(false))}
    </div>
  </nav>
}

export function WorkspaceToolsGroup({ title, children }: { title: string; children: ReactNode }) {
  return <div role="group" aria-label={title} className="border-t border-border first:border-t-0">
    <h3 className="px-3 pb-1 pt-2 text-[10px] font-medium text-muted-foreground">{title}</h3>
    {children}
  </div>
}

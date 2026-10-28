import { useState, type ReactNode } from 'react'
import { ChevronDown, ChevronRight } from 'lucide-react'

export function GitSection({ title, count, action, children, defaultOpen = true }: { title: string; count: number; action?: ReactNode; children: ReactNode; defaultOpen?: boolean }) {
  const [open, setOpen] = useState(defaultOpen)
  if (count === 0) return null
  return (
    <div>
      <div className="group/section flex h-7 items-center gap-1 px-2 text-xs font-semibold text-muted-foreground">
        <button type="button" onClick={() => setOpen(value => !value)} aria-expanded={open} className="flex min-w-0 flex-1 items-center gap-1 text-left">
          {open ? <ChevronDown className="h-3.5 w-3.5 shrink-0" /> : <ChevronRight className="h-3.5 w-3.5 shrink-0" />}
          <span className="truncate">{title}</span>
          <span className="rounded-full bg-muted px-1.5 text-[10px] font-medium normal-case leading-4 text-foreground">{count}</span>
        </button>
        <span className="hidden group-hover/section:flex group-focus-within/section:flex">{action}</span>
      </div>
      {open && children}
    </div>
  )
}


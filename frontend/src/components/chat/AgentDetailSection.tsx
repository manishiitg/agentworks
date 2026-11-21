import { ChevronDown } from 'lucide-react'
import { useId, useState, type ReactNode } from 'react'

/** A collapsed section of the agent card; summaries stay visible when it is closed. */
export function AgentDetailSection({ title, value, summary, children }: { title: string; value?: string; summary?: ReactNode; children: ReactNode }) {
  const id = useId()
  const [open, setOpen] = useState(false)
  return <section className="min-w-0 border-t border-border pt-0.5">
    <button type="button" aria-expanded={open} aria-controls={id} onClick={() => setOpen(!open)}
      className="flex min-h-9 w-full min-w-0 items-center gap-2 rounded-md px-1 text-left hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring max-md:min-h-11">
      <span className="shrink-0 font-medium">{title}</span>
      <span title={value} className="min-w-0 flex-1 truncate text-right text-muted-foreground">{value}</span>
      <ChevronDown aria-hidden="true" className={`h-3.5 w-3.5 shrink-0 text-muted-foreground ${open ? '' : '-rotate-90'}`} />
    </button>
    {summary}
    <div id={id} hidden={!open} className="space-y-1 px-1 pb-2">{children}</div>
  </section>
}

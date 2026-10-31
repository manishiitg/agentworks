import { useId, useState, type ReactNode } from 'react'
import { ChevronRight, type LucideIcon } from 'lucide-react'

/** Display-only disclosure; children keep their existing destinations and admission. */
export function WorkspaceCompactGroup({ title, icon: Icon, detail, preview, children }: {
  title: string; icon: LucideIcon; detail?: ReactNode; preview?: ReactNode; children: ReactNode
}) {
  const [expanded, setExpanded] = useState(false)
  const id = useId()
  return <section>
    <div className="flex min-h-9 items-center gap-2">
      <button type="button" aria-expanded={expanded} aria-controls={id} aria-label={title} onClick={() => setExpanded(value => !value)} className="flex min-h-9 min-w-0 flex-1 items-center gap-2.5 rounded-md px-1 text-left text-xs text-foreground/85 hover:bg-background/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <Icon aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-muted-foreground"/>
        <span className="min-w-0 flex-1 truncate">{title}</span>
        {detail !== undefined && detail !== null && <span className="shrink-0 text-[10px] text-muted-foreground">{detail}</span>}
        {!preview && <ChevronRight aria-hidden="true" className={`h-3 w-3 shrink-0 transition-transform ${expanded ? 'rotate-90' : ''}`}/>}
      </button>
      {preview && <>
        <div className="flex shrink-0 items-center gap-0.5">{preview}</div>
        <button type="button" aria-label={`${expanded ? 'Collapse' : 'Expand'} ${title.toLowerCase()}`} aria-expanded={expanded} aria-controls={id} onClick={() => setExpanded(value => !value)} className="flex h-8 w-6 shrink-0 items-center justify-center rounded text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
          <ChevronRight aria-hidden="true" className={`h-3 w-3 transition-transform ${expanded ? 'rotate-90' : ''}`}/>
        </button>
      </>}
    </div>
    {expanded && <div id={id} className="ml-2.5 mt-1 space-y-1 border-l border-border pl-3">{children}</div>}
  </section>
}

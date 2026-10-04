import { ArrowLeft, ChevronRight } from 'lucide-react'
import { Button } from '../ui/Button'

/** Drill into a workspace section without adding another tab bar. */
export function WorkspaceViewBreadcrumbs({ parent, current, onBack }: { parent: string; current?: string; onBack: () => void }) {
  return <span className="inline-flex min-w-0 items-center gap-1">
    <Button variant="ghost" size="xs" className="-ml-1 text-muted-foreground" aria-label={`Back to ${parent}`} onClick={onBack}><ArrowLeft className="h-3.5 w-3.5"/>{parent}</Button>
    {current && <><ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden/>
    <span className="truncate">{current}</span></>}
  </span>
}

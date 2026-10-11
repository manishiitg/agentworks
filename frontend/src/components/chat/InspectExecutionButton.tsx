import { ExternalLink } from 'lucide-react'
import { openStepInspection, readInspectionTarget } from '../../utils/stepInspection'

export function InspectExecutionButton({ target }: { target: unknown }) {
 const resolved = readInspectionTarget(target)
 if (!resolved) return null
 return <button type="button" title="Inspect step" aria-label={`Inspect ${resolved.name || 'step'}`}
  className="ml-auto inline-flex h-6 w-6 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
  onClick={event => { event.preventDefault(); event.stopPropagation(); openStepInspection(resolved) }}>
  <ExternalLink className="h-3.5 w-3.5" aria-hidden="true" />
 </button>
}

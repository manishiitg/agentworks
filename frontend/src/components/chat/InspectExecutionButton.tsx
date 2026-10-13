import { useChatStore } from '../../stores/useChatStore'
import { ExternalLink } from 'lucide-react'
import { openStepInspection, readInspectionTarget, isWorkflowInspectionTab } from '../../utils/stepInspection'

export function InspectExecutionButton({ target, label }: { target: unknown; label?: string }) {
 const tab = useChatStore(state => state.activeTabId ? state.chatTabs[state.activeTabId] : undefined)
 const resolved = readInspectionTarget(target)
 if (!resolved || !isWorkflowInspectionTab(tab)) return null
 return <button type="button" title={`Inspect ${resolved.name || 'execution'}`} aria-label={`Inspect ${resolved.name || 'step'}`}
  className={`ml-auto inline-flex h-6 shrink-0 items-center justify-center gap-1 rounded text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${label ? 'px-2 text-[11px]' : 'w-6'}`}
  onClick={event => { event.preventDefault(); event.stopPropagation(); openStepInspection(resolved) }}>
  <ExternalLink className="h-3.5 w-3.5" aria-hidden="true" />{label}
 </button>
}

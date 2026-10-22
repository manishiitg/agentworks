import { useEffect, useRef } from 'react'
import { GitBranch, X } from 'lucide-react'
import type { ChatTab } from '../../stores/useChatStore'
import { inspectionKey, selectStepInspection, stepInspectionState } from '../../utils/stepInspection'

/** Step conversations share the existing chat strip; they never create sessions. */
export function StepInspectionTabItems({ tab, active, onActivate }: { tab: ChatTab; active: boolean; onActivate: (tabId: string) => void }) {
  const { items, selected } = stepInspectionState(tab)
  const selectedKey = selected ? inspectionKey(selected) : null
  const selectedRef = useRef<HTMLButtonElement>(null)
  useEffect(() => { if (active && selectedKey) selectedRef.current?.focus() }, [active, selectedKey])
  return <>{items.map(item => {
    const key = inspectionKey(item)
    const isActive = active && selectedKey === key
    const label = item.name || item.step_id || 'Step'
    return <div key={key} className={`group flex shrink-0 items-center gap-1 border-b-2 px-2 py-1 text-xs ${isActive ? 'border-primary font-semibold text-foreground' : 'border-transparent text-muted-foreground hover:bg-muted hover:text-foreground'}`}>
      <button ref={isActive ? selectedRef : undefined} type="button" aria-label={`Inspect ${label}`} aria-pressed={isActive} title={[label, item.run_folder, item.started_at].filter(Boolean).join(' · ')} onClick={() => {
        if (selectStepInspection(tab.tabId, tab.sessionId, key)) onActivate(tab.tabId)
      }} className="flex min-w-0 items-center gap-1.5 rounded py-1 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <GitBranch aria-hidden="true" className="h-3 w-3 shrink-0" /><span className="max-w-56 truncate">{label}</span>
      </button>
      <button type="button" aria-label={`Close inspection ${label}`} title="Close inspection" onClick={() => {
        if (selectStepInspection(tab.tabId, tab.sessionId, selectedKey === key ? null : selectedKey, key) && isActive) onActivate(tab.tabId)
      }} className="grid h-5 w-5 shrink-0 place-items-center rounded text-muted-foreground hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><X aria-hidden="true" className="h-3 w-3" /></button>
    </div>
  })}</>
}

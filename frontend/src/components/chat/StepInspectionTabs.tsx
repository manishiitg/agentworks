import { lazy, Suspense, useEffect, useId, useRef, type ReactNode } from 'react'
import { X } from 'lucide-react'
import { useChatStore } from '../../stores/useChatStore'
import { inspectionKey, selectStepInspection, stepInspectionState } from '../../utils/stepInspection'

const StepInspectionPanel = lazy(() => import('./StepInspectionPanel'))

export function StepInspectionTabs({ tabId, children, navigation = 'inline' }: { tabId: string | null; children: ReactNode; navigation?: 'inline' | 'shared' }) {
 const prefix = useId()
 const tabList = useRef<HTMLDivElement>(null)
 const tab = useChatStore(state => tabId ? state.chatTabs[tabId] : undefined)
 const { items, selected } = stepInspectionState(tab)
 const activeId = tab?.metadata?.stepInspections?.activeId
 useEffect(() => {
  tabList.current?.querySelector<HTMLButtonElement>('[role="tab"][aria-selected="true"]')?.focus()
 }, [activeId])
 const select = (id: string | null, close?: string) => {
  if (!tabId) return
  selectStepInspection(tabId, tab?.sessionId, id, close)
 }
 return <div className="flex h-full min-h-0 min-w-0 flex-col">
  {navigation === 'inline' && items.length > 0 && <div role="tablist" ref={tabList} aria-label="Chat and step inspections" onKeyDown={event => {
   if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key) || (event.target as HTMLElement).getAttribute('role') !== 'tab') return
   const keys = [null, ...items.map(inspectionKey)]
   const current = Math.max(0, keys.indexOf(selected ? inspectionKey(selected) : null))
   const next = event.key === 'Home' ? 0 : event.key === 'End' ? keys.length - 1 : (current + (event.key === 'ArrowRight' ? 1 : -1) + keys.length) % keys.length
   event.preventDefault(); select(keys[next])
  }} className="flex shrink-0 items-center gap-1 overflow-x-auto border-b border-border px-2 py-1">
   <button role="tab" id={`${prefix}-chat-tab`} aria-controls={`${prefix}-chat`} tabIndex={selected ? -1 : 0} aria-selected={!selected} onClick={() => select(null)} className={`shrink-0 rounded px-2 py-1 text-xs ${!selected ? 'bg-muted text-foreground' : 'text-muted-foreground'}`}>Chat</button>
   {items.map(item => { const id = inspectionKey(item); return <div key={id} className={`flex shrink-0 items-center rounded ${selected && inspectionKey(selected) === id ? 'bg-muted' : ''}`}>
    <button role="tab" id={`${prefix}-${items.indexOf(item)}-tab`} tabIndex={activeId === id ? 0 : -1} aria-selected={activeId === id} aria-controls={`${prefix}-panel`} onClick={() => select(id)} className="max-w-44 truncate px-2 py-1 text-xs" title={[item.name, item.run_folder, item.started_at].filter(Boolean).join(" · ")}>{item.name || item.step_id || 'Step'}</button>
    <button type="button" aria-label={`Close inspection ${item.name || 'step'}`} title="Close inspection" onClick={() => select(activeId === id ? null : activeId ?? null, id)} className="mr-1 rounded p-1 text-muted-foreground hover:bg-accent"><X className="h-3 w-3" /></button>
   </div> })}
  </div>}
  <div id={`${prefix}-chat`} role={navigation === 'inline' ? 'tabpanel' : 'region'} aria-label={navigation === 'shared' ? 'Chat' : undefined} aria-labelledby={navigation === 'inline' ? `${prefix}-chat-tab` : undefined} hidden={!!selected} className="h-full min-h-0 flex-1" style={selected ? {display:'none'} : undefined}>{children}</div>
  {selected && <div id={`${prefix}-panel`} role={navigation === 'inline' ? 'tabpanel' : 'region'} aria-label={navigation === 'shared' ? `Step: ${selected.name || selected.step_id || 'Step'}` : undefined} aria-labelledby={navigation === 'inline' ? `${prefix}-${items.indexOf(selected)}-tab` : undefined} className="min-h-0 flex-1">
   <Suspense fallback={<div className="p-4 text-xs text-muted-foreground">Loading step…</div>}><StepInspectionPanel key={inspectionKey(selected)} target={selected} /></Suspense>
  </div>}
 </div>
}

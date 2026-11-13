import { lazy, Suspense, useEffect } from 'react'
import { Clock, Plus, RefreshCw, Search } from 'lucide-react'
import { useScheduleRunsData } from './scheduleRuns/useScheduleRunsData'
import { ScheduleListView } from './scheduleRuns/ScheduleListView'
import { useChatStore } from '../../stores/useChatStore'
import { openWorkspacePaneChatWithDraft } from '../../utils/workspacePaneChat'
import { useCanPauseSchedules } from '../../hooks/useCanPauseSchedules'

const Calendar = lazy(() => import('./scheduleRuns/ScheduleCalendarView').then(module => ({ default: module.ScheduleCalendarView })))

/** The overview is a list, not a second dashboard. All mutations/history reuse the existing schedule panel. */
export default function SimpleSchedulesOverview({ active, entityType, initialSearch, onClose, onTriggers }: { active: boolean; entityType: 'workflow' | 'product'; initialSearch: string; onClose: () => void; onTriggers: () => void }) {
  const panel = useScheduleRunsData({ active, entityType, productProfileId: entityType === 'product' ? 'work' : undefined, initialView: 'schedules', onClose })
  const { setSearchQuery } = panel
  useEffect(() => { setSearchQuery(initialSearch) }, [initialSearch, setSearchQuery])
  const canPause = useCanPauseSchedules()
  const create = () => {
    const chat = useChatStore.getState(), tab = chat.activeTabId ? chat.chatTabs[chat.activeTabId] : undefined
    if (!tab || tab.metadata?.isViewOnly || tab.metadata?.agentProfileTabCanWrite === false) {
      chat.addToast('Open one of your agents, then choose New schedule.', 'info')
      window.dispatchEvent(new CustomEvent('open-quick-switcher', { detail: { query: '@crew ' } }))
      return
    }
    void openWorkspacePaneChatWithDraft({ tabId: tab.tabId, message: 'Help me create a schedule for this project. Ask me what it should do and when it should run.' }).catch(() => chat.addToast('Could not open the chat. Open your agent and ask it to create a schedule.', 'error'))
  }
  return <div className="flex h-full min-h-0 flex-col bg-background">
    <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border px-5 py-4">
      <div className="relative min-w-40 flex-1"><Search aria-hidden="true" className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" /><input type="search" aria-label="Search schedules" placeholder="Search schedules…" value={panel.searchQuery} onChange={event => panel.setSearchQuery(event.target.value)} className="h-9 w-full rounded-lg border border-border bg-transparent pl-9 pr-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring" /></div>
      {!panel.isReadOnlyUser && <button type="button" onClick={create} className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-foreground px-3 text-xs font-medium text-background"><Plus aria-hidden="true" className="h-3.5 w-3.5" />New schedule</button>}
      <button type="button" title="Refresh schedules" aria-label="Refresh schedules" disabled={panel.isLoading} onClick={() => void panel.loadJobs(true)} className="grid h-9 w-9 place-items-center rounded-lg border border-border text-muted-foreground hover:bg-muted"><RefreshCw aria-hidden="true" className={`h-3.5 w-3.5 ${panel.isLoading ? 'animate-spin' : ''}`} /></button>
      <details className="relative"><summary className="flex h-9 cursor-pointer list-none items-center rounded-lg border border-border px-3 text-xs text-muted-foreground">More</summary>
        <div className="absolute right-0 top-full z-20 mt-1 w-56 space-y-1 rounded-xl border border-border bg-popover p-2 shadow-xl" onClick={event => { if ((event.target as Element).closest('button')) event.currentTarget.closest('details')?.removeAttribute('open') }}>
          <button type="button" onClick={() => panel.setActiveView('schedules')} className="block w-full rounded px-2 py-1.5 text-left text-xs hover:bg-muted">List</button>
          <button type="button" onClick={() => panel.setActiveView('calendar')} className="block w-full rounded px-2 py-1.5 text-left text-xs hover:bg-muted">Calendar</button>
          <button type="button" onClick={onTriggers} className="block w-full rounded px-2 py-1.5 text-left text-xs hover:bg-muted">Triggers</button>
          {panel.workflowOptions.length > 1 && <label className="block px-2 py-1 text-xs text-muted-foreground">{entityType === 'product' ? 'Crew' : 'Project'}<select aria-label="Filter schedules by project" value={panel.selectedWorkflowFilter} onChange={event => panel.setSelectedWorkflowFilter(event.target.value)} className="mt-1 w-full rounded border border-border bg-popover p-1"><option value="all">All projects</option>{panel.workflowOptions.map(item => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label>}
          <label className="block px-2 py-1 text-xs text-muted-foreground">State<select aria-label="Filter schedules by state" value={panel.activeFilter} onChange={event => panel.setActiveFilter(event.target.value as typeof panel.activeFilter)} className="mt-1 w-full rounded border border-border bg-popover p-1">{panel.filterPills.map(item => <option key={item.key} value={item.key}>{item.label} ({item.count})</option>)}</select></label>
          {canPause && !panel.isReadOnlyUser && <button type="button" disabled={panel.isUpdatingSchedulerPause} onClick={() => void panel.handleToggleGlobalPause()} className="block w-full rounded px-2 py-1.5 text-left text-xs hover:bg-muted">{panel.isSchedulerPaused ? 'Resume all schedules' : 'Pause all schedules'}</button>}
        </div>
      </details>
    </div>
    {panel.isSchedulerPaused && <p role="status" className="px-5 py-2 text-xs text-muted-foreground">All schedules are paused.</p>}
    {(panel.summary.missed > 0 || panel.summary.issues > 0) && <div className="flex gap-3 px-5 py-2 text-xs text-destructive">{panel.summary.missed > 0 && <button onClick={() => panel.setActiveFilter('missed')}>{panel.summary.missed} missed</button>}{panel.summary.issues > 0 && <button onClick={() => panel.setActiveFilter('issues')}>{panel.summary.issues} need attention</button>}</div>}
    <div className="min-h-0 flex-1 overflow-y-auto px-3 py-2">
      {panel.error ? <div role="alert" className="p-5 text-sm text-muted-foreground">{panel.error} <button className="underline" onClick={() => void panel.loadJobs(true)}>Retry</button></div>
        : panel.isLoading && !panel.panelJobs.length ? <p role="status" className="p-5 text-sm text-muted-foreground">Loading schedules…</p>
          : !panel.panelJobs.length ? <div className="flex flex-col items-center gap-2 py-16 text-sm text-muted-foreground"><Clock aria-hidden="true" className="h-6 w-6" /><p>No schedules yet. Create one in chat.</p></div>
            : panel.activeView === 'calendar' ? <Suspense fallback={<p role="status" className="p-5 text-sm text-muted-foreground">Opening calendar…</p>}><Calendar panel={panel} /></Suspense>
              : panel.filteredJobs.length ? <ScheduleListView panel={panel} compact /> : <p className="p-5 text-sm text-muted-foreground">No matching schedules. <button className="underline" onClick={() => { panel.setSearchQuery(''); panel.setActiveFilter('all'); panel.setSelectedWorkflowFilter('all') }}>Clear filters</button></p>}
    </div>
  </div>
}

import { useEffect } from 'react'
import { AutomationOverviewToolbar } from './AutomationOverviewToolbar'
import { useScheduleRunsData } from './scheduleRuns/useScheduleRunsData'
import { ScheduleListView } from './scheduleRuns/ScheduleListView'
import { WorkspacePageContent } from '../workspace/WorkspacePageContent'

/** Browse schedules and their run history; management stays in the owning project. */
export default function SimpleSchedulesOverview({ active, entityType, initialSearch, onClose }: {
  active: boolean
  entityType: 'workflow' | 'product'
  initialSearch: string
  onClose: () => void
}) {
  const panel = useScheduleRunsData({ active, entityType, productProfileId: entityType === 'product' ? 'work' : undefined, initialView: 'schedules', canManage: false, onClose })
  const { setSearchQuery } = panel
  useEffect(() => { setSearchQuery(initialSearch) }, [initialSearch, setSearchQuery])

  return <WorkspacePageContent className="flex h-full min-h-0 w-full flex-col">
    <AutomationOverviewToolbar label="schedules" search={panel.searchQuery} onSearch={setSearchQuery}
      loading={panel.isLoading} onRefresh={() => void panel.loadJobs(true)} />
    {panel.isSchedulerPaused && <p role="status" className="mb-3 text-xs text-muted-foreground">All schedules are paused.</p>}
    <div className="min-h-0 flex-1 overflow-y-auto">
      {panel.error ? <p role="alert" className="rounded-lg border border-border bg-card p-4 text-sm">{panel.error} <button type="button" className="underline" onClick={() => void panel.loadJobs(true)}>Retry</button></p>
        : panel.isLoading && !panel.panelJobs.length ? <p role="status" className="rounded-lg border border-border bg-card p-4 text-sm text-muted-foreground">Loading schedules…</p>
          : !panel.panelJobs.length ? <p className="rounded-lg border border-border bg-card p-4 text-sm text-muted-foreground">No schedules yet. Ask an agent in a Crew or workflow to set one up.</p>
            : panel.filteredJobs.length ? <ScheduleListView panel={panel} overview /> : <p className="rounded-lg border border-border bg-card p-4 text-sm text-muted-foreground">No matching schedules. <button type="button" className="underline" onClick={() => setSearchQuery('')}>Clear search</button></p>}
    </div>
  </WorkspacePageContent>
}

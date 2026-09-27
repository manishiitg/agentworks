import React from 'react'
import { ChevronRight, Pause, Play } from 'lucide-react'
import { formatExactDateTime, formatLastRunLabel, formatLocalScheduleTime } from './helpers'
import type { ScheduleRunsPanelState } from './useScheduleRunsData'

type ScheduleGroupsViewProps = {
  entityLabel?: string
  panel: Pick<ScheduleRunsPanelState,
    | 'workflowGroups' | 'isSchedulerPaused' | 'isReadOnlyUser' | 'setActiveFilter' | 'setActiveView'
    | 'setSelectedWorkflowFilter' | 'handleToggleWorkflowGroupPause' | 'bulkUpdatingGroupKey'
  >
}

/** Global scheduling is a workflow-level view. Per-schedule detail belongs in All Schedules. */
export const ScheduleGroupsView: React.FC<ScheduleGroupsViewProps> = ({ panel, entityLabel = 'Automation' }) => {
  const {
    workflowGroups,
    isSchedulerPaused,
    isReadOnlyUser,
    setActiveFilter,
    setActiveView,
    setSelectedWorkflowFilter,
    handleToggleWorkflowGroupPause,
    bulkUpdatingGroupKey,
  } = panel

  const openWorkflowSchedules = (workflowKey: string, filter: 'all' | 'missed' | 'issues' = 'all') => {
    setSelectedWorkflowFilter(workflowKey)
    setActiveFilter(filter)
    setActiveView('schedules')
  }

  return (
    <div className="px-4 py-3 sm:px-6">
      <div className="overflow-x-auto rounded-lg border border-border">
        <table className="w-full min-w-[860px] text-left text-sm">
          <thead className="border-b border-border bg-muted/30 text-xs text-muted-foreground">
            <tr>
              <th className="px-4 py-2 font-medium">{entityLabel}</th>
              <th className="px-3 py-2 font-medium">Schedules</th>
              <th className="px-3 py-2 font-medium">Needs attention</th>
              <th className="px-3 py-2 font-medium">Next run (local)</th>
              <th className="px-3 py-2 font-medium">Last run</th>
              <th className="px-4 py-2 text-right font-medium">Actions</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {workflowGroups.map(group => {
              const fullyPaused = group.enabled === 0
              const partlyPaused = !fullyPaused && group.paused > 0
              const isRunning = group.running > 0
              const stateLabel = isRunning ? `${group.running} running` : fullyPaused ? 'All paused' : partlyPaused ? 'Partly paused' : 'All active'
              return (
                <tr key={group.key} className="transition-colors hover:bg-muted/20">
                  <td className="px-4 py-4">
                    <button type="button" onClick={() => openWorkflowSchedules(group.key)} className="block max-w-[320px] truncate text-left font-medium text-foreground hover:text-primary hover:underline" title={group.label}>
                      {group.label}
                    </button>
                    <div className="mt-0.5 text-xs text-muted-foreground">{group.jobs.length} schedule{group.jobs.length === 1 ? '' : 's'}</div>
                  </td>
                  <td className="whitespace-nowrap px-3 py-4">
                    <div className="text-xs text-foreground">{group.enabled} active · {group.paused} paused</div>
                    <div className="mt-0.5 text-xs text-muted-foreground">{stateLabel}</div>
                  </td>
                  <td className="px-3 py-4">
                    <div className="flex max-w-[270px] flex-wrap gap-1.5 text-xs">
                      {group.missed > 0 && <button type="button" onClick={() => openWorkflowSchedules(group.key, 'missed')} className="rounded bg-warning/15 px-1.5 py-0.5 text-warning hover:bg-warning/25">{group.missed} with missed runs</button>}
                      {group.issues > 0 && <button type="button" onClick={() => openWorkflowSchedules(group.key, 'issues')} className="rounded bg-destructive/15 px-1.5 py-0.5 text-destructive hover:bg-destructive/25">{group.issues} with run issues</button>}
                      {group.overlap > 0 && <span className="rounded bg-warning/10 px-1.5 py-0.5 text-warning" title="Next starts may overlap based on recent average durations">{group.overlap} at overlap risk</span>}
                      {group.missed === 0 && group.issues === 0 && group.overlap === 0 && <span className="text-muted-foreground">None</span>}
                    </div>
                  </td>
                  <td className="whitespace-nowrap px-3 py-4 text-xs text-foreground">
                    {fullyPaused || isSchedulerPaused ? <span className="text-muted-foreground">{isSchedulerPaused && !fullyPaused ? 'Paused globally' : '—'}</span> : formatLocalScheduleTime(group.nextRunAt)}
                  </td>
                  <td className="whitespace-nowrap px-3 py-4 text-xs text-muted-foreground" title={formatExactDateTime(group.lastRunAt)}>
                    {formatLastRunLabel(group.lastRunAt)}
                  </td>
                  <td className="px-4 py-4">
                    <div className="flex items-center justify-end gap-3">
                      {!isReadOnlyUser && <button
                        type="button"
                        onClick={() => handleToggleWorkflowGroupPause(group)}
                        disabled={bulkUpdatingGroupKey === group.key}
                        aria-label={`${fullyPaused ? 'Resume' : 'Pause'} ${group.label} schedules`}
                        title={fullyPaused ? 'Resume workflow' : 'Pause workflow'}
                        className="inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:opacity-50"
                      >
                        {fullyPaused ? <Play className="h-3.5 w-3.5" /> : <Pause className="h-3.5 w-3.5" />}
                        <span className="hidden lg:inline">{fullyPaused ? 'Resume' : 'Pause'}</span>
                      </button>}
                      <button type="button" onClick={() => openWorkflowSchedules(group.key)} aria-label={`View ${group.label} schedules`} className="inline-flex items-center gap-1 whitespace-nowrap text-xs font-medium text-primary hover:underline">
                        View <ChevronRight className="h-3.5 w-3.5" />
                      </button>
                    </div>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </div>
  )
}

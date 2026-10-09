import React from 'react'
import { Radio } from 'lucide-react'
import {
  formatLocalScheduleTime,
  formatMissedScheduleReason,
  formatOverdueDuration,
  formatTimeUntil,
  getLocalizedJobName,
  getMissedScheduleDelayMs,
  localizeTimezoneLabel,
  timeAgo,
} from './helpers'
import type { ScheduleRunsPanelState } from './useScheduleRunsData'
import { PauseCatchUpPanel } from './PauseCatchUpPanel'
import type { SchedulerPauseEvent } from '../../../services/api-types'

// "by alice from the browser" / "by alice via the API (an agent or script)".
function pausedByText(event?: SchedulerPauseEvent): string {
  if (!event || event.action !== 'paused') return ''
  const who = event.username || event.user_id || 'someone'
  const agent = (event.user_agent || '').toLowerCase()
  const fromBrowser = agent.includes('mozilla') || event.via === 'frontend-user'
  return fromBrowser ? `by ${who} from the browser` : `by ${who} via the API (an agent or script)`
}

type ScheduleOverviewViewProps = {
  panel: Pick<ScheduleRunsPanelState,
    | 'isSchedulerPaused' | 'isGloballyPaused' | 'pauseProductLabel' | 'schedulerConfig' | 'summary' | 'setActiveFilter' | 'setActiveView'
    | 'missedJobs' | 'presetMap' | 'showJobInWorkflowGroups' | 'upcomingJobs'
    | 'pauseCatchUp' | 'isRunningCatchUp' | 'runPauseCatchUp' | 'dismissPauseCatchUp' | 'isReadOnlyUser'
  >
}

export const ScheduleOverviewView: React.FC<ScheduleOverviewViewProps> = ({ panel }) => {
  const {
    isSchedulerPaused,
    isGloballyPaused,
    pauseProductLabel,
    schedulerConfig,
    summary,
    setActiveFilter,
    setActiveView,
    missedJobs,
    presetMap,
    showJobInWorkflowGroups,
    upcomingJobs,
    pauseCatchUp,
    isRunningCatchUp,
    runPauseCatchUp,
    dismissPauseCatchUp,
    isReadOnlyUser,
  } = panel
  const pausedBy = pausedByText(schedulerConfig?.recent_pause_events?.[0])

  return (
    <div className="px-5 py-4 space-y-4">

      {isSchedulerPaused && (
        <div className="rounded-xl border border-warning/30 bg-warning/10 px-4 py-3">
          <div className="flex items-center justify-between gap-3">
            <div>
              <div className="text-sm font-medium text-foreground">{isGloballyPaused ? 'All scheduled automation triggers are paused' : `${pauseProductLabel} scheduled triggers are paused`}</div>
              <div className="mt-1 text-xs text-muted-foreground">
                Existing manual runs still work. Cron-triggered executions will not start until you resume schedules.{isGloballyPaused ? '' : ' Other products keep running.'}
              </div>
            </div>
            {schedulerConfig?.paused_at && (
              <div className="text-right text-xs text-muted-foreground">
                <div className="whitespace-nowrap">Paused {timeAgo(schedulerConfig.paused_at)}</div>
                {pausedBy ? <div>{pausedBy}</div> : null}
              </div>
            )}
          </div>
        </div>
      )}

      <PauseCatchUpPanel
        items={pauseCatchUp}
        running={isRunningCatchUp}
        canRun={!isReadOnlyUser}
        onRun={ids => { void runPauseCatchUp(ids) }}
        onDismiss={dismissPauseCatchUp}
      />

      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        <button
          onClick={() => {
            setActiveFilter('running')
            setActiveView('by-workflow')
          }}
          className="text-left rounded-xl border border-border bg-background px-3 py-2 text-foreground shadow-sm hover:bg-muted transition-colors"
        >
          <div className="text-[11px] uppercase tracking-wide text-muted-foreground">Running schedules</div>
          <div className="mt-1 flex items-center gap-2">
            <Radio className="w-3.5 h-3.5 text-warning" />
            <span className="text-lg font-semibold text-foreground">{summary.running}</span>
          </div>
        </button>
        <button
          onClick={() => {
            setActiveFilter('enabled')
            setActiveView('by-workflow')
          }}
          className="text-left rounded-xl border border-border bg-background px-3 py-2 text-foreground shadow-sm hover:bg-muted transition-colors"
        >
          <div className="text-[11px] uppercase tracking-wide text-muted-foreground">Enabled schedules</div>
          <div className="mt-1 text-lg font-semibold text-foreground">{summary.enabled}</div>
        </button>
        <button
          onClick={() => {
            setActiveFilter('paused')
            setActiveView('by-workflow')
          }}
          className="text-left rounded-xl border border-border bg-background px-3 py-2 text-foreground shadow-sm hover:bg-muted transition-colors"
        >
          <div className="text-[11px] uppercase tracking-wide text-muted-foreground">Paused schedules</div>
          <div className="mt-1 text-lg font-semibold text-foreground">{summary.paused}</div>
        </button>
        <button
          onClick={() => {
            setActiveFilter('issues')
            setActiveView('by-workflow')
          }}
          className="text-left rounded-xl border border-border bg-background px-3 py-2 text-foreground shadow-sm hover:bg-muted transition-colors"
        >
          <div className="text-[11px] uppercase tracking-wide text-muted-foreground">Schedule issues</div>
          <div className="mt-1 text-lg font-semibold text-foreground">{summary.issues}</div>
        </button>
      </div>

      {missedJobs.length > 0 && (
        <div className="rounded-xl border border-warning/30 bg-warning/10 px-3 py-3">
          <div className="mb-2">
            <div>
              <div className="text-[11px] uppercase tracking-wide text-warning">Missed schedules</div>
              <div className="text-sm font-medium text-foreground">Schedules that were due but have not run yet</div>
            </div>
          </div>

          <div className="grid gap-2 md:grid-cols-2">
            {missedJobs.map((job) => {
              const preset = presetMap.get(job.preset_query_id ?? '')
              const label = localizeTimezoneLabel(
                preset?.label || job.workflow_label || job.name,
                job.next_run_at
              )
              const overdueMs = getMissedScheduleDelayMs(job) ?? 0
              const missedReason = formatMissedScheduleReason(job)

              return (
                <button
                  key={`missed-${job.id}`}
                  onClick={() => showJobInWorkflowGroups(job)}
                  className="rounded-lg border border-warning/30 bg-card px-3 py-2 text-left hover:bg-muted transition-colors"
                >
                  <div className="flex items-center justify-between gap-3">
                    <span className="truncate text-sm font-medium text-foreground" title={label}>
                      {label}
                    </span>
                    <span className="text-xs font-medium text-warning whitespace-nowrap">
                      {job.missed_run_count && job.missed_run_count > 1
                        ? `${job.missed_run_count} missed`
                        : `Missed by ${formatOverdueDuration(overdueMs)}`}
                    </span>
                  </div>
                  <div className="mt-1 flex items-center justify-between gap-3 text-xs text-muted-foreground">
                    <span className="truncate" title={job.name}>{getLocalizedJobName(job)}</span>
                    <span className="whitespace-nowrap">{formatLocalScheduleTime(job.latest_missed_run_at || job.next_run_at)}</span>
                  </div>
                  <div className="mt-1 truncate text-xs text-warning" title={missedReason}>
                    {missedReason}
                  </div>
                </button>
              )
            })}
          </div>
        </div>
      )}

      <div className="rounded-xl border border-border bg-background px-3 py-3">
        <div className="flex items-center justify-between gap-3 mb-2">
          <div>
            <div className="text-[11px] uppercase tracking-wide text-muted-foreground">Next scheduled</div>
            <div className="text-sm font-medium text-foreground">Which schedules will run soonest</div>
          </div>
          <div className="text-xs text-muted-foreground whitespace-nowrap">
            {upcomingJobs.length} upcoming
          </div>
        </div>

        {upcomingJobs.length === 0 ? (
          <div className="text-sm text-muted-foreground">No upcoming enabled schedules.</div>
        ) : (
          <div className="grid gap-2 md:grid-cols-2">
            {upcomingJobs.map((job) => {
              const preset = presetMap.get(job.preset_query_id ?? '')
              const label = localizeTimezoneLabel(
                preset?.label || job.workflow_label || job.name,
                job.next_run_at
              )
              const localizedJobName = getLocalizedJobName(job)
              return (
                <button
                  key={`upcoming-${job.id}`}
                  onClick={() => showJobInWorkflowGroups(job)}
                  className="rounded-lg border border-border bg-card px-3 py-2 text-left hover:bg-muted transition-colors"
                >
                  <div className="flex items-center justify-between gap-3">
                    <span className="truncate text-sm font-medium text-foreground" title={label}>
                      {label}
                    </span>
                    <span className="text-xs font-medium text-warning whitespace-nowrap">
                      {formatTimeUntil(job.next_run_at)}
                    </span>
                  </div>
                  <div className="mt-1 flex items-center justify-between gap-3 text-xs text-muted-foreground">
                    <span className="truncate" title={job.name}>{localizedJobName}</span>
                    <span className="whitespace-nowrap">{formatLocalScheduleTime(job.next_run_at)}</span>
                  </div>
                </button>
              )
            })}
          </div>
        )}
      </div>
    </div>
  )
}

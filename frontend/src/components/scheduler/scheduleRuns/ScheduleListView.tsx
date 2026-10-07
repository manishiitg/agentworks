import React from 'react'
import {
  Clock, CheckCircle, XCircle, Minus, Loader,
  AlertTriangle, Square
} from 'lucide-react'
import { WebhookEndpoint } from './WebhookEndpoint'
import { describeCron } from './cron'
import {
  formatDuration,
  formatExactDateTime,
  formatLastRunLabel,
  formatLocalScheduleTime,
  formatMissedScheduleReason,
  formatOverdueDuration,
  getLocalizedJobName,
  getMissedScheduleDelayMs,
  getScheduleDependencyIds,
  getScheduleExecutionScope,
  isMissedSchedule,
  isScheduleIssueStatus,
  isSchedulePartialStatus,
  isScheduleWaitingStatus,
  timeAgo,
} from './helpers'
import type { ScheduleRunsPanelState } from './useScheduleRunsData'
import { ScheduleRowActions } from './ScheduleRowActions'
import { ScheduleExecutionHistoryList } from '../../ScheduleExecutionHistoryList'
import { ScheduleAfterRunControls } from './ScheduleAfterRunControls'

type ScheduleListViewProps = {
  panel: Pick<ScheduleRunsPanelState,
    | 'filteredJobs' | 'presetMap' | 'showWorkflowIdentityInScheduleRows' | 'isReadOnlyUser'
    | 'handleStopRun' | 'handleTrigger' | 'triggering' | 'handleToggle'
    | 'handleRunDestination' | 'handleAfterRun'
    | 'openActionMenuJobId' | 'setOpenActionMenuJobId' | 'handleDelete'
    | 'expandedRunHistoryJobIds' | 'runsByJob' | 'runsLoadingJobIds' | 'deletingRunSessionIds'
    | 'toggleRunHistory' | 'openScheduledRun' | 'deleteScheduledRunSession'
  >
}

export const ScheduleListView: React.FC<ScheduleListViewProps> = ({ panel }) => {
  const {
    filteredJobs,
    presetMap,
    showWorkflowIdentityInScheduleRows,
    isReadOnlyUser,
    handleStopRun,
    handleTrigger,
    triggering,
    handleToggle,
    handleRunDestination,
    handleAfterRun,
    openActionMenuJobId,
    setOpenActionMenuJobId,
    handleDelete,
    expandedRunHistoryJobIds,
    runsByJob,
    runsLoadingJobIds,
    deletingRunSessionIds,
    toggleRunHistory,
    openScheduledRun,
    deleteScheduledRunSession,
  } = panel

  const missedCount = filteredJobs.filter(job => isMissedSchedule(job)).length
  const runningCount = filteredJobs.filter(job => job.last_status === 'running').length
  const scheduledCount = filteredJobs.length - missedCount - runningCount

  return (
    <div className="divide-y divide-border">
      {filteredJobs.map((job, index, jobsList) => {
        const preset = presetMap.get(job.preset_query_id ?? '')
        const cronDesc = job.kind === 'gmail' ? 'Gmail trigger · on email' : job.schedule_type === 'webhook' ? 'API trigger · on request' : describeCron(job.cron_expression)
        const localizedJobName = getLocalizedJobName(job)
        const workflowDisplayLabel = preset?.label || job.workflow_label || job.name
        const executionScope = getScheduleExecutionScope(job)
        const dependencyNames = getScheduleDependencyIds(job).map(id => jobsList.find(candidate => candidate.id === id)?.name ?? id)
        const previousJob = index > 0 ? jobsList[index - 1] : null
        const isRunningJob = job.last_status === 'running'
        const isWaitingJob = isScheduleWaitingStatus(job.last_status)
        const isMissedJob = isMissedSchedule(job)
        const missedDelayMs = getMissedScheduleDelayMs(job)
        const missedReason = isMissedJob ? formatMissedScheduleReason(job) : ''
        const showRunningHeader = isRunningJob && (!previousJob || previousJob.last_status !== 'running')
        const previousJobWasMissed = previousJob ? isMissedSchedule(previousJob) : false
        const showMissedHeader = isMissedJob && (!previousJob || previousJob.last_status === 'running' || !previousJobWasMissed)
        const showScheduledHeader = !isRunningJob && !isMissedJob && (!previousJob || previousJob.last_status === 'running' || previousJobWasMissed)

        return (
          <React.Fragment key={job.id}>
            {showRunningHeader && (
              <div className="px-5 py-3 bg-warning/5 border-b border-warning/10">
                <div className="text-sm font-semibold text-warning">Running schedules <span className="font-normal text-muted-foreground">· {runningCount}</span></div>
              </div>
            )}

            {showScheduledHeader && (
              <div className="px-5 py-3 bg-muted/30 border-b border-border">
                <div className="text-sm font-semibold text-foreground">Automation schedules <span className="font-normal text-muted-foreground">· {scheduledCount}</span></div>
              </div>
            )}

            {showMissedHeader && (
              <div className="px-5 py-3 bg-warning/5 border-b border-warning/10">
                <div className="text-sm font-semibold text-warning">Missed schedules <span className="font-normal text-muted-foreground">· {missedCount}</span></div>
              </div>
            )}

            <div className={`px-5 py-4 ${!job.enabled ? 'opacity-60' : ''}`}>
            {/* Row top */}
            <div className="relative flex items-start gap-3">
              {/* Status dot */}
              <div className={`mt-1 w-2 h-2 rounded-full flex-shrink-0 ${
                job.last_status === 'running' ? 'bg-warning animate-pulse' :
                isWaitingJob ? 'bg-info animate-pulse' :
                isMissedJob ? 'bg-warning' :
                job.enabled ? 'bg-success' : 'bg-muted-foreground/50'
              }`} />

              {/* Main content */}
              <div className="flex-1 min-w-0">
                {showWorkflowIdentityInScheduleRows && (
                  <div className="min-w-0 pr-28">
                    <div className="text-[11px] uppercase tracking-wide text-muted-foreground">
                      Automation
                    </div>
                    <div className="mt-0.5 flex items-center gap-2 flex-wrap">
                      <span className="text-sm font-semibold text-foreground truncate" title={workflowDisplayLabel}>
                        {workflowDisplayLabel}
                      </span>
                    </div>
                  </div>
                )}

                <div className={`${showWorkflowIdentityInScheduleRows ? 'mt-1' : ''} flex items-center gap-2 flex-wrap pr-28`}>
                  {showWorkflowIdentityInScheduleRows && (
                    <span className="text-[11px] uppercase tracking-wide text-muted-foreground">
                      {job.kind === 'gmail' ? 'Gmail' : job.schedule_type === 'webhook' ? 'Webhook' : 'Schedule'}
                    </span>
                  )}
                  <span className={`${showWorkflowIdentityInScheduleRows ? 'text-xs font-medium' : 'text-sm font-semibold'} text-foreground truncate`} title={job.name}>
                    {localizedJobName}
                  </span>
                  {isMissedJob && (
                    <span className="text-xs px-1.5 py-0.5 rounded-full bg-warning/15 text-warning">
                      Missed
                    </span>
                  )}
                  {isWaitingJob && (
                    <span className="text-xs px-1.5 py-0.5 rounded-full bg-info/15 text-info">
                      {job.last_status === 'waiting_for_capacity'
                        ? 'Waiting for capacity'
                        : `Queued${job.queued_occurrences && job.queued_occurrences > 1 ? ` · ${job.queued_occurrences} combined` : ''}`}
                    </span>
                  )}
                  {!job.enabled && (
                    <span className="text-xs px-1.5 py-0.5 rounded-full bg-muted text-muted-foreground">
                      Paused
                    </span>
                  )}
                  {executionScope && (
                    <span className="text-xs px-1.5 py-0.5 rounded-full bg-secondary text-secondary-foreground font-medium" title={executionScope.title}>
                      {executionScope.label}
                    </span>
                  )}
                </div>

                {/* Cron + groups */}
                <div className="mt-1 flex flex-wrap gap-x-3 gap-y-1 pr-28 text-xs text-muted-foreground">
                  <span className="flex items-center gap-1">
                    <Clock className="w-3 h-3" />
                    {cronDesc}
                  </span>
                  {job.group_names && job.group_names.length > 1 && (
                    <span title={job.group_names.join(', ')}>Groups: {job.group_names.join(', ')}</span>
                  )}
                  {job.entity_type === 'product' && (
                    <label className="inline-flex items-center gap-1.5">
                      <span>Runs in</span>
                      <select
                        aria-label={`Run destination for ${job.name}`}
                        value={job.run_destination || 'crew_chat'}
                        disabled={isReadOnlyUser || job.last_status === 'running'}
                        onChange={event => void handleRunDestination(job, event.target.value as 'crew_chat' | 'isolated')}
                        className="rounded border border-border bg-background px-1.5 py-0.5 text-xs text-foreground disabled:opacity-50"
                      >
                        <option value="crew_chat">Main chat</option>
                        <option value="isolated">Own conversation</option>
                      </select>
                    </label>
                  )}
                  <ScheduleAfterRunControls job={job} disabled={isReadOnlyUser} onChange={(target, next) => void handleAfterRun(target, next)} />
                </div>
                {dependencyNames.length > 0 && (
                  <div className="mt-1 flex flex-wrap gap-x-3 gap-y-0.5 pr-28 text-xs text-info">
                    {dependencyNames.length > 0 && <span>Waits for: {dependencyNames.join(', ')}</span>}
                  </div>
                )}
                {job.mode === 'workshop' && job.messages && job.messages.length > 0 && (
                  <div className="mt-1 pr-28">
                    <div className="space-y-0.5">
                      {job.messages.map((m, i) => (
                        <div key={i} className="flex items-start gap-1 text-xs text-muted-foreground">
                          <span className="shrink-0 text-muted-foreground">{i + 1}.</span>
                          <span>{m}</span>
                        </div>
                      ))}
                    </div>
                  </div>
                )}

                {job.schedule_type === 'webhook' && <WebhookEndpoint id={job.id} name={job.name} address={job.gmail?.address} />}

                {/* Run stats */}
                <div className="mt-1.5 flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
                  <span className="flex items-center gap-1" title={formatExactDateTime(job.last_run_at)}>
                    {job.last_status === 'running' ? (
                      <Loader className="w-3 h-3 text-warning animate-spin" />
                    ) : isWaitingJob ? (
                      <Clock className="w-3 h-3 text-info" />
                    ) : job.last_status === 'success' ? (
                      <CheckCircle className="w-3 h-3 text-success" />
                    ) : job.last_status === 'error' ? (
                      <XCircle className="w-3 h-3 text-destructive" />
                    ) : isSchedulePartialStatus(job.last_status) ? (
                      <AlertTriangle className="w-3 h-3 text-warning" />
                    ) : job.last_status === 'stopped' ? (
                      <Square className="w-3 h-3 text-muted-foreground" />
                    ) : (
                      <Minus className="w-3 h-3" />
                    )}
                    {isWaitingJob
                      ? job.waiting_until
                        ? `Waiting until ${formatLocalScheduleTime(job.waiting_until)}`
                        : 'Waiting to start'
                      : job.last_status === 'running'
                      ? job.last_run_at
                        ? `Running since ${formatLocalScheduleTime(job.last_run_at)} (${timeAgo(job.last_run_at)})`
                        : 'Running...'
                      : job.last_run_at
                      ? `Last ran: ${formatLastRunLabel(job.last_run_at)}`
                      : 'Not run yet'}
                  </span>
                  <span>
                    {job.enabled
                      ? isMissedJob && missedDelayMs != null
                        ? job.missed_run_count && job.missed_run_count > 1
                          ? `${job.missed_run_count} missed · latest ${formatOverdueDuration(missedDelayMs)} ago`
                          : `Missed by ${formatOverdueDuration(missedDelayMs)}`
                        : `Next: ${formatLocalScheduleTime(job.next_run_at)}`
                      : 'paused'}
                  </span>
                  {isMissedJob && (
                    <span className="text-warning" title={missedReason}>
                      {missedReason}
                    </span>
                  )}
                  <span>{job.run_count} run{job.run_count !== 1 ? 's' : ''}</span>
                  {job.last_duration_ms != null && job.last_status !== 'running' && (
                    <span title="Last duration">{formatDuration(job.last_duration_ms)}</span>
                  )}
                </div>

                {/* Error message */}
                {(isScheduleIssueStatus(job.last_status) || job.last_status === 'stopped') && job.last_error && (
                  <div className={`mt-1 text-xs truncate max-w-lg ${job.last_status === 'error' ? 'text-destructive' : isSchedulePartialStatus(job.last_status) ? 'text-warning' : 'text-muted-foreground'}`} title={job.last_error}>
                    ✗ {job.last_error}
                  </div>
                )}
                {isWaitingJob && job.waiting_reason && (
                  <div className="mt-1 text-xs truncate max-w-lg text-info" title={job.waiting_reason}>
                    {job.waiting_reason}
                  </div>
                )}
              </div>

              {/* Keep the immediate operational action visible; secondary actions live in one menu. */}
              <div className="absolute right-0 top-0 flex items-center gap-1">
                <ScheduleRowActions
                  job={job}
                  isRunning={job.last_status === 'running'}
                  isMissedJob={isMissedJob}
                  isReadOnlyUser={isReadOnlyUser}
                  triggering={triggering}
                  openActionMenuJobId={openActionMenuJobId}
                  setOpenActionMenuJobId={setOpenActionMenuJobId}
                  handleStopRun={handleStopRun}
                  handleTrigger={handleTrigger}
                  handleToggle={handleToggle}
                  handleDelete={handleDelete}
                  menuButtonClassName="rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                />
              </div>
            </div>
            {(job.run_count || 0) > 0 && <ScheduleExecutionHistoryList
                job={job}
                runs={runsByJob[job.id] || []}
                historyOpen={expandedRunHistoryJobIds.has(job.id)}
                historyLoading={runsLoadingJobIds.has(job.id)}
                recordedRunCount={job.run_count || 0}
                onToggle={() => { void toggleRunHistory(job) }}
                onOpen={run => { void openScheduledRun(run, job) }}
                onDelete={job.entity_type === 'product' ? undefined : run => { void deleteScheduledRunSession(run) }}
                deletingRunIds={deletingRunSessionIds}
            />}
            </div>
          </React.Fragment>
        )
      })}
    </div>
  )
}

import React from 'react'
import { CalendarClock } from 'lucide-react'
import { IconTile, StatusPill } from '../../automation/AutomationKit'
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
  isScheduleWaitingStatus,
  timeAgo,
} from './helpers'
import type { ScheduleRunsPanelState } from './useScheduleRunsData'
import { ScheduleRowActions } from './ScheduleRowActions'
import { ScheduleExecutionHistoryList } from '../../ScheduleExecutionHistoryList'

type ScheduleListViewProps = {
  panel: Pick<ScheduleRunsPanelState,
    | 'filteredJobs' | 'presetMap' | 'showWorkflowIdentityInScheduleRows' | 'isReadOnlyUser'
    | 'handleStopRun' | 'handleTrigger' | 'triggering' | 'handleToggle'
    | 'handleRunDestination'
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

  return (
    <div className="space-y-3 p-4" data-testid="schedule-list">
      {filteredJobs.map((job, _index, jobsList) => {
        const preset = presetMap.get(job.preset_query_id ?? '')
        const cronDesc = job.schedule_type === 'webhook' ? 'API trigger · on request' : describeCron(job.cron_expression)
        const localizedJobName = getLocalizedJobName(job)
        const workflowDisplayLabel = preset?.label || job.workflow_label || job.name
        const executionScope = getScheduleExecutionScope(job)
        const dependencyNames = getScheduleDependencyIds(job).map(id => jobsList.find(candidate => candidate.id === id)?.name ?? id)
        const isRunningJob = job.last_status === 'running'
        const isWaitingJob = isScheduleWaitingStatus(job.last_status)
        // The server writes the status word and the last-run sentence; while the product's schedules are paused every row says Paused.
        const statusWord = job.status_word ?? ''
        const isMissedJob = statusWord === 'Missed'
        const missedDelayMs = getMissedScheduleDelayMs(job)
        const quiet = statusWord === 'Paused' || !job.enabled

        return (
          <section key={job.id} className={`min-w-0 space-y-2 rounded-xl border border-border p-3 ${quiet ? 'opacity-70' : ''}`}>
            <div className="flex items-center justify-between gap-2">
              <IconTile icon={CalendarClock} tone="violet" />
              <div className="min-w-0 flex-1">
                {showWorkflowIdentityInScheduleRows && (
                  <p className="truncate text-xs text-muted-foreground" title={workflowDisplayLabel}>
                    {workflowDisplayLabel} · {job.schedule_type === 'webhook' ? 'Webhook' : 'Schedule'}
                  </p>
                )}
                <h3 className="truncate text-sm font-medium" title={job.name}>{localizedJobName}</h3>
              </div>
              <div className="flex shrink-0 items-center gap-2">
                {statusWord && <StatusPill status={statusWord === 'Paused' || !job.enabled ? 'paused' : /fail|missed|error/i.test(statusWord) ? 'failing' : isRunningJob ? 'on' : 'on'} label={statusWord} />}
                <ScheduleRowActions
                  job={job}
                  isRunning={isRunningJob}
                  isMissedJob={isMissedJob}
                  isReadOnlyUser={isReadOnlyUser}
                  triggering={triggering}
                  openActionMenuJobId={openActionMenuJobId}
                  setOpenActionMenuJobId={setOpenActionMenuJobId}
                  handleStopRun={handleStopRun}
                  handleTrigger={handleTrigger}
                  handleToggle={handleToggle}
                  handleDelete={handleDelete}
                  menuButtonClassName="rounded-md px-1.5 py-1 text-xs text-muted-foreground hover:bg-muted hover:text-foreground"
                />
              </div>
            </div>

            <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
              <span>{cronDesc}</span>
              {executionScope && <span title={executionScope.title}>{executionScope.label}</span>}
              {job.group_names && job.group_names.length > 1 && (
                <span title={job.group_names.join(', ')}>Groups: {job.group_names.join(', ')}</span>
              )}
              {(job.entity_type === 'product' || Boolean(job.run_destinations?.length)) && (
                <label className="inline-flex items-center gap-1.5">
                  <span>Runs in</span>
                  <select
                    aria-label={`Run destination for ${job.name}`}
                    value={job.run_destination === 'isolated' || job.run_destination === 'new' ? job.run_destination : job.chat_id || 'main'}
                    disabled={isReadOnlyUser || isRunningJob}
                    onChange={event => void handleRunDestination(job, event.target.value)}
                    className="rounded-md border border-border bg-background px-1.5 py-0.5 text-xs text-foreground disabled:opacity-50"
                  >
                    {(job.run_destinations?.length ? job.run_destinations : [{ id: 'main', label: 'Main chat' }, { id: 'isolated', label: 'Own conversation' }]).map(item => (
                      <option key={item.id} value={item.id}>{item.label}</option>
                    ))}
                  </select>
                </label>
              )}
            </div>

            {dependencyNames.length > 0 && <p className="text-xs text-muted-foreground">Waits for: {dependencyNames.join(', ')}</p>}
            {job.mode === 'workshop' && job.messages && job.messages.length > 0 && (
              <ol className="space-y-0.5 text-xs text-muted-foreground">
                {job.messages.map((m, i) => <li key={i} className="flex items-start gap-1"><span className="shrink-0">{i + 1}.</span><span>{m}</span></li>)}
              </ol>
            )}

            {job.schedule_type === 'webhook' && <WebhookEndpoint id={job.id} name={job.name} />}

            <div className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
              <span title={formatExactDateTime(job.last_run_at)}>
                {isWaitingJob
                  ? job.waiting_until ? `Waiting until ${formatLocalScheduleTime(job.waiting_until)}` : 'Waiting to start'
                  : isRunningJob
                    ? job.last_run_at ? `Running since ${formatLocalScheduleTime(job.last_run_at)} (${timeAgo(job.last_run_at)})` : 'Running...'
                    : job.last_run_at ? `Last ran: ${formatLastRunLabel(job.last_run_at)}` : 'Not run yet'}
              </span>
              {!quiet && (
                isMissedJob && missedDelayMs != null
                  ? <span title={formatMissedScheduleReason(job)}>
                      {job.missed_run_count && job.missed_run_count > 1
                        ? `${job.missed_run_count} missed · latest ${formatOverdueDuration(missedDelayMs)} ago`
                        : `Missed by ${formatOverdueDuration(missedDelayMs)}`}
                    </span>
                  : <span>Next: {formatLocalScheduleTime(job.next_run_at)}</span>
              )}
              <span>{job.run_count} run{job.run_count !== 1 ? 's' : ''}</span>
              {job.last_duration_ms != null && !isRunningJob && <span title="Last duration">{formatDuration(job.last_duration_ms)}</span>}
            </div>

            {job.last_result && (
              <p className={`text-xs ${job.last_status === 'error' ? 'text-destructive' : 'text-muted-foreground'}`} title={job.last_error || undefined}>{job.last_result}</p>
            )}

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
          </section>
        )
      })}
    </div>
  )
}

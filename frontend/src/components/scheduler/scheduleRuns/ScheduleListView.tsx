import React, { useState } from 'react'
import { DetailLine, PageRow, RowButton } from '../../automation/AutomationKit'
import { Switch } from '../../ui/Switch'
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
import { ScheduleExecutionHistoryList } from '../../ScheduleExecutionHistoryList'

type ScheduleListViewProps = {
  panel: Pick<ScheduleRunsPanelState,
    | 'filteredJobs' | 'presetMap' | 'showWorkflowIdentityInScheduleRows' | 'isReadOnlyUser'
    | 'handleStopRun' | 'handleTrigger' | 'triggering' | 'handleToggle'
    | 'handleRunDestination' | 'handleDelete'
    | 'expandedRunHistoryJobIds' | 'runsByJob' | 'runsLoadingJobIds' | 'deletingRunSessionIds'
    | 'toggleRunHistory' | 'openScheduledRun' | 'deleteScheduledRunSession'
  >
}

/** "Mon 9 Nov" style short date for the "next" part of a row's first line. */
function nextLabel(value?: string): string {
  if (!value) return ''
  const at = new Date(value)
  if (Number.isNaN(at.getTime())) return ''
  return at.toLocaleDateString(undefined, { weekday: 'short', day: 'numeric', month: 'short' })
}

/*
 * Schedule rows in the owner-approved mock's look (8 Nov): a switch, the schedule in plain words, up to two detail
 * lines, one action by state and a chevron that opens today's details in place. Rows only: the host supplies the card.
 */
export const ScheduleListView: React.FC<ScheduleListViewProps> = ({ panel }) => {
  const {
    filteredJobs, presetMap, showWorkflowIdentityInScheduleRows, isReadOnlyUser, handleStopRun, handleTrigger, triggering,
    handleToggle, handleRunDestination, handleDelete, expandedRunHistoryJobIds, runsByJob, runsLoadingJobIds,
    deletingRunSessionIds, toggleRunHistory, openScheduledRun, deleteScheduledRunSession,
  } = panel
  const [openId, setOpenId] = useState<string | null>(null)

  return (
    <div className="divide-y divide-border" data-testid="schedule-list">
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
        const open = openId === job.id
        const isWebhook = job.schedule_type === 'webhook'

        // First line: what it runs and when it runs next.
        const routes = Object.values(job.route_selections ?? {}).filter(Boolean)
        const what = job.query ? `Asks: "${job.query}"`
          : routes.length === 1 ? `Runs the ${routes[0]} route`
          : executionScope ? `Runs: ${executionScope.label}` : localizedJobName
        const where = job.run_destination === 'new' ? 'new chat each time' : job.run_destination === 'isolated' ? 'own conversation' : ''
        const next = !quiet && !isMissedJob && !isRunningJob && !isWebhook ? nextLabel(job.next_run_at) : ''
        const firstLine = [showWorkflowIdentityInScheduleRows ? workflowDisplayLabel : '', what, next ? `next ${next}` : '', where].filter(Boolean).join(' · ')

        // Second line (and a catch-up line when missed): the state.
        const runs = `${job.run_count} run${job.run_count !== 1 ? 's' : ''}`
        let lines: React.ReactNode[]
        if (isMissedJob && missedDelayMs != null) {
          const late = job.missed_run_count && job.missed_run_count > 1
            ? `${job.missed_run_count} missed · latest ${formatOverdueDuration(missedDelayMs)} ago`
            : `by ${formatOverdueDuration(missedDelayMs)}`
          lines = [
            <span key="m" className="text-destructive" title={formatMissedScheduleReason(job)}>Missed {late}</span>,
            'Catch-up: runs once now for the missed time',
          ]
        } else if (isWaitingJob) {
          lines = [firstLine, <span key="w" title={formatExactDateTime(job.waiting_since)}>{job.waiting_until ? `Waiting until ${formatLocalScheduleTime(job.waiting_until)}` : 'Waiting: another run is still going'}</span>]
        } else if (isRunningJob) {
          lines = [firstLine, <span key="r" className="text-primary" title={formatExactDateTime(job.last_run_at)}>{job.last_run_at ? `Running now · since ${formatLocalScheduleTime(job.last_run_at)} (${timeAgo(job.last_run_at)})` : 'Running now'}</span>]
        } else {
          const last = job.last_result
            ?? `${job.last_run_at ? `Last ran: ${formatLastRunLabel(job.last_run_at)}` : 'Not run yet'}${job.last_duration_ms != null ? ` · ${formatDuration(job.last_duration_ms)}` : ''}`
          lines = [firstLine, <span key="h" className={job.last_status === 'error' ? 'text-destructive' : undefined} title={job.last_error || formatExactDateTime(job.last_run_at)}>{last} · {runs}</span>]
        }

        // One action on the right, chosen by state.
        let action: React.ReactNode = null
        if (!isReadOnlyUser && job.enabled) {
          if (isRunningJob) action = <RowButton onClick={() => void handleStopRun(job)}>Stop</RowButton>
          else if (!isWebhook && !isWaitingJob) {
            action = <RowButton onClick={() => void handleTrigger(job)} disabled={triggering === job.id}
              title={isMissedJob ? 'Start one run now. Missed times are not replayed individually.' : 'Start this schedule now'}>{isMissedJob ? 'Catch up' : 'Run now'}</RowButton>
          }
        }

        return (
          <PageRow
            key={job.id}
            testId={`schedule-row-${job.id}`}
            lead={<Switch checked={job.enabled} disabled={isReadOnlyUser} aria-label={`${job.enabled ? 'Pause' : 'Resume'} ${job.name}`} onCheckedChange={() => void handleToggle(job)} />}
            title={<span title={job.name} className={quiet ? 'opacity-70' : ''}>{cronDesc}</span>}
            lines={lines}
            aside={quiet ? 'Paused' : ['Needs attention', 'Queued', 'Waiting for capacity'].includes(statusWord) ? statusWord : undefined}
            action={action}
            open={open}
            onToggle={() => setOpenId(open ? null : job.id)}
          >
            <DetailLine label="Name">{localizedJobName}</DetailLine>
            {executionScope && <DetailLine label="Runs"><span title={executionScope.title}>{executionScope.label}</span></DetailLine>}
            {job.input_preset && <DetailLine label="Inputs"><span title={job.input_preset}>Saved inputs: {job.input_preset}</span></DetailLine>}
            {(job.entity_type === 'product' || Boolean(job.run_destinations?.length)) && (
              <DetailLine label="Where">
                <label className="inline-flex items-center gap-1.5">
                  <span>Runs in</span>
                  <select
                    aria-label={`Run destination for ${job.name}`}
                    value={job.run_destination === 'isolated' || job.run_destination === 'new' ? job.run_destination : job.chat_id || 'chat'}
                    disabled={isReadOnlyUser || isRunningJob}
                    onChange={event => void handleRunDestination(job, event.target.value)}
                    className="rounded-md border border-border bg-background px-1.5 py-0.5 text-xs text-foreground disabled:opacity-50"
                  >
                    {(job.run_destinations?.length ? job.run_destinations : [{ id: 'chat', label: 'Main chat' }, { id: 'isolated', label: 'Own conversation' }]).map(item => (
                      <option key={item.id} value={item.id}>{item.label}</option>
                    ))}
                  </select>
                </label>
              </DetailLine>
            )}
            {dependencyNames.length > 0 && <DetailLine label="Waits for">{dependencyNames.join(', ')}</DetailLine>}
            {job.mode === 'workshop' && job.messages && job.messages.length > 0 && (
              <DetailLine label="Messages">
                <ol className="space-y-0.5">
                  {job.messages.map((m, i) => <li key={i} className="flex items-start gap-1"><span className="shrink-0">{i + 1}.</span><span>{m}</span></li>)}
                </ol>
              </DetailLine>
            )}
            {isWebhook && <WebhookEndpoint id={job.id} name={job.name} />}
            {statusWord && <DetailLine label="Status">{statusWord}</DetailLine>}
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
            {!isReadOnlyUser && <div className="pt-1"><button type="button" onClick={() => void handleDelete(job)} className="text-xs text-destructive hover:underline">Delete schedule</button></div>}
          </PageRow>
        )
      })}
    </div>
  )
}

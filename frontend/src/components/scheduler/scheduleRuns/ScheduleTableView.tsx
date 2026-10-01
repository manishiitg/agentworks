import React, { useEffect, useState } from 'react'
import { ChevronDown, ChevronRight, Play, Square } from 'lucide-react'
import { WebhookEndpoint } from './WebhookEndpoint'
import { describeCron } from './cron'
import { formatDuration, formatExactDateTime, formatLastRunLabel, getLocalizedJobName, getScheduleDependencyIds, getScheduleExecutionScope, isMissedSchedule, isScheduleIssueStatus, isScheduleWaitingStatus } from './helpers'
import { ScheduleRowActions } from './ScheduleRowActions'
import { ScheduleExecutionHistoryList } from '../../ScheduleExecutionHistoryList'
import type { ScheduleRunsPanelState } from './useScheduleRunsData'

type ScheduleTableViewProps = {
  entityType?: 'workflow' | 'product'
  panel: Pick<ScheduleRunsPanelState,
    | 'focusedScheduleId' | 'filteredJobs' | 'presetMap' | 'potentialOverlaps' | 'isSchedulerPaused' | 'isReadOnlyUser' | 'triggering'
    | 'handleStopRun' | 'handleTrigger' | 'handleToggle' | 'handleDelete'
    | 'openActionMenuJobId' | 'setOpenActionMenuJobId'
    | 'expandedRunHistoryJobIds' | 'runsByJob' | 'runsLoadingJobIds' | 'toggleRunHistory' | 'openScheduledRun'
  >
}

/** Global schedules show timing first; instructions and run diagnostics are opt-in. */
export function ScheduleTableView({ panel, entityType = 'workflow' }: ScheduleTableViewProps) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  useEffect(() => {
    if (panel.focusedScheduleId) {
      setExpanded(previous => new Set(previous).add(panel.focusedScheduleId!))
    }
  }, [panel.focusedScheduleId])
  const localTimeZone = Intl.DateTimeFormat().resolvedOptions().timeZone
  return <div className="px-4 py-3 sm:px-6">
    <p className="mb-2 text-xs text-muted-foreground">Next run and last run are shown in your local time ({localTimeZone}). Frequency uses each schedule’s time zone.</p>
    <div className="overflow-x-auto rounded-lg border border-border">
      <table className="w-full min-w-[1080px] text-left text-sm">
        <thead className="border-b border-border bg-muted/30 text-xs text-muted-foreground">
          <tr>
            <th className="px-4 py-2 font-medium">Schedule / {entityType === 'product' ? 'Crew' : 'automation'}</th>
            <th className="px-3 py-2 font-medium">Frequency</th>
            <th className="px-3 py-2 font-medium">State</th>
            <th className="px-3 py-2 font-medium">Next run (local)</th>
            <th className="px-3 py-2 font-medium">Run status</th>
            <th className="px-3 py-2 font-medium">Avg duration</th>
            <th className="px-3 py-2 font-medium">Needs attention</th>
            <th className="px-4 py-2 text-right font-medium">Actions</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-border">
          {panel.filteredJobs.map(job => {
            const name = getLocalizedJobName(job)
            const isWebhook = job.schedule_type === 'webhook'
            const frequency = isWebhook ? 'Webhook · on request' : describeCron(job.cron_expression)
            const workflow = panel.presetMap.get(job.preset_query_id ?? '')?.label || job.workflow_label || job.name
            const isRunning = job.last_status === 'running'
            const isMissed = isMissedSchedule(job)
            const isWaiting = isScheduleWaitingStatus(job.last_status)
            const canRunMissed = isMissed && job.enabled && !isRunning && !isWaiting && !isWebhook && !panel.isReadOnlyUser
            const isIssue = isScheduleIssueStatus(job.last_status)
            const overlapWith = panel.potentialOverlaps.get(job.id)
            const outcome = isRunning ? 'Running now' : isWaiting ? job.last_status === 'waiting_for_capacity' ? 'Waiting for capacity' : 'Queued' : job.last_status === 'success' ? 'Succeeded' : job.last_status === 'error' ? 'Failed' : job.last_status === 'partial' ? 'Partial' : job.last_status === 'interrupted' ? 'Interrupted' : job.last_status === 'stopped' ? 'Stopped' : 'Not run yet'
            const state = isRunning ? 'Running' : isScheduleWaitingStatus(job.last_status) ? 'Queued' : job.enabled ? 'Enabled' : 'Paused'
            const open = expanded.has(job.id)
            const detailsId = `schedule-details-${job.id}`
            const toggle = () => setExpanded(previous => {
              const next = new Set(previous)
              if (next.has(job.id)) next.delete(job.id)
              else next.add(job.id)
              return next
            })
            const scope = getScheduleExecutionScope(job)
            const dependencyIds = getScheduleDependencyIds(job)
            const dependencyNames = dependencyIds.map(id => panel.filteredJobs.find(candidate => candidate.id === id)?.name ?? id)
            const hasRuntimePolicy = dependencyIds.length > 0 || !!job.collision_policy || !!job.max_start_delay_minutes || job.concurrency_mode === 'parallel'
            return <React.Fragment key={job.id}>
              <tr className="transition-colors hover:bg-muted/20">
                <td className="max-w-[340px] px-4 py-3">
                  <button type="button" onClick={toggle} aria-expanded={open} aria-controls={open ? detailsId : undefined} className="block max-w-full truncate text-left font-medium text-foreground hover:text-primary" title={name}>{name}</button>
                  <div className="mt-0.5 truncate text-xs text-muted-foreground" title={workflow}>{workflow}</div>
                </td>
                <td className="max-w-56 px-3 py-3 text-xs text-muted-foreground"><span className="line-clamp-2" title={frequency}>{frequency}</span><span className="mt-0.5 block text-[11px]">{job.timezone || localTimeZone}</span></td>
                <td className="whitespace-nowrap px-3 py-3 text-xs text-muted-foreground"><span className="inline-flex items-center gap-1.5"><span className={`h-1.5 w-1.5 rounded-full ${isRunning ? 'animate-pulse bg-warning' : job.enabled ? 'bg-success' : 'bg-muted-foreground/50'}`} />{state}</span></td>
                <td className="whitespace-nowrap px-3 py-3 text-xs">{!job.enabled ? '—' : panel.isSchedulerPaused ? <span className="text-muted-foreground">Paused globally</span> : isWebhook ? 'On request' : job.next_run_at ? formatExactDateTime(job.next_run_at) : '—'}</td>
                <td className="whitespace-nowrap px-3 py-3 text-xs" title={formatExactDateTime(job.last_run_at)}><span className={isIssue ? 'text-destructive' : 'text-foreground'}>{outcome}</span>{job.last_run_at && <span className="mt-0.5 block text-muted-foreground">{formatLastRunLabel(job.last_run_at)}</span>}</td>
                <td className="whitespace-nowrap px-3 py-3 text-xs">{job.avg_duration_ms && job.avg_duration_samples ? <><span>{formatDuration(job.avg_duration_ms)}</span><span className="mt-0.5 block text-[11px] text-muted-foreground">last {job.avg_duration_samples} successful</span></> : <span className="text-muted-foreground">No history</span>}</td>
                <td className="max-w-56 px-3 py-3 text-xs"><div className="flex flex-wrap gap-1">
                  {isMissed && <button type="button" onClick={toggle} className="rounded bg-warning/15 px-1.5 py-0.5 text-warning hover:bg-warning/25">{job.missed_run_count} missed</button>}
                  {isIssue && <button type="button" onClick={toggle} className="rounded bg-destructive/15 px-1.5 py-0.5 text-destructive hover:bg-destructive/25">{job.last_status === 'error' && job.consecutive_failures > 1 ? `${job.consecutive_failures} failures in a row` : outcome}</button>}
                  {isWaiting && <button type="button" onClick={toggle} className="rounded bg-info/15 px-1.5 py-0.5 text-info hover:bg-info/25">{job.last_status === 'waiting_for_capacity' ? 'Waiting for capacity' : 'Queued'}</button>}
                  {overlapWith && <button type="button" onClick={toggle} title={`Next run may overlap with ${overlapWith} based on recent average duration`} className="rounded bg-warning/10 px-1.5 py-0.5 text-warning hover:bg-warning/20">Possible overlap</button>}
                  {!isMissed && !isIssue && !isWaiting && !overlapWith && <span className="text-muted-foreground">None</span>}
                </div></td>
                <td className="px-4 py-3"><div className="flex items-center justify-end gap-2">
                  {isRunning && job.kind !== 'gmail' && !panel.isReadOnlyUser && <button type="button" onClick={() => panel.handleStopRun(job)} className="inline-flex items-center gap-1 rounded px-2 py-1 text-xs text-destructive hover:bg-destructive/10"><Square className="h-3 w-3" />Stop</button>}
                  {canRunMissed && <button type="button" onClick={() => panel.handleTrigger(job)} disabled={panel.triggering === job.id} aria-label={`Run missed schedule ${name} now`} title="Start one run now. Missed times are not replayed individually." className="inline-flex items-center gap-1 whitespace-nowrap rounded-md border border-warning/30 bg-warning/10 px-2 py-1 text-xs font-medium text-warning transition-colors hover:bg-warning/20 disabled:cursor-wait disabled:opacity-50"><Play className="h-3 w-3" />{panel.triggering === job.id ? 'Starting…' : 'Run now'}</button>}
                  <button type="button" aria-label={`${open ? 'Hide' : 'Show'} ${name} details`} aria-expanded={open} aria-controls={open ? detailsId : undefined} onClick={toggle} className="rounded-md p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground">{open ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}</button>
                </div></td>
              </tr>
              {open && <tr><td colSpan={8} className="bg-muted/15 px-4 py-4">
                <div id={detailsId} role="region" aria-label={`${name} details`} className="min-h-36 space-y-3">
                  <div className="flex items-center justify-between gap-4">
                    <p className="text-xs text-muted-foreground">{job.run_count} runs · Last result: {job.last_status || 'Not run yet'}{scope ? ` · ${scope.label}` : ''}</p>
                    {!panel.isReadOnlyUser && <div className="flex items-center gap-1">
                      <ScheduleRowActions {...panel} job={job} isRunning={isRunning} isMissedJob={isMissed} menuButtonClassName="rounded-md p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground" />
                    </div>}
                  </div>
                  {isWebhook && <WebhookEndpoint id={job.id} name={job.name} address={job.gmail?.address} />}
                  {job.group_names?.length ? <p className="text-xs text-muted-foreground">Groups: {job.group_names.join(', ')}</p> : null}
                  {hasRuntimePolicy && <div className="max-w-4xl space-y-1 rounded-md border border-border bg-background/70 p-3 text-xs text-muted-foreground">
                    <h4 className="font-medium text-foreground">Coordination and runtime policy</h4>
                    {dependencyNames.length > 0 && <p>Waits for: {dependencyNames.join(', ')} · Release: {job.after_terminal_status || 'completed'}{job.after_delay_minutes ? ` + ${job.after_delay_minutes}m delay` : ''}{job.dependency_deadline ? ` · Deadline: ${job.dependency_deadline} local` : ''}</p>}
                    {job.collision_policy && <p>When busy: {job.collision_policy.replaceAll('_', ' ')}{job.max_start_delay_minutes ? ` · Start within ${job.max_start_delay_minutes}m` : ''}</p>}
                    {job.concurrency_mode === 'parallel' && <p>Concurrency: parallel · shared-state overwrite and duplicate-action risks accepted</p>}
                  </div>}
                  {job.messages?.length ? <div className="max-w-4xl space-y-2"><h4 className="text-xs font-medium text-foreground">Instructions</h4>{job.messages.map((message, i) => <p key={i} className="whitespace-pre-wrap text-sm leading-6 text-muted-foreground">{message}</p>)}</div> : null}
                  {job.last_error && <p className="max-w-4xl whitespace-pre-wrap break-words text-xs leading-5 text-muted-foreground">Last run: {job.last_error}</p>}
                  {overlapWith && <p className="text-xs text-warning">Possible overlap with {overlapWith}: next start times are closer than a recent average run duration. The scheduler’s collision policy decides whether a run waits, queues, or skips.</p>}
                  {isMissed && <p className="text-xs text-muted-foreground">{job.missed_run_count} missed occurrence{job.missed_run_count === 1 ? '' : 's'} recorded. Run now starts one new execution; it does not replay each missed time.</p>}
                  {job.waiting_reason && <p className="text-xs text-muted-foreground">{job.waiting_reason}</p>}
                  {(job.run_count || 0) > 0 && <ScheduleExecutionHistoryList
                    job={job}
                    runs={panel.runsByJob[job.id] ?? []}
                    historyOpen={panel.expandedRunHistoryJobIds.has(job.id)}
                    historyLoading={panel.runsLoadingJobIds.has(job.id)}
                    recordedRunCount={job.run_count}
                    onToggle={() => void panel.toggleRunHistory(job)}
                    onOpen={run => void panel.openScheduledRun(run, job)}
                    deletingRunIds={new Set()}
                    compact
                  />}
                  <p className="text-xs text-muted-foreground">{entityType === 'product'
                    ? 'Open this Crew’s Automation panel to change this schedule.'
                    : isWebhook ? 'Ask the workflow builder chat to configure this webhook.' : 'Ask the workflow Builder chat to change timing, busy-run handling, or schedule dependencies.'}</p>
                </div>
              </td></tr>}
            </React.Fragment>
          })}
        </tbody>
      </table>
    </div>
  </div>
}

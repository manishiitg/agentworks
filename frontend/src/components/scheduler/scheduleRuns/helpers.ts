import { missedScheduleReasonText } from '../../../utils/scheduleRunPresentation'
import type { ScheduledJob } from '../../../services/api-types'
import { normalizeWorkspacePath } from '../../../utils/workspacePathUtils'

export type JobFilter = 'running' | 'waiting' | 'enabled' | 'paused' | 'missed' | 'issues' | 'overlap' | 'all'
export type SchedulePanelView = 'overview' | 'calendar' | 'by-workflow' | 'schedules'

/** Global views open on the per-workflow grouping with drill-down into each
 *  workflow's schedules; a workflow-scoped view already identifies one
 *  workflow, so it opens on the schedule list directly. */
export function defaultSchedulePanelView(isWorkflowScoped: boolean): SchedulePanelView {
  return isWorkflowScoped ? 'schedules' : 'by-workflow'
}

export type WorkflowScope = {
  workflowId?: string | null
  presetQueryId?: string | null
  workspacePath?: string | null
  label?: string | null
}

export type PresetMap = Map<string, { label: string; workspacePath: string | null }>

export const isScheduleIssueStatus = (status?: ScheduledJob['last_status']) =>
  status === 'error' || status === 'partial' || status === 'interrupted'

export const isSchedulePartialStatus = (status?: ScheduledJob['last_status']) =>
  status === 'partial' || status === 'interrupted'

export const isScheduleWaitingStatus = (status?: ScheduledJob['last_status']) =>
  status === 'waiting_for_workflow' || status === 'waiting_for_capacity'

export const WORKFLOW_SCHEDULE_PANEL_LIMIT = 10_000

/** The Schedules surface owns only time-based work. Webhook jobs and their
 * delivery history live in Triggers. */
export function timeScheduledJobs(jobs: ScheduledJob[]): ScheduledJob[] {
  return jobs.filter(job => job.schedule_type !== 'webhook')
}

export type WorkflowScheduleGroup = {
  key: string
  label: string
  workspacePath?: string
  jobs: ScheduledJob[]
  running: number
  missed: number
  issues: number
  overlap: number
  enabled: number
  paused: number
  nextRunAt?: string
  lastRunAt?: string
  runCount: number
}

/** A forecast, not a confirmed collision: two enabled schedules in the same
 * workflow have next starts closer together than the first one's observed
 * average duration. The scheduler's actual policy still decides what happens. */
export function getPotentialScheduleOverlaps(jobs: ScheduledJob[], presetMap: PresetMap): Map<string, string> {
  const byWorkflow = new Map<string, ScheduledJob[]>()
  for (const job of jobs) {
    if (!job.enabled || !job.next_run_at || job.concurrency_mode === 'parallel') continue
    const key = getWorkflowFilterMeta(job, presetMap).value
    byWorkflow.set(key, [...(byWorkflow.get(key) ?? []), job])
  }

  const overlaps = new Map<string, string>()
  for (const workflowJobs of byWorkflow.values()) {
    workflowJobs.sort((a, b) => (a.next_run_at ?? '').localeCompare(b.next_run_at ?? ''))
    for (let i = 0; i < workflowJobs.length; i += 1) {
      const first = workflowJobs[i]
      const firstStart = new Date(first.next_run_at!).getTime()
      if (!Number.isFinite(firstStart) || !first.avg_duration_ms || first.avg_duration_ms <= 0) continue
      for (let j = i + 1; j < workflowJobs.length; j += 1) {
        const second = workflowJobs[j]
        const secondStart = new Date(second.next_run_at!).getTime()
        if (!Number.isFinite(secondStart) || secondStart >= firstStart + first.avg_duration_ms) break
        overlaps.set(first.id, second.name)
        overlaps.set(second.id, first.name)
      }
    }
  }
  return overlaps
}

export type CalendarEntry = {
  job: ScheduledJob
  time: string
  label: string
  note?: string
  sourceTime?: string
  timezone?: string
}

export type CalendarCell = { key: string; day?: number; date?: string; items: CalendarEntry[] }

export function timeAgo(dateStr?: string): string {
  if (!dateStr) return '—'
  const diff = Date.now() - new Date(dateStr).getTime()
  const mins = Math.floor(diff / 60000)
  if (mins < 60) return `${mins}m ago`
  const hrs = Math.floor(mins / 60)
  if (hrs < 24) return `${hrs}h ago`
  const days = Math.floor(hrs / 24)
  return `${days}d ago`
}

export function formatLastRunLabel(dateStr?: string): string {
  return dateStr ? timeAgo(dateStr) : 'never'
}

// Same options as the per-call toLocaleString this replaces, built once.
const exactDateTimeFormatter = new Intl.DateTimeFormat([], {
  year: 'numeric',
  month: 'short',
  day: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
  hour12: true,
})

export function formatExactDateTime(dateStr?: string): string {
  if (!dateStr) return 'No scheduled runs recorded yet'
  try {
    return exactDateTimeFormatter.format(new Date(dateStr))
  } catch {
    return dateStr
  }
}

export function formatDuration(ms?: number): string {
  if (!ms) return ''
  if (ms < 1000) return `${ms}ms`
  const secs = Math.floor(ms / 1000)
  if (secs < 60) return `${secs}s`
  const mins = Math.floor(secs / 60)
  const remSecs = secs % 60
  if (mins < 60) return `${mins}m ${remSecs}s`
  const hrs = Math.floor(mins / 60)
  const remMins = mins % 60
  return `${hrs}h ${remMins}m`
}

export function formatTimeUntil(dateStr?: string): string {
  if (!dateStr) return '—'

  const diffMs = new Date(dateStr).getTime() - Date.now()
  if (Number.isNaN(diffMs)) return '—'
  if (diffMs <= 0) return 'due now'

  const totalMinutes = Math.ceil(diffMs / 60000)
  if (totalMinutes < 60) return `in ${totalMinutes}m`

  const hours = Math.floor(totalMinutes / 60)
  const minutes = totalMinutes % 60
  if (hours < 24) return minutes > 0 ? `in ${hours}h ${minutes}m` : `in ${hours}h`

  const days = Math.floor(hours / 24)
  const remHours = hours % 24
  if (days < 7) return remHours > 0 ? `in ${days}d ${remHours}h` : `in ${days}d`

  return new Date(dateStr).toLocaleDateString([], { month: 'short', day: 'numeric' })
}

export function getMissedScheduleDelayMs(job: ScheduledJob): number | null {
  if (!job.enabled || job.last_status === 'running' || !job.latest_missed_run_at) return null

  const missedAtMs = new Date(job.latest_missed_run_at).getTime()
  if (Number.isNaN(missedAtMs)) return null

  return Math.max(0, Date.now() - missedAtMs)
}

export function isMissedSchedule(job: ScheduledJob): boolean {
  return !!job.enabled && (job.missed_run_count ?? 0) > 0
}

type ScheduleExecutionScope = {
  label: string
  title: string
}

export function getScheduleExecutionScope(job: ScheduledJob): ScheduleExecutionScope | null {
  if (job.entity_type !== 'workflow') return null

  const selectedRoutes = Object.values(job.route_selections ?? {}).filter(Boolean)
  if (selectedRoutes.length > 0) {
    const routeList = selectedRoutes.join(', ')
    return {
      label: selectedRoutes.length === 1 ? `Route: ${routeList}` : `${selectedRoutes.length} selected routes`,
      title: `Runs the full workflow using the saved route selection${selectedRoutes.length === 1 ? '' : 's'}: ${routeList}`,
    }
  }

  const instructions = job.messages?.join('\n').toLowerCase() ?? ''
  if (!instructions || instructions.includes('run_full_workflow')) {
    return {
      label: 'Full workflow',
      title: 'Runs the complete workflow from the beginning for the selected group or groups.',
    }
  }

  if (instructions.includes('execute_step') || instructions.includes('run only step-')) {
    return {
      label: 'Selected step',
      title: 'Runs only the step or steps named in this schedule, not the full workflow.',
    }
  }

  return null
}

export function getScheduleDependencyIds(job: ScheduledJob): string[] {
  const seen = new Set<string>()
  const ids: string[] = []
  for (const rawId of [job.after_schedule_id, ...(job.after_schedule_ids ?? [])]) {
    const id = rawId?.trim()
    if (!id || seen.has(id)) continue
    seen.add(id)
    ids.push(id)
  }
  return ids
}

export function formatMissedScheduleReason(job: ScheduledJob): string {
  return missedScheduleReasonText(job.missed_run_reason)
}

export function formatOverdueDuration(durationMs: number): string {
  if (durationMs < 60_000) return `${Math.round(durationMs / 1000)}s`
  if (durationMs < 3_600_000) return `${Math.round(durationMs / 60_000)}m`
  if (durationMs < 86_400_000) return `${Math.round(durationMs / 3_600_000)}h`
  return `${Math.round(durationMs / 86_400_000)}d`
}

export function formatLocalScheduleTime(dateStr?: string): string {
  if (!dateStr) return '—'

  try {
    const d = new Date(dateStr)
    const now = new Date()
    const diffDays = Math.floor((d.getTime() - now.getTime()) / 86400000)

    if (diffDays < 1) {
      return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: true })
    }

    if (diffDays < 7) {
      return d.toLocaleDateString([], { weekday: 'short', hour: '2-digit', minute: '2-digit', hour12: true })
    }

    return d.toLocaleDateString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: true })
  } catch {
    return dateStr
  }
}

function formatLocalScheduleTimeShort(dateStr?: string): string {
  if (!dateStr) return ''

  try {
    return new Date(dateStr).toLocaleTimeString([], {
      hour: '2-digit',
      minute: '2-digit',
      hour12: true,
    })
  } catch {
    return ''
  }
}

function stripTimezoneSuffix(label: string): string {
  return label.replace(/\s*\([^()]*\b(?:[A-Z]{2,5}|UTC|GMT)\b[^()]*\)\s*$/, '').trim()
}

export function localizeTimezoneLabel(label: string, dateStr?: string): string {
  const baseLabel = stripTimezoneSuffix(label)
  if (!dateStr) return baseLabel || label

  const localizedTime = formatLocalScheduleTimeShort(dateStr)
  if (!localizedTime) return baseLabel || label

  const hasTimezoneSuffix = /\([^()]*\b(?:[A-Z]{2,5}|UTC|GMT)\b[^()]*\)\s*$/.test(label)
  if (!hasTimezoneSuffix) return label

  return `${baseLabel} (${localizedTime})`
}

export function getLocalizedJobName(job: ScheduledJob): string {
  return localizeTimezoneLabel(job.name, job.next_run_at)
}

export function getWorkflowFilterMeta(
  job: ScheduledJob,
  presetMap: PresetMap
): { value: string; label: string; workflowLabel: string } {
  const workflowLabel = presetMap.get(job.preset_query_id ?? '')?.label || job.workflow_label || job.name
  const value = job.workflow_id || job.preset_query_id || job.workspace_path || workflowLabel

  return {
    value,
    label: workflowLabel,
    workflowLabel,
  }
}

export function getWorkflowScopeLabel(
  workflowScope: WorkflowScope | undefined,
  presetMap: PresetMap
): string {
  if (!workflowScope) return 'Automation Schedules'
  if (workflowScope.label) return workflowScope.label
  if (workflowScope.presetQueryId) {
    const presetLabel = presetMap.get(workflowScope.presetQueryId)?.label
    if (presetLabel) return presetLabel
  }
  const path = normalizeWorkspacePath(workflowScope.workspacePath)
  return path.split('/').filter(Boolean).pop() || 'Automation'
}

export function jobMatchesWorkflowScope(
  job: ScheduledJob,
  workflowScope: WorkflowScope | undefined,
  presetMap: PresetMap
): boolean {
  if (!workflowScope) return true

  if (workflowScope.workflowId && job.workflow_id === workflowScope.workflowId) {
    return true
  }

  if (workflowScope.presetQueryId && job.preset_query_id === workflowScope.presetQueryId) {
    return true
  }

  const scopePath = normalizeWorkspacePath(workflowScope.workspacePath)
  if (!scopePath) return false

  const presetPath = job.preset_query_id
    ? presetMap.get(job.preset_query_id)?.workspacePath
    : null
  return normalizeWorkspacePath(job.workspace_path) === scopePath ||
    normalizeWorkspacePath(presetPath) === scopePath
}

export function sortJobs(a: ScheduledJob, b: ScheduledJob): number {
  const rank = (job: ScheduledJob) => {
    if (job.last_status === 'running') return 0
    if (isMissedSchedule(job)) return 1
    if (job.enabled && job.next_run_at) return 2
    if (job.enabled) return 3
    if (isScheduleIssueStatus(job.last_status)) return 4
    return 5
  }

  const rankDiff = rank(a) - rank(b)
  if (rankDiff !== 0) return rankDiff

  if (a.enabled && b.enabled) {
    if (a.next_run_at && b.next_run_at) {
      return a.next_run_at.localeCompare(b.next_run_at)
    }
    if (a.next_run_at) return -1
    if (b.next_run_at) return 1
  }

  const aTime = a.updated_at || a.last_run_at || a.created_at || ''
  const bTime = b.updated_at || b.last_run_at || b.created_at || ''
  return bTime.localeCompare(aTime)
}

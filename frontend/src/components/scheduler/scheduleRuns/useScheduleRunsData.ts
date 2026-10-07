import { useState, useEffect, useCallback, useMemo } from 'react'
import { schedulerApi } from '../../../api/scheduler'
import { agentApi } from '../../../services/api'
import { useGlobalPresetStore } from '../../../stores/useGlobalPresetStore'
import { useChatStore } from '../../../stores/useChatStore'
import { useModeStore } from '../../../stores/useModeStore'
import { useWorkflowStore } from '../../../stores/useWorkflowStore'
import { activateTab } from '../../../utils/activateTab'
import { selectWorkflowPreset } from '../../../utils/workflowNavigation'
import { scheduleTabLabel } from '../../../utils/scheduleTabLabel'
import { resolveWorkflowTabForSession } from '../../../utils/workflowTabResolution'
import { hydrateExecutionConversation } from '../../../utils/executionConversationRestore'
import type { ScheduleAfterRun, ScheduledJob, ScheduledJobRun, SchedulerConfig, SkippedWhilePaused } from '../../../services/api-types'
import { useCanWriteWorkflow } from '../../../hooks/useCanWriteWorkflow'
import {
  WORKFLOW_SCHEDULE_PANEL_LIMIT,
  defaultSchedulePanelView,
  getMissedScheduleDelayMs,
  getPotentialScheduleOverlaps,
  getWorkflowFilterMeta,
  getWorkflowScopeLabel,
  isMissedSchedule,
  isScheduleIssueStatus,
  isScheduleWaitingStatus,
  jobMatchesWorkflowScope,
  sortJobs,
  timeScheduledJobs,
  type CalendarCell,
  type CalendarEntry,
  type JobFilter,
  type PresetMap,
  type SchedulePanelView,
  type WorkflowScheduleGroup,
  type WorkflowScope,
} from './helpers'
import {
  addMonths,
  dateKeyFromLocalDate,
  expandCronForMonth,
  formatLocalTimeFromDate,
  scheduledDateTimeInLocal,
} from './cron'

export type UseScheduleRunsDataArgs = {
  active?: boolean
  onClose: () => void
  onJobsLoaded?: (jobs: ScheduledJob[]) => void
  workflowScope?: WorkflowScope
  entityType?: 'workflow' | 'product'
  productProfileId?: string
  workflowKind?: 'workflow' | 'relay'
  canManage?: boolean
}

// Mutation responses do not include the list's recent-run aggregate. Keep it
// until the next list refresh so toggling a schedule does not blank the metric.
function retainDurationAverage(updated: ScheduledJob, previous: ScheduledJob): ScheduledJob {
  return {
    ...updated,
    avg_duration_ms: updated.avg_duration_ms ?? previous.avg_duration_ms,
    avg_duration_samples: updated.avg_duration_samples ?? previous.avg_duration_samples,
  }
}

export function useScheduleRunsData({ onClose, onJobsLoaded, workflowScope, entityType = 'workflow', productProfileId, workflowKind, canManage, active = true }: UseScheduleRunsDataArgs) {
  const [jobs, setJobs] = useState<ScheduledJob[]>([])
  const [isLoading, setIsLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [openActionMenuJobId, setOpenActionMenuJobId] = useState<string | null>(null)
  const [expandedWorkflowKeys, setExpandedWorkflowKeys] = useState<string[]>([])
  const isWorkflowScoped = !!workflowScope
  const [activeView, setActiveView] = useState<SchedulePanelView>(() => defaultSchedulePanelView(isWorkflowScoped))
  const [calendarMonth, setCalendarMonth] = useState(() => new Date())
  const [selectedCalendarDate, setSelectedCalendarDate] = useState<string | null>(null)
  const [focusedScheduleId, setFocusedScheduleId] = useState<string | null>(null)
  const [activeFilter, setActiveFilter] = useState<JobFilter>('all')
  const [searchQuery, setSearchQuery] = useState('')
  const [selectedWorkflowFilter, setSelectedWorkflowFilter] = useState('all')
  const [schedulerConfig, setSchedulerConfig] = useState<SchedulerConfig | null>(null)
  // After a resume: the runs the pause skipped, offered for a manual catch-up.
  const [pauseCatchUp, setPauseCatchUp] = useState<SkippedWhilePaused[]>([])
  const [isRunningCatchUp, setIsRunningCatchUp] = useState(false)
  const [isUpdatingSchedulerPause, setIsUpdatingSchedulerPause] = useState(false)
  const [bulkUpdatingGroupKey, setBulkUpdatingGroupKey] = useState<string | null>(null)

  const [triggering, setTriggering] = useState<string | null>(null)

  // Execution history — only wired up in the per-workflow scoped (embedded)
  // view; the global cross-workflow popup does not show this.
  const [expandedRunHistoryJobIds, setExpandedRunHistoryJobIds] = useState<Set<string>>(new Set())
  const [runsByJob, setRunsByJob] = useState<Record<string, ScheduledJobRun[]>>({})
  const [runsLoadingJobIds, setRunsLoadingJobIds] = useState<Set<string>>(new Set())
  const [deletingRunSessionIds, setDeletingRunSessionIds] = useState<Set<string>>(new Set())

  useEffect(() => {
    if (!openActionMenuJobId) return
    const closeMenu = () => setOpenActionMenuJobId(null)
    document.addEventListener('pointerdown', closeMenu)
    return () => document.removeEventListener('pointerdown', closeMenu)
  }, [openActionMenuJobId])

  const workflowPresets = useGlobalPresetStore(state => state.workflowPresets)
  const refreshPresets = useGlobalPresetStore(state => state.refreshPresets)

  // Build presetId → {label, workspacePath} map
  const presetMap = useMemo<PresetMap>(() => {
    const map: PresetMap = new Map()
    workflowPresets.forEach((p) => {
      map.set(p.id, {
        label: p.label,
        workspacePath: p.selectedFolder?.filepath ?? null,
      })
    })
    return map
  }, [workflowPresets])

  // Callers pass `workflowScope` as an inline object literal, so depending on
  // it directly would re-run every memo below (including the 3-month cron
  // expansion) on each parent render. Depend on its primitives instead.
  const scopeWorkflowId = workflowScope?.workflowId ?? null
  const scopePresetId = workflowScope?.presetQueryId ?? null
  const scopePath = workflowScope?.workspacePath ?? null
  const scopeLabel = workflowScope?.label ?? null
  const canWriteWorkflow = useCanWriteWorkflow(scopePath)
  const isReadOnlyUser = canManage === undefined ? !canWriteWorkflow : !canManage
  const stableScope = useMemo<WorkflowScope | undefined>(
    () => (isWorkflowScoped ? { workflowId: scopeWorkflowId, presetQueryId: scopePresetId, workspacePath: scopePath, label: scopeLabel } : undefined),
    [isWorkflowScoped, scopeWorkflowId, scopePresetId, scopePath, scopeLabel],
  )

  const panelJobs = useMemo(() => {
    return jobs.filter(job => jobMatchesWorkflowScope(job, stableScope, presetMap)
      && (!workflowKind || workflowPresets.some(preset => (preset.workflowKind || 'workflow') === workflowKind
        && jobMatchesWorkflowScope(job, { workflowId: preset.id, presetQueryId: preset.id, workspacePath: preset.selectedFolder?.filepath }, presetMap))))
  }, [jobs, stableScope, presetMap, workflowKind, workflowPresets])

  const panelTitle = useMemo(() => {
    if (!isWorkflowScoped) return 'Automation Schedules'
    return `Schedules for ${getWorkflowScopeLabel(stableScope, presetMap)}`
  }, [isWorkflowScoped, stableScope, presetMap])

  useEffect(() => {
    const runningWorkflowKeys = new Set(
      panelJobs
        .filter(job => job.last_status === 'running')
        .map(job => getWorkflowFilterMeta(job, presetMap).value)
    )
    if (runningWorkflowKeys.size === 0) return

    setExpandedWorkflowKeys(prev => {
      const next = [...prev]
      runningWorkflowKeys.forEach((key) => {
        if (!next.includes(key)) next.push(key)
      })
      return next.length === prev.length ? prev : next
    })
  }, [panelJobs, presetMap])

  const loadJobs = useCallback(async (showLoading = false) => {
    if (showLoading) setIsLoading(true)
    setError(null)
    try {
      const [resp, config] = await Promise.all([
        schedulerApi.listJobs({
          entity_type: entityType,
          limit: WORKFLOW_SCHEDULE_PANEL_LIMIT,
        }),
        schedulerApi.getConfig().catch(() => null),
      ])
      const scheduleJobs = timeScheduledJobs(productProfileId && entityType === 'product'
        ? resp.jobs.filter(job => job.id.startsWith(`product-project:${productProfileId}:`))
        : resp.jobs)
      setJobs(scheduleJobs)
      onJobsLoaded?.(scheduleJobs)
      setSchedulerConfig(config)
    } catch {
      setError('Failed to load automation schedules')
    } finally {
      if (showLoading) setIsLoading(false)
    }
  }, [entityType, productProfileId, onJobsLoaded])

  useEffect(() => {
    if (!active) return
    loadJobs(true)
    refreshPresets()
  }, [loadJobs, refreshPresets, active])

  // Auto-refresh while any schedule is running: jobs list (every 5s)
  const hasRunningJob = panelJobs.some(j => j.last_status === 'running')
  const activeScheduleCount = panelJobs.filter(j => j.enabled).length
  const isSchedulerPaused = !!schedulerConfig?.globally_paused
  const potentialOverlaps = useMemo(
    () => isSchedulerPaused ? new Map<string, string>() : getPotentialScheduleOverlaps(panelJobs, presetMap),
    [panelJobs, presetMap, isSchedulerPaused],
  )

  const workflowScheduleSummary = useMemo(() => {
    const workflows = new Map<string, { enabled: number; paused: number; running: number; attention: boolean }>()

    panelJobs.forEach((job) => {
      const key = getWorkflowFilterMeta(job, presetMap).value
      const current = workflows.get(key) ?? { enabled: 0, paused: 0, running: 0, attention: false }
      if (job.enabled) current.enabled += 1
      else current.paused += 1
      if (job.last_status === 'running') current.running += 1
      if (isMissedSchedule(job) || isScheduleIssueStatus(job.last_status)) current.attention = true
      workflows.set(key, current)
    })

    const values = Array.from(workflows.values())

    return {
      workflows: values.length,
      running: values.filter(workflow => workflow.running > 0).length,
      attention: values.filter(workflow => workflow.attention).length,
      fullyPaused: values.filter(workflow => workflow.enabled === 0).length,
      partlyPaused: values.filter(workflow => workflow.enabled > 0 && workflow.paused > 0).length,
    }
  }, [panelJobs, presetMap])

  const summary = useMemo(() => {
    const running = panelJobs.filter(j => j.last_status === 'running').length
    const missed = panelJobs.filter(isMissedSchedule).length
    const issues = panelJobs.filter(j => isScheduleIssueStatus(j.last_status)).length
    const waiting = panelJobs.filter(j => isScheduleWaitingStatus(j.last_status)).length
    const paused = panelJobs.filter(j => !j.enabled).length
    const lastRunAt = panelJobs.reduce<string | undefined>((latest, job) => {
      if (!job.last_run_at) return latest
      return !latest || job.last_run_at > latest ? job.last_run_at : latest
    }, undefined)
    return {
      running,
      missed,
      issues,
      waiting,
      overlap: potentialOverlaps.size,
      paused,
      enabled: activeScheduleCount,
      total: panelJobs.length,
      lastRunAt,
    }
  }, [panelJobs, activeScheduleCount, potentialOverlaps])

  const normalizedSearch = searchQuery.trim().toLowerCase()
  const workflowOptions = useMemo(() => {
    const seen = new Map<string, string>()

    panelJobs.forEach((job) => {
      const meta = getWorkflowFilterMeta(job, presetMap)
      if (!meta.label) return
      if (!seen.has(meta.value)) seen.set(meta.value, meta.label)
    })

    return Array.from(seen.entries())
      .map(([value, label]) => ({ value, label }))
      .sort((a, b) => a.label.localeCompare(b.label))
  }, [panelJobs, presetMap])

  // Jobs narrowed to the selected workflow filter; upcoming/missed both derive from it.
  const scopedJobs = useMemo(() => {
    if (selectedWorkflowFilter === 'all') return panelJobs
    return panelJobs.filter(job => getWorkflowFilterMeta(job, presetMap).value === selectedWorkflowFilter)
  }, [panelJobs, presetMap, selectedWorkflowFilter])

  const upcomingJobs = useMemo(() => {
    return scopedJobs
      .filter(job => job.enabled && !!job.next_run_at && !isMissedSchedule(job))
      .sort((a, b) => (a.next_run_at || '').localeCompare(b.next_run_at || ''))
      .slice(0, 4)
  }, [scopedJobs])

  const missedJobs = useMemo(() => {
    return scopedJobs
      .filter(job => isMissedSchedule(job))
      .sort((a, b) => {
        const aDelay = getMissedScheduleDelayMs(a) ?? 0
        const bDelay = getMissedScheduleDelayMs(b) ?? 0
        return bDelay - aDelay
      })
      .slice(0, 4)
  }, [scopedJobs])

  const filteredJobs = useMemo(() => {
    return [...panelJobs]
      .sort(sortJobs)
      .filter((job) => {
        switch (activeFilter) {
          case 'running':
            if (job.last_status !== 'running') return false
            break
          case 'waiting':
            if (!isScheduleWaitingStatus(job.last_status)) return false
            break
          case 'enabled':
            if (!job.enabled) return false
            break
          case 'paused':
            if (job.enabled) return false
            break
          case 'missed':
            if (!isMissedSchedule(job)) return false
            break
          case 'issues':
            // The Issues badge counts error, partial AND interrupted
            // (isScheduleIssueStatus), so filtering to 'error' alone made a
            // schedule that ended partial contribute to the count and then
            // vanish when the count was clicked — the badge said 1, the list
            // said nothing. hetzner-ssh hit exactly this: its workflow
            // succeeded, Pulse finalized partial, and the schedule became
            // unfindable.
            if (!isScheduleIssueStatus(job.last_status)) return false
            break
          case 'overlap':
            if (!potentialOverlaps.has(job.id)) return false
            break
          case 'all':
          default:
            break
        }

        const preset = presetMap.get(job.preset_query_id ?? '')
        const workflowMeta = getWorkflowFilterMeta(job, presetMap)
        const workflowLabel = workflowMeta.workflowLabel

        if (selectedWorkflowFilter !== 'all' && workflowMeta.value !== selectedWorkflowFilter) {
          return false
        }

        if (!normalizedSearch) return true

        const haystack = [
          job.name,
          job.description,
          workflowLabel,
          preset?.label,
          job.workspace_path,
          job.cron_expression,
        ]
          .filter(Boolean)
          .join(' ')
          .toLowerCase()

        return haystack.includes(normalizedSearch)
      })
  }, [panelJobs, activeFilter, normalizedSearch, presetMap, selectedWorkflowFilter, potentialOverlaps])

  // When the panel or explicit workflow filter already identifies one
  // automation, repeating "Automation / <name>" on every schedule adds noise.
  // Keep that identity only in the mixed-workflow list where it supplies
  // information the surrounding UI does not.
  const showWorkflowIdentityInScheduleRows = !isWorkflowScoped && selectedWorkflowFilter === 'all'

  const workflowGroups = useMemo<WorkflowScheduleGroup[]>(() => {
    const groups = new Map<string, WorkflowScheduleGroup>()

    filteredJobs.forEach((job) => {
      const preset = presetMap.get(job.preset_query_id ?? '')
      const workflowMeta = getWorkflowFilterMeta(job, presetMap)
      const existing = groups.get(workflowMeta.value)
      const group = existing ?? {
        key: workflowMeta.value,
        label: workflowMeta.workflowLabel,
        workspacePath: job.workspace_path || preset?.workspacePath || undefined,
        jobs: [],
        running: 0,
        missed: 0,
        issues: 0,
        overlap: 0,
        enabled: 0,
        paused: 0,
        nextRunAt: undefined,
        lastRunAt: undefined,
        runCount: 0,
      }

      group.jobs.push(job)
      group.runCount += job.run_count || 0
      if (job.last_status === 'running') group.running += 1
      if (isMissedSchedule(job)) group.missed += 1
      if (isScheduleIssueStatus(job.last_status)) group.issues += 1
      if (potentialOverlaps.has(job.id)) group.overlap += 1
      if (job.enabled) group.enabled += 1
      else group.paused += 1

      if (job.enabled && job.next_run_at && (!group.nextRunAt || job.next_run_at < group.nextRunAt)) {
        group.nextRunAt = job.next_run_at
      }
      if (job.last_run_at && (!group.lastRunAt || job.last_run_at > group.lastRunAt)) {
        group.lastRunAt = job.last_run_at
      }

      groups.set(workflowMeta.value, group)
    })

    return Array.from(groups.values())
      .map(group => ({ ...group, jobs: [...group.jobs].sort(sortJobs) }))
      .sort((a, b) => {
        if (a.running !== b.running) return b.running - a.running
        if (a.missed !== b.missed) return b.missed - a.missed
        if (a.issues !== b.issues) return b.issues - a.issues
        if (a.nextRunAt && b.nextRunAt) return a.nextRunAt.localeCompare(b.nextRunAt)
        if (a.nextRunAt) return -1
        if (b.nextRunAt) return 1
        return a.label.localeCompare(b.label)
      })
  }, [filteredJobs, presetMap, potentialOverlaps])

  const monthlyCalendar = useMemo(() => {
    const year = calendarMonth.getFullYear()
    const month = calendarMonth.getMonth()
    const monthKey = `${year}-${String(month + 1).padStart(2, '0')}`
    const localTimeZone = Intl.DateTimeFormat().resolvedOptions().timeZone
    const byDate: Record<string, CalendarEntry[]> = {}

    panelJobs.forEach((job) => {
      const workflowMeta = getWorkflowFilterMeta(job, presetMap)
      const label = workflowMeta.workflowLabel || job.name
      const jobTimezone = job.timezone || localTimeZone

      if (job.schedule_type === 'calendar' && job.calendar_items?.length) {
        job.calendar_items.forEach((item) => {
          if (!item.date || !item.time) return
          const localDate = scheduledDateTimeInLocal(item.date, item.time, jobTimezone)
          const localDateKey = dateKeyFromLocalDate(localDate)
          if (!localDateKey.startsWith(monthKey)) return
          byDate[localDateKey] = [
            ...(byDate[localDateKey] || []),
            {
              job,
              time: formatLocalTimeFromDate(localDate),
              label,
              note: item.description || job.name,
              sourceTime: item.time,
              timezone: jobTimezone,
            },
          ]
        })
        return
      }

      const monthsToExpand = [addMonths(calendarMonth, -1), calendarMonth, addMonths(calendarMonth, 1)]
      monthsToExpand.forEach((sourceMonth) => {
        expandCronForMonth(job, sourceMonth.getFullYear(), sourceMonth.getMonth()).forEach((occurrence) => {
          const localDate = scheduledDateTimeInLocal(occurrence.date, occurrence.time, jobTimezone)
          const localDateKey = dateKeyFromLocalDate(localDate)
          if (!localDateKey.startsWith(monthKey)) return
          byDate[localDateKey] = [
            ...(byDate[localDateKey] || []),
            {
              job,
              time: formatLocalTimeFromDate(localDate),
              label,
              note: `${job.name}${jobTimezone !== localTimeZone ? ` (${jobTimezone})` : ''}`,
              sourceTime: occurrence.time,
              timezone: jobTimezone,
            },
          ]
        })
      })
    })

    Object.values(byDate).forEach(items => items.sort((a, b) => a.time.localeCompare(b.time)))

    const first = new Date(year, month, 1)
    const daysInMonth = new Date(year, month + 1, 0).getDate()
    const cells: CalendarCell[] = []
    for (let i = 0; i < first.getDay(); i += 1) cells.push({ key: `empty-${i}`, items: [] })
    for (let day = 1; day <= daysInMonth; day += 1) {
      const date = `${year}-${String(month + 1).padStart(2, '0')}-${String(day).padStart(2, '0')}`
      cells.push({ key: date, day, date, items: byDate[date] || [] })
    }

    return {
      label: calendarMonth.toLocaleDateString([], { month: 'long', year: 'numeric' }),
      localTimeZone,
      cells,
      total: Object.values(byDate).reduce((sum, items) => sum + items.length, 0),
    }
  }, [calendarMonth, panelJobs, presetMap])

  const selectedCalendarCell = useMemo(
    () => monthlyCalendar.cells.find(cell => cell.date === selectedCalendarDate),
    [monthlyCalendar.cells, selectedCalendarDate],
  )

  useEffect(() => {
    if (!active || !hasRunningJob) return
    const interval = setInterval(() => {
      loadJobs()
    }, 5000)
    return () => clearInterval(interval)
  }, [hasRunningJob, loadJobs, active])

  const handleToggle = async (job: ScheduledJob) => {
    try {
      const updated = job.enabled
        ? await schedulerApi.disableJob(job.id)
        : await schedulerApi.enableJob(job.id)
      setJobs(prev => prev.map(j => j.id === job.id ? retainDurationAverage(updated, j) : j))
    } catch { /* ignore */ }
  }

  const handleRunDestination = async (job: ScheduledJob, runDestination: 'crew_chat' | 'isolated') => {
    try {
      const updated = await schedulerApi.updateJob(job.id, { run_destination: runDestination })
      setJobs(previous => previous.map(candidate => candidate.id === job.id ? retainDurationAverage(updated, candidate) : candidate))
      useChatStore.getState().addToast(runDestination === 'isolated'
        ? `“${job.name}” will run in its own automation conversation.`
        : `“${job.name}” will queue in the Crew chat.`, 'success')
    } catch (error) {
      const detail = error instanceof Error ? error.message : 'Unable to change run destination'
      useChatStore.getState().addToast(detail, 'error')
    }
  }

  const handleAfterRun = async (job: ScheduledJob, afterRun: ScheduleAfterRun) => {
    try {
      const updated = await schedulerApi.updateJob(job.id, { after_run: afterRun })
      setJobs(previous => previous.map(candidate => candidate.id === job.id ? retainDurationAverage(updated, candidate) : candidate))
    } catch (error) {
      const detail = error instanceof Error ? error.message : 'Unable to change after-run options'
      useChatStore.getState().addToast(detail, 'error')
    }
  }

  // Pause (or resume) every schedule belonging to a single workflow group in one click.
  // If any schedule in the group is enabled, pause them all; otherwise resume all paused ones.
  const handleToggleWorkflowGroupPause = async (group: WorkflowScheduleGroup) => {
    const targets = group.enabled > 0
      ? group.jobs.filter(j => j.enabled)
      : group.jobs.filter(j => !j.enabled)
    if (targets.length === 0) return
    const shouldPause = group.enabled > 0
    setBulkUpdatingGroupKey(group.key)
    try {
      const updated = await Promise.all(
        targets.map(j => shouldPause ? schedulerApi.disableJob(j.id) : schedulerApi.enableJob(j.id))
      )
      const updatedById = new Map(updated.map(u => [u.id, u]))
      setJobs(prev => prev.map(j => {
        const updated = updatedById.get(j.id)
        return updated ? retainDurationAverage(updated, j) : j
      }))
    } catch (e) {
      console.error('Failed to toggle workflow schedules:', e)
      loadJobs()
    } finally {
      setBulkUpdatingGroupKey(null)
    }
  }

  const handleDelete = async (job: ScheduledJob) => {
    if (!window.confirm(`Remove schedule for "${job.name}"?`)) return
    try {
      await schedulerApi.deleteJob(job.id)
      setJobs(prev => prev.filter(j => j.id !== job.id))
      useChatStore.getState().addToast(`Removed schedule "${job.name}"`, 'success')
    } catch (error) {
      const responseData = (error as { response?: { data?: unknown } })?.response?.data
      const detail = typeof responseData === 'string'
        ? responseData
        : typeof responseData === 'object' && responseData !== null && 'error' in responseData
          ? String((responseData as { error: unknown }).error)
          : error instanceof Error
            ? error.message
            : 'Unknown error'
      console.error('Failed to remove schedule:', error)
      useChatStore.getState().addToast(`Failed to remove schedule: ${detail}`, 'error')
    }
  }

  const handleTrigger = async (job: ScheduledJob) => {
    setTriggering(job.id)
    try {
      const result = await schedulerApi.triggerJob(job.id)
      useChatStore.getState().addToast(
        result.session_id === 'queued'
          ? `“${job.name}” was queued for a run.`
          : `Started a run for “${job.name}”.`,
        'success',
      )
      setTimeout(loadJobs, 1500)
    } catch (error) {
      const responseData = (error as { response?: { data?: unknown } })?.response?.data
      const detail = typeof responseData === 'string'
        ? responseData
        : typeof responseData === 'object' && responseData !== null && 'error' in responseData
          ? String((responseData as { error: unknown }).error)
          : error instanceof Error
            ? error.message
            : 'Unknown error'
      console.error('Failed to start schedule:', error)
      useChatStore.getState().addToast(
        `Failed to start schedule: ${detail.trim() || 'Unknown error'}`,
        'error',
      )
    }
    finally { setTriggering(null) }
  }

  const handleStopRun = async (job: ScheduledJob) => {
    if (!window.confirm('Stop this running execution?')) return
    try {
      await schedulerApi.stopJob(job.id)
      // Refresh after a brief delay to let status propagate
      setTimeout(() => {
        loadJobs()
      }, 1500)
    } catch (e) {
      console.error('Failed to stop run:', e)
    }
  }

  const toggleRunHistory = useCallback(async (job: ScheduledJob) => {
    const alreadyOpen = expandedRunHistoryJobIds.has(job.id)
    if (alreadyOpen) {
      setExpandedRunHistoryJobIds(current => {
        const next = new Set(current)
        next.delete(job.id)
        return next
      })
      return
    }

    setExpandedRunHistoryJobIds(current => new Set(current).add(job.id))
    if (runsByJob[job.id]) return
    setRunsLoadingJobIds(current => new Set(current).add(job.id))
    try {
      const response = await schedulerApi.getJobRuns(job.id, 200)
      setRunsByJob(current => ({ ...current, [job.id]: response.runs || [] }))
    } catch {
      setExpandedRunHistoryJobIds(current => {
        const next = new Set(current)
        next.delete(job.id)
        return next
      })
      useChatStore.getState().addToast(`Could not load execution history for ${job.name}`, 'error')
    } finally {
      setRunsLoadingJobIds(current => {
        const next = new Set(current)
        next.delete(job.id)
        return next
      })
    }
  }, [expandedRunHistoryJobIds, runsByJob])

  // Opens a scheduled run's session directly from run.session_id — no session
  // list to fetch first, unlike PreviousChatHistoryPanel's onSelectSession
  // (which resolves a ChatHistorySession because it's browsing durable chat
  // history broadly, not one workflow's own schedule runs).
  const openScheduledRun = useCallback(async (run: ScheduledJobRun, job: ScheduledJob) => {
    const sessionId = run.session_id
    if (!sessionId) return

    const chatStore = useChatStore.getState()
    const existingTab = Object.values(chatStore.chatTabs).find(t => t.sessionId === sessionId)

    if (job.entity_type === 'product') {
      if (existingTab) activateTab(existingTab.tabId)
      else chatStore.addToast('Open this project\'s Builder chat to view the scheduled conversation.', 'info')
      onClose()
      return
    }

    let effectivePresetQueryId = job.preset_query_id || existingTab?.metadata?.presetQueryId
    if (!effectivePresetQueryId) {
      try {
        const running = await agentApi.getRunningWorkflow(sessionId)
        effectivePresetQueryId = running.preset_query_id || undefined
      } catch {
        // Leave undefined rather than rebinding the scheduled run to whichever
        // workflow is currently open.
      }
    }

    if (effectivePresetQueryId) {
      selectWorkflowPreset(effectivePresetQueryId)
    }
    useModeStore.getState().setModeCategory('workflow')
    useWorkflowStore.getState().setShowChatArea(true)

    const desiredName = scheduleTabLabel(job.name)
    const metadata = {
      mode: 'workflow' as const,
      phaseId: undefined,
      phaseName: undefined,
      ...(effectivePresetQueryId ? { presetQueryId: effectivePresetQueryId } : {}),
      isViewOnly: true,
      isExecutionRun: true,
      isScheduledRun: true,
      scheduledJobName: job.name,
    }

    // Same resolution as every other opener: the tab already on this run,
    // else this schedule's finished lane rebound to it, else a new tab.
    const { tabId, via } = await resolveWorkflowTabForSession({
      getTabs: () => useChatStore.getState().chatTabs,
      presetQueryId: effectivePresetQueryId ?? '',
      sessionId,
      name: desiredName,
      metadata,
      createChatTab: chatStore.createChatTab,
      updateTabSessionId: chatStore.updateTabSessionId,
    })
    if (via !== 'created') {
      chatStore.setTabMetadata(tabId, metadata)
      if (useChatStore.getState().chatTabs[tabId]?.name !== desiredName) {
        useChatStore.setState((state) => {
          const t = state.chatTabs[tabId]
          if (!t) return state
          return { chatTabs: { ...state.chatTabs, [tabId]: { ...t, name: desiredName } } }
        })
      }
    }

    // Scheduled runs are execution diagnostics, not interactive chats. Read
    // their saved JSON artifact explicitly; never route it through SQLite chat.
    try {
      const workspacePath = stableScope?.workspacePath
        || (effectivePresetQueryId ? presetMap.get(effectivePresetQueryId)?.workspacePath : undefined)
        || undefined
      const runtime = await hydrateExecutionConversation(sessionId, workspacePath)
      chatStore.setTabCompleted(tabId, runtime.status !== 'running')
      chatStore.setTabStreaming(tabId, runtime.status === 'running')
      chatStore.setTabHasRunningBgAgents(tabId, !!runtime.hasRunningBackgroundAgents)
      chatStore.setTabSyntheticTurn(tabId, !!runtime.isSyntheticTurn)
      chatStore.setTabCanSteer(tabId, false)
    } catch {
      chatStore.addToast('Could not load the saved schedule conversation.', 'error')
      return
    }
    activateTab(tabId)
    onClose()
  }, [onClose, presetMap, stableScope?.workspacePath])

  const deleteScheduledRunSession = useCallback(async (run: ScheduledJobRun) => {
    const sessionId = run.session_id
    if (!sessionId || !workflowScope?.workspacePath) return
    if (!window.confirm('Delete this conversation record? The schedule execution itself remains.')) return

    setDeletingRunSessionIds(current => new Set(current).add(sessionId))
    try {
      await agentApi.deleteChatHistorySession(sessionId, workflowScope.workspacePath)
      useChatStore.getState().addToast('Deleted conversation record', 'success')
    } catch {
      useChatStore.getState().addToast('Failed to delete conversation record', 'error')
    } finally {
      setDeletingRunSessionIds(current => {
        const next = new Set(current)
        next.delete(sessionId)
        return next
      })
    }
  }, [workflowScope?.workspacePath])

  const handleToggleGlobalPause = async () => {
    if (!isSchedulerPaused && !window.confirm(`Pause all ${summary.enabled} active schedules? Timed runs will not start until scheduling is resumed.`)) return
    setIsUpdatingSchedulerPause(true)
    try {
      const updated = await schedulerApi.updateConfig({
        globally_paused: !isSchedulerPaused,
        paused_by: !isSchedulerPaused ? 'frontend-user' : '',
      })
      setSchedulerConfig(updated)
      if (isSchedulerPaused) setPauseCatchUp(updated.skipped_while_paused ?? [])
    } catch (e) {
      console.error('Failed to update scheduler config:', e)
    } finally {
      setIsUpdatingSchedulerPause(false)
    }
  }

  // Runs each chosen schedule once now. A pause may have skipped several
  // occurrences of one schedule; one catch-up run stands for them.
  const runPauseCatchUp = async (scheduleIds: string[]) => {
    if (scheduleIds.length === 0) return
    setIsRunningCatchUp(true)
    const failed: string[] = []
    for (const id of scheduleIds) {
      try {
        await schedulerApi.triggerJob(id)
      } catch {
        failed.push(id)
      }
    }
    setIsRunningCatchUp(false)
    setPauseCatchUp(prev => prev.filter(item => failed.includes(item.schedule_id)))
    if (failed.length > 0) useChatStore.getState().addToast(`${failed.length} catch-up run(s) could not start.`, 'error')
  }

  const toggleWorkflowGroup = useCallback((workflowKey: string) => {
    setExpandedWorkflowKeys(prev => (
      prev.includes(workflowKey)
        ? prev.filter(key => key !== workflowKey)
        : [...prev, workflowKey]
    ))
  }, [])

  const showJobInWorkflowGroups = useCallback((job: ScheduledJob) => {
    const workflowKey = getWorkflowFilterMeta(job, presetMap).value
    setSelectedWorkflowFilter(workflowKey)
    setActiveFilter('all')
    setSearchQuery(job.name)
    setFocusedScheduleId(job.id)
    setActiveView('schedules')
  }, [presetMap])

  const filterPills: Array<{ key: JobFilter; label: string; count: number }> = [
    { key: 'running', label: 'Running', count: summary.running },
    { key: 'waiting', label: 'Queued / waiting', count: summary.waiting },
    { key: 'enabled', label: 'Enabled', count: summary.enabled },
    { key: 'paused', label: 'Paused', count: summary.paused },
    { key: 'missed', label: 'Missed', count: summary.missed },
    { key: 'issues', label: 'Issues', count: summary.issues },
    { key: 'overlap', label: 'Possible overlap', count: summary.overlap },
    { key: 'all', label: 'All', count: summary.total },
  ]
  const activeFilterLabel = filterPills.find((pill) => pill.key === activeFilter)?.label ?? 'All'

  return {
    pauseCatchUp,
    isRunningCatchUp,
    runPauseCatchUp,
    dismissPauseCatchUp: () => setPauseCatchUp([]),
    isReadOnlyUser,
    isLoading,
    error,
    openActionMenuJobId,
    setOpenActionMenuJobId,
    expandedWorkflowKeys,
    isWorkflowScoped,
    activeView,
    setActiveView,
    setCalendarMonth,
    selectedCalendarDate,
    setSelectedCalendarDate,
    activeFilter,
    setActiveFilter,
    searchQuery,
    setSearchQuery,
    selectedWorkflowFilter,
    setSelectedWorkflowFilter,
    schedulerConfig,
    isUpdatingSchedulerPause,
    bulkUpdatingGroupKey,
    triggering,
    expandedRunHistoryJobIds,
    runsByJob,
    runsLoadingJobIds,
    deletingRunSessionIds,
    presetMap,
    panelJobs,
    panelTitle,
    loadJobs,
    isSchedulerPaused,
    workflowScheduleSummary,
    summary,
    normalizedSearch,
    workflowOptions,
    upcomingJobs,
    missedJobs,
    filteredJobs,
    showWorkflowIdentityInScheduleRows,
    workflowGroups,
    potentialOverlaps,
    monthlyCalendar,
    selectedCalendarCell,
    handleToggle,
    handleRunDestination,
    handleAfterRun,
    handleToggleWorkflowGroupPause,
    handleDelete,
    handleTrigger,
    handleStopRun,
    toggleRunHistory,
    openScheduledRun,
    deleteScheduledRunSession,
    handleToggleGlobalPause,
    toggleWorkflowGroup,
    showJobInWorkflowGroups,
    focusedScheduleId,
    filterPills,
    activeFilterLabel,
  }
}

export type ScheduleRunsPanelState = ReturnType<typeof useScheduleRunsData>

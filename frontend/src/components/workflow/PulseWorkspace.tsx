import { GoalProgress } from './GoalProgress'
import { PulseGoalWork } from './PulseGoalWork'
import { pulseFixStats, pulseFixSummary } from './pulseFixStats'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  CheckCircle2,
  Loader2,
  X,
} from 'lucide-react'
import { agentApi } from '../../services/api'
import { findProductCommand } from '../../commands/registry'
import type { CommandContext } from '../../commands/types'
import { DEFAULT_PULSE_AUTONOMY } from '../../services/api-types'
import { playbooksApi } from '../../api/playbooks'
import { useChatStore } from '../../stores/useChatStore'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'
import type {
  PulseAutonomy,
  PulseFinalCommandState,
  PulseGoalWorkItem,
  PulseFindingLifecycle,
  PulseImpactLedger,
  PulseModuleState,
  PulsePlanDriftDueItem,
  PulseReviewRecord,
  PulseReviewFocus,
  PulseReviewAudit,
  PulseReviewerModule,
} from '../../services/api-types'
import { WORKFLOW_LOG_REFRESH_EVENT } from './workflowEvents'
import { mergePulseReviewCoverage } from './pulseReviewCoverage'
import { PulseReviewOverview, type InstalledPlaybookReviewFocus } from './PulseReviewOverview'
import { SoulViewer, WORKFLOW_SOUL_REFRESH_EVENT } from './SoulViewer'
import { PulseFindingCard } from './PulseFindingCard'
import { pulseFindingPresentation, type PulseFindingQueue } from './pulseFindingPresentation'
import { isPulseOwnedFinding, pulseIssueForFinding } from './pulseModuleInspectorUtils'
import {
  buildPulseWorkspaceModuleSummaries,
  normalizePulseWorkspaceModule,
  pulseFindingReviewAreas,
  pulseFindingMatchesFocus,
  pulseWorkspaceQueueCounts,
  type PulseFocus,
} from './pulseWorkspaceUtils'
import {
  PULSE_MODULE_COMMANDS,
} from './canvas/pulseSections'

function readable(value?: string): string {
  const text = (value || '').trim().replaceAll('_', ' ')
  return text ? text.charAt(0).toUpperCase() + text.slice(1) : 'No data'
}

type ReviewFocusLabel = {
  label: string
  relatedCount: number
}

const FOCUS_TITLES: Record<PulseFocus, string> = {
  all: 'Current work',
  needs_action: 'Pulse to fix',
  queued_repair: 'Queued for Pulse',
  waiting_proof: 'Waiting for evidence',
  decisions: 'Your decisions',
  proposals: 'Proposed improvements',
  blocked: 'Paused',
  platform: 'Platform repair pending',
  resolved: 'Resolved',
  workflow_reported: 'Workflow evidence',
}

const FOCUS_HINTS: Record<PulseFocus, string> = {
  all: 'Open work, grouped by who or what can move it forward',
  needs_action: 'Issues Pulse can diagnose, repair, or reopen',
  queued_repair: 'Safe workflow repairs retained for a later Engineering pass',
  waiting_proof: 'Fixes and recommendations waiting for a verification check or future evidence',
  decisions: 'Items that cannot continue without your approval or direction',
  proposals: 'Ideas Pulse recommends considering; these are not waiting for your answer',
  blocked: 'Diagnosed issues with no safe action currently available',
  platform: 'Diagnosed runtime or product repairs outside this workflow',
  resolved: 'Verified fixes and legitimate no-change closures',
  workflow_reported: 'Evidence filed by workflow steps, kept separate from Pulse\u2019s repair queue',
}

const REVIEW_HISTORY_MODULES = [
  { id: 'plan_drift_review', label: 'Workflow Review' },
  { id: 'technical_review', label: 'Technical' },
  { id: 'architecture_review', label: 'Architecture' },
  { id: 'strategic_review', label: 'Strategic' },
] as const

export type PulseWorkspaceTab = 'for_you' | 'platform'

function reviewRunDate(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.getTime())
    ? value
    : date.toLocaleString(undefined, { day: 'numeric', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit' })
}

// Pulse tab buttons run the same product command a chat user would, so each
// action has one prompt. The hard-coded text below is only a fallback for when
// product commands have not loaded yet.
const PULSE_TAB_COMMANDS: Record<string, string> = {
  strategic_review: 'run-goal-work',
  technical_review: 'run-technical-review',
  plan_drift_review: 'run-plan-drift',
  architecture_review: 'run-architecture-review',
}

export function pulseTabReviewMessage(module: string, workspacePath = '<this workflow>'): string {
  const command = findProductCommand(PULSE_TAB_COMMANDS[module] ?? '', 'workflow', 'workshop')
  let message = ''
  command?.execute({
    beforeSlash: 'Requested from the Pulse tab.',
    onSubmit: (submitted: string) => { message = submitted },
    workshopMode: 'workshop',
  } as unknown as CommandContext)
  return message || manualPulseReviewMessage(module, workspacePath)
}

export function manualPulseReviewMessage(module: string, workspacePath = '<this workflow>'): string {
  switch (module) {
    case 'strategic_review':
      return 'Run a Goal Work pass for this workflow now. Call get_workflow_command_guidance(kind="strategy-auditor", focus="Manual Goal Work requested from the Pulse UI") and follow the returned instructions exactly. Run it as a background task so this chat stays responsive.'
    case 'architecture_review':
      return `Run a manual Architecture Review for this workflow now. First call record_pulse_result(workspace_path=${JSON.stringify(workspacePath)}, module="architecture_review", pulse_run_id="current", result="running", note_only=true, manual=true, reason="Manual Architecture Review requested from the Pulse UI"). If another Pulse pass owns it, report that collision and stop. Otherwise load read_skill(skills=[{"name":"builder-reference","path":"references/architecture-review.md"}]) and follow it exactly as a read-only review. Persist findings, decisions, impact records, and one terminal architecture_review result with focuses included; do not edit the workflow.`
    case 'technical_review':
      return 'Run the Technical Review for this workflow now as a background task. Call get_workflow_command_guidance(kind="engineering-review", focus="Manual Technical Review requested from the Pulse UI") and follow the returned instructions; after the review receipt is saved, call get_workflow_command_guidance(kind="pulse-fixer") and fix every issue it found in the same task, so each ends fixed, not a problem, a decision for the user, or platform-owned.'
    case 'plan_drift_review':
      return 'Run the Workflow Review (plan_drift_review) for this workflow now. Call get_workflow_command_guidance(kind="review-artifact-drift", focus="Manual Plan Drift requested from the Pulse UI") and follow the returned instructions exactly. Apply only the bounded compatibility repairs that Plan Drift authorizes.'
    default:
      throw new Error(`Unsupported Pulse review module: ${module}`)
  }
}

export function PulseWorkspace({
  activeTab,
  onTabChange,
  workspacePath,
  moduleStates,
  planDriftDue = false,
  planDriftDueItems = [],
  planDriftDueError = null,
  reviewFocuses,
  reviewFocusSelections,
  disabledReviewModules = [],
  reviewModuleSaving = null,
  onToggleReviewModule,
  statusError,
  goalWork = [],
  autonomy = DEFAULT_PULSE_AUTONOMY,
  autonomySaving = false,
  onChangeAutonomy,
  focusAreas = [],
  focusSaving = false,
  onSaveFocusAreas,
}: {
  activeTab?: PulseWorkspaceTab
  onTabChange?: (tab: PulseWorkspaceTab) => void
  workspacePath: string
  moduleStates: PulseModuleState[]
  planDriftDue?: boolean
  planDriftDueItems?: PulsePlanDriftDueItem[]
  planDriftDueError?: string | null
  finalCommandStates: PulseFinalCommandState[]
  reviewFocuses: PulseReviewFocus[]
  reviewFocusSelections: PulseReviewFocus[]
  disabledReviewModules?: PulseReviewerModule[]
  reviewModuleSaving?: PulseReviewerModule | null
  onToggleReviewModule?: (module: PulseReviewerModule) => void
  statusError: string | null
  goalWork?: PulseGoalWorkItem[]
  autonomy?: PulseAutonomy
  autonomySaving?: boolean
  onChangeAutonomy?: (next: PulseAutonomy) => void
  focusAreas?: string[]
  focusSaving?: boolean
  onSaveFocusAreas?: (areas: string[]) => Promise<boolean>
}) {
  const loadVersion = useRef(0)
  const [findings, setFindings] = useState<PulseFindingLifecycle[]>([])
  const fixStats = useMemo(() => pulseFixStats(findings), [findings])
  const [reviews, setReviews] = useState<PulseReviewRecord[]>([])
  const [coverage, setCoverage] = useState<PulseReviewFocus[]>([])
  const [audits, setAudits] = useState<PulseReviewAudit[]>([])
  const [impact, setImpact] = useState<PulseImpactLedger>({ interventions: [], observations: [], assessments: [] })
  const [playbookFocuses, setPlaybookFocuses] = useState<InstalledPlaybookReviewFocus[]>([])
  const [focus, setFocus] = useState<PulseFocus>('all')
  const [moduleFilter, setModuleFilter] = useState<string | null>(null)
  // For you = Goal Work (Pulse's main job); Platform health = Drift,
  // Technical and Architecture upkeep (docs/design/pulse_goal_work.md).
  const [internalTab, setInternalTab] = useState<PulseWorkspaceTab>('for_you')
  const tab = activeTab ?? internalTab
  const setTab = useCallback((next: PulseWorkspaceTab) => {
    setInternalTab(next)
    onTabChange?.(next)
  }, [onTabChange])
  const [selectedReviewModule, setSelectedReviewModule] = useState<string>('')
  const [manualReviewStarting, setManualReviewStarting] = useState<string | null>(null)
  const [expandedFinding, setExpandedFinding] = useState<string | null>(null)
  const [showCompleteBacklog, setShowCompleteBacklog] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async (showLoading = true) => {
    if (!workspacePath) return
    const version = ++loadVersion.current
    if (showLoading) setLoading(true)
    if (showLoading) setError(null)
    const [findingResult, reviewResult, impactResult, playbookResult] = await Promise.allSettled([
      agentApi.getPulseFindings(workspacePath),
      agentApi.getPulseReviews(workspacePath),
      agentApi.getPulseImpact(workspacePath),
      Promise.all([playbooksApi.list(), playbooksApi.listInstalled(workspacePath)]),
    ])
    if (version !== loadVersion.current) return
    const errors: string[] = []
    if (findingResult.status === 'fulfilled' && findingResult.value.success) {
      setFindings(findingResult.value.findings || [])
    } else {
      setFindings([])
      errors.push(
        findingResult.status === 'rejected'
          ? (findingResult.reason instanceof Error ? findingResult.reason.message : 'Could not load findings.')
          : findingResult.value.error || 'Could not load findings.',
      )
    }
    if (reviewResult.status === 'fulfilled' && reviewResult.value.success) {
      setReviews(reviewResult.value.reviews || [])
      setCoverage(reviewResult.value.coverage || [])
      setAudits(reviewResult.value.audits || [])
    } else {
      setReviews([])
      setCoverage([])
      setAudits([])
      errors.push(
        reviewResult.status === 'rejected'
          ? (reviewResult.reason instanceof Error ? reviewResult.reason.message : 'Could not load reviews.')
          : reviewResult.value.error || 'Could not load reviews.',
      )
    }
    if (impactResult.status === 'fulfilled' && impactResult.value.success) {
      setImpact(impactResult.value.impact || { interventions: [], observations: [], assessments: [] })
    } else {
      setImpact({ interventions: [], observations: [], assessments: [] })
      errors.push(
        impactResult.status === 'rejected'
          ? (impactResult.reason instanceof Error ? impactResult.reason.message : 'Could not load goal impact.')
          : impactResult.value.error || 'Could not load goal impact.',
      )
    }
    if (playbookResult.status === 'fulfilled') {
      const [catalog, installed] = playbookResult.value
      const installedIDs = new Set(installed.filter(item => item.status !== 'disabled').map(item => item.id))
      setPlaybookFocuses(catalog.filter(playbook => installedIDs.has(playbook.id)).flatMap(playbook =>
        (playbook.pulseFocus || []).filter(focus => focus.module === 'strategic_review').map(focus => ({
          playbookId: playbook.id,
          playbookTitle: playbook.title,
          module: focus.module,
          label: focus.label,
          focusAreas: focus.focus_areas,
          reviewWhen: focus.review_when,
        })),
      ))
    } else {
      // Playbook guidance supplements Pulse history, so a catalog outage should not hide review results.
      setPlaybookFocuses([])
    }
    setError(errors.length > 0 ? errors.join(' ') : null)
    setLoading(false)
  }, [workspacePath])

  useEffect(() => {
    setFocus('all')
    setModuleFilter(null)
    setSelectedReviewModule('')
    setTab('for_you')
    setExpandedFinding(null)
    setShowCompleteBacklog(false)
    setCoverage([])
    setAudits([])
    setFindings([])
    setReviews([])
    setImpact({ interventions: [], observations: [], assessments: [] })
    setPlaybookFocuses([])
    void load()
  }, [load, setTab])

  const runReviewNow = useCallback(async (module: string) => {
    if (manualReviewStarting) return
    setManualReviewStarting(module)
    try {
      await sendWorkspacePaneMessageToChat({ workspacePath, message: pulseTabReviewMessage(module, workspacePath) })
      useChatStore.getState().addToast(`${module === 'strategic_review' ? 'Goal Work' : module === 'architecture_review' ? 'Architecture Review' : module === 'technical_review' ? 'Technical Review' : 'Workflow Review'} opened in chat`, 'success')
    } catch (err) {
      useChatStore.getState().addToast(err instanceof Error ? err.message : 'Could not start the review', 'error')
    } finally {
      setManualReviewStarting(null)
    }
  }, [manualReviewStarting, workspacePath])

  useEffect(() => {
    const onRefresh = () => { void load(false) }
    window.addEventListener(WORKFLOW_LOG_REFRESH_EVENT, onRefresh)
    window.addEventListener(WORKFLOW_SOUL_REFRESH_EVENT, onRefresh)
    return () => {
      window.removeEventListener(WORKFLOW_LOG_REFRESH_EVENT, onRefresh)
      window.removeEventListener(WORKFLOW_SOUL_REFRESH_EVENT, onRefresh)
    }
  }, [load])

  const areaFindings = useMemo(() => findings.filter((finding) => !moduleFilter
    || pulseFindingReviewAreas(finding, reviewFocusSelections).includes(moduleFilter)),
  [findings, moduleFilter, reviewFocusSelections])
  const queueCounts = useMemo(() => pulseWorkspaceQueueCounts(areaFindings), [areaFindings])
  const moduleSummaries = useMemo(
    () => buildPulseWorkspaceModuleSummaries(PULSE_MODULE_COMMANDS, findings, reviews, reviewFocusSelections),
    [findings, reviews, reviewFocusSelections],
  )
  const reviewFocusByIssueID = useMemo(() => {
    const labels = new Map<string, string[]>()
    reviewFocusSelections.forEach((selection) => {
      if (moduleFilter && normalizePulseWorkspaceModule(selection.module) !== moduleFilter) return
      const label = `${readable(normalizePulseWorkspaceModule(selection.module))} › ${readable(selection.focus_key)}`
      selection.issue_ids?.forEach((issueID) => {
        const normalizedID = issueID.trim().toUpperCase()
        if (!normalizedID) return
        const existing = labels.get(normalizedID) || []
        if (!existing.includes(label)) existing.push(label)
        labels.set(normalizedID, existing)
      })
    })
    return new Map<string, ReviewFocusLabel>(
      Array.from(labels, ([issueID, focusLabels]) => [
        issueID,
        { label: focusLabels[0], relatedCount: Math.max(0, focusLabels.length - 1) },
      ]),
    )
  }, [reviewFocusSelections, moduleFilter])

  const reviewRunHistory = useMemo(() => REVIEW_HISTORY_MODULES.map((module) => ({
    ...module,
    runs: reviews
      .filter((review) => normalizePulseWorkspaceModule(review.module) === module.id)
      .sort((left, right) => right.recorded_at.localeCompare(left.recorded_at)),
  })), [reviews])

  const matchingFindings = useMemo(
    () => {
      const matched = areaFindings
        .filter((finding) => pulseFindingMatchesFocus(finding, focus))
        .sort((a, b) => {
          const rank: Record<PulseFindingQueue, number> = {
            needs_action: 6,
            queued_repair: 5,
            waiting_proof: 4,
            decisions: 3,
            proposals: 2,
            blocked: 1,
            platform: 0,
            workflow_reported: 0,
            resolved: 0,
          }
          const priority = rank[pulseFindingPresentation(b).queue] - rank[pulseFindingPresentation(a).queue]
          return priority || (b.last_seen_at || '').localeCompare(a.last_seen_at || '')
        })
      return matched
    },
    [areaFindings, focus],
  )
  // Keep the initial dashboard scannable, but never hide the complete backlog
  // behind an unexplained cap. Queue and module filters always show every match;
  // the unfiltered backlog exposes an explicit one-click expansion.
  const attentionFindings = useMemo(
    () => focus === 'all' && !moduleFilter && !showCompleteBacklog
      ? matchingFindings.slice(0, 25)
      : matchingFindings,
    [focus, matchingFindings, moduleFilter, showCompleteBacklog],
  )
  if (loading && findings.length === 0 && reviews.length === 0) {
    return (
      <div className="space-y-4">
        <div className="flex min-h-96 items-center justify-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="h-4 w-4 animate-spin" />
          Loading Pulse workspace…
        </div>
      </div>
    )
  }

  const openIssueCount = pulseWorkspaceQueueCounts(findings).all
  const driftBlocksRun = planDriftDue || !!planDriftDueError
  return (
    <div className="space-y-4">
      <div role="tablist" aria-label="Pulse views" className="flex gap-1 rounded-lg border bg-muted/30 p-1">
        {([['for_you', 'For you'], ['platform', 'Platform health']] as const).map(([value, label]) => (
          <button key={value} type="button" role="tab" aria-selected={tab === value} onClick={() => setTab(value)}
            className={`flex flex-1 items-center justify-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-semibold transition-colors ${tab === value ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}`}>
            {label}
            {value === 'platform' && (openIssueCount > 0 || planDriftDue) && <span className={`rounded-full px-1.5 py-0.5 text-[9px] tabular-nums ${planDriftDue ? 'bg-amber-500/15 text-amber-700 dark:text-amber-300' : 'bg-muted text-muted-foreground'}`}>{planDriftDue ? 'Drift due' : openIssueCount}</span>}
          </button>
        ))}
      </div>

      {(error || statusError) && (
        <div className="rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-700 dark:text-amber-300">
          Some Pulse data could not be loaded: {[statusError, error].filter(Boolean).join(' ')}
        </div>
      )}

      {tab === 'for_you' ? <>
        <SoulViewer workspacePath={workspacePath} pulseSummary />
        <GoalProgress workspacePath={workspacePath} impact={impact} />
        <PulseGoalWork workspacePath={workspacePath} items={goalWork} autonomy={autonomy} autonomySaving={autonomySaving}
          onChangeAutonomy={onChangeAutonomy} onRunGoalWork={() => { void runReviewNow('strategic_review') }}
          focusAreas={focusAreas} focusSaving={focusSaving} onSaveFocusAreas={onSaveFocusAreas}
          playbookFocusAreas={playbookFocuses.flatMap(item => item.focusAreas.map(area => ({ area, source: item.playbookTitle })))}
          running={manualReviewStarting === 'strategic_review'}
          runBlockedReason={driftBlocksRun ? 'Workflow Review is due: Goal Work will prepare and research but not run workflow steps or change the workflow.' : undefined} />
        {(findings.length > 0 || planDriftDue) && (
          <button type="button" onClick={() => setTab('platform')} className="w-full rounded-lg border bg-muted/20 px-3 py-2 text-left text-xs text-muted-foreground hover:bg-muted/40">
            <span className="font-medium text-foreground">Platform health:</span> {pulseFixSummary(fixStats)}{planDriftDue ? ' · Workflow Review due' : ''}
          </button>
        )}
      </> : <>
      <p className="rounded-lg border bg-muted/20 px-3 py-2 text-xs text-muted-foreground" aria-label="Fix speed">{pulseFixSummary(fixStats)}</p>
      <PulseReviewOverview platformOnly moduleStates={moduleStates} planDriftDue={planDriftDue} planDriftDueItems={planDriftDueItems} planDriftDueError={planDriftDueError} coverage={mergePulseReviewCoverage(coverage, reviewFocuses, reviewFocusSelections)}
        audits={audits} findings={findings} moduleFilter={selectedReviewModule || null} reviewFocusSelections={reviewFocusSelections}
        playbookFocuses={playbookFocuses}
        disabledReviewModules={disabledReviewModules} reviewModuleSaving={reviewModuleSaving} onToggleReviewModule={onToggleReviewModule}
        runningReviewModule={manualReviewStarting} onRunReviewModule={runReviewNow}
        onSelectModule={module => {
          const counts = pulseWorkspaceQueueCounts(findings.filter(item => pulseFindingReviewAreas(item, reviewFocusSelections).includes(module)))
          setSelectedReviewModule(module)
          setModuleFilter(module)
          setFocus(module === 'plan_drift_review' && counts.all === 0 && counts.resolved > 0 ? 'resolved' : 'all')
          setShowCompleteBacklog(false)
          setExpandedFinding(null)
        }} />

      <div className="grid gap-4">
        <section className="overflow-hidden rounded-xl border bg-background">
          <div className="border-b px-4 py-3">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div>
                <h3 className="text-sm font-semibold text-foreground">Maintenance issues</h3>
                <p className="mt-0.5 text-[11px] text-muted-foreground">
                  {FOCUS_TITLES[focus]}
                  {moduleFilter && (
                    <span className="ml-1 font-normal">
                      in {moduleFilter === 'plan_drift_review' ? 'Workflow Review' : moduleSummaries.find((m) => m.id === moduleFilter)?.label || moduleFilter}
                    </span>
                  )}
                  {' · '}{FOCUS_HINTS[focus]}
                </p>
              </div>
              <div className="flex flex-wrap items-center gap-2">
                {moduleFilter && (
                  <button
                    type="button"
                    aria-label="Clear review area filter"
                    onClick={() => { setModuleFilter(null); setShowCompleteBacklog(false) }}
                    className="flex items-center gap-1 rounded-full border border-primary/35 bg-primary/10 px-2 py-1 text-[10px] font-semibold text-primary"
                  >
                    {moduleFilter === 'plan_drift_review' ? 'Workflow Review' : moduleSummaries.find((module) => module.id === moduleFilter)?.label || readable(moduleFilter)}
                    <X className="h-3 w-3" />
                  </button>
                )}
                {(focus !== 'all' || moduleFilter) && (
                  <button
                    type="button"
                    onClick={() => { setFocus('all'); setModuleFilter(null); setShowCompleteBacklog(false) }}
                    className="rounded-full border px-2 py-1 text-[10px] font-semibold text-muted-foreground hover:bg-muted"
                  >
                    Clear filter
                  </button>
                )}
                {focus === 'all' && !moduleFilter && matchingFindings.length > 25 && (
                  <button
                    type="button"
                    onClick={() => setShowCompleteBacklog((shown) => !shown)}
                    className="rounded-full border px-2 py-1 text-[10px] font-semibold text-primary hover:bg-primary/10"
                  >
                    {showCompleteBacklog ? 'Show first 25' : `Show all ${matchingFindings.length}`}
                  </button>
                )}
                <span className="rounded-full bg-muted px-2 py-1 text-[10px] font-semibold text-muted-foreground">
                  {attentionFindings.length === matchingFindings.length
                    ? `${attentionFindings.length} shown`
                    : `${attentionFindings.length} of ${matchingFindings.length} shown`}
                </span>
              </div>
            </div>
            <div className="mt-3 flex flex-wrap gap-1.5" aria-label="Issue filters">
              {([
                ['all', 'Open', queueCounts.all],
                ['resolved', 'Closed', queueCounts.resolved],
              ] as Array<[PulseFocus, string, number]>).map(([value, label, count]) => (
                <button
                  key={value}
                  type="button"
                  aria-pressed={focus === value}
                  onClick={() => { setFocus(value); setShowCompleteBacklog(false) }}
                  className={`rounded-full border px-2.5 py-1 text-[10px] font-semibold transition-colors ${
                    focus === value
                      ? 'border-primary/35 bg-primary/10 text-primary'
                      : 'text-muted-foreground hover:bg-muted hover:text-foreground'
                  }`}
                >
                  {label} <span className="ml-1 tabular-nums opacity-75">{count}</span>
                </button>
              ))}
            </div>
          </div>
          {attentionFindings.length === 0 ? (
            <div className="flex min-h-40 flex-col items-center justify-center px-6 py-8 text-center">
              <CheckCircle2 className="h-5 w-5 text-emerald-500" />
              <div className="mt-2 text-sm font-medium text-foreground">
                {moduleFilter === 'plan_drift_review' && focus === 'all' ? 'No current drift findings' : focus === 'resolved' ? 'No resolved issues yet' : 'Nothing in this queue'}
              </div>
              <div className="mt-1 text-xs text-muted-foreground">{moduleFilter === 'plan_drift_review' && focus === 'all' ? 'Completed drift checks and their details are shown above.' : <>Choose another queue{moduleFilter ? ' or clear the review area filter' : ' or inspect a review area above'}.</>}</div>
            </div>
          ) : (
            <div className="space-y-2 p-3">
              {attentionFindings.map((finding) => {
                const moduleID = moduleFilter || pulseFindingReviewAreas(finding, reviewFocusSelections)[0]
                const module = moduleSummaries.find((item) => item.id === moduleID)
                const issueID = pulseIssueForFinding(finding).id.toUpperCase()
                const reviewFocus = reviewFocusByIssueID.get(issueID) || (isPulseOwnedFinding(finding)
                  ? { label: module ? `${module.label} › Unclassified` : 'Review area unassigned', relatedCount: 0 }
                  : undefined)
                return (
                  <PulseFindingCard
                    key={issueID}
                    finding={finding}
                    moduleLabel={module?.label}
                    reviewFocus={reviewFocus}
                    expanded={expandedFinding === issueID}
                    onToggle={() => setExpandedFinding(
                      expandedFinding === issueID ? null : issueID,
                    )}
                    onOpenModule={moduleID ? () => {
                      setSelectedReviewModule(moduleID)
                      setModuleFilter(moduleID)
                      setFocus('all')
                      setShowCompleteBacklog(false)
                    } : undefined}
                  />
                )
              })}
            </div>
          )}
        </section>

      </div>


      <section className="overflow-hidden rounded-xl border bg-background" aria-label="Review run history">
        <div className="border-b px-4 py-3">
          <h3 className="text-sm font-semibold text-foreground">Review run history</h3>
          <p className="mt-0.5 text-[11px] text-muted-foreground">Recorded completed runs and their dates for each Pulse reviewer</p>
        </div>
        <div className="grid gap-px bg-border sm:grid-cols-2 lg:grid-cols-4">
          {reviewRunHistory.map((module) => (
            <div key={module.id} className="min-w-0 bg-background p-3">
              <div className="flex items-center justify-between gap-2">
                <span className="text-xs font-semibold text-foreground">{module.label}</span>
                <span className="rounded-full border bg-muted px-2 py-0.5 text-[9px] font-semibold tabular-nums text-muted-foreground">
                  {module.runs.length} {module.runs.length === 1 ? 'run' : 'runs'}
                </span>
              </div>
              {module.runs.length === 0 ? (
                <p className="mt-2 text-[10px] text-muted-foreground">No completed runs recorded.</p>
              ) : (
                <>
                  <div className="mt-2 text-[9px] font-semibold uppercase tracking-wide text-muted-foreground">Recent dates</div>
                  <ul className="mt-1 space-y-1 text-[10px] text-foreground">
                    {module.runs.slice(0, 3).map((run) => <li key={run.id} className="tabular-nums">{reviewRunDate(run.recorded_at)}</li>)}
                  </ul>
                  {module.runs.length > 3 && (
                    <details className="mt-2 text-[10px] text-muted-foreground">
                      <summary className="cursor-pointer font-medium text-primary">View all {module.runs.length} run dates</summary>
                      <ul className="mt-1 space-y-1 pl-2 tabular-nums">
                        {module.runs.map((run) => <li key={run.id}>{reviewRunDate(run.recorded_at)}</li>)}
                      </ul>
                    </details>
                  )}
                </>
              )}
            </div>
          ))}
        </div>
      </section>
      </>}

    </div>
  )
}

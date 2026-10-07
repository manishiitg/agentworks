import type { ReactNode } from 'react'
import { AlertTriangle, GitCompare, Lightbulb, Loader2, Play, Wrench, Blocks } from 'lucide-react'
import type { PulseFindingLifecycle, PulseModuleState, PulsePlanDriftDueItem, PulseReviewAudit, PulseReviewFocus, PulseReviewerModule } from '../../services/api-types'
import { normalizePulseWorkspaceModule, pulseFindingReviewAreas, pulseWorkspaceQueueCounts } from './pulseWorkspaceUtils'
import { pulseReviewDate, reviewCoverageForArea, TECHNICAL_REVIEW_AREAS, ARCHITECTURE_REVIEW_AREAS } from './pulseReviewCoverage'

const labels: Record<string, string> = { technical_review: 'Technical review', architecture_review: 'Architecture', strategic_review: 'Strategic review', plan_drift_review: 'Workflow Review', done: 'Completed', changed: 'Changes made', timed_out: 'Timed out' }
const readable = (text?: string) => labels[text || ''] || (text ? text.charAt(0).toUpperCase() + text.slice(1).replaceAll('_', ' ') : 'Recorded')

function nextAssessment(state?: PulseModuleState): string | null {
  if (state?.next_check_at) return pulseReviewDate(state.next_check_at)
  if (state?.next_check_after_run_id) return `After workflow run ${state.next_check_after_run_id}`
  if (state?.cooldown_runs) return `After ${state.cooldown_runs} scheduled ${state.cooldown_runs === 1 ? 'run' : 'runs'}`
  return null
}

export type InstalledPlaybookReviewFocus = {
  playbookId: string
  playbookTitle: string
  module: 'strategic_review'
  label: string
  focusAreas: string[]
  reviewWhen: string[]
}

function Evidence({ items, title = 'Evidence' }: { items?: string[]; title?: string }) {
  if (!items?.length) return null
  return <div className="mt-3"><h5 className="font-medium text-foreground">{title}</h5>
    <ul className="mt-1 list-disc space-y-1 break-words pl-4">{items.map((item, i) => <li key={i}>{item}</li>)}</ul>
  </div>
}

function FocusDetails({ items, findings }: { items: PulseReviewFocus[]; findings: PulseFindingLifecycle[] }) {
  return <div className="space-y-4">
    {!items.length && <p>No review recorded for this scope. A Pulse tick or general store check does not establish coverage.</p>}
    {items.map(item => <div key={`${item.focus_key}:${item.route_scope}:${item.last_pulse_run_id}`}>
      <p className="font-medium text-foreground">{item.route_scope || 'General review'} · {pulseReviewDate(item.last_reviewed_at)}</p>
      <p className="mt-1">{item.last_verdict || item.last_selection_reason || 'Review recorded without a summary.'}</p>
      <Evidence items={item.evidence} />
      {findings.filter(finding => item.issue_ids?.includes(finding.finding_id || '')).map(issue => <div key={issue.finding_id || issue.text} className="mt-3 border-l-2 pl-3">
        <p className="font-medium text-foreground">{issue.text}</p>
        {issue.fix_attempts.map(attempt => <p key={attempt.attempt_id} className="mt-1">{attempt.summary} · {readable(attempt.status)}</p>)}
        {issue.verifications.map((check, index) => <div key={index} className="mt-2"><p>{check.check} · <strong>{readable(check.verdict)}</strong></p>
          {check.observed && <p>{check.observed}</p>}<Evidence items={check.evidence} /></div>)}
      </div>)}
    </div>)}
  </div>
}

export function PulseReviewOverview({ platformOnly = false, goalLeadOwnsReviews = false, moduleStates, planDriftDue = false, planDriftDueItems = [], planDriftDueError = null, coverage, audits, findings, moduleFilter, onSelectModule, reviewFocusSelections = [], playbookFocuses = [], disabledReviewModules = [], reviewModuleSaving = null, onToggleReviewModule, runningReviewModule = null, onRunReviewModule, strategySupplement }: {
  /** Platform health tab: Drift, Technical and Architecture only. Goal Work
   * (strategic_review) has its own "For you" view. */
  platformOnly?: boolean;
  /** A workflow with a goal: its Pulse conversation owns QA and architecture
   * (PLAT-697), so they get no panels of their own here. */
  goalLeadOwnsReviews?: boolean;
  moduleStates: PulseModuleState[]; coverage: PulseReviewFocus[]; audits: PulseReviewAudit[];
  planDriftDue?: boolean; planDriftDueItems?: PulsePlanDriftDueItem[]; planDriftDueError?: string | null;
  findings: PulseFindingLifecycle[]; moduleFilter: string | null;
  onSelectModule: (module: string) => void; reviewFocusSelections?: PulseReviewFocus[];
  playbookFocuses?: InstalledPlaybookReviewFocus[];
  disabledReviewModules?: PulseReviewerModule[]; reviewModuleSaving?: PulseReviewerModule | null;
  onToggleReviewModule?: (module: PulseReviewerModule) => void;
  runningReviewModule?: string | null; onRunReviewModule?: (module: string) => void | Promise<void>;
  strategySupplement?: ReactNode;
}) {
  const areas = [
    { id: 'plan_drift_review', label: 'Workflow Review', description: 'Changes to the plan and their follow-up checks.', Icon: GitCompare },
    { id: 'technical_review', label: 'Technical', description: 'Correctness, failures, and regressions.', Icon: Wrench },
    { id: 'architecture_review', label: 'Architecture', description: 'Better prompts, orchestration, learning, knowledge, data and reports.', Icon: Blocks },
    { id: 'strategic_review', label: 'Strategy', description: 'Progress toward the goal and recommendations.', Icon: Lightbulb },
  ]
  const ownedByPulse = (id: string) => goalLeadOwnsReviews && (id === 'technical_review' || id === 'architecture_review')
  const selected = areas.find(area => area.id === moduleFilter && !ownedByPulse(area.id))
  const latestAuditFor = (module: string) => audits.find(item => normalizePulseWorkspaceModule(item.module) === module && item.result !== 'skipped')
  const stateFor = (module: string) => moduleStates.find(item => normalizePulseWorkspaceModule(item.module) === module)
  const coverageFor = (module: string) => coverage.filter(item => normalizePulseWorkspaceModule(item.module) === module)
  const playbookFocusFor = (module: string) => playbookFocuses.filter(item => item.module === module)
  const latestCoverageFor = (module: string) => [...coverageFor(module)].sort((a, b) => (b.last_reviewed_at || '').localeCompare(a.last_reviewed_at || ''))[0]
  const drift = stateFor('plan_drift_review')
  const driftAudit = latestAuditFor('plan_drift_review')
  const pendingDrift = drift?.last_gate_decision === 'due' && drift.last_pulse_run_id !== driftAudit?.pulse_run_id
  const liveDriftDue = planDriftDue || pendingDrift
  const driftStatus = liveDriftDue ? 'Due now' : driftAudit
    ? `${['done', 'changed'].includes(driftAudit.result) ? 'Checked' : readable(driftAudit.result)} · ${pulseReviewDate(driftAudit.recorded_at)}`
    : 'No completed check recorded'
  const selectedAudit = selected && latestAuditFor(selected.id)
  const selectedState = selected && stateFor(selected.id)
  const selectedNextAssessment = nextAssessment(selectedState || undefined)
  const selectedCoverage = selected ? coverageFor(selected.id) : []
  const latestCoverage = selected && latestCoverageFor(selected.id)
  const selectedPlaybookFocuses = selected ? playbookFocusFor(selected.id) : []
  const driftCounts = pulseWorkspaceQueueCounts(findings.filter(item => pulseFindingReviewAreas(item, reviewFocusSelections).includes('plan_drift_review')))
  const reviewCard = (area: (typeof areas)[number], prominent = false) => {
    const module = area.id as PulseReviewerModule
    const disabled = disabledReviewModules.includes(module)
    const audit = latestAuditFor(area.id)
    const lastReviewed = audit?.recorded_at || latestCoverageFor(area.id)?.last_reviewed_at
    const recommendedFocus = area.id === 'strategic_review' ? playbookFocusFor(area.id) : []
    const active = moduleFilter === area.id
    const running = runningReviewModule === area.id
    const runBlocked = liveDriftDue || !!planDriftDueError || (!!runningReviewModule && !running)
    return <div key={area.id} className={`overflow-hidden rounded-xl border transition-colors ${prominent ? 'shadow-sm' : ''} ${active ? 'border-primary/40 bg-primary/10' : disabled ? 'bg-muted/20' : 'bg-background'}`}>
      <button type="button" aria-pressed={active} onClick={() => onSelectModule(area.id)} className={`w-full text-left hover:bg-muted/30 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary ${prominent ? 'p-4' : 'p-3'}`}>
        <span className={`flex items-center gap-2 font-semibold ${prominent ? 'text-sm' : 'text-xs'}`}><area.Icon className={prominent ? 'h-5 w-5 shrink-0 text-primary' : 'h-4 w-4 shrink-0'} />{area.label}{disabled && <span className="ml-auto rounded-full border border-border bg-muted px-1.5 py-0.5 text-[9px] font-medium uppercase tracking-wide text-muted-foreground">Automatic off</span>}</span>
        {prominent && <span className="mt-2 block text-xs leading-5 text-muted-foreground">Connects workflow results to goals and metrics, then recommends what should change next.</span>}
        <span className={`mt-2 block text-[11px] leading-5 ${liveDriftDue && !disabled ? 'font-medium text-amber-700 dark:text-amber-300' : 'text-muted-foreground'}`}>{disabled ? 'Disabled for automatic Pulse runs' : liveDriftDue ? 'Waiting for Workflow Review' : lastReviewed ? `Last reviewed ${pulseReviewDate(lastReviewed)}` : 'No review recorded'}</span>
        {area.id === 'strategic_review' && <span className="mt-1.5 block text-[11px] leading-4 text-foreground/80">
          <span className="font-medium">Strategic focus:</span> {recommendedFocus.length > 0
            ? recommendedFocus.flatMap(item => item.focusAreas).slice(0, 2).join(' · ')
            : 'None configured'}
        </span>}
      </button>
      <div className="flex flex-wrap items-center justify-between gap-2 border-t border-border/70 px-3 py-2">
        <div className="flex items-center gap-2 text-[10px] text-muted-foreground">
          <span>Run automatically</span>
          <button type="button" role="switch" aria-label={`Include ${area.label} reviewer in Pulse reviews`} aria-checked={!disabled} disabled={!!reviewModuleSaving || !onToggleReviewModule} onClick={() => onToggleReviewModule?.(module)} className={`relative inline-flex h-4 w-7 items-center rounded-full transition-colors disabled:opacity-50 ${disabled ? 'bg-muted-foreground/30' : 'bg-primary'}`}>
            <span className={`h-3 w-3 rounded-full bg-white shadow-sm transition-transform ${disabled ? 'translate-x-0.5' : 'translate-x-3.5'}`} />
          </button>
        </div>
        <button type="button" aria-label={`Run ${area.label} review now`} disabled={!onRunReviewModule || runBlocked} title={liveDriftDue || planDriftDueError ? 'Run Workflow Review successfully first' : disabled ? 'Run once without enabling automatic reviews' : 'Run this review now'} onClick={() => onRunReviewModule?.(area.id)} className="inline-flex items-center gap-1.5 rounded-md border bg-background px-2 py-1 text-[10px] font-semibold text-foreground hover:bg-muted disabled:cursor-not-allowed disabled:opacity-50">
          {running ? <Loader2 className="h-3 w-3 animate-spin" /> : <Play className="h-3 w-3" />}{running ? 'Starting…' : 'Run now'}
        </button>
      </div>
    </div>
  }
  return <section className="space-y-4" aria-label="Pulse reviews">
    <div aria-label="Pulse work areas" className="space-y-4">
      {!platformOnly && <section aria-labelledby="pulse-strategy-heading">
        <div className="mb-2"><h3 id="pulse-strategy-heading" className="text-sm font-semibold">Goals, metrics &amp; strategy</h3><p className="mt-1 text-xs text-muted-foreground">The user-facing view: is this workflow producing the right outcome, and what should improve?</p></div>
        {reviewCard(areas.find(area => area.id === 'strategic_review')!, true)}
        {strategySupplement && <div className="mt-3">{strategySupplement}</div>}
      </section>}
      <section aria-labelledby="pulse-platform-heading">
        <div className="mb-2"><h3 id="pulse-platform-heading" className="text-sm font-semibold">Platform health &amp; stability</h3><p className="mt-1 text-xs text-muted-foreground">Maintenance Pulse handles in the background to keep the workflow correct and reliable. Goal Work does not wait for it.</p></div>
        {goalLeadOwnsReviews
          ? <p className="rounded-lg border bg-muted/20 px-3 py-2 text-xs text-muted-foreground" aria-label="QA and architecture">QA and architecture for this workflow are handled by its Pulse: it asks for a QA run when a failure threatens the goal, and checks the structure when its checks raise a question. Their results appear in the Pulse conversation above.</p>
          : <div className="grid gap-2 md:grid-cols-2">
            {areas.filter(area => ['technical_review', 'architecture_review'].includes(area.id)).map(area => reviewCard(area))}
          </div>}
        {liveDriftDue && <div className="mt-2 flex w-full flex-wrap items-start gap-3 rounded-xl border border-amber-500/40 bg-amber-500/10 p-4 text-left text-amber-900 dark:text-amber-100" aria-label="Workflow Review due">
          <AlertTriangle className="mt-0.5 h-5 w-5 shrink-0" aria-hidden="true" />
          <span className="min-w-0 flex-1">
            <span className="block text-sm font-semibold">Workflow Review is due</span>
            <span className="mt-1 block text-xs leading-5">{planDriftDueItems.length > 0
              ? `${planDriftDueItems.length} ${planDriftDueItems.length === 1 ? 'step needs' : 'steps need'} compatibility review before Technical or Architecture can run. Goal Work continues but will not run workflow steps.`
              : 'A drift review is pending. Technical and Architecture wait until the plan is current; Goal Work continues without running steps.'}</span>
            {planDriftDueItems.length > 0 && <span className="mt-2 flex flex-wrap gap-1.5">{planDriftDueItems.slice(0, 5).map(item => <span key={item.step_id} title={item.reason} className="rounded-full border border-amber-500/30 bg-background/70 px-2 py-0.5 text-[10px] font-medium">{item.step_id === '__workflow_drift_review__' ? 'Deleted-step dependencies' : item.step_id}</span>)}{planDriftDueItems.length > 5 && <span className="px-1 py-0.5 text-[10px]">+{planDriftDueItems.length - 5} more</span>}</span>}
          </span>
          <span className="flex shrink-0 flex-wrap items-center gap-2">
            <button type="button" onClick={() => onSelectModule('plan_drift_review')} className="rounded-md px-2 py-1 text-xs font-medium hover:bg-amber-500/10">View details</button>
            <button type="button" aria-label="Run Workflow Review now" disabled={!onRunReviewModule || (!!runningReviewModule && runningReviewModule !== 'plan_drift_review')} onClick={() => onRunReviewModule?.('plan_drift_review')} className="inline-flex items-center gap-1.5 rounded-md border border-amber-500/30 bg-background px-2 py-1 text-[10px] font-semibold text-foreground hover:bg-muted disabled:cursor-not-allowed disabled:opacity-50">
              {runningReviewModule === 'plan_drift_review' ? <Loader2 className="h-3 w-3 animate-spin" /> : <Play className="h-3 w-3" />}{runningReviewModule === 'plan_drift_review' ? 'Starting…' : 'Run Workflow Review'}
            </button>
          </span>
        </div>}
        {planDriftDueError && <div role="alert" className="mt-2 rounded-xl border border-red-500/30 bg-red-500/10 px-4 py-3 text-xs text-red-700 dark:text-red-300"><span className="font-semibold">Workflow Review status unavailable.</span> The plan cannot be treated as clean until this check succeeds. <span className="break-words">{planDriftDueError}</span></div>}
        {!liveDriftDue && <div className="mt-2 flex flex-wrap items-center justify-between gap-2 rounded-lg border bg-muted/20 px-3 py-2">
          <button type="button" onClick={() => onSelectModule('plan_drift_review')} aria-pressed={moduleFilter === 'plan_drift_review'} className={`flex items-center gap-2 text-left text-xs hover:text-foreground ${liveDriftDue ? 'font-medium text-amber-700 dark:text-amber-300' : 'text-muted-foreground'}`}><GitCompare className="h-3.5 w-3.5" /><span><span className="font-medium text-foreground">Workflow Review</span> · {driftStatus}<span className="mt-0.5 block text-[10px] text-muted-foreground">Required Workflow Review after plan changes</span></span></button>
          <button type="button" aria-label="Run Workflow Review now" disabled={!onRunReviewModule || (!!runningReviewModule && runningReviewModule !== 'plan_drift_review')} onClick={() => onRunReviewModule?.('plan_drift_review')} className="inline-flex items-center gap-1.5 rounded-md border bg-background px-2 py-1 text-[10px] font-semibold text-foreground hover:bg-muted disabled:cursor-not-allowed disabled:opacity-50">
            {runningReviewModule === 'plan_drift_review' ? <Loader2 className="h-3 w-3 animate-spin" /> : <Play className="h-3 w-3" />}{runningReviewModule === 'plan_drift_review' ? 'Starting…' : 'Run drift check'}
          </button>
        </div>}
      </section>
    </div>
    {selected && <section key={selected.id} aria-label={`${selected.label} content`} className="space-y-4 rounded-xl border bg-background p-4">
      <div><h4 className="text-sm font-semibold">{selected.label}</h4><p className="mt-1 text-xs text-muted-foreground">{selected.description}</p></div>
      {selected.id !== 'plan_drift_review' && disabledReviewModules.includes(selected.id as PulseReviewerModule) && <p className="rounded-lg border border-border bg-muted/40 px-3 py-2 text-xs text-muted-foreground">This reviewer is off. Future Pulse runs will skip it; previous findings and coverage remain below.</p>}
      {selected.id !== 'plan_drift_review' && liveDriftDue && <p className="rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-800 dark:text-amber-200">Waiting for the Workflow Review to establish a current plan baseline. This review will resume in a later Pulse cycle.</p>}
      {selected.id === 'plan_drift_review' ? <div className="space-y-2 text-xs leading-5 text-muted-foreground">
        <p className="font-medium text-foreground">{driftStatus}</p>
        <p>{driftCounts.all ? `${driftCounts.all} current drift finding${driftCounts.all === 1 ? '' : 's'}.` : 'No current drift findings.'}
          {driftCounts.resolved > 0 && ` ${driftCounts.resolved} resolved finding${driftCounts.resolved === 1 ? '' : 's'} available below.`}</p>
        {planDriftDueItems.length > 0 && <div className="rounded-lg border border-amber-500/20 bg-amber-500/5 px-3 py-2"><p className="font-medium text-foreground">Steps requiring review</p><ul className="mt-1 space-y-1">{planDriftDueItems.map(item => <li key={item.step_id}><span className="font-medium text-foreground">{item.step_id === '__workflow_drift_review__' ? 'Deleted-step dependencies' : item.step_id}</span> — {item.reason}</li>)}</ul></div>}
        <details><summary className="cursor-pointer">Check details</summary><p className="mt-2 break-words">{drift?.last_reason || driftAudit?.reason || 'No drift-check details recorded.'}</p>
          {pendingDrift && driftAudit && <p className="mt-2">Previous check: {pulseReviewDate(driftAudit.recorded_at)} · {readable(driftAudit.result)}</p>}
        </details>
      </div> : <div className="text-xs leading-5 text-muted-foreground">
        <p className="font-medium text-foreground">{selectedAudit?.reason || latestCoverage?.last_verdict || 'No review outcome recorded yet.'}</p>
        {selectedState?.last_gate_decision === 'skipped' && <details className="mt-2"><summary className="cursor-pointer">Why no new review ran</summary><p className="mt-2">{selectedState.last_reason}</p></details>}
      </div>}
      {selected.id === 'strategic_review' && <div>
        <h5 className="mb-2 text-xs font-semibold">Strategic playbook focus</h5>
        {selectedPlaybookFocuses.length === 0 ? <p className="rounded-lg border border-dashed p-3 text-xs text-muted-foreground">No installed playbook defines a strategic focus.</p> : <div className="divide-y rounded-lg border">
          {selectedPlaybookFocuses.map(item => <div key={`${item.playbookId}:${item.module}:${item.label}`} className="px-3 py-3 text-xs">
            <p className="font-medium text-foreground">{item.playbookTitle} · {item.label}</p>
            <ul className="mt-2 list-disc space-y-1 pl-4 leading-5 text-muted-foreground">{item.focusAreas.map(area => <li key={area}>{area}</li>)}</ul>
            {item.reviewWhen.length > 0 && <p className="mt-2 leading-5 text-muted-foreground"><span className="font-medium text-foreground/80">Review when:</span> {item.reviewWhen.join(' · ')}</p>}
          </div>)}
        </div>}
      </div>}
      {['technical_review', 'architecture_review'].includes(selected.id) && <div><h5 className="mb-2 text-xs font-semibold">Review coverage</h5><div className="divide-y rounded-lg border">
        {(selected.id === 'architecture_review' ? ARCHITECTURE_REVIEW_AREAS : TECHNICAL_REVIEW_AREAS).map(area => {
          const items = reviewCoverageForArea(area, selectedCoverage, selected.id)
          return <details key={area.key} className="px-3 py-2.5">
            <summary className="cursor-pointer text-xs"><span className="font-medium text-foreground">{area.label}</span>
              <span className={`mt-1 block pl-4 ${items[0] ? 'text-muted-foreground' : 'text-amber-600 dark:text-amber-300'}`}>
                {items[0] ? `Last reviewed ${pulseReviewDate(items[0].last_reviewed_at)}` : 'No specific review recorded'}</span></summary>
            <div className="mt-3 border-t pt-3 text-xs leading-5 text-muted-foreground"><FocusDetails items={items} findings={findings} /></div>
          </details>
        })}
      </div></div>}
      {selected.id !== 'plan_drift_review' && selectedNextAssessment && <p className="text-xs text-muted-foreground">Next assessment: {selectedNextAssessment}</p>}
    </section>}
  </section>
}

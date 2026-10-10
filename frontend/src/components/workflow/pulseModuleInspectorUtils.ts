import type {
  PulseFindingEvent,
  PulseFindingLifecycle,
  PulseIssue,
} from '../../services/api-types'

export type PulseModuleSummary = {
  total: number
  open: number
  /** Acknowledged and waiting on the operator to decide something. */
  awaitingUser: number
  /** Acknowledged but Pulse has no way to act — nothing here is yours to do. */
  blocked: number
  /** Real, but only waiting on a scheduled run to produce its evidence. */
  awaitingRun: number
  /** Safe workflow work retained for a future Engineering/Pulse pass. */
  queuedForEngineering: number
  /** Understood and recorded as a proposal; Pulse chose not to repair it. */
  proposals: number
  /**
   * Concerns the workflow's own steps filed while running — prevalidation,
   * execution, message-sequence. They share `run_concerns` with Pulse findings
   * but are not Pulse's queue: the backend lifecycle only claims
   * `phase === 'review'`, and these ride along with module state as Gate
   * evidence. Counting them under "Pulse can fix" made social-media read 105
   * when Pulse owned 10.
   */
  workflowReported: number
  /**
   * A status this UI does not know. Never folded into `open`: a fallthrough on
   * the most alarming metric means every disposition added later silently
   * inflates it, which is exactly how proposals became "Pulse can fix".
   */
  unclassified: number
  fixing: number
  awaitingVerification: number
  closed: number
  externalAction: number
  recurring: number
  harnessIssues: number
  attempts: number
  passedChecks: number
  failedChecks: number
  inconclusiveChecks: number
}

/**
 * An acknowledged finding is triaged, not outstanding, and the three reasons it
 * can be acknowledged demand opposite responses: blocked means Pulse cannot
 * act, awaiting_user means only you can, and a recorded proposal means neither
 * is urgent. The status column flattens all three, so counting them as one
 * number made latency-report read as "25 need action" when 12 were blocked, 4 were
 * questions for the operator, and only 6 were work Pulse could pick up.
 *
 * The reason lives in the finding's events rather than its status, so the most
 * recent one that carries a reason decides.
 */
export function acknowledgedReason(
  finding: PulseFindingLifecycle,
): 'blocked' | 'awaiting_user' | 'proposal' | 'other' {
  // Events arrive newest first from the lifecycle API.
  for (let index = 0; index < finding.events.length; index += 1) {
    const eventType = finding.events[index]?.event_type
    if (eventType === 'blocked' || eventType === 'awaiting_user') return eventType
    // The comment above always described three reasons; only two were ever
    // implemented, so proposal_only findings fell through to `other` and were
    // counted as work Pulse could pick up. That is backwards — a proposal is
    // one Pulse deliberately decided not to fix. social-media read 109 when 105
    // were actionable and 4 were recorded proposals.
    if (eventType === 'proposal_recorded') return 'proposal'
  }
  return 'other'
}

/**
 * Compatibility projection for a UI talking to an older backend. New servers
 * provide finding.issue directly; issue_id is the only public identity.
 */
export function pulseIssueForFinding(finding: PulseFindingLifecycle): PulseIssue {
  if (finding.issue) return finding.issue
  const reason = finding.status === 'acknowledged' ? acknowledgedReason(finding) : 'other'
  const status = finding.status === 'fixing'
    ? 'in_progress'
    : finding.status === 'awaiting_verification'
      ? 'in_review'
      : finding.status === 'resolved'
        ? 'done'
        : finding.status === 'rejected'
          ? 'canceled'
          : finding.status === 'external_action_required'
            ? 'external'
            : reason === 'awaiting_user'
              ? 'needs_input'
              : reason === 'blocked'
                ? 'blocked'
                : 'backlog'
  const severity = (finding.details?.severity || '').toLowerCase()
  const priority = severity === 'critical' || severity === 'urgent'
    ? 'urgent'
    : ['high', 'medium', 'low'].includes(severity)
      ? severity
      : 'none'
  const title = finding.details?.summary?.trim() || finding.text.trim()
  return {
    id: finding.finding_id || 'PUL-UNKNOWN',
    title,
    description: title === finding.text.trim() ? undefined : finding.text.trim(),
    status,
    priority,
    module: finding.module,
    created_at: finding.first_seen_at,
    updated_at: finding.last_seen_at,
    seen_count: finding.seen_count,
  }
}

export type PulseModuleActivity = PulseFindingEvent & {
  findingID?: string
  findingText: string
}

/**
 * `run_concerns` holds two species. The backend now projects their explicit
 * lifecycle kind: observations are workflow evidence; issues were accepted by
 * a reviewer or entered repair lifecycle work. Phase remains only a backwards-
 * compatibility fallback for an older backend.
 *
 * An absent phase is treated as Pulse-owned so an older backend that does not
 * send the field keeps showing its findings rather than silently hiding them.
 */
export function isPulseOwnedFinding(finding: PulseFindingLifecycle): boolean {
  if (finding.kind === 'issue') return true
  if (finding.kind === 'observation') return false
  const phase = (finding.phase || '').trim()
  return phase === '' || phase === 'review'
}

export function isPulseFindingClosed(status: string): boolean {
  return status === 'resolved'
    || status === 'rejected'
    || status === 'external_action_required'
}

export function summarizePulseModule(findings: PulseFindingLifecycle[]): PulseModuleSummary {
  const summary: PulseModuleSummary = {
    total: findings.length,
    open: 0,
    fixing: 0,
    awaitingVerification: 0,
    closed: 0,
    externalAction: 0,
    recurring: 0,
    harnessIssues: 0,
    attempts: 0,
    passedChecks: 0,
    failedChecks: 0,
    inconclusiveChecks: 0,
    awaitingUser: 0,
    blocked: 0,
    awaitingRun: 0,
    queuedForEngineering: 0,
    proposals: 0,
    workflowReported: 0,
    unclassified: 0,
  }
  findings.forEach((finding) => {
    // Decided before status: a workflow-reported concern is not a Pulse finding
    // in any state, so it must not land in a Pulse bucket.
    if (!isPulseOwnedFinding(finding)) {
      summary.workflowReported++
      return
    }
    if (finding.status === 'external_action_required') summary.externalAction++
    else if (isPulseFindingClosed(finding.status)) summary.closed++
    else if (finding.status === 'fixing') summary.fixing++
    else if (finding.status === 'awaiting_verification') summary.awaitingVerification++
    // Waiting for data is not a blocker and not the operator's move; counting it
    // with either sends you looking for a decision that does not exist.
    else if (finding.status === 'awaiting_run') summary.awaitingRun++
    else if (finding.status === 'queued_for_engineering') summary.queuedForEngineering++
    else if (finding.status === 'acknowledged') {
      const reason = acknowledgedReason(finding)
      if (reason === 'awaiting_user') summary.awaitingUser++
      else if (reason === 'blocked') summary.blocked++
      else if (reason === 'proposal') summary.proposals++
      else summary.open++
    }
    // `open` is matched explicitly. Anything else is a status this build does
    // not model, and it goes to `unclassified` rather than swelling the number
    // the operator is most likely to act on.
    else if (finding.status === 'open') summary.open++
    else summary.unclassified++
    if (finding.seen_count > 1 && finding.status !== 'external_action_required') summary.recurring++
    if (finding.details?.issue_kind === 'harness_issue') summary.harnessIssues++
    summary.attempts += finding.fix_attempts.length
    finding.verifications.forEach((verification) => {
      if (verification.verdict === 'passed') summary.passedChecks++
      else if (verification.verdict === 'failed') summary.failedChecks++
      else summary.inconclusiveChecks++
    })
  })
  return summary
}

export function buildPulseModuleActivity(
  findings: PulseFindingLifecycle[],
  limit = 8,
): PulseModuleActivity[] {
  return findings
    .flatMap((finding) => finding.events.map((event) => ({
      ...event,
      findingID: finding.issue?.id || finding.finding_id || event.finding_id,
      findingText: finding.text,
    })))
    .sort((a, b) => b.recorded_at.localeCompare(a.recorded_at))
    .slice(0, Math.max(0, limit))
}

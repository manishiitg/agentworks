import { describe, expect, it } from 'vitest'
import type { PulseImpactLedger, ReportHumanInput } from '../services/api-types'
import {
  parseReportHumanInputContext,
  reportHumanInputHistory,
  reportHumanInputImpact,
  reportHumanInputStatusLabel,
} from './reportHumanInputFormatting'

function input(status: ReportHumanInput['status'], id = status): ReportHumanInput {
  return {
    id,
    workspace_path: 'Workflow/example',
    source: 'pulse',
    priority: 'medium',
    question: `Question ${id}`,
    options: [],
    allow_free_text: true,
    status,
    created_at: '2026-07-14T08:00:00Z',
    updated_at: '2026-07-14T09:00:00Z',
  }
}

describe('report human input context formatting', () => {
  it('turns compact proposal text into readable sections and a numbered list', () => {
    const sections = parseReportHumanInputContext(
      'Proposal: Align the objective. Exact intended edits if approved: (1) Update the objective. (2) Update success criteria. Rationale: The workflow already runs hourly. Expected impact: Less drift. Risk: Wording may weaken a constraint.',
    )

    expect(sections.map(section => section.label)).toEqual([
      'Proposal',
      'Intended edits',
      'Rationale',
      'Expected impact',
      'Risk',
    ])
    expect(sections[1].items).toEqual(['Update the objective.', 'Update success criteria.'])
  })

  it('keeps unstructured context as escaped plain text', () => {
    expect(parseReportHumanInputContext('A short explanation.')).toEqual([
      { label: '', body: 'A short explanation.', items: [] },
    ])
  })

  it('renders both advisor specialization texts as separate sections', () => {
    const sections = parseReportHumanInputContext(
      'Proposal: Specialize both reviewers. Strategy Auditor specialization: Check channel concentration. Goal Advisor specialization: Explore partnerships and referral loops.',
    )

    expect(sections.map(section => section.label)).toEqual([
      'Proposal',
      'Strategy auditor specialization',
      'Goal advisor specialization',
    ])
    expect(sections[1].body).toBe('Check channel concentration.')
    expect(sections[2].body).toBe('Explore partnerships and referral loops.')
  })

  it('keeps consumed inputs in decision history so their outcome remains visible', () => {
    const consumed = {
      ...input('consumed'),
      outcome_summary: 'Updated the publishing schedule from weekly to daily.',
    }

    expect(reportHumanInputHistory([input('pending'), input('answered'), consumed, input('dismissed')]))
      .toEqual([input('answered'), consumed, input('dismissed')])
  })

  it('uses lifecycle labels that distinguish waiting from completed action', () => {
    expect(reportHumanInputStatusLabel(input('pending'))).toBe('Needs answer')
    expect(reportHumanInputStatusLabel(input('answered'))).toBe('Decision saved · waiting for action')
    expect(reportHumanInputStatusLabel({ ...input('answered'), source: 'engineering_review' })).toBe('Decision saved · waiting for action')
    expect(reportHumanInputStatusLabel({ ...input('answered'), source: 'ops_review', selected_option_id: 'approve' })).toBe('Approved · waiting for action')
    expect(reportHumanInputStatusLabel({ ...input('answered'), source: 'goal_advisor' })).toBe('Decision saved · waiting for action')
		expect(reportHumanInputStatusLabel({ ...input('answered'), source: 'strategy_auditor', selected_option_id: 'reject' })).toBe('Rejected · waiting for closure')
    expect(reportHumanInputStatusLabel({ ...input('claimed'), source: 'goal_advisor' })).toBe('Decision action is running')
		expect(reportHumanInputStatusLabel({ ...input('claimed'), source: 'engineering_review' })).toBe('Decision action is running')
		expect(reportHumanInputStatusLabel({ ...input('claimed'), source: 'ops_review' })).toBe('Decision action is running')
		expect(reportHumanInputStatusLabel({ ...input('claimed'), source: 'strategy_auditor' })).toBe('Decision action is running')
    expect(reportHumanInputStatusLabel(input('consumed'))).toBe('Action completed')
    expect(reportHumanInputStatusLabel(input('dismissed'))).toBe('Dismissed')
    expect(reportHumanInputStatusLabel(input('withdrawn'))).toBe('Withdrawn')
  })

  it('joins an applied decision to its newest durable impact assessment', () => {
    const ledger: PulseImpactLedger = {
      interventions: [{
        intervention_id: 'intervention-1',
        title: 'Flatten execution pipeline',
        criterion_id: 'reliable-completion',
        impact_type: 'reliability',
        metric: 'successful_run_rate',
        expected_direction: 'increase',
        minimum_evidence_runs: 2,
        status: 'measuring',
        human_input_id: 'decision-1',
      }],
      observations: [],
      assessments: [
        {
          assessment_id: 'new',
          intervention_id: 'intervention-1',
          verdict: 'improved',
          before_window: 'previous 3 runs',
          after_window: 'next 3 runs',
          before_value: 0.33,
          after_value: 1,
          confidence: 'medium',
          assessed_at: '2026-08-20T09:00:00Z',
        },
        {
          assessment_id: 'old',
          intervention_id: 'intervention-1',
          verdict: 'inconclusive',
          before_window: 'previous 3 runs',
          after_window: 'first run',
          confidence: 'low',
          assessed_at: '2026-08-19T09:00:00Z',
        },
      ],
    }

    expect(reportHumanInputImpact(input('consumed', 'decision-1'), ledger)).toEqual({
      intervention: ledger.interventions[0],
      latestAssessment: ledger.assessments[0],
    })
    expect(reportHumanInputImpact(input('consumed', 'another-decision'), ledger)).toBeNull()
  })
})

import { describe, expect, it } from 'vitest'
import type { ReportHumanInput } from '../services/api-types'
import { parseReportHumanInputContext, reportHumanInputHistory, reportHumanInputStatusLabel } from './reportHumanInputFormatting'

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


})

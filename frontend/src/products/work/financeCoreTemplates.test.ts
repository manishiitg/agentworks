import { describe, expect, it } from 'vitest'
import { getCrewTemplate, parseCrewTemplateSetupState } from './crewTemplates'

describe('core Finance Crew templates', () => {
  it('extends Payables to a reviewed vendor purchase without claiming execution', () => {
    const payables = getCrewTemplate('spend-payables-coordinator')
    expect(payables.version).toBe(2)
    expect(payables.files['skills/spend-payables-coordinator/SKILL.md']).toContain('vendor-purchase-review/v1')
    expect(payables.files['skills/spend-payables-coordinator/SKILL.md']).toContain('purchase_action_state=none')
  })

  it('keeps cloud savings verification in the Finance Analyst skill', () => {
    const analyst = getCrewTemplate('finance-analyst')
    expect(analyst.files['skills/finance-analyst/SKILL.md']).toContain('cloud-savings-readout/v1')
    expect(analyst.files['skills/finance-analyst/SKILL.md']).toContain('pending_verification')
  })

  it.each([
    ['billing-operations-coordinator', 'case_mapping', '6,500 minor units remaining'],
    ['revenue-close-analyst', 'reconciliation', 'USD 50.00'],
    ['spend-payables-coordinator', 'duplicate_check', 'approval_state **pending**'],
  ] as const)('keeps %s as a pending, evidence-led install', (id, probeId, exampleResult) => {
    const template = getCrewTemplate(id)
    const skill = template.files[`skills/${id}/SKILL.md`]
    expect(skill).toContain('## Fictional worked example')
    expect(skill).toContain(exampleResult)
    expect(skill).toContain('## Inadequate output to reject')
    expect(skill).toContain('does not complete setup')
    const setup = parseCrewTemplateSetupState(template.files[template.setupPath], template)
    expect(setup?.checks).toHaveLength(9)
    expect(setup?.completed_steps).toEqual([])
    expect((setup?.checks.find(check => check.id === probeId)?.instructions.length ?? 0)).toBeGreaterThan(250)
    expect(template.files[template.setupGuidePath]).toContain('## Source probe and example')
  })
})

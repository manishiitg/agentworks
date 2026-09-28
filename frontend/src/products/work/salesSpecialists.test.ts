import { describe, expect, it } from 'vitest'
import { crewTemplates, getCrewTemplate, parseCrewTemplateSetupState } from './crewTemplates'

describe('Sales Crew templates', () => {
  it('offers three distinct reusable roles with local skills and chat setup checks', () => {
    const sales = crewTemplates.filter(item => ['lead-intake-qualifier', 'account-researcher', 'sales-followup-coordinator'].includes(item.id))
    expect(sales.map(item => item.id)).toEqual(['lead-intake-qualifier', 'account-researcher', 'sales-followup-coordinator'])
    for (const template of sales) {
      expect(getCrewTemplate(template.id)).toBe(template)
      expect(template.files[`skills/${template.id}/SKILL.md`]).toContain(`# ${template.name}`)
      expect(template.files[`skills/${template.id}/SKILL.md`]).toContain('## Automation handoff')
      expect(template.files[`skills/${template.id}/SKILL.md`]).toContain('## Fictional worked example')
      expect(template.files[`skills/${template.id}/SKILL.md`]).toContain('## Inadequate output to reject')
      expect(template.files[`skills/${template.id}/SKILL.md`]).toContain('does not complete setup')
      const setup = parseCrewTemplateSetupState(template.files[template.setupPath], template)
      expect(setup?.checks).toHaveLength(9)
      expect(setup?.completed_steps).toEqual([])
      expect((setup?.checks.find(check => check.id === 'policy')?.instructions.length ?? 0)).toBeGreaterThan(150)
      expect(template.files[template.setupGuidePath]).toContain('Fictional example and failure')
    }
    expect(sales[0].files['skills/lead-intake-qualifier/SKILL.md']).toContain('budget **unknown**')
    expect(sales[1].files['skills/account-researcher/SKILL.md']).toContain('**hypothesis**')
    expect(sales[2].files['skills/sales-followup-coordinator/SKILL.md']).toContain('send_state **not_sent**')
    expect(sales[2].version).toBe(3)
    expect(sales[2].files['skills/sales-followup-coordinator/SKILL.md']).toContain('trial-sales-assist/v1')
    expect(sales[2].files[sales[2].setupPath]).toContain('Select inbound lead or trial route')
  })
})

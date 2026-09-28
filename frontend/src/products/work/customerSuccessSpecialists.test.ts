import { describe, expect, it } from 'vitest'
import { crewTemplates, getCrewTemplate, parseCrewTemplateSetupState } from './crewTemplates'

describe('Customer Success Crew templates', () => {
  it('offers five reusable roles with local skills and pending chat setup', () => {
    const success = crewTemplates.filter(item => item.category === 'Customer Success')
    expect(success.map(item => item.id)).toEqual(['customer-onboarding-coordinator', 'product-adoption-analyst', 'lifecycle-analyst', 'customer-health-coordinator', 'renewal-coordinator'])
    for (const template of success) {
      expect(getCrewTemplate(template.id)).toBe(template)
      expect(template.files[`skills/${template.id}/SKILL.md`]).toContain('## Automation handoff')
      expect(template.files[`skills/${template.id}/SKILL.md`]).toContain('## Fictional worked example')
      expect(template.files[`skills/${template.id}/SKILL.md`]).toContain('## Inadequate output to reject')
      expect(template.files[`skills/${template.id}/SKILL.md`]).toContain('does not complete setup')
      const setup = parseCrewTemplateSetupState(template.files[template.setupPath], template)
      expect(setup?.checks).toHaveLength(9)
      expect(setup?.completed_steps).toEqual([])
      expect((setup?.checks.find(check => check.id === 'definitions')?.instructions.length ?? 0)).toBeGreaterThan(150)
      expect(template.files[template.setupGuidePath]).toContain('Source and connection choice')
    }
    expect(success[0].files['skills/customer-onboarding-coordinator/SKILL.md']).toContain('first-report”: **pending**')
    expect(success[1].files['skills/product-adoption-analyst/SKILL.md']).toContain('Status: **reached**')
    expect(success[1].version).toBe(3)
    expect(success[1].files['skills/product-adoption-analyst/SKILL.md']).toContain('24/80 used (30%)')
    expect(success[1].files['skills/product-adoption-analyst/SKILL.md']).toContain('trial-usage-observation/v1')
    expect(success[1].files[success[1].setupPath]).toContain('Select first value, released feature or trial usage')
    expect(success[2].files['skills/lifecycle-analyst/SKILL.md']).toContain('pending_maturity')
    expect(success[3].files['skills/customer-health-coordinator/SKILL.md']).toContain('labeled **hypothesis**')
    expect(success[4].files['skills/renewal-coordinator/SKILL.md']).toContain('six days to notice')
  })
})

import { describe, expect, it } from 'vitest'
import { crewTemplates, getCrewTemplate, matchesCrewTemplateSearch, parseCrewTemplateSetupState } from './crewTemplates'

describe('GTM Crew templates', () => {
  it('offers strategy and launch roles with pending setup and bounded examples', () => {
    const gtm = crewTemplates.filter(item => item.category === 'GTM')
    expect(gtm.map(item => item.id)).toEqual(['gtm-strategy-analyst', 'launch-coordinator', 'revenue-operations-analyst', 'sales-enablement-coordinator', 'pricing-packaging-analyst'])
    for (const template of gtm.slice(0, 2)) {
      expect(getCrewTemplate(template.id)).toBe(template)
      expect(template.version).toBe(2)
      const skill = template.files['skills/' + template.id + '/SKILL.md']
      expect(skill).toContain('## Fictional worked example')
      expect(skill).toContain('## Source probe and acceptance')
      expect(skill).toContain('## Automation handoff')
      const setup = parseCrewTemplateSetupState(template.files[template.setupPath], template)
      expect(setup?.checks.map(check => check.id)).toEqual([
        'identity', 'skill', 'scope', 'access', 'policy', 'first_result', 'review', 'delivery', 'recurrence',
      ])
      expect(setup?.completed_steps).toEqual([])
    }
    expect(matchesCrewTemplateSearch(gtm[0], 'buyer evidence')).toBe(true)
    expect(gtm[1].files['skills/launch-coordinator/SKILL.md']).toContain('A launch asset marked done in a project board is not proof')
    expect(gtm[0].files['skills/gtm-strategy-analyst/SKILL.md']).toContain('two dated buyer observations')
    expect(gtm[1].files['skills/launch-coordinator/SKILL.md']).toContain('missing receipt or contact eligibility')
  })
})

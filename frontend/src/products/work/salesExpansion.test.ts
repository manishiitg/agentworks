import { describe, expect, it } from 'vitest'
import { crewTemplates, getCrewTemplate, parseCrewTemplateSetupState } from './crewTemplates'

describe('expanded Sales templates', () => {
  it('installs four reviewable roles with independent pending setup and examples', () => {
    const templates = crewTemplates.filter(item => ['sales-call-briefing', 'proposal-drafter', 'pipeline-analyst', 'deal-follow-through-coordinator'].includes(item.id))
    expect(templates.map(item => item.id)).toEqual(['sales-call-briefing', 'proposal-drafter', 'pipeline-analyst', 'deal-follow-through-coordinator'])
    for (const template of templates) {
      expect(getCrewTemplate(template.id)).toBe(template)
      const setup = parseCrewTemplateSetupState(template.files[template.setupPath], template)
      expect(setup?.checks.map(check => check.id)).toEqual([
        'identity', 'skill', 'scope', 'access', 'policy', 'first_result', 'review', 'delivery', 'recurrence',
      ])
      expect(setup?.completed_steps).toEqual([])
      const skill = template.files[`skills/${template.id}/SKILL.md`]
      expect(skill).toContain('## Fictional worked example')
      expect(skill).toContain('Inadequate:')
      expect(skill).toContain('Installation enables no schedule')
    }
    expect(templates[1].files['skills/proposal-drafter/SKILL.md']).toContain('unsent')
    expect(templates[2].files['skills/pipeline-analyst/SKILL.md']).toContain('stable opportunity IDs')
    expect(templates[3].files['skills/deal-follow-through-coordinator/SKILL.md']).toContain('current opportunity')
  })
})

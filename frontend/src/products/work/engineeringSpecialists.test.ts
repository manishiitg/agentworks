import { describe, expect, it } from 'vitest'
import { crewTemplates, getCrewTemplate, matchesCrewTemplateSearch, parseCrewTemplateSetupState } from './crewTemplates'

describe('Engineering Crew templates', () => {
  it('offers cross-tool roles with pending, source-verified chat setup', () => {
    const engineering = crewTemplates.filter(item => item.category === 'Engineering')
    expect(engineering.map(item => item.id)).toEqual([
      'incident-investigator',
      'engineering-delivery-coordinator',
      'engineering-operations-analyst',
      'performance-investigator',
      'cloud-cost-analyst',
      'post-incident-reviewer',
      'improvement-follow-through-coordinator',
    ])
    for (const template of engineering) {
      expect(getCrewTemplate(template.id)).toBe(template)
      expect(template.files[`skills/${template.id}/SKILL.md`]).toContain('## Automation handoff')
      expect(template.files[`skills/${template.id}/SKILL.md`]).toContain('## Follow-through')
      expect(template.files[`skills/${template.id}/SKILL.md`]).toContain('## Fictional worked example')
      expect(template.files[`skills/${template.id}/SKILL.md`]).toContain('## Inadequate output to reject')
      expect(template.files[`skills/${template.id}/SKILL.md`]).toContain('does not complete setup')
      const setup = parseCrewTemplateSetupState(template.files[template.setupPath], template)
      expect(setup?.checks.map(check => check.id)).toEqual([
        'identity', 'skill', 'scope', 'access', 'join_rules', 'first_result', 'review', 'delivery', 'recurrence',
      ])
      expect(setup?.completed_steps).toEqual([])
      expect((setup?.checks.find(check => check.id === 'join_rules')?.instructions.length ?? 0)).toBeGreaterThan(150)
    }
    expect(matchesCrewTemplateSearch(engineering[1], 'blocked CI')).toBe(true)
    expect(engineering.some(item => /code review|PR Review Assistant/i.test(item.name))).toBe(false)
    expect(engineering[0].files['skills/incident-investigator/SKILL.md']).toContain('160/2,000 = 8%')
    expect(engineering[1].files['skills/engineering-delivery-coordinator/SKILL.md']).toContain('Production: **not deployed**')
    expect(engineering[2].files['skills/engineering-operations-analyst/SKILL.md']).toContain('engineering-metric-observation/v1')
    expect(engineering[3].files['skills/performance-investigator/SKILL.md']).toContain('64.3%')
    expect(engineering[4].files['skills/cloud-cost-analyst/SKILL.md']).toContain('total explained: $2,700')
    expect(engineering[4].files['skills/cloud-cost-analyst/SKILL.md']).toContain('cloud-cost-review/v1')
    expect(engineering[1].files['skills/engineering-delivery-coordinator/SKILL.md']).toContain('cloud-change-review/v1')
  })
})

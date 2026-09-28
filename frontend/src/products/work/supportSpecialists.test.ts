import { describe, expect, it } from 'vitest'
import { crewTemplates, getCrewTemplate, matchesCrewTemplateSearch, parseCrewTemplateSetupState } from './crewTemplates'

describe('Customer Support Crew templates', () => {
  it('offers case and feedback roles with pending source-verified setup', () => {
    const support = crewTemplates.filter(item => item.category === 'Customer Support')
    expect(support.map(item => item.id)).toEqual([
      'support-triage-assistant',
      'support-reply-drafter',
      'escalation-coordinator',
      'feedback-review-analyst',
      'support-knowledge-curator',
    ])
    for (const template of support.slice(0, 4)) {
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
    expect(matchesCrewTemplateSearch(support[1], 'approved help content')).toBe(true)
    expect(support[1].files['skills/support-reply-drafter/SKILL.md']).toContain('delivery_state=unsent')
    expect(support[0].files['skills/support-triage-assistant/SKILL.md']).toContain('Reproduce the priority and first-response deadline')
    expect(support[1].files['skills/support-reply-drafter/SKILL.md']).toContain('a stale article instruction')
    expect(support[2].files['skills/escalation-coordinator/SKILL.md']).toContain('a posted message alone is not an accepted owner assignment')
    expect(support[2].files['skills/escalation-coordinator/SKILL.md']).toContain('receiving-owner acceptance stays pending')
    expect(support[3].files['skills/feedback-review-analyst/SKILL.md']).toContain('Recalculate one theme numerator')
  })
})

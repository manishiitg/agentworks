import { describe, expect, it } from 'vitest'
import { crewTemplates, getCrewTemplate, matchesCrewTemplateSearch, parseCrewTemplateSetupState } from './crewTemplates'

describe('Operations Crew templates', () => {
  it('offers six distinct jobs with evidence-backed pending setup', () => {
    const operations = crewTemplates.filter(item => item.category === 'Operations')
    expect(operations.map(item => item.id)).toEqual([
      'chief-of-staff',
      'meeting-actions-coordinator',
      'project-status-reporter',
      'order-operations-coordinator',
      'vendor-researcher',
      'document-intake-assistant',
    ])
    for (const template of operations) {
      expect(template.version).toBe(2)
      expect(getCrewTemplate(template.id)).toBe(template)
      const skill = template.files['skills/' + template.id + '/SKILL.md']
      expect(skill).toContain('## Source probe and setup acceptance')
      expect(skill).toContain('## Fictional worked example')
      expect(skill).toContain('## Automation handoff')
      expect(skill).toContain('Installation enables no schedule')
      const setup = parseCrewTemplateSetupState(template.files[template.setupPath], template)
      expect(setup?.checks.map(check => check.id)).toEqual([
        'identity', 'skill', 'scope', 'access', 'policy', 'first_result', 'review', 'delivery', 'recurrence',
      ])
      expect(setup?.completed_steps).toEqual([])
      expect(setup?.template_version).toBe(2)
      expect(setup?.checks.find(check => check.id === 'access')?.instructions).toContain('Read')
      expect(setup?.checks.find(check => check.id === 'policy')?.instructions).toContain('setup remains pending')
    }
    expect(matchesCrewTemplateSearch(operations[4], 'vendor comparison')).toBe(true)
    expect(operations[1].files['skills/meeting-actions-coordinator/SKILL.md']).toContain('a suggestion')
    expect(operations[5].files['skills/document-intake-assistant/SKILL.md']).toContain('OCR')
    expect(operations[3].files['skills/order-operations-coordinator/SKILL.md']).toContain('pickup unverified')
  })
})

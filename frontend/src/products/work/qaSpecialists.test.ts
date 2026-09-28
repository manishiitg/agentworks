import { describe, expect, it } from 'vitest'
import { crewTemplates, getCrewTemplate, matchesCrewTemplateSearch, parseCrewTemplateSetupState } from './crewTemplates'

describe('QA Crew templates', () => {
  it('offers three source-specific jobs with pending chat setup', () => {
    const qa = crewTemplates.filter(item => item.category === 'QA')
    expect(qa.map(item => item.id)).toEqual([
      'browser-journey-qa-analyst',
      'flaky-test-investigator',
      'release-quality-assistant',
    ])
    for (const template of qa) {
      expect(getCrewTemplate(template.id)).toBe(template)
      expect(template.version).toBe(2)
      expect(template.files['skills/' + template.id + '/SKILL.md']).toContain('## Workflow Playbook and handoff')
      expect(template.files['skills/' + template.id + '/SKILL.md']).toContain('## Source probe and acceptance')
      const setup = parseCrewTemplateSetupState(template.files[template.setupPath], template)
      expect(setup?.checks.map(check => check.id)).toEqual([
        'identity', 'skill', 'scope', 'access', 'policy', 'first_result', 'review', 'delivery', 'recurrence',
      ])
      expect(setup?.completed_steps).toEqual([])
      expect(setup?.checks.find(check => check.id === 'access')?.instructions).toContain('Record access scope')
    }
    expect(matchesCrewTemplateSearch(qa[2], 'release gate')).toBe(true)
    expect(qa[0].files['skills/browser-journey-qa-analyst/SKILL.md']).toContain('skipped, blocked, missing, or stale')
    expect(qa[0].files['skills/browser-journey-qa-analyst/SKILL.md']).toContain('A successful retry must remain a separate attempt')
    expect(qa[1].files['skills/flaky-test-investigator/SKILL.md']).toContain('A green retry is not proof of stability')
    expect(qa[1].files['skills/flaky-test-investigator/SKILL.md']).toContain('Reject a proposed wait, quarantine, or assertion change')
    expect(qa[2].files['skills/release-quality-assistant/SKILL.md']).toContain('different SHA or environment')
    expect(qa[2].files['skills/release-quality-assistant/SKILL.md']).toContain('enumerate its required suites before fetching CI results')
  })
})

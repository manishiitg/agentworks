import { describe, expect, it } from 'vitest'
import { crewTemplates, getCrewTemplate, matchesCrewTemplateSearch, parseCrewTemplateSetupState } from './crewTemplates'

describe('Security Crew templates', () => {
  it('offers scoped findings, access, and remediation roles with pending setup', () => {
    const security = crewTemplates.filter(item => item.category === 'Security')
    expect(security.map(item => item.id)).toEqual([
      'security-findings-analyst',
      'access-review-analyst',
      'security-remediation-coordinator',
    ])
    for (const template of security) {
      expect(getCrewTemplate(template.id)).toBe(template)
      expect(template.version).toBe(2)
      const skill = template.files['skills/' + template.id + '/SKILL.md']
      expect(skill).toContain('## Fictional worked example')
      expect(skill).toContain('## Source probe and acceptance')
      expect(skill).toContain('## Workflow Playbook and handoff')
      expect(skill).toContain('written scope')
      const setup = parseCrewTemplateSetupState(template.files[template.setupPath], template)
      expect(setup?.checks.map(check => check.id)).toEqual([
        'identity', 'skill', 'scope', 'access', 'policy', 'first_result', 'review', 'delivery', 'recurrence',
      ])
      expect(setup?.completed_steps).toEqual([])
    }
    expect(matchesCrewTemplateSearch(security[1], 'tenant permission')).toBe(true)
    expect(security[0].files['skills/security-findings-analyst/SKILL.md']).toContain('scanner-only or stale reports unconfirmed')
    expect(security[0].files['skills/security-findings-analyst/SKILL.md']).toContain('join to the deployed artifact or SBOM')
    expect(security[1].files['skills/access-review-analyst/SKILL.md']).toContain('A hidden UI link does not prove a server denial')
    expect(security[1].files['skills/access-review-analyst/SKILL.md']).toContain('Access Exception to Verified Fix consumes access-review-matrix/v1')
    expect(security[2].files['skills/security-remediation-coordinator/SKILL.md']).toContain('A merged PR or passing unit test is not deployed remediation')
    expect(security[2].files['skills/security-remediation-coordinator/SKILL.md']).toContain('old-build scan or self-reviewed retest')
    expect(security[2].files['skills/security-remediation-coordinator/SKILL.md']).toContain('access-remediation-ledger/v1')
  })
})

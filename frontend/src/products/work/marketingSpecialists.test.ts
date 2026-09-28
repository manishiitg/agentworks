import { describe, expect, it } from 'vitest'
import { crewTemplates, getCrewTemplate, matchesCrewTemplateSearch, parseCrewTemplateSetupState } from './crewTemplates'

describe('Marketing Crew templates', () => {
  it('offers six distinct jobs with evidence-backed pending setup', () => {
    const marketing = crewTemplates.filter(item => item.category === 'Marketing')
    expect(marketing.map(item => item.id)).toEqual([
      'competitor-intelligence-analyst',
      'campaign-performance-analyst',
      'funnel-analyst',
      'growth-experiment-planner',
      'experiment-run-coordinator',
      'growth-outcome-analyst',
    ])
    for (const template of marketing) {
      expect(getCrewTemplate(template.id)).toBe(template)
      expect(template.version).toBe(2)
      const skill = template.files['skills/' + template.id + '/SKILL.md']
      expect(skill).toContain('## Fictional worked example')
      expect(skill).toContain('## Source probe and acceptance')
      expect(skill).toContain('## Automation handoff')
      expect(skill).toContain('Installation enables no schedule')
      const setup = parseCrewTemplateSetupState(template.files[template.setupPath], template)
      expect(setup?.checks.map(check => check.id)).toEqual([
        'identity', 'skill', 'scope', 'access', 'policy', 'first_result', 'review', 'delivery', 'recurrence',
      ])
      expect(setup?.completed_steps).toEqual([])
    }
    expect(matchesCrewTemplateSearch(marketing[0], 'competitor positioning')).toBe(true)
    expect(marketing[1].files['skills/campaign-performance-analyst/SKILL.md']).toContain('causal')
    expect(marketing[2].files['skills/funnel-analyst/SKILL.md']).toContain('funnel-observation/v1')
    expect(marketing[3].files['skills/growth-experiment-planner/SKILL.md']).toContain('funnel-experiment-plan/v1')
    expect(marketing[4].files['skills/experiment-run-coordinator/SKILL.md']).toContain('provider receipt')
    expect(marketing[5].files['skills/growth-outcome-analyst/SKILL.md']).toContain('inconclusive')
    expect(marketing[0].files['skills/competitor-intelligence-analyst/SKILL.md']).toContain('same product, plan, region, seat and term')
    expect(marketing[1].files['skills/campaign-performance-analyst/SKILL.md']).toContain('Recompute a qualified-event rate and cost')
    expect(marketing[2].files['skills/funnel-analyst/SKILL.md']).toContain('paid/eligible rate')
    expect(marketing[3].files['skills/growth-experiment-planner/SKILL.md']).toContain('baseline-first or pending plan')
    expect(marketing[4].files['skills/experiment-run-coordinator/SKILL.md']).toContain('ticket-only launch claim')
    expect(marketing[5].files['skills/growth-outcome-analyst/SKILL.md']).toContain('immature window, sample shortfall')
  })
})

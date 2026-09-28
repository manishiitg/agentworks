import { describe, expect, it } from 'vitest'
import { crewTemplates, getCrewTemplate, parseCrewTemplateSetupState } from './crewTemplates'

describe('Website Growth Crew templates', () => {
  it('makes every planned specialist selectable with its own skill and chat checklist', () => {
    const growth = crewTemplates.filter(item => item.category === 'Website Growth')
    expect(growth).toHaveLength(11)
    expect(new Set(growth.map(item => item.id)).size).toBe(growth.length)
    for (const template of growth) {
      expect(getCrewTemplate(template.id)).toBe(template)
      expect(template.files[`skills/${template.id}/SKILL.md`]).toContain(`# ${template.name}`)
      const setup = parseCrewTemplateSetupState(template.files[template.setupPath], template)
      expect(setup?.checks.length).toBeGreaterThanOrEqual(5)
      expect(setup?.checks.length).toBeLessThanOrEqual(10)
      expect(setup?.completed_steps).toEqual([])
      expect(template.files[template.setupGuidePath]).toContain('setup')
    }
  })

  it('gives each specialist a worked judgment and two checks for its hard task', () => {
    const expected: Record<string, [string, string]> = {
      'seo-analyst': ['crawl_observation', 'issue_retest'],
      'search-opportunity-mapper': ['question_sources', 'mapping_decision'],
      'content-brief-writer': ['opportunity_trace', 'claim_review'],
      'content-page-builder': ['brief_claims', 'draft_acceptance'],
      'website-publishing-coordinator': ['release_authority', 'live_retest'],
      'search-console-optimizer': ['dimension_coverage', 'reproduce_metric'],
      'traffic-engagement-analyst': ['event_definition', 'rate_reproduction'],
      'ai-visibility-analyst': ['sampling_policy', 'citation_check'],
      'landing-page-optimizer': ['journey_observation', 'decision_rule'],
      'content-distribution-coordinator': ['asset_channel_fit', 'draft_delivery_log'],
    }
    for (const [id, checks] of Object.entries(expected)) {
      const template = getCrewTemplate(id as Parameters<typeof getCrewTemplate>[0])!
      const setup = parseCrewTemplateSetupState(template.files[template.setupPath], template)!
      expect(setup.checks.map(check => check.id)).toEqual([
        'identity', 'skill', 'scope', 'inputs', 'access', ...checks, 'first_result', 'review', 'recurrence',
      ])
      const skill = template.files[`skills/${id}/SKILL.md`]
      expect(skill).toContain('Fictional')
      expect(skill).toMatch(/Inadequate:|Review failure:/)
      expect(template.files[template.setupGuidePath]).toContain('Fictional')
    }
  })

  it('gives AI visibility and mapping distinct typed handoff instructions', () => {
    expect(getCrewTemplate('ai-visibility-analyst')?.files['skills/ai-visibility-analyst/SKILL.md']).toContain('ai-visibility-snapshot/v1')
    expect(getCrewTemplate('search-opportunity-mapper')?.files['skills/search-opportunity-mapper/SKILL.md']).toContain('ai-citation-opportunity/v1')
  })

  it('gives SEO Intelligence distinct issue and opportunity artifact instructions', () => {
    const technical = getCrewTemplate('seo-analyst')
    const search = getCrewTemplate('search-opportunity-mapper')
    expect(technical.files['skills/seo-analyst/SKILL.md']).toContain('seo-issue-list/v1')
    expect(search.files['skills/search-opportunity-mapper/SKILL.md']).toContain('seo-opportunity-list/v1')
    expect(search.files['skills/search-opportunity-mapper/SKILL.md']).toContain('source_issue_artifact_id')
  })
})

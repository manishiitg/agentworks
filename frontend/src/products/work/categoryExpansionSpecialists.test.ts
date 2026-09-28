import { describe, expect, it } from 'vitest'
import { crewTemplates, crewTemplateBrowseCategory, getCrewTemplate, parseCrewTemplateSetupState } from './crewTemplates'

const newIds = [
  'product-discovery-researcher', 'roadmap-prioritization-analyst', 'product-requirements-coordinator', 'product-release-coordinator',
  'revenue-operations-analyst', 'sales-enablement-coordinator', 'pricing-packaging-analyst', 'support-knowledge-curator',
] as const

describe('balanced category Crew expansion', () => {
  it('installs eight distinct roles with real-source setup still pending', () => {
    for (const id of newIds) {
      const template = getCrewTemplate(id)
      const state = parseCrewTemplateSetupState(template.files[template.setupPath], template)
      expect(state?.completed_steps).toEqual([])
      expect(state?.checks.map(check => check.id)).toEqual([
        'identity', 'skill', 'scope', 'access', 'policy', 'first_result', 'review', 'delivery', 'recurrence',
      ])
      expect(template.files[`skills/${id}/SKILL.md`]).toContain('## Fictional worked example')
      expect(template.files[`skills/${id}/SKILL.md`]).toContain('## Source probe and acceptance')
      expect(template.files[`skills/${id}/SKILL.md`]).toContain('Inadequate:')
    }
    expect(crewTemplates.filter(item => item.category === 'Product')).toHaveLength(5)
    expect(crewTemplates.filter(item => item.category === 'GTM')).toHaveLength(5)
    expect(crewTemplates.filter(item => item.category === 'Customer Support')).toHaveLength(5)
  })

  it('blocks a misleading claim in each new category', () => {
    expect(getCrewTemplate('product-release-coordinator').files['skills/product-release-coordinator/SKILL.md']).toContain('Approval, deployment, exposure and adoption are separate states')
    expect(getCrewTemplate('revenue-operations-analyst').files['skills/revenue-operations-analyst/SKILL.md']).toContain('A form event, CRM lead and opportunity are different objects')
    expect(getCrewTemplate('support-knowledge-curator').files['skills/support-knowledge-curator/SKILL.md']).toContain('A draft or approval is not publication or deflection proof')
  })

  it('keeps every major browse category above the five-template floor', () => {
    const majorCategories = ['Finance', 'Marketing', 'Sales', 'Customer Success', 'Customer Support', 'Product', 'Operations', 'Engineering', 'GTM', 'Shopify']
    for (const category of majorCategories) {
      expect(crewTemplates.filter(item => crewTemplateBrowseCategory(item) === category).length, category).toBeGreaterThanOrEqual(5)
    }
  })
})

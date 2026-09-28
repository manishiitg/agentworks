import { describe, expect, it } from 'vitest'
import { crewTemplates, getCrewTemplate, matchesCrewTemplateSearch, parseCrewTemplateSetupState } from './crewTemplates'

describe('Shopify Crew templates', () => {
  it('offers seven merchant jobs with source-verified pending setup', () => {
    const shopify = crewTemplates.filter(item => item.category === 'Shopify')
    expect(shopify.map(item => item.id)).toEqual([
      'store-operations-coordinator',
      'returns-refunds-coordinator',
      'catalog-merchandising-analyst',
      'shopify-growth-analyst',
      'payment-operations-investigator',
      'checkout-recovery-coordinator',
      'replenishment-planner',
    ])
    for (const template of shopify) {
      expect(getCrewTemplate(template.id)).toBe(template)
      expect(template.files[`skills/${template.id}/SKILL.md`]).toContain('## Automation handoff')
      expect(template.files[`skills/${template.id}/SKILL.md`]).toContain('## Boundaries')
      expect(template.files[`skills/${template.id}/SKILL.md`]).toContain('## Source probe and decision rules')
      expect(template.files[`skills/${template.id}/SKILL.md`]).toContain('Fictional output to reject:')
      expect(template.files[template.setupGuidePath]).toContain('**Setup proof:**')
      const setup = parseCrewTemplateSetupState(template.files[template.setupPath], template)
      expect(setup?.checks.map(check => check.id)).toEqual([
        'identity', 'skill', 'scope', 'access', 'policy', 'first_result', 'review', 'action_route', 'recurrence',
      ])
      expect(setup?.completed_steps).toEqual([])
    }
    expect(matchesCrewTemplateSearch(shopify[1], 'refund policy')).toBe(true)
    expect(shopify[1].files[`skills/${shopify[1].id}/SKILL.md`]).toContain('Only a fulfilled item is eligible for a Shopify return')
    expect(shopify[2].files[`skills/${shopify[2].id}/SKILL.md`]).toContain('variant-level facts')
    expect(shopify[3].files[`skills/${shopify[3].id}/SKILL.md`]).toContain('different denominators')
    expect(shopify[4].files[`skills/${shopify[4].id}/SKILL.md`]).toContain('An authorization reserves funds')
    expect(shopify[5].files[`skills/${shopify[5].id}/SKILL.md`]).toContain('existing Shopify or provider recovery automation')
    expect(shopify[6].files[`skills/${shopify[6].id}/SKILL.md`]).toContain('Do not subtract committed units again')
  })
})

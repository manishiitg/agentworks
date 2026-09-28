import { describe, expect, it } from 'vitest'
import { crewTemplates, getCrewTemplate, matchesCrewTemplateSearch, parseCrewTemplateSetupState } from './crewTemplates'

describe('Billing capability packs', () => {
  it('offers four scoped packs with independent pending setup and reviewed action boundaries', () => {
    const packs = crewTemplates.filter(item => item.subcategory === 'Billing capability pack')
    expect(packs.map(item => item.id)).toEqual([
      'invoice-chasing', 'failed-payment-recovery', 'refund-review', 'dispute-review',
    ])
    for (const pack of packs) {
      expect(getCrewTemplate(pack.id)).toBe(pack)
      const setup = parseCrewTemplateSetupState(pack.files[pack.setupPath], pack)
      expect(setup?.checks.map(check => check.id)).toEqual([
        'identity', 'skill', 'scope', 'access', 'policy', 'first_result', 'review', 'delivery', 'recurrence',
      ])
      expect(setup?.completed_steps).toEqual([])
      const skill = pack.files[`skills/${pack.id}/SKILL.md`]
      expect(skill).toContain('## Fictional worked example')
      expect(skill).toContain('Inadequate:')
      expect(skill).toContain('Installation enables no schedule')
    }
    expect(matchesCrewTemplateSearch(packs[2], 'customer refund')).toBe(true)
    expect(packs[2].files['skills/refund-review/SKILL.md']).toContain('remaining refundable amount')
    expect(packs[3].files['skills/dispute-review/SKILL.md']).toContain('Staging is not submission')
  })
})

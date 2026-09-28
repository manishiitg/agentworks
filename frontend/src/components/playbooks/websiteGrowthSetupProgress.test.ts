import { expect, it } from 'vitest'
import { parseWebsiteGrowthSetupProgress } from './websiteGrowthSetupProgress'

const checks = ['goal_owner', 'team_bindings', 'test_run']

it('counts only expected checks with saved evidence', () => {
  const content = JSON.stringify({
    schema_version: 1, playbook_id: 'website-growth-loop', playbook_version: '0.2.0',
    checks: checks.map(id => ({ id })),
    completed_steps: ['goal_owner', 'team_bindings', 'unknown'],
    evidence: { goal_owner: 'Site and owner confirmed', team_bindings: '' },
  })
  expect(parseWebsiteGrowthSetupProgress(content, '0.2.0', checks)).toEqual({ completed: ['goal_owner'], remaining: ['team_bindings', 'test_run'] })
  expect(parseWebsiteGrowthSetupProgress(content, '0.3.0', checks)).toBeNull()
})

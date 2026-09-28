import { expect, it } from 'vitest'
import { parsePlaybookSetupProgress } from './playbookSetupProgress'

it('counts Finance setup checks only when evidence is saved for the installed version', () => {
  const checks = ['goal_owner', 'team_bindings', 'test_run']
  const content = JSON.stringify({
    schema_version: 1, playbook_id: 'finance-operations-review', playbook_version: '0.1.0',
    checks: checks.map(id => ({ id })), completed_steps: ['goal_owner', 'team_bindings'],
    evidence: { goal_owner: 'Entity and owner confirmed', team_bindings: '' },
  })
  expect(parsePlaybookSetupProgress(content, 'finance-operations-review', '0.1.0', checks)).toEqual({ completed: ['goal_owner'], remaining: ['team_bindings', 'test_run'] })
  expect(parsePlaybookSetupProgress(content, 'website-growth-loop', '0.1.0', checks)).toBeNull()
})

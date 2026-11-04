import { describe, expect, it } from 'vitest'
import type { WorkflowContractUpgradeStatus } from '../../services/api-types'
import { hasWorkflowContractUpgrade } from './workflowContractUpgradeEvents'

describe('workflow contract upgrade banner', () => {
  it('keeps a compatible workflow migration visible until the pending migration is applied', () => {
    const status: WorkflowContractUpgradeStatus = {
      required: false, current_version: '1.0.47', platform_version: '1.0.48', pending_count: 1,
      pending: [{ label: 'upgrade-description-first', target_version: '1.0.48', details: 'Review the agent sequence phases.' }],
      applied: [], history_basis: 'contract_version',
    }
    expect(hasWorkflowContractUpgrade(status)).toBe(true)
    expect(hasWorkflowContractUpgrade({ ...status, pending_count: 0 })).toBe(true)
    expect(hasWorkflowContractUpgrade({ ...status, current_version: '1.0.48', pending: [], pending_count: 0 })).toBe(false)
    expect(hasWorkflowContractUpgrade({ ...status, required: true, pending: [], pending_count: 0 })).toBe(true)
    expect(hasWorkflowContractUpgrade(null)).toBe(false)
  })
})

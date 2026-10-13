import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import type { WorkflowContractUpgradeStatus } from '../../services/api-types'
import { hasWorkflowContractUpgrade } from './workflowContractUpgradeEvents'

describe('workflow contract upgrade banner', () => {
  const source = readFileSync('src/components/workflow/WorkflowLayout.tsx', 'utf8')
  const upgradeMessage = readFileSync('src/components/workflow/workflowContractUpgradeEvents.ts', 'utf8')

  it('shows pending upgrades above chat and starts them manually', () => {
    expect(source).toContain('contractUpgrade && hasWorkflowContractUpgrade(contractUpgrade)')
    expect(source).toContain('!hasWorkflowContractUpgrade(contractUpgrade)')
    expect(source).toContain('Schedules keep running the saved version and will not update it automatically.')
    expect(source).toContain('Update workflow')
    expect(source).toContain('sendWorkspacePaneMessageToChat')
    expect(upgradeMessage).toContain('Use get_contract_upgrades')
  })

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

  it('does not offer the write action to read-only collaborators', () => {
    expect(source).toContain("activeWorkflowAccess !== 'read'")
    expect(source).toContain('canStartContractUpgrade ?')
  })
})

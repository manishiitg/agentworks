import type { WorkflowContractUpgradeStatus } from '../../services/api-types'

// An execution-compatible saved version can still owe a manual migration.
// `required` governs starting runs; pending migrations govern update visibility.
export function hasWorkflowContractUpgrade(status: WorkflowContractUpgradeStatus | null | undefined): boolean {
  return Boolean(status && (status.required || status.pending_count > 0 || status.pending?.length > 0))
}

export const WORKFLOW_CONTRACT_UPGRADE_STATUS_EVENT = 'workflow-contract-upgrade-status'

export type WorkflowContractUpgradeStatusEvent = CustomEvent<{
  workspacePath: string
  status: WorkflowContractUpgradeStatus | null
}>

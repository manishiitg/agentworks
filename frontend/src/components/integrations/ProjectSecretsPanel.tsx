import { SecretSelectionSection } from '../secrets/SecretSelectionSection'
import { useCanWriteWorkflow } from '../../hooks/useCanWriteWorkflow'
import { ProjectVaultPanel } from './ProjectVaultPanel'
import type { ProjectVaultInventory } from '../../api/projectVault'

/** The Vault secrets this project uses, ticked from the Vault picker; Add saves a new one in the owner's Vault. */
export function ProjectSecretsPanel({ workspacePath, onSelectionSaved, tabId }: {
 workspacePath:string;tabId?:string
 onSelectionSaved?:(inventory:ProjectVaultInventory)=>void
}) {
 const canWrite=useCanWriteWorkflow(workspacePath)
 return <SecretSelectionSection workflowPath={workspacePath} workspaceSecretHeading="Secrets"
  inventory={<ProjectVaultPanel workspacePath={workspacePath} tabId={tabId} disabled={!canWrite} title="Secrets" flat itemKinds={['shared_secret']} onSelectionSaved={onSelectionSaved}/>} />
}

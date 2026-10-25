import { SecretSelectionSection } from '../secrets/SecretSelectionSection'
import { useCanWriteWorkflow } from '../../hooks/useCanWriteWorkflow'
import { ProjectVaultPanel } from './ProjectVaultPanel'
import type { ProjectVaultInventory } from '../../api/projectVault'

export function ProjectSecretsPanel({ workspacePath, selectedSecrets, onSecretChange, onSelectionSaved, tabId }: {
 workspacePath:string; placeNoun:string;tabId?:string;selectedSecrets:string[]
 onSecretChange:(names:string[])=>void|Promise<unknown>
 onSelectionSaved?:(inventory:ProjectVaultInventory)=>void
}) {
 const canWrite=useCanWriteWorkflow(workspacePath)
 return <SecretSelectionSection workflowPath={workspacePath} selectedSecrets={selectedSecrets} onSecretChange={onSecretChange} workspaceSecretHeading="Secrets"
  inventory={actions=><ProjectVaultPanel workspacePath={workspacePath} tabId={tabId} disabled={!canWrite} title="Secrets" flat itemKinds={['project_secret','shared_secret']} onSelectionSaved={onSelectionSaved} renderItemActions={item=>item.kind==='project_secret'?actions(item.id):null}/>} />
}

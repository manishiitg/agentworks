import { SecretSelectionSection } from '../secrets/SecretSelectionSection'
import { useCanWriteWorkflow } from '../../hooks/useCanWriteWorkflow'
import { ProjectVaultPanel } from './ProjectVaultPanel'


/** One ordered secrets view for workflows, Relay, Crew and Code. */
export function ProjectSecretsPanel({ workspacePath, placeNoun, selectedSecrets, onSecretChange, onSharedSecretsSaved, tabId }: {
  workspacePath: string
  tabId?: string
  placeNoun: string
  selectedSecrets: string[]
  onSecretChange: (names: string[]) => void | Promise<unknown>
  onSharedSecretsSaved?: (names: string[]) => void
}) {
  const canWrite = useCanWriteWorkflow(workspacePath)
  return <div className="space-y-4">
    <SecretSelectionSection workflowPath={workspacePath} selectedSecrets={selectedSecrets}
      onSecretChange={onSecretChange} showGlobalSecrets={false} projectSource="project"
      workspaceSecretHeading={`For this ${placeNoun}`} allowGlobalPromotion={false} />
    <section aria-label="Shared from Vault" className="space-y-3 border-t border-border pt-4">
      <h3 className="text-sm font-medium">Shared from Vault</h3>
      <ProjectVaultPanel workspacePath={workspacePath} tabId={tabId} disabled={!canWrite} itemKinds={['shared_secret']} onSelectionSaved={inventory => onSharedSecretsSaved?.(inventory.selected_global_secret_names)} />
    </section>
  </div>
}

import { SecretSelectionSection } from '../secrets/SecretSelectionSection'
import { isLocalProductInstallation } from '../../products/productSurfaceConfig'

/** One ordered secrets view for workflows, Relay, Crew and Code. */
export function ProjectSecretsPanel({ workspacePath, placeNoun, selectedSecrets, selectedGlobalSecrets = [], onSecretChange, onGlobalSecretChange }: {
  workspacePath: string
  placeNoun: string
  selectedSecrets: string[]
  selectedGlobalSecrets?: string[] | null
  onSecretChange: (names: string[]) => void | Promise<unknown>
  onGlobalSecretChange?: (names: string[] | null) => void | Promise<unknown>
}) {
  return <div className="space-y-4">
    <SecretSelectionSection workflowPath={workspacePath} selectedSecrets={selectedSecrets}
      onSecretChange={onSecretChange} showGlobalSecrets={false} projectSource="project"
      workspaceSecretHeading={`${placeNoun} secrets`} allowGlobalPromotion={!isLocalProductInstallation()} />
    {!isLocalProductInstallation() && <div className="border-t border-border pt-4">
      <SecretSelectionSection workflowPath={workspacePath} selectedSecrets={selectedSecrets}
        onSecretChange={onSecretChange} projectSource="vault" workspaceSecretHeading="Vault secrets"
        selectedGlobalSecrets={selectedGlobalSecrets} onGlobalSecretChange={onGlobalSecretChange} />
    </div>}
  </div>
}

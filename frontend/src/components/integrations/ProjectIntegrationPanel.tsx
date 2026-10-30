import { GitHubVaultCard } from './GitHubVault'
import { useEffect, useState, type ReactNode } from 'react'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { Button } from '../ui/Button'
import WorkflowEmailPanel from '../workflow/WorkflowEmailPanel'
import { ProjectMcpPanel } from './ProjectMcpPanel'
import { ProjectSecretsPanel } from './ProjectSecretsPanel'
import { ProductSlackPanel } from './ProductSlackPanel'
import { CompanySignInSetup } from './CompanySignInSetup'
import { WorkspacePanelActions } from '../workspace/WorkspacePanelHeaderSlots'
import type { ProjectVaultInventory } from '../../api/projectVault'
import type { VaultItemKind } from '../../api/projectVault'
import { ProjectVaultPanel } from './ProjectVaultPanel'

export type IntegrationSection = 'connected' | 'available' | 'secrets' | 'gmail' | 'slack-account' | 'laptop' | 'github'
export const INTEGRATION_LABELS: Record<IntegrationSection, string> = {
  github: 'GitHub', connected: 'MCPs', available: 'Available MCPs', secrets: 'Secrets', gmail: 'Google apps', 'slack-account': 'Slack', laptop: 'Local laptop',
}
export type ProjectSecretControls = {
  selectedSecrets: string[]
  onSelectedSecretsChange: (names: string[]) => Promise<unknown> | void
  onSecretSelectionSaved?: (inventory: ProjectVaultInventory) => void
}

type Props = ProjectSecretControls & {
  workspacePath: string; placeNoun: string; section?: IntegrationSection; canEdit: boolean; tabId?: string
  onAsk: (message: string) => Promise<void>; laptop?: ReactNode; onAppsSaved?: (servers: string[]) => void
  /** Studio projects offer Connect GitHub beside their secrets (PLAT-746-158 B8); Cowork shows nothing new. */
  github?: boolean
}
/** Familiar menu entries open the same native editors directly. */
export function ProjectIntegrationPanel(props: Props) {
  const actor = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  return <ProjectIntegrationContents key={`${server}:${actor}:${props.workspacePath}`} {...props} />
}
function ProjectIntegrationContents({ workspacePath, tabId, placeNoun, section = 'connected', canEdit, onAsk, laptop, onAppsSaved, github = false, ...secrets }: Props) {
  const [apps, setApps] = useState<'connected' | 'available'>(section === 'available' ? 'available' : 'connected')
  useEffect(() => { if (section === 'connected' || section === 'available') setApps(section) }, [section])
  const kinds: Record<IntegrationSection, VaultItemKind[]> = {github:[],connected:['mcp'],available:['mcp'],secrets:['project_secret','shared_secret'],gmail:['google'],'slack-account':['slack'],laptop:['computer']}
  if (section === 'github' && (!canEdit || !github)) return <p className="text-sm text-muted-foreground">Open your own Code project to manage your GitHub connection.</p>
  if (!canEdit) return <ProjectVaultPanel workspacePath={workspacePath} tabId={tabId} disabled title={INTEGRATION_LABELS[section]} itemKinds={kinds[section]} />
  return <div className="space-y-5" aria-label={INTEGRATION_LABELS[section]}>
    {(section === 'connected' || section === 'available') && <>
      <WorkspacePanelActions><Button size="sm" variant="outline" onClick={() => setApps(apps === 'connected' ? 'available' : 'connected')}>{apps === 'connected' ? 'Add an app' : 'Back to connected apps'}</Button></WorkspacePanelActions>
      <ProjectMcpPanel view={apps} workspacePath={workspacePath} tabId={tabId} placeNoun={placeNoun} canEdit={canEdit} onAsk={onAsk} onSelectionSaved={onAppsSaved} />

    </>}
    {section === 'github' && github && <GitHubVaultCard changeConnectionOpen />}
    {section === 'secrets' && github && <GitHubVaultCard />}
    {section === 'secrets' && <><ProjectSecretsPanel workspacePath={workspacePath} tabId={tabId} placeNoun={placeNoun} selectedSecrets={secrets.selectedSecrets} onSecretChange={secrets.onSelectedSecretsChange} onSelectionSaved={secrets.onSecretSelectionSaved} /></>}
    {section === 'gmail' && <><WorkflowEmailPanel workspacePath={workspacePath} /><CompanySignInSetup provider="Google" tabId={tabId} /></>}
    {section === 'slack-account' && <><ProductSlackPanel workspacePath={workspacePath} noun={placeNoun} section="account" allowBots={placeNoun !== 'Flow'} tabId={tabId}/></>}
    {section === 'laptop' && laptop}
  </div>
}

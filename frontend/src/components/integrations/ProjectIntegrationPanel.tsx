import { useEffect, useState, type ReactNode } from 'react'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { Button } from '../ui/Button'
import WorkflowEmailPanel from '../workflow/WorkflowEmailPanel'
import { ProjectMcpPanel } from './ProjectMcpPanel'
import { ProjectSecretsPanel } from './ProjectSecretsPanel'
import PersonalAccountsPanel from './PersonalAccountsPanel'
import { ProjectAccountSharingPanel, CompanySignInSetup } from './ProjectAccountSharingPanel'
import type { VaultItemKind } from '../../api/projectVault'
import { ProjectVaultPanel } from './ProjectVaultPanel'

export type IntegrationSection = 'connected' | 'available' | 'secrets' | 'gmail' | 'slack-account' | 'laptop'
export const INTEGRATION_LABELS: Record<IntegrationSection, string> = {
  connected: 'MCPs', available: 'Available MCPs', secrets: 'Secrets', gmail: 'Google apps', 'slack-account': 'Slack account', laptop: 'Local laptop',
}
export type ProjectSecretControls = {
  selectedSecrets: string[]
  onSelectedSecretsChange: (names: string[]) => Promise<unknown> | void
  onSharedSecretsSaved?: (names: string[]) => void
}

type Props = ProjectSecretControls & {
  workspacePath: string; placeNoun: string; section?: IntegrationSection; canEdit: boolean; tabId?: string
  onAsk: (message: string) => Promise<void>; laptop?: ReactNode; onAppsSaved?: (servers: string[]) => void
}
/** Familiar menu entries open the same native editors directly. */
export function ProjectIntegrationPanel(props: Props) {
  const actor = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  return <ProjectIntegrationContents key={`${server}:${actor}:${props.workspacePath}`} {...props} />
}
function ProjectIntegrationContents({ workspacePath, tabId, placeNoun, section = 'connected', canEdit, onAsk, laptop, onAppsSaved, ...secrets }: Props) {
  const [apps, setApps] = useState<'connected' | 'available'>(section === 'available' ? 'available' : 'connected')
  useEffect(() => { if (section === 'connected' || section === 'available') setApps(section) }, [section])
  const kinds: Record<IntegrationSection, VaultItemKind[]> = {connected:['mcp'],available:['mcp'],secrets:['project_secret','shared_secret'],gmail:['google'],'slack-account':['slack'],laptop:['computer']}
  if (!canEdit) return <ProjectVaultPanel workspacePath={workspacePath} tabId={tabId} disabled title={INTEGRATION_LABELS[section]} itemKinds={kinds[section]} />
  return <div className="space-y-5" aria-label={INTEGRATION_LABELS[section]}>
    {(section === 'connected' || section === 'available') && <>
      <div className="flex justify-end"><Button size="sm" variant="outline" onClick={() => setApps(apps === 'connected' ? 'available' : 'connected')}>{apps === 'connected' ? 'Add an app' : 'Back to connected apps'}</Button></div>
      <ProjectMcpPanel view={apps} workspacePath={workspacePath} tabId={tabId} placeNoun={placeNoun} canEdit={canEdit} onAsk={onAsk} onSelectionSaved={onAppsSaved} />
      <ProjectAccountSharingPanel workspacePath={workspacePath} tabId={tabId} kind="apps" />
    </>}
    {section === 'secrets' && <><ProjectSecretsPanel workspacePath={workspacePath} tabId={tabId} placeNoun={placeNoun} selectedSecrets={secrets.selectedSecrets} onSecretChange={secrets.onSelectedSecretsChange} onSharedSecretsSaved={secrets.onSharedSecretsSaved} /><ProjectAccountSharingPanel workspacePath={workspacePath} tabId={tabId} kind="secrets" /></>}
    {section === 'gmail' && <><WorkflowEmailPanel workspacePath={workspacePath} scopeNoun={placeNoun === 'Flow' ? 'relay' : 'project'} onAsk={onAsk} /><CompanySignInSetup provider="Google" tabId={tabId} /></>}
    {section === 'slack-account' && <><PersonalAccountsPanel workspacePath={workspacePath} provider="slack" /><CompanySignInSetup provider="Slack" tabId={tabId} /></>}
    {section === 'laptop' && laptop}
  </div>
}

import { useEffect, useState, type ReactNode } from 'react'
import { SecretSelectionSection } from '../../components/secrets/SecretSelectionSection'
import { UserRound, UsersRound } from 'lucide-react'
import { VAULT_PANELS } from '../productPanels'
import { WorkspaceViewHeader } from '../../components/workflow/WorkspaceViewHeader'
import { SettingsCardLayout } from '../../components/ui/SettingsCard'
import { GatewayGroupsPanel } from './GatewayGroupsPanel'
import { GatewayUsersPanel } from './GatewayUsersPanel'
import { useVaultReadOnly } from './vaultReadOnly'
import { GatewayFeedbackBoundary } from './gatewayConsoleShared'

// The list lives in products/productPanels.ts with every other product's panels.
export const gatewayPanels = VAULT_PANELS
export type GatewayPanel = (typeof gatewayPanels)[number]['id']

export function GatewayWorkspacePane({ base, servers, panel, chatBusy, modelSettings, revision, hideHeader = true, peopleTabRequest }: {
  base: string; servers: ReactNode; panel: GatewayPanel; chatBusy: boolean; modelSettings: ReactNode; revision?: string; hideHeader?: boolean
  /** ⌘/Ctrl+K opens People on Users or Groups; the token lets the same request repeat. */
  peopleTabRequest?: { tab: 'users' | 'groups'; token: number }
}) {
  const [peopleTab, setPeopleTab] = useState<'users' | 'groups'>(peopleTabRequest?.tab ?? 'users')
  useEffect(() => { if (peopleTabRequest) setPeopleTab(peopleTabRequest.tab) }, [peopleTabRequest])
  const currentPanel = gatewayPanels.find(item => item.id === panel) ?? gatewayPanels[0]
  const readOnly = useVaultReadOnly()
  return <div className="flex h-full min-h-0 flex-col">
    <WorkspaceViewHeader hideHeader={hideHeader} icon={currentPanel.icon} title={currentPanel.label} showWalkthrough={false}
      tabs={panel === 'people' ? {
        value: peopleTab, onChange: value => setPeopleTab(value === 'groups' ? 'groups' : 'users'),
        options: [{ value: 'users', label: 'Users', icon: UserRound }, { value: 'groups', label: 'Groups', icon: UsersRound }], ariaLabel: 'People tabs',
      } : undefined} />
    <div className="min-h-0 flex-1 space-y-4 overflow-y-auto p-4">
      <SettingsCardLayout unboxed={hideHeader}>
      <GatewayFeedbackBoundary>
      {(panel === 'servers' || panel === 'available-mcps') && servers}
      {panel === 'access' && <GatewayGroupsPanel base={base} revision={revision} chatBusy={chatBusy} />}
      {panel === 'people' && (peopleTab === 'users' ? (readOnly ? <ReaderNote>Accounts are managed by administrators. To see what one person can reach, ask the Vault chat (inspect a person).</ReaderNote> : <GatewayUsersPanel base={base} />) : <GatewayGroupsPanel base={base} directoryOnly revision={revision} />)}
      {panel === 'secrets' && (readOnly ? <ReaderNote>Secret values are managed by Vault managers. Each group's Secrets tab lists the secret names it can use.</ReaderNote>
        : <SecretSelectionSection key={revision} mode="vault" selectedSecrets={[]} onSecretChange={() => {}} />)}
      {panel === 'models' && (readOnly ? <ReaderNote>The Vault chat's model is set by Vault managers.</ReaderNote> : modelSettings)}
      </GatewayFeedbackBoundary>
      </SettingsCardLayout>
    </div>
  </div>
}

function ReaderNote({ children }: { children: ReactNode }) {
  return <p className="rounded-md border border-border/60 p-3 text-xs text-muted-foreground" role="status">{children}</p>
}

import { useState, type ReactNode } from 'react'
import { SecretSelectionSection } from '../../components/secrets/SecretSelectionSection'
import { BarChart3, BrainCircuit, KeyRound, Plus, PlugZap, ScrollText, Server, ShieldAlert, ShieldCheck, UserRound, UsersRound } from 'lucide-react'
import { WorkspaceViewHeader } from '../../components/workflow/WorkspaceViewHeader'
import { SettingsCardLayout } from '../../components/ui/SettingsCard'
import { GatewayGroupsPanel } from './GatewayGroupsPanel'
import { GatewayUsersPanel } from './GatewayUsersPanel'
import { GatewayAuditPanel } from './GatewayAuditPanel'
import { GatewayPIIPanel } from './GatewayPIIPanel'
import { GatewayConnectPanel } from './GatewayConnectPanel'
import { GatewayFeedbackBoundary } from './gatewayConsoleShared'

export const gatewayPanels = [
  { id: 'access', label: 'Access', icon: ShieldCheck },
  { id: 'servers', label: 'Connected MCPs', icon: Server },
  { id: 'available-mcps', label: 'Available MCPs', icon: Plus },
  { id: 'secrets', label: 'Secrets', icon: KeyRound },
  { id: 'people', label: 'People', icon: UsersRound },
  { id: 'audit', label: 'Audit', icon: ScrollText },
  { id: 'pii', label: 'PII', icon: ShieldAlert },
  { id: 'models', label: 'Models', icon: BrainCircuit },
  { id: 'connect', label: 'Connect', icon: PlugZap },
] as const
export type GatewayPanel = (typeof gatewayPanels)[number]['id']

export function GatewayWorkspacePane({ base, servers, panel, chatBusy, modelSettings, revision, hideHeader = true }: {
  base: string; servers: ReactNode; panel: GatewayPanel; chatBusy: boolean; modelSettings: ReactNode; revision?: string; hideHeader?: boolean
}) {
  const [peopleTab, setPeopleTab] = useState<'users' | 'groups'>('users')
  const [auditTab, setAuditTab] = useState<'logs' | 'analysis'>('logs')
  const currentPanel = gatewayPanels.find(item => item.id === panel) ?? gatewayPanels[0]
  return <div className="flex h-full min-h-0 flex-col">
    <WorkspaceViewHeader hideHeader={hideHeader} icon={currentPanel.icon} title={currentPanel.label} showWalkthrough={false}
      tabs={panel === 'people' ? {
        value: peopleTab, onChange: value => setPeopleTab(value === 'groups' ? 'groups' : 'users'),
        options: [{ value: 'users', label: 'Users', icon: UserRound }, { value: 'groups', label: 'Groups', icon: UsersRound }], ariaLabel: 'People tabs',
      } : panel === 'audit' ? {
        value: auditTab, onChange: value => setAuditTab(value === 'analysis' ? 'analysis' : 'logs'),
        options: [{ value: 'logs', label: 'Logs', icon: ScrollText }, { value: 'analysis', label: 'Analysis', icon: BarChart3 }], ariaLabel: 'Audit tabs',
      } : undefined} />
    <div className="min-h-0 flex-1 space-y-4 overflow-y-auto p-4">
      <SettingsCardLayout unboxed={hideHeader}>
      <GatewayFeedbackBoundary>
      {(panel === 'servers' || panel === 'available-mcps') && servers}
      {panel === 'access' && <GatewayGroupsPanel base={base} revision={revision} chatBusy={chatBusy} />}
      {panel === 'people' && (peopleTab === 'users' ? <GatewayUsersPanel base={base} /> : <GatewayGroupsPanel base={base} directoryOnly revision={revision} />)}
      {panel === 'secrets' && <SecretSelectionSection key={revision} mode="vault" selectedSecrets={[]} onSecretChange={() => {}} />}
      {panel === 'audit' && <GatewayAuditPanel base={base} tab={auditTab} />}
      {panel === 'pii' && <GatewayPIIPanel base={base} />}
      {panel === 'models' && modelSettings}
      {panel === 'connect' && <GatewayConnectPanel base={base} />}
      </GatewayFeedbackBoundary>
      </SettingsCardLayout>
    </div>
  </div>
}

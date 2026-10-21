import { MessageCircle, PlugZap, ScrollText, Vault } from 'lucide-react'
import { WorkspaceContextCard, WorkspaceContextRow as Row, WorkspaceContextSection as Section } from '../../components/workspace/WorkspaceContextCard'
import { useAuthStore } from '../../stores/useAuthStore'
import { gatewayPanels, type GatewayPanel } from './GatewayWorkspacePane'

export type VaultPage = 'audit' | 'connect' | 'my-vaults'

/** Shortcuts reuse the existing Vault destinations and their permission checks. */
export function GatewayContextPanel({ panel, page, onOpen, onPage, onChat, readOnly }: {
  panel: GatewayPanel
  page: VaultPage | null
  onOpen: (panel: GatewayPanel) => void
  onPage: (page: VaultPage) => void
  onChat: () => void
  readOnly: boolean
}) {
  const canOpenMyVaults = useAuthStore(state => Boolean(state.user && state.user.can_create !== false))
  return <WorkspaceContextCard title="Vault" summary="Manage tools and who can use them." subtle>
    <Section title="Connections">
      {canOpenMyVaults && <Row icon={Vault} active={page === 'my-vaults'} hint="Your accounts and vaults shared with you" onClick={() => onPage('my-vaults')}>My vaults</Row>}
      {gatewayPanels.filter(item => ['servers', 'available-mcps', 'secrets'].includes(item.id)).map(item => <Row key={item.id} icon={item.icon}
        active={!page && panel === item.id} onClick={() => onOpen(item.id)}>{item.label}</Row>)}
    </Section>
    <Section title="Permissions">
      {gatewayPanels.filter(item => ['access', 'people'].includes(item.id)).map(item => <Row key={item.id} icon={item.icon}
        active={!page && panel === item.id} onClick={() => onOpen(item.id)}>{item.label}</Row>)}
    </Section>
    <Section title="Activity & setup">
        <Row icon={ScrollText} active={page === 'audit'} onClick={() => onPage('audit')}>Audit logs</Row>
        <Row icon={PlugZap} active={page === 'connect'} onClick={() => onPage('connect')}>Vault MCP endpoint</Row>
        {gatewayPanels.filter(item => item.id === 'models').map(item => <Row key={item.id} icon={item.icon}
          active={!page && panel === item.id} onClick={() => onOpen(item.id)}>{item.label}</Row>)}
    </Section>
    <div className="border-t border-border pt-2"><Row icon={MessageCircle} hint="Open the existing assistant without sending a message" onClick={onChat}>Vault assistant</Row></div>
    {readOnly && <p className="border-t border-border pt-3 text-[11px] leading-4 text-muted-foreground">You can view this setup. A Vault administrator manages changes.</p>}
  </WorkspaceContextCard>
}

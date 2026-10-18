import { MessageCircle, PlugZap, ScrollText } from 'lucide-react'
import { WorkspaceContextCard, WorkspaceContextRow as Row, WorkspaceContextSection as Section } from '../../components/workspace/WorkspaceContextCard'
import { gatewayPanels, type GatewayPanel } from './GatewayWorkspacePane'

export type VaultPage = 'audit' | 'connect'

/** Shortcuts reuse the existing Vault destinations and their permission checks. */
export function GatewayContextPanel({ panel, page, onOpen, onPage, onChat, readOnly }: {
  panel: GatewayPanel
  page: VaultPage | null
  onOpen: (panel: GatewayPanel) => void
  onPage: (page: VaultPage) => void
  onChat: () => void
  readOnly: boolean
}) {
  return <WorkspaceContextCard title="Vault" summary="Tools, secrets and access for your agents.">
    <Section title="Tools & access">
      {gatewayPanels.filter(item => item.id !== 'models').map(item => <Row key={item.id} icon={item.icon}
        active={!page && panel === item.id} onClick={() => onOpen(item.id)}>{item.label}</Row>)}
    </Section>
    <Section title="Activity & connections">
      <Row icon={ScrollText} active={page === 'audit'} onClick={() => onPage('audit')}>Audit logs</Row>
      <Row icon={PlugZap} active={page === 'connect'} onClick={() => onPage('connect')}>Vault MCP endpoint</Row>
    </Section>
    <Section title="Assistant">
      <Row icon={MessageCircle} hint="Open the existing assistant without sending a message" onClick={onChat}>Vault assistant</Row>
      {gatewayPanels.filter(item => item.id === 'models').map(item => <Row key={item.id} icon={item.icon}
        active={!page && panel === item.id} onClick={() => onOpen(item.id)}>{item.label}</Row>)}
    </Section>
    {readOnly && <p className="border-t border-border pt-3 text-[11px] leading-4 text-muted-foreground">You can view this setup. A Vault administrator manages changes.</p>}
  </WorkspaceContextCard>
}

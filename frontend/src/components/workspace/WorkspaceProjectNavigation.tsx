import { AppWindow, Braces, CalendarClock, Mail, MessageCircle, Slack, Webhook, Zap } from 'lucide-react'
import { useGmailInboundUIEnabled } from '../workflow/bots/gmailAskAI'
import { useAuthStore } from '../../stores/useAuthStore'
import { workspaceIntegrationShortcuts } from './workspaceIntegrationShortcuts'
import { WorkspaceContextRow as Row } from './WorkspaceContextCard'


export function projectConnectionSections(options: Parameters<typeof workspaceIntegrationShortcuts>[0]) {
  const admitted = workspaceIntegrationShortcuts(options)
  return ['brain', 'secrets', 'connected', 'gmail', 'slack', 'folders'].flatMap(id => admitted.find(item => item.id === `mcp:${id}`) ?? [])
}

export function projectChannelSections(slack: boolean, whatsapp: boolean) {
  return [
    ...(slack ? [{ id: 'slack-app', label: 'Slack app', icon: Slack }] : []),
    ...(whatsapp ? [{ id: 'whatsapp', label: 'WhatsApp', icon: MessageCircle }] : []),
    { id: 'cli', label: 'MCP access', icon: AppWindow },
  ]
}

export const PROJECT_AUTOMATIONS = [
  { id: 'schedules', label: 'Schedules', icon: CalendarClock },
  { id: 'conditions', label: 'Watchers', icon: Zap },
  { id: 'triggers', label: 'Webhooks', icon: Webhook },
  { id: 'functions', label: 'Functions', icon: Braces },
] as const

export function projectAutomationSections({ relay = false, triggers = true, incomingEmail = false }: { relay?: boolean; triggers?: boolean; incomingEmail?: boolean } = {}) {
  return [
    ...PROJECT_AUTOMATIONS.filter(item => (!relay || item.id === 'triggers' || item.id === 'conditions') && (triggers || item.id === 'schedules')),
    ...(!relay && triggers && incomingEmail ? [{ id: 'email', label: 'Incoming email', icon: Mail }] : []),
  ]
}

export function WorkspaceAutomationRows({ onOpen, relay = false, triggers = true }: { onOpen: (section: string) => void; relay?: boolean; triggers?: boolean }) {
  const incomingEmail = useGmailInboundUIEnabled()
  return <div className="mt-2">
    <h3 className="px-1 py-1 text-[10px] text-muted-foreground">Automations</h3>
    {projectAutomationSections({ relay, triggers, incomingEmail }).map(item =>
      <Row key={item.id} icon={item.icon} onClick={() => onOpen(item.id)}>{item.label}</Row>)}
  </div>
}

export function WorkspaceChannelRows({ onOpen, slack, whatsapp }: { onOpen: (section: string) => void; slack: boolean; whatsapp: boolean }) {
  return <div className="mt-2">
    <h3 className="px-1 py-1 text-[10px] text-muted-foreground">Channels</h3>
    {projectChannelSections(slack, whatsapp).map(item => <Row key={item.id} icon={item.icon} onClick={() => onOpen(item.id)}>{item.label}</Row>)}
  </div>
}

/** Uses the same already-admitted connection options as the existing panels. */
export function WorkspaceConnectionRows({ onOpen, relayMode = false, workMode = false, enabledPanels }: { onOpen: (section: string) => void; relayMode?: boolean; workMode?: boolean; enabledPanels?: Set<string> }) {
  const allowedProducts = useAuthStore(state => state.user?.allowed_products)
  const options = projectConnectionSections({ allowedProducts, relayMode, workMode, enabledPanels, onOpen })
  return <>
    {options.map(item => <Row key={item.id} icon={item.icon} onClick={item.onClick}>{item.label}</Row>)}
  </>
}

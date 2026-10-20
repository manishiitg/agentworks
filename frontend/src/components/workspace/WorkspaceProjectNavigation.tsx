import { Braces, CalendarClock, Mail, MessageCircle, Plug, Slack, Webhook, Zap } from 'lucide-react'
import { useAgentInboxEnabled } from '../integrations/AgentInboxPanel'
import { useAuthStore } from '../../stores/useAuthStore'
import { workspaceIntegrationShortcuts } from './workspaceIntegrationShortcuts'
import { WorkspaceContextRow as Row } from './WorkspaceContextCard'
import type { ProjectStatus } from './useProjectStatus'


export function projectConnectionSections(options: Parameters<typeof workspaceIntegrationShortcuts>[0]) {
  const admitted = workspaceIntegrationShortcuts(options)
  return ['brain', 'secrets', 'connected', 'gmail', 'folders'].flatMap(id => admitted.find(item => item.id === `mcp:${id}`) ?? [])
}

export function projectChannelSections(slack: boolean, whatsapp: boolean) {
  return [
    ...(slack ? [{ id: 'slack', label: 'Slack', icon: Slack }] : []),
    ...(whatsapp ? [{ id: 'whatsapp', label: 'WhatsApp', icon: MessageCircle }] : []),
    { id: 'cli', label: 'MCP access', icon: Plug },
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
    ...(incomingEmail ? [{ id: 'email', label: 'Inbox', icon: Mail }] : []),
  ]
}

const countText = (value?: number) => (value === undefined ? undefined : value ? String(value) : 'None')

export function WorkspaceAutomationRows({ onOpen, relay = false, triggers = true, counts }: { onOpen: (section: string) => void; relay?: boolean; triggers?: boolean; counts?: ProjectStatus }) {
  const byId: Record<string, number | undefined> = { schedules: counts?.schedules, conditions: counts?.conditions, triggers: counts?.triggers, functions: counts?.functions }
  return <div role="group" aria-label="Automations" className="mt-1 space-y-1 border-t border-border/60 pt-2">
    {projectAutomationSections({ relay, triggers }).map(item =>
      <Row key={item.id} icon={item.icon} detail={countText(byId[item.id])} onClick={() => onOpen(item.id)}>{item.label}</Row>)}
  </div>
}

/** Inbox is a way to reach the project, so it sits with the other channels. */
export function WorkspaceChannelRows({ onOpen, slack, whatsapp, slackBot = true, slackAccountStatus, slackBotStatus, whatsappStatus, emailStatus, onOpenEmail }: { onOpen: (section: string) => void; slack: boolean; whatsapp: boolean; slackBot?: boolean; slackAccountStatus?: string; slackBotStatus?: string; whatsappStatus?: string; emailStatus?: string; onOpenEmail?: () => void }) {
  const incomingEmail = useAgentInboxEnabled()
  return <div role="group" aria-label="Channels" className="mt-1 space-y-1 border-t border-border/60 pt-2">
    {projectChannelSections(slack, whatsapp).flatMap(item => [
      ...(item.id === 'slack' ? [
        <Row key="slack-account" icon={item.icon} detail={slackAccountStatus} onClick={() => onOpen('slack')}>Slack as me</Row>,
        ...(slackBot ? [<Row key="slack-bot" icon={item.icon} detail={slackBotStatus} onClick={() => onOpen('slack')}>Slack bot</Row>] : []),
      ] : [<Row key={item.id} icon={item.icon} detail={item.id === 'whatsapp' ? whatsappStatus : undefined} onClick={() => onOpen(item.id)}>{item.label}</Row>]),
      ...(item.id === (whatsapp ? 'whatsapp' : slack ? 'slack' : '') && onOpenEmail && incomingEmail ? [<Row key="email" icon={Mail} detail={emailStatus} onClick={onOpenEmail}>Inbox</Row>] : []),
    ])}
  </div>
}

/** Uses the same already-admitted connection options as the existing panels. */
export function WorkspaceConnectionRows({ onOpen, relayMode = false, workMode = false, enabledPanels, details }: { onOpen: (section: string) => void; relayMode?: boolean; workMode?: boolean; enabledPanels?: Set<string>; details?: Record<string, string | undefined> }) {
  const allowedProducts = useAuthStore(state => state.user?.allowed_products)
  const options = projectConnectionSections({ allowedProducts, relayMode, workMode, enabledPanels, onOpen })
  return <>
    {options.map(item => <Row key={item.id} icon={item.icon} detail={details?.[item.id]} onClick={item.onClick}>{item.label}</Row>)}
  </>
}

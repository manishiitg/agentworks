import { Braces, CalendarClock, Mail, MessageCircle, Plug, Slack, Webhook, Zap } from 'lucide-react'
import { workspaceIntegrationShortcuts } from './workspaceIntegrationShortcuts'


export function projectConnectionSections(options: Parameters<typeof workspaceIntegrationShortcuts>[0]) {
  const admitted = workspaceIntegrationShortcuts(options)
  return ['connected', 'secrets', 'brain', 'gmail', 'folders'].flatMap(id => admitted.find(item => item.id === `mcp:${id}`) ?? [])
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

export function projectAutomationSections({ triggers = true, incomingEmail = false }: { triggers?: boolean; incomingEmail?: boolean } = {}) {
  return [
    ...PROJECT_AUTOMATIONS.filter(item => (triggers || item.id === 'schedules')),
    ...(incomingEmail ? [{ id: 'email', label: 'Inbox', icon: Mail }] : []),
  ]
}

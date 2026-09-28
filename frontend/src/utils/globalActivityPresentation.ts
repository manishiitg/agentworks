import type { ChatTab } from '../stores/useChatStore'
import type { ActiveSessionInfo } from '../services/api-types'

export type ActivityType = 'Scheduled' | 'Webhook' | 'Manual' | 'Bot' | 'Chat'

/** Tooltip for each activity type's icon, so every row says what started it. */
export const activityTypeLabels: Record<ActivityType, string> = {
  Scheduled: 'Scheduled run',
  Webhook: 'Webhook or function call',
  Manual: 'Manual run',
  Bot: 'Bot conversation (Slack, WhatsApp…)',
  Chat: 'Chat with a person',
}

export function crewActivityTitle(tab: ChatTab | undefined, fallback: string): string {
  return tab?.metadata?.agentProfileIdentityName?.trim() ||
    tab?.metadata?.agentProfileProjectTitle?.trim() ||
    fallback
}

const botPlatformNames: Record<string, string> = { slack: 'Slack', whatsapp: 'WhatsApp', telegram: 'Telegram', discord: 'Discord', teams: 'Teams' }

/** The bot's platform name ("Slack", "WhatsApp"), from the session's
 * bot_platform or its "bot-<platform>-" session ID; empty when unknown. */
export function botPlatformLabel(botPlatform: string | undefined, sessionId: string): string {
  const raw = (botPlatform || '').trim().toLowerCase() || (/^bot-([a-z]+)-/i.exec(sessionId)?.[1] || '').toLowerCase()
  if (!raw) return ''
  return botPlatformNames[raw] || raw.charAt(0).toUpperCase() + raw.slice(1)
}

/** What started a running session, for every active-work row: the server's
 * label when it has one ("Called by RTS Flow Tester", "Schedule: Daily
 * digest"), else a name for the trigger kind. */
export function sessionOriginLabel(session: Pick<ActiveSessionInfo, 'session_id' | 'triggered_by' | 'triggered_by_label' | 'bot_platform'>): string {
  const label = session.triggered_by_label?.trim()
  if (label) return label
  const trigger = (session.triggered_by || '').trim().toLowerCase()
  const id = session.session_id.toLowerCase()
  const platform = botPlatformLabel(session.bot_platform, session.session_id)
  if (platform || trigger.startsWith('bot')) return `${platform || 'Bot'} message`
  if (trigger === 'webhook' || id.startsWith('schedule-webhook--')) return 'Webhook'
  if (trigger === 'cron' || id.startsWith('schedule-')) return 'Schedule'
  if (trigger === 'external') return 'MCP / API call'
  if (trigger === 'auto_notification') return 'Background result'
  if (trigger === 'manual') return 'Started manually'
  if (trigger === 'workflow_builder' || trigger === 'workflow_phase') return 'Automation builder'
  return 'Chat'
}

/** Drops a trailing " · <origin>" from a title that already carries it, so
 * a row does not repeat its origin line. */
export function titleWithoutOrigin(title: string, origin: string): string {
  const suffix = ` · ${origin}`
  return origin && title.endsWith(suffix) && title.length > suffix.length ? title.slice(0, -suffix.length) : title
}

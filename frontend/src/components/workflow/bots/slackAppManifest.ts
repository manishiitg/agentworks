// The one list of what an AgentWorks Slack app needs. The permissions
// checklist renders it and the app manifest is generated from it, so the
// two can never drift. The server's "Save & test" check
// (services/slack_connection_diagnostics.go) verifies the same required scopes.

export const SLACK_REQUIRED_SCOPE_GROUPS: ReadonlyArray<{ label: string; scopes: readonly string[] }> = [
  { label: 'Read mentions and channel context', scopes: ['app_mentions:read', 'channels:history', 'groups:history', 'channels:read', 'groups:read'] },
  { label: 'Reply and react', scopes: ['chat:write', 'reactions:write'] },
  { label: 'Identify the sender', scopes: ['users:read', 'users:read.email'] },
]

export const SLACK_REQUIRED_SCOPES: readonly string[] = SLACK_REQUIRED_SCOPE_GROUPS.flatMap(group => group.scopes)

export const SLACK_OPTIONAL_SCOPES: ReadonlyArray<{ scope: string; purpose: string }> = [
  { scope: 'files:read', purpose: 'read incoming attachments' },
  { scope: 'chat:write.public', purpose: 'post in public channels without joining them' },
]

export const SLACK_BOT_EVENTS: readonly string[] = ['app_mention', 'message.channels', 'message.groups']

// 1:1 direct messages with the bot. A DM runs with the sender's own
// AgentWorks permissions (matched by their Slack email); channels always run
// in Run mode. Channel bots work without these, so "Save & test" reports
// them as needed for DMs rather than failing.
export const SLACK_DM_SCOPES: readonly string[] = ['im:history', 'im:read', 'im:write']
export const SLACK_DM_EVENTS: readonly string[] = ['message.im']

/** App-level token scope for Socket Mode (not part of the manifest). */
export const SLACK_APP_TOKEN_SCOPE = 'connections:write'

/** The bot posts buttons (confirm/cancel, feedback) and reads the clicks over Socket Mode. */
export const SLACK_INTERACTIVITY_REQUIRED = true

export const SLACK_DEFAULT_APP_NAME = 'AgentWorks'

// Slack limits: display_information.name ≤ 35 chars; bot_user.display_name
// ≤ 80 chars of a-z, 0-9, '-', '_' and '.'.
const APP_NAME_MAX = 35
const BOT_DISPLAY_NAME_MAX = 80

export function slackBotDisplayName(name: string): string {
  const slug = name.toLowerCase().replace(/[^a-z0-9._-]+/g, '-').replace(/-{2,}/g, '-').replace(/^[-.]+|[-.]+$/g, '').slice(0, BOT_DISPLAY_NAME_MAX)
  return slug || SLACK_DEFAULT_APP_NAME.toLowerCase()
}

export interface SlackAppManifest {
  display_information: { name: string }
  features: {
    app_home: { home_tab_enabled: boolean; messages_tab_enabled: boolean; messages_tab_read_only_enabled: boolean }
    bot_user: { display_name: string; always_online: boolean }
  }
  oauth_config: { scopes: { bot: string[] } }
  settings: {
    event_subscriptions: { bot_events: string[] }
    interactivity?: { is_enabled: boolean }
    org_deploy_enabled: boolean
    socket_mode_enabled: boolean
    token_rotation_enabled: boolean
  }
}

export function buildSlackAppManifest({ name, optionalScopes = [], directMessagesOnly = false }: { name?: string; optionalScopes?: readonly string[]; directMessagesOnly?: boolean } = {}): SlackAppManifest {
  const appName = (name || '').trim().slice(0, APP_NAME_MAX).trim() || SLACK_DEFAULT_APP_NAME
  const allowedOptional = new Set(SLACK_OPTIONAL_SCOPES.map(item => item.scope))
  const extra = optionalScopes.filter(scope => allowedOptional.has(scope) && !SLACK_REQUIRED_SCOPES.includes(scope) && !(directMessagesOnly && scope === 'chat:write.public'))
  // DMs-only bots (Codes) never read or join channels: least privilege.
  const baseScopes = directMessagesOnly
    ? SLACK_REQUIRED_SCOPE_GROUPS.filter(group => group.label !== 'Read mentions and channel context').flatMap(group => group.scopes)
    : SLACK_REQUIRED_SCOPES
  const botEvents = directMessagesOnly ? [...SLACK_DM_EVENTS] : [...SLACK_BOT_EVENTS, ...SLACK_DM_EVENTS]
  return {
    display_information: { name: appName },
    features: {
      // The Messages tab is where people DM the app; the Home tab is what
      // they see when they open it (the agent publishes it, views.publish).
      app_home: { home_tab_enabled: true, messages_tab_enabled: true, messages_tab_read_only_enabled: false },
      bot_user: { display_name: slackBotDisplayName(appName), always_online: true },
    },
    oauth_config: { scopes: { bot: [...baseScopes, ...SLACK_DM_SCOPES, ...new Set(extra)] } },
    settings: {
      event_subscriptions: { bot_events: botEvents },
      ...(SLACK_INTERACTIVITY_REQUIRED ? { interactivity: { is_enabled: true } } : {}),
      org_deploy_enabled: false,
      socket_mode_enabled: true,
      token_rotation_enabled: false,
    },
  }
}

export function slackAppManifestJSON(options?: Parameters<typeof buildSlackAppManifest>[0]): string {
  return JSON.stringify(buildSlackAppManifest(options), null, 2)
}

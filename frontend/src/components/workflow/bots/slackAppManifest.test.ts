import { describe, expect, it } from 'vitest'
import {
  SLACK_BOT_EVENTS, SLACK_DM_EVENTS, SLACK_DM_SCOPES, SLACK_REQUIRED_SCOPE_GROUPS, SLACK_REQUIRED_SCOPES,
  buildSlackAppManifest, slackAppManifestJSON, slackBotDisplayName,
} from './slackAppManifest'

describe('Slack app manifest', () => {
  it('takes scopes and events from the permissions checklist', () => {
    const manifest = buildSlackAppManifest()
    expect(manifest.oauth_config.scopes.bot).toEqual([...SLACK_REQUIRED_SCOPE_GROUPS.flatMap(group => group.scopes), ...SLACK_DM_SCOPES])
    expect(manifest.oauth_config.scopes.bot).toEqual([
      'app_mentions:read', 'channels:history', 'groups:history', 'channels:read', 'groups:read',
      'chat:write', 'reactions:write', 'users:read', 'users:read.email', 'im:history', 'im:read', 'im:write',
    ])
    expect(manifest.settings.event_subscriptions.bot_events).toEqual([...SLACK_BOT_EVENTS, ...SLACK_DM_EVENTS])
    expect(manifest.settings.event_subscriptions.bot_events).toEqual(['app_mention', 'message.channels', 'message.groups', 'message.im'])
  })

  it('opens the Messages tab (DMs) and the Home tab', () => {
    expect(buildSlackAppManifest().features.app_home).toEqual({ home_tab_enabled: true, messages_tab_enabled: true, messages_tab_read_only_enabled: false })
  })

  it('enables Socket Mode and interactivity, disables org deploy and token rotation', () => {
    const { settings, features } = buildSlackAppManifest()
    expect(settings.socket_mode_enabled).toBe(true)
    expect(settings.interactivity).toEqual({ is_enabled: true })
    expect(settings.org_deploy_enabled).toBe(false)
    expect(settings.token_rotation_enabled).toBe(false)
    expect(features.bot_user.always_online).toBe(true)
  })

  it('applies the name, defaulting to AgentWorks', () => {
    expect(buildSlackAppManifest().display_information.name).toBe('AgentWorks')
    expect(buildSlackAppManifest({ name: '  ' }).display_information.name).toBe('AgentWorks')
    const named = buildSlackAppManifest({ name: 'Support Desk Bot' })
    expect(named.display_information.name).toBe('Support Desk Bot')
    expect(named.features.bot_user.display_name).toBe('support-desk-bot')
    expect(buildSlackAppManifest({ name: 'x'.repeat(50) }).display_information.name).toHaveLength(35)
    expect(slackBotDisplayName('!!!')).toBe('agentworks')
  })

  it('adds only known optional scopes, once', () => {
    const scopes = buildSlackAppManifest({ optionalScopes: ['files:read', 'files:read', 'admin', 'chat:write'] }).oauth_config.scopes.bot
    expect(scopes).toEqual([...SLACK_REQUIRED_SCOPES, ...SLACK_DM_SCOPES, 'files:read'])
  })

  it('trims channel scopes and events for DMs-only bots', () => {
    const manifest = buildSlackAppManifest({ directMessagesOnly: true, optionalScopes: ['chat:write.public', 'files:read'] })
    expect(manifest.oauth_config.scopes.bot).not.toContain('channels:history')
    expect(manifest.oauth_config.scopes.bot).not.toContain('chat:write.public')
    expect(manifest.oauth_config.scopes.bot).toContain('im:history')
    expect(manifest.oauth_config.scopes.bot).toContain('im:read')
    expect(manifest.oauth_config.scopes.bot).toContain('im:write')
    expect(manifest.oauth_config.scopes.bot).toContain('chat:write')
    expect(manifest.settings.event_subscriptions.bot_events).toEqual(['message.im'])
  })

  it('serialises to valid JSON with exactly the manifest fields', () => {
    const parsed = JSON.parse(slackAppManifestJSON({ name: 'Ops' }))
    expect(Object.keys(parsed).sort()).toEqual(['display_information', 'features', 'oauth_config', 'settings'])
    expect(Object.keys(parsed.settings).sort()).toEqual(['event_subscriptions', 'interactivity', 'org_deploy_enabled', 'socket_mode_enabled', 'token_rotation_enabled'])
    expect(parsed.display_information.name).toBe('Ops')
  })
})

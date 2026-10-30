import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('bot and notification settings separation', () => {
  it('keeps email out of the bots panel and webhooks in Notifications', () => {
    const bots = readFileSync('src/components/workflow/WorkflowBotsPanel.tsx', 'utf8')
    expect(bots).not.toContain('Slack Incoming Webhook')
    expect(bots).toContain('Routes for this ${scopeNoun}')
    expect(bots).toContain('route.kind === channel')
    expect(bots).toContain('kind={channel} manageRoutes')
    expect(bots).not.toContain('Gmail')
    const email = readFileSync('src/components/workflow/WorkflowEmailPanel.tsx', 'utf8')
    expect(email).toContain('<GmailNotifications')
  })

  it('exposes Notifications at workflow level and keeps it out of the global header', () => {
    const header = readFileSync('src/components/ModePresetBar.tsx', 'utf8')
    // Notify (like Pulse/Backup/Publish) is a registry view now: the toolbar
    // button navigates the pane, and the host's InspectorBody switch is what
    // actually mounts the view component.
    const host = readFileSync('src/components/workflow/canvas/WorkspaceViewHost.tsx', 'utf8')
    const notifications = readFileSync('src/components/workflow/WorkflowNotificationView.tsx', 'utf8')
    expect(header).not.toContain('notification-settings-button')
    expect(header).not.toContain('bot-connector')
    expect(host).toContain('<WorkflowNotificationView')
    expect(notifications).toContain('Notifications on')
    expect(notifications).toContain('Workflow Slack webhook')
    expect(notifications).not.toContain('My routing')
  })
})

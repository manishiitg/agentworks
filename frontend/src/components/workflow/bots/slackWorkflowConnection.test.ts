import { describe, expect, it } from 'vitest'
import { resolveWorkflowSlackConnection, sameBotWorkspacePath, selectedWorkflowSlackReady } from './slackWorkflowConnection'
import type { SlackConnection } from '../../../services/api-types'

const entry = (overrides: Partial<SlackConnection> & { id: string }): SlackConnection => ({
  display_name: overrides.id,
  enabled: true,
  configured: true,
  is_default: false,
  ...overrides,
})

describe('Workflow Slack connection resolution', () => {
  const connections = [
    entry({ id: 'slack_001', display_name: 'Platform', is_default: true }),
    entry({ id: 'slack_aaa', display_name: 'Alpha', workspace_path: 'Workflow/alpha' }),
  ]

  it('inherits the platform default without a selection', () => {
    const resolved = resolveWorkflowSlackConnection(connections, 'Workflow/alpha', undefined, 'slack_001')
    expect(resolved.effective?.id).toBe('slack_001')
    expect(resolved.own?.id).toBe('slack_aaa')
    expect(resolved.selectionId).toBe('')
  })

  it('prefers the manifest selection over the default', () => {
    const resolved = resolveWorkflowSlackConnection(connections, 'Workflow/alpha', 'slack_aaa', 'slack_001')
    expect(resolved.effective?.id).toBe('slack_aaa')
    expect(selectedWorkflowSlackReady(resolved)).toBe(true)
  })

  it('keeps an unselected or disabled app from making routes ready', () => {
    expect(selectedWorkflowSlackReady(resolveWorkflowSlackConnection(connections, 'Workflow/alpha', undefined, 'slack_001'))).toBe(false)
    const disabled = [entry({ id: 'slack_off', workspace_path: 'Workflow/alpha', enabled: false })]
    expect(selectedWorkflowSlackReady(resolveWorkflowSlackConnection(disabled, 'Workflow/alpha', 'slack_off', undefined))).toBe(false)
  })

  it('reports a dangling selection instead of silently falling back', () => {
    const resolved = resolveWorkflowSlackConnection(connections, 'Workflow/alpha', 'slack_ghost', 'slack_001')
    expect(resolved.effective).toBeNull()
    expect(resolved.selectionId).toBe('slack_ghost')
  })

  it('finds nothing without a registry', () => {
    const resolved = resolveWorkflowSlackConnection(undefined, 'Workflow/alpha', undefined, undefined)
    expect(resolved.own).toBeNull()
    expect(resolved.effective).toBeNull()
  })

  it('matches a project connection only with its profile', () => {
    const projectPath = '_users/alice/Chats/Work/projects/alpha'
    const projectConns = [
      entry({ id: 'slack_001', display_name: 'Platform', is_default: true }),
      entry({ id: 'slack_ppp', display_name: 'Crew', workspace_path: projectPath, profile_id: 'work' }),
    ]
    const resolved = resolveWorkflowSlackConnection(projectConns, projectPath, undefined, 'slack_001', 'work')
    expect(resolved.own?.id).toBe('slack_ppp')
    expect(resolved.effective?.id).toBe('slack_001')
    const otherProfile = resolveWorkflowSlackConnection(projectConns, projectPath, undefined, 'slack_001', 'example-product')
    expect(otherProfile.own).toBeNull()
    const workflowView = resolveWorkflowSlackConnection(projectConns, projectPath, undefined, 'slack_001')
    expect(workflowView.own).toBeNull()
  })
})

describe('crew bot scope', () => {
  it('matches the physical stored path with the crew UI logical path', () => {
    expect(sameBotWorkspacePath('_users/u1/Chats/Work/projects/sde', 'Chats/Work/projects/sde')).toBe(true)
    expect(sameBotWorkspacePath('Chats/Work/projects/sde', 'Chats/Work/projects/other')).toBe(false)
    expect(sameBotWorkspacePath('', '')).toBe(false)
  })

  it('finds a crew bot saved under the physical path as the crew own bot', () => {
    const bot = entry({ id: 'slack_sde', workspace_path: '_users/u1/Chats/Work/projects/sde', profile_id: 'work' })
    const selection = resolveWorkflowSlackConnection([bot], 'Chats/Work/projects/sde', 'slack_sde', undefined, 'work')
    expect(selection.own?.id).toBe('slack_sde')
  })
})

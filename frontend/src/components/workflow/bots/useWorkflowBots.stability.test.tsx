// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'

vi.mock('../../../services/api', () => ({
  agentApi: {
    getBotConfig: vi.fn(async () => ({ allowed_emails: [] })),
    getSlackFeedbackConfig: vi.fn(async () => ({ enabled: false, connections: [], channel_routing: {} })),
    getProjectSlackSelection: vi.fn(async () => ({ slack_connection_id: '' })),
    getWhatsAppStatus: vi.fn(async () => ({ enabled: true, paired: false, connected: false, qr_available: false })),
    getWhatsAppRouting: vi.fn(async () => ({ routing: {} })),
  },
}))

vi.mock('../../../stores/useWorkflowManifestStore', () => {
  const state = { workflows: [], refreshWorkflows: () => Promise.resolve(), updateWorkflow: () => Promise.resolve() }
  return { useWorkflowManifestStore: (selector: (value: typeof state) => unknown) => selector(state) }
})

vi.mock('../../../stores/useAuthStore', () => ({
  useAuthStore: (selector: (state: unknown) => unknown) => selector({ user: { id: 'alice', is_admin: true }, isMultiUserMode: true, isMultiUserModeChecked: true }),
}))

vi.mock('../../../hooks/useCanWriteWorkflow', () => ({ useCanWriteWorkflow: () => true }))

import { agentApi } from '../../../services/api'
import { useWorkflowBots } from './useWorkflowBots'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

function Probe({ profileId = 'work', label = 'Crew' }: { profileId?: string; label?: string }) {
  const bots = useWorkflowBots('/projects/example', {
    profileId,
    conversationKey: 'project-1',
    label,
  }, 'bots')
  return <span>{bots.slackLoading ? 'Loading Slack' : 'Slack ready'}</span>
}

describe('Crew bot setup stability', () => {
  it('keeps Slack loaded when the parent rerenders with a new but equivalent target', async () => {
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<Probe />))
      expect(host.textContent).toBe('Slack ready')
      expect(agentApi.getSlackFeedbackConfig).toHaveBeenCalledTimes(1)
      expect(agentApi.getProjectSlackSelection).toHaveBeenCalledTimes(1)

      await act(async () => root.render(<Probe />))
      await act(async () => root.render(<Probe label="Renamed Crew" />))
      expect(host.textContent).toBe('Slack ready')
      expect(agentApi.getSlackFeedbackConfig).toHaveBeenCalledTimes(1)
      expect(agentApi.getProjectSlackSelection).toHaveBeenCalledTimes(1)

      await act(async () => root.render(<Probe profileId="another-profile" />))
      expect(agentApi.getProjectSlackSelection).toHaveBeenCalledTimes(2)
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })
})

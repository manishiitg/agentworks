// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import api from '../../services/api'
import { AgentSetupMark } from '../../components/chat/AgentIcon'
import { agentSetupForTab, teamAgentForTab, useTeamTab } from './teamApi'

vi.mock('../../services/api', () => ({ default: { get: vi.fn(), request: vi.fn() }, getAuthToken: () => '', getDesktopApiBaseUrl: () => '', getLocalApiBaseUrl: () => '' }))
vi.mock('../../services/desktopConnections', () => ({ desktopRequest: vi.fn() }))
vi.mock('../../services/liveFeed', () => ({ liveFeed: { subscribe: () => () => {} } }))
vi.mock('../../utils/desktopConnection', () => ({ getRawActiveWorkspaceId: () => 'local' }))
vi.mock('../../utils/remoteSessions', () => ({ registerRemoteSession: vi.fn() }))
vi.mock('../../stores/useConnectedWorkStore', () => ({ hasDesktopConnections: () => true, useConnectedWorkStore: { getState: () => ({ connections: [] }) } }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (state: unknown) => unknown) => select({ user: { id: 'owner' } }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: (select: (state: unknown) => unknown) => select({ activeWorkspaceId: 'local' }) }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// PLAT-746: a private, unpaired Crew must show the server's automation mark
// without turning its local metadata into a Team/sharing target.
it('shows private automation setup without an online Team binding', async () => {
  vi.mocked(api.get).mockResolvedValue({ data: {} } as never)
  const setup = { reach: '', starts_on_own: true, automations: [{ kind: 'Schedule', name: 'Daily report', line: 'every day', on: true }] }
  vi.mocked(api.request).mockResolvedValue({ data: { agents: [{ mine: true, tab: 'analyst', setup }, { mine: false, tab: 'other', setup }] } } as never)
  const host = document.createElement('div'), root = createRoot(host)
  function Probe() {
    const state = useTeamTab('private-project')
    expect(state.target).toBeUndefined()
    expect(state.view).toBeUndefined()
    expect(teamAgentForTab(state, 'private-project:chat:analyst', false, true)).toBeUndefined()
    expect(agentSetupForTab(state, 'private-project:chat:other', false, false)).toBeUndefined()
    return <AgentSetupMark setup={agentSetupForTab(state, 'private-project:chat:analyst', false, true)} />
  }
  try {
    await act(async () => root.render(<Probe />))
    expect(api.request).toHaveBeenCalledWith(expect.objectContaining({ url: '/api/crew-teams/private-project/tab', method: 'GET' }))
    expect(host.querySelector('[aria-label="Only you · starts on its own"]')).not.toBeNull()
  } finally { await act(async () => root.unmount()) }
})

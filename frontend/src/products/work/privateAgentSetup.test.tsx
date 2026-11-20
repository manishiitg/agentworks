// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import api from '../../services/api'
import { AgentSetupMark } from '../../components/chat/AgentIcon'
import { agentSetupForTab, teamAgentForTab, useTeamTab } from './teamApi'

const mode = vi.hoisted(() => ({ local: true as boolean | undefined }))
const notices = vi.hoisted(() => ({ reload: () => {} }))
vi.mock('../../stores/useCapabilitiesStore', () => ({ useCapabilitiesStore: { getState: () => ({ capabilities: { local_mode: mode.local } }) } }))
vi.mock('../productSurfaceConfig', () => ({ isLocalDeployment: () => true }))
vi.mock('../../services/api', () => ({ default: { get: vi.fn(), request: vi.fn() }, getAuthToken: () => '', getDesktopApiBaseUrl: () => '', getLocalApiBaseUrl: () => '' }))
vi.mock('../../services/desktopConnections', () => ({ desktopRequest: vi.fn() }))
vi.mock('../../services/liveFeed', () => ({ liveFeed: { subscribe: (_topics: unknown, _session: unknown, reload: () => void) => { notices.reload = reload; return () => {} } } }))
vi.mock('../../utils/desktopConnection', () => ({ getRawActiveWorkspaceId: () => 'local' }))
vi.mock('../../utils/remoteSessions', async original => ({ ...await original<typeof import('../../utils/remoteSessions')>(), registerRemoteSession: vi.fn() }))
vi.mock('../../stores/useConnectedWorkStore', () => ({ hasDesktopConnections: () => false, useConnectedWorkStore: { getState: () => ({ connections: [] }) } }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (state: unknown) => unknown) => select({ user: { id: 'owner' } }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: (select: (state: unknown) => unknown) => select({ activeWorkspaceId: 'local' }) }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => { mode.local = true; vi.clearAllMocks(); vi.unstubAllGlobals() })

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
    expect(host.querySelector('[role="img"]')?.getAttribute('title')).toContain('Daily report')
    expect(host.querySelector('.lucide-clock')).not.toBeNull()
  } finally { await act(async () => root.unmount()) }
})

// The local backend, not an Electron API, chooses a paired Team target.
it.each([true, undefined])('routes a paired local Crew from Chrome using backend mode or runtime fallback (%s)', async local => {
  mode.local = local
  vi.mocked(api.get).mockResolvedValue({ data: { binding: { connection_id: 'server', peer_project_id: 'online', side: 'local' } } } as never)
  const remote = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({ team: { folder: { id: 'folder' } }, agents: [] }) })
  vi.stubGlobal('fetch', remote)
  const host = document.createElement('div'), root = createRoot(host)
  function Probe() { const state = useTeamTab('paired'); return <span>{state.target?.project || state.error}</span> }
  try {
    await act(async () => root.render(<Probe />))
    expect(api.get).toHaveBeenCalledWith('/api/crew-sync/paired', expect.anything())
    expect(remote).toHaveBeenCalledWith('/api/connections/server/api/crew-teams/online/tab', expect.anything())
    expect(host.textContent).toBe('online')
    expect(api.request).not.toHaveBeenCalled()
  } finally { await act(async () => root.unmount()) }
})

it('keeps server-hosted Team reads direct even when runtime fallback says local', async () => {
  mode.local = false
  vi.mocked(api.request).mockResolvedValue({ data: { team: { folder: { id: 'folder' } }, agents: [] } } as never)
  const host = document.createElement('div'), root = createRoot(host)
  function Probe() { const state = useTeamTab('server-crew'); return <span>{state.target?.project || state.error}</span> }
  try {
    await act(async () => root.render(<Probe />))
    expect(api.get).not.toHaveBeenCalled()
    expect(api.request).toHaveBeenCalledWith(expect.objectContaining({ url: '/api/crew-teams/server-crew/tab' }))
    expect(host.textContent).toBe('server-crew')
  } finally { await act(async () => root.unmount()) }
})

// PLAT-856-572: a notice storm during a slow Team read previously set `again`
// and re-read immediately on completion, bypassing the refresh throttle.
it('coalesces slow Team notice storms and stops automatic reads while hidden', async () => {
  mode.local = false
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-01-01T00:00:00Z'))
  let hidden = false
  const visible = vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden)
  let finish!: (value: never) => void
  const reply = { data: { team: { folder: {} }, agents: [] } } as never
  vi.mocked(api.request).mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    .mockResolvedValue(reply)
  const host = document.createElement('div'), root = createRoot(host)
  function Probe() { useTeamTab('notice-storm'); return null }
  try {
    await act(async () => root.render(<><Probe /><Probe /></>))
    expect(api.request).toHaveBeenCalledTimes(1)
    for (let i = 0; i < 100; i++) notices.reload()
    // Cross the old 3-second timer while the first request is still pending.
    await act(async () => { await vi.advanceTimersByTimeAsync(4000) })
    expect(api.request).toHaveBeenCalledTimes(1)
    await act(async () => { finish(reply) })
    expect(api.request).toHaveBeenCalledTimes(1)
    await act(async () => { await vi.advanceTimersByTimeAsync(10999) })
    expect(api.request).toHaveBeenCalledTimes(1)
    await act(async () => { await vi.advanceTimersByTimeAsync(1) })
    expect(api.request).toHaveBeenCalledTimes(2)
    hidden = true
    for (let i = 0; i < 100; i++) notices.reload()
    await act(async () => { await vi.advanceTimersByTimeAsync(60000) })
    expect(api.request).toHaveBeenCalledTimes(2)
    hidden = false
    document.dispatchEvent(new Event('visibilitychange'))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(api.request).toHaveBeenCalledTimes(3)
  } finally {
    await act(async () => root.unmount())
    visible.mockRestore()
    vi.useRealTimers()
  }
})

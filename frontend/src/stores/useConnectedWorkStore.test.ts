// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({ directory: vi.fn(), header: vi.fn() }))
vi.mock('../services/desktopConnections', () => ({ desktopRequest: mocks.directory }))
vi.mock('../services/api', () => ({ agentApi: { getHeaderSummary: mocks.header }, getAuthToken: () => 'local-owner', getLocalApiBaseUrl: () => 'http://localhost', getDesktopApiBaseUrl: () => 'http://localhost' }))
import { connectedActivity, connectedLocations, useConnectedWorkStore } from './useConnectedWorkStore'
import { invalidateChatIdentity } from '../utils/chatIdentity'
const previous = window.electronAPI
const remote = 'a'.repeat(32)
afterEach(() => { window.electronAPI = previous; vi.unstubAllGlobals() })
it('keeps disconnected activity visibly stale but clears denied accounts and rejects late previous-account results', async () => {
  // PLAT-746: combining locations must not keep another account’s activity after refusal.
  window.electronAPI = { getDesktopConnection: () => 'local' }
  invalidateChatIdentity()
  mocks.directory.mockImplementation(async (path: string) => path ? { status: 'connected', projects: [] } : { connections: [{ id: remote, name: 'Team', server: 'https://example.com' }] })
  mocks.header.mockResolvedValue({ active_sessions: [] })
  let code = 200
  const request = vi.fn(async () => new Response(JSON.stringify({ active_sessions: [{ session_id: 'same' }] }), { status: code }))
  vi.stubGlobal('fetch', request)
  await useConnectedWorkStore.getState().refreshActivity()
  expect(useConnectedWorkStore.getState().activity[remote].sessions).toHaveLength(1)
  code = 503
  await useConnectedWorkStore.getState().refreshActivity()
  expect(useConnectedWorkStore.getState().activity[remote]).toMatchObject({ status: 'unavailable', sessions: [{ session_id: 'same' }] })
  code = 401
  await useConnectedWorkStore.getState().refreshActivity()
  expect(useConnectedWorkStore.getState().activity[remote]).toMatchObject({ status: 'sign_in', sessions: [] })
  let finish!: (value: Response) => void
  request.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
  const pending = useConnectedWorkStore.getState().refreshActivity()
  invalidateChatIdentity()
  expect(connectedActivity(useConnectedWorkStore.getState())).toEqual({})
  expect(connectedLocations(useConnectedWorkStore.getState()).map(item => item.id)).toEqual(['local'])
  await useConnectedWorkStore.getState().refresh()
  finish(new Response(JSON.stringify({ active_sessions: [{ session_id: 'old-private' }] })))
  await pending
  expect(useConnectedWorkStore.getState().activity[remote]).toBeUndefined()
})

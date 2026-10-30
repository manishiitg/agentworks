// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({ sessions: vi.fn(), open: vi.fn(), tab: vi.fn(), toast: vi.fn(), tabs: {} as Record<string, any> }))
vi.mock('../stores/useChatStore', () => ({ useChatStore: { getState: () => ({ getActiveSessions: mocks.sessions, chatTabs: mocks.tabs, addToast: mocks.toast }), subscribe: () => () => {} } }))
vi.mock('./globalProductNavigation', () => ({ openGlobalActivitySession: mocks.open, openGlobalTab: mocks.tab }))
import { openConnectedActivity, restoreConnectedActivity } from './connectedActivityNavigation'
const original = window.electronAPI
afterEach(() => { window.electronAPI = original; sessionStorage.clear(); vi.clearAllMocks() })
it('revalidates the destination account before opening an exact remote conversation', async () => {
  // PLAT-746: a selector from another server is not authorization or a local tab binding.
  let location = 'local'
  const open = vi.fn(async (id: string) => { location = id })
  window.electronAPI = { getDesktopConnection: () => location, openDesktopConnection: open }
  mocks.sessions.mockResolvedValue([])
  await openConnectedActivity('remote', 'same')
  await restoreConnectedActivity()
  expect(mocks.open).not.toHaveBeenCalled()
  expect(mocks.toast).toHaveBeenCalled()
  mocks.sessions.mockResolvedValue([{ session_id: 'same', title: 'Admitted by destination' }])
  mocks.tabs = { restored: { tabId: 'restored', sessionId: 'same' } }
  await openConnectedActivity('remote', 'same')
  await restoreConnectedActivity()
  expect(mocks.open).toHaveBeenCalledWith({ session_id: 'same', title: 'Admitted by destination' }, { source: 'connected-monitor' })
  expect(mocks.tab).toHaveBeenCalledWith('restored')
  expect(sessionStorage.getItem('agentworks-open-connected-activity')).toBeNull()
})

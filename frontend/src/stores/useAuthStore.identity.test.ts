// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest'

// The store installs persistence at import time, before beforeEach runs.
vi.hoisted(() => vi.stubGlobal('localStorage', { getItem: () => null, setItem: () => {}, removeItem: () => {} }))

const mocks = vi.hoisted(() => ({
  getAuthMode: vi.fn(), login: vi.fn(), handleOAuthCallback: vi.fn(), logout: vi.fn(), getCurrentUser: vi.fn(),
  getAuthToken: vi.fn(), setAuthToken: vi.fn(), clearAuthToken: vi.fn(), switchChatAccount: vi.fn(), desktopRequest: vi.fn(),
}))
vi.mock('../services/api', () => ({
  authApi: mocks, getAuthToken: mocks.getAuthToken, setAuthToken: mocks.setAuthToken, clearAuthToken: mocks.clearAuthToken,
}))
vi.mock('../services/desktopConnections', () => ({ desktopRequest: mocks.desktopRequest }))
vi.mock('./useChatStore', () => ({ switchChatAccount: mocks.switchChatAccount, migrateLegacyChatStateForVerifiedAccount: vi.fn() }))
import { useAuthStore } from './useAuthStore'
import { captureChatIdentity, isChatIdentityCurrent } from '../utils/chatIdentity'

const alice = { id: 'alice', username: 'Alice' }
const bob = { id: 'bob', username: 'Bob' }

describe('authentication conversation boundary', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    useAuthStore.persist.setOptions({ storage: { getItem: () => null, setItem: () => {}, removeItem: () => {} } })
    useAuthStore.setState({ user: null, isAuthenticated: false, isLoading: false })
  })

  it('switches the owner for password login, OAuth callback and identity refresh', async () => {
    mocks.login.mockResolvedValue({ user: alice, token: 'alice-token' })
    await useAuthStore.getState().login('alice', 'secret')
    expect(mocks.switchChatAccount).toHaveBeenLastCalledWith('alice')
    vi.stubGlobal('sessionStorage', {
      getItem: () => JSON.stringify({ state: 'csrf', provider: 'oauth' }), removeItem: vi.fn(),
    })
    mocks.handleOAuthCallback.mockResolvedValue({ user: bob, token: 'bob-token' })
    await useAuthStore.getState().handleOAuthCallback('code', 'csrf')
    expect(mocks.switchChatAccount).toHaveBeenLastCalledWith('bob')
    mocks.getAuthToken.mockReturnValue('alice-token')
    mocks.getCurrentUser.mockResolvedValue(alice)
    await useAuthStore.getState().checkAuth()
    expect(mocks.switchChatAccount).toHaveBeenLastCalledWith('alice')
    vi.unstubAllGlobals()
  })

  it('invalidates chat callbacks immediately on logout and ignores a late old auth check', async () => {
    mocks.getAuthToken.mockReturnValue('alice-token')
    let resolveCheck!: (value: typeof alice) => void
    mocks.getCurrentUser.mockImplementation(() => new Promise(resolve => { resolveCheck = resolve }))
    const pendingCheck = useAuthStore.getState().checkAuth()
    const identity = captureChatIdentity()
    let resolveLogout!: () => void
    mocks.logout.mockImplementation(() => new Promise<void>(resolve => { resolveLogout = resolve }))
    const pendingLogout = useAuthStore.getState().logout()
    expect(isChatIdentityCurrent(identity)).toBe(false)
    expect(useAuthStore.getState().isAuthenticated).toBe(false)
    mocks.login.mockResolvedValue({ user: bob, token: 'bob-token' })
    await useAuthStore.getState().login('bob', 'secret')
    resolveCheck(alice)
    resolveLogout()
    await Promise.all([pendingCheck, pendingLogout])
    expect(useAuthStore.getState().user?.id).toBe('bob')
    expect(mocks.setAuthToken).toHaveBeenLastCalledWith('bob-token')
    expect(mocks.clearAuthToken).toHaveBeenCalledTimes(1)
  })
  it('signs out through the remote account before deleting its saved credential', async () => {
    const open = vi.fn()
    window.electronAPI = { getDesktopConnection: () => 'saved', openDesktopConnection: open } as unknown as typeof window.electronAPI
    let finishLogout!: () => void
    mocks.logout.mockImplementation(() => new Promise<void>(resolve => { finishLogout = resolve }))
    try {
      const pending = useAuthStore.getState().logout()
      await vi.waitFor(() => expect(mocks.logout).toHaveBeenCalledOnce())
      expect(mocks.desktopRequest).not.toHaveBeenCalled()
      finishLogout()
      await pending
      expect(mocks.desktopRequest).toHaveBeenCalledWith('/saved', 'DELETE')
      expect(open).toHaveBeenCalledWith('local', '/home')
      mocks.logout.mockRejectedValueOnce(new Error('unavailable'))
      mocks.desktopRequest.mockClear()
      await expect(useAuthStore.getState().logout()).rejects.toThrow('unavailable')
      expect(mocks.desktopRequest).not.toHaveBeenCalled()
    } finally { delete window.electronAPI }
  })

  it('does not infer single-user mode from a failed authentication-mode request', async () => {
    mocks.getAuthMode.mockRejectedValue(new Error('offline'))
    await useAuthStore.getState().checkAuthMode()
    expect(useAuthStore.getState().isMultiUserMode).toBe(true)
    expect(useAuthStore.getState().error).toContain('Cannot verify')
  })

  it('recovers single-user mode after a temporary authentication-mode failure', async () => {
    mocks.getAuthMode.mockRejectedValueOnce(new Error('temporarily offline'))
      .mockResolvedValueOnce({ multi_user_mode: false, providers: [] })
    await useAuthStore.getState().checkAuthMode()
    await useAuthStore.getState().checkAuthMode()
    expect(useAuthStore.getState().isMultiUserMode).toBe(false)
    expect(useAuthStore.getState().error).toBeNull()
  })

})

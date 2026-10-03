// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { OAuthStatusBadge } from './OAuthStatusBadge'
import { oauthApi } from '../services/oauthApi'
vi.mock('../services/oauthApi', () => ({ oauthApi: { getOAuthStatus: vi.fn(), startOAuthFlow: vi.fn() } }))
vi.mock('../services/mcpConfigApi', () => ({ mcpConfigApi: {} }))
vi.mock('../stores', () => ({ useChatStore: { getState: () => ({ addToast: vi.fn() }) } }))
vi.mock('../stores/useAuthStore', () => ({ useAuthStore: (select: (state: unknown) => unknown) => select({ user: { is_admin: true } }) }))
vi.mock('../hooks/useCanWriteWorkflow', () => ({ READ_ONLY_TITLE: 'Read only' }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => { vi.restoreAllMocks(); vi.clearAllMocks(); vi.useRealTimers(); vi.unstubAllGlobals() })
it('reuses an existing shared sign-in without starting another OAuth flow or polling catalog rows', async () => {
 vi.mocked(oauthApi.getOAuthStatus).mockResolvedValue({ valid: true } as never)
 const changed = vi.fn(); const div = document.createElement('div'); const root = createRoot(div)
 await act(async () => { root.render(<OAuthStatusBadge scope="vault" serverName="Test" requiresOAuth connection="available" reuseAuthentication connectLabel="Connect with OAuth" onAuthChange={changed} />) })
 expect(oauthApi.getOAuthStatus).not.toHaveBeenCalled()
 expect(div.textContent).toContain('Connect with OAuth')
 await act(async () => { div.querySelector('button')!.click() })
 expect(changed).toHaveBeenCalledWith(true)
 expect(oauthApi.startOAuthFlow).not.toHaveBeenCalled()
 await act(async () => root.unmount())
})
it('uses the existing login flow and stops its completion poll on unmount', async () => {
 vi.useFakeTimers()
 vi.stubGlobal('confirm', vi.fn(() => true))
 const open = vi.fn(() => null); vi.stubGlobal('open', open)
 vi.mocked(oauthApi.getOAuthStatus).mockResolvedValue({ valid: false } as never)
 vi.mocked(oauthApi.startOAuthFlow).mockResolvedValue({ auth_url: 'http://localhost/authorize', state: 'test' } as never)
 const div = document.createElement('div'); const root = createRoot(div)
 await act(async () => root.render(<OAuthStatusBadge scope="vault" serverName="Test" requiresOAuth connection="available" reuseAuthentication />))
 await act(async () => div.querySelector('button')!.click())
 expect(oauthApi.startOAuthFlow).toHaveBeenCalledWith('Test', undefined, undefined, 'vault')
 expect(open).toHaveBeenCalledWith('http://localhost/authorize', '_blank')
 await act(async () => root.unmount())
 const calls = vi.mocked(oauthApi.getOAuthStatus).mock.calls.length
 await act(async () => vi.advanceTimersByTimeAsync(6000))
 expect(vi.mocked(oauthApi.getOAuthStatus).mock.calls.length).toBe(calls)
 expect(vi.getTimerCount()).toBe(0)
})

// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { useAuthStore } from '../stores/useAuthStore'
import { Login } from './Login'

vi.mock('../stores/useAuthStore', () => ({ useAuthStore: vi.fn() }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

afterEach(() => {
  delete (window as Window & { __APP_RUNTIME_CONFIG__?: unknown }).__APP_RUNTIME_CONFIG__
  window.history.replaceState({}, '', '/')
})

describe('sign-in recovery', () => {
  it.each([true, false])('offers retry after a mode request fails, with gateway SSO %s', async gatewaySSO => {
    ;(window as Window & { __APP_RUNTIME_CONFIG__?: unknown }).__APP_RUNTIME_CONFIG__ = { gatewaySso: gatewaySSO }
    window.history.replaceState({}, '', '/file?path=YWJj')
    const checkAuthMode = vi.fn().mockResolvedValue(undefined)
    vi.mocked(useAuthStore).mockReturnValue({
      providers: [], isMultiUserModeChecked: true, isLoading: false,
      error: 'Cannot verify server authentication mode. Please retry.',
      checkAuthMode,
    } as ReturnType<typeof useAuthStore>)
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<Login />))
      expect(host.textContent).not.toContain('No authentication providers have been configured')
      const retry = Array.from(host.querySelectorAll('button')).find(button => button.textContent === 'Try again')!
      await act(async () => retry.click())
      expect(checkAuthMode).toHaveBeenCalledTimes(1)
      const google = host.querySelector('a')
      if (gatewaySSO) {
        expect(google?.textContent).toContain('Continue with Google')
        expect(google?.getAttribute('href')).toBe('/auth/google/start?next=%2Ffile%3Fpath%3DYWJj')
      } else {
        expect(google).toBeNull()
        expect(host.textContent).toContain('Cannot verify server authentication mode')
      }
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })
})

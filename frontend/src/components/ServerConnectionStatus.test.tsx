// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import ServerConnectionStatus from './ServerConnectionStatus'

vi.mock('../services/api', () => ({
  getApiBaseUrl: () => 'http://127.0.0.1:18743/api/connections/saved-server',
  getAuthToken: () => 'local-owner-token',
}))

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// PLAT-746: signing in worked, but the unauthenticated health request blocked
// opening the remote crew with 401 before the app could mount.
it('opens the connected workspace when its health proxy requires local authentication', async () => {
  vi.stubGlobal('fetch', vi.fn(async (_url, options) => {
    const authorized = new Headers(options?.headers).get('Authorization') === 'Bearer local-owner-token'
    return new Response(JSON.stringify(authorized ? { status: 'healthy' } : { error: 'Authorization header required' }), {
      status: authorized ? 200 : 401,
    })
  }))
  const host = document.createElement('div'), root = createRoot(host)
  try {
    await act(async () => root.render(<ServerConnectionStatus><div>Remote crew ready</div></ServerConnectionStatus>))
    expect(host.textContent).toBe('Remote crew ready')
  } finally {
    await act(async () => root.unmount())
    vi.unstubAllGlobals()
  }
})

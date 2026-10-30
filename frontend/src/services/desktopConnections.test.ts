// @vitest-environment happy-dom
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { desktopRequest } from './desktopConnections'
import { getApiBaseUrl } from './api'

// Use browser storage explicitly: current Node exposes an unavailable native localStorage.
beforeEach(() => {
  const values = new Map<string, string>()
  vi.stubGlobal('localStorage', { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => values.set(key, value), removeItem: (key: string) => values.delete(key) })
})
afterEach(() => { vi.unstubAllGlobals(); delete window.electronAPI; delete window.__APP_RUNTIME_CONFIG__ })

// PLAT-746-219: a Vite-origin request was proxied without desktop proof and returned 403.
it('sends connection management and remote chat requests directly to the desktop backend', async () => {
  const backend = 'http://127.0.0.1:18743'
  Object.assign(window, { __APP_RUNTIME_CONFIG__: { apiBaseUrl: 'http://127.0.0.1:51733' } })
  let connection = 'local'
  window.electronAPI = { getApiBaseUrl: () => 'http://127.0.0.1:51733', getDesktopApiBaseUrl: () => backend, getDesktopConnection: () => connection }
  localStorage.setItem('auth_token', 'test-token')
  const fetch = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({ connections: [] }) })
  vi.stubGlobal('fetch', fetch)
  await desktopRequest()
  expect(fetch.mock.calls[0][0]).toBe(backend + '/api/desktop-connections')
  expect(getApiBaseUrl()).toBe('http://127.0.0.1:51733')
  connection = 'a'.repeat(32)
  expect(getApiBaseUrl()).toBe(backend + '/api/connections/' + connection)
  delete window.electronAPI
  expect(getApiBaseUrl()).toBe('http://127.0.0.1:51733')
})

// PLAT-746-238: server address validation returned HTTP 400 but was hidden as a network error.
it.each([
  [400, "Paste an HTTPS link from the server you want to connect.", "Paste an HTTPS link from the server you want to connect."],
  [403, JSON.stringify({ error: 'Use the app window.' }), 'Use the app window.'],
  [502, '<html>Bad gateway</html>', 'Could not complete this request. Try again.'],
])('preserves actionable local connection errors (%s)', async (status, body, message) => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(body, { status })))
  await expect(desktopRequest('', 'POST', { server: 'https://example.com/crew' })).rejects.toThrow(`${message} (HTTP ${status})`)
})

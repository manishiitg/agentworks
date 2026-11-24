// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
import axios from 'axios'
import api, { getApiBaseUrl, getLocalApiBaseUrl } from './api'
import { registerRemoteSession } from '../utils/remoteSessions'

vi.mock('../stores/useWorkspaceConnectionStore', async importOriginal => ({ ...await importOriginal<typeof import('../stores/useWorkspaceConnectionStore')>(), getRawActiveWorkspaceId: () => '0123456789abcdef0123456789abcdef' }))
afterEach(() => { delete (window as { electronAPI?: unknown }).electronAPI })

// #3400: opening another server's project in the desktop app switched to that server's theme.
it('keeps the look on this Mac in the desktop app while other requests go to the open server', async () => {
  const seen: Record<string, string> = {}
  const adapter = async (config: { url?: string; baseURL?: string }) => { seen[config.url || ''] = config.baseURL || ''; return { data: {}, status: 200, statusText: 'OK', headers: {}, config } }
  ;(window as { electronAPI?: unknown }).electronAPI = {}
  await api.get('/api/appearance', { adapter } as never)
  await api.get('/api/crews', { adapter } as never)
  expect(seen['/api/appearance']).toBe(getLocalApiBaseUrl())
  expect(seen['/api/crews']).toContain('/api/connections/0123456789abcdef0123456789abcdef')
  // In a browser the server's own theme still shows.
  delete (window as { electronAPI?: unknown }).electronAPI
  await api.get('/api/appearance', { adapter } as never)
  expect(seen['/api/appearance']).toContain('/api/connections/')
})

// A teammate chat's requests reach its own server from every API client, not only the main one.
it('sends every API client of a teammate chat to that chat server', async () => {
  registerRemoteSession('teammate-chat', 'srv')
  const seen: string[] = []
  const adapter = async (config: { baseURL?: string }) => { seen.push(config.baseURL || ''); return { data: {}, status: 200, statusText: 'OK', headers: {}, config } }
  const client = axios.create({ baseURL: getApiBaseUrl() })
  await client.get('/api/sessions/teammate-chat/events', { adapter } as never)
  await axios.get('/api/stop', { baseURL: getApiBaseUrl(), params: { session_id: 'teammate-chat' }, adapter } as never)
  await client.get('/api/crew-functions', { params: { project_id: 'mac-crew' }, adapter } as never)
  expect(seen[0]).toContain('/api/connections/srv')
  expect(seen[1]).toContain('/api/connections/srv')
  expect(seen[2]).toBe(getApiBaseUrl())
})

import { expect, it, vi } from 'vitest'
import api from '../../services/api'
import { teamSideChats } from './teamApi'
vi.mock('../../services/api', () => ({ default: { get: vi.fn() }, getAuthToken: () => 'test', getDesktopApiBaseUrl: () => 'http://desktop', getLocalApiBaseUrl: () => 'http://local' }))
vi.mock('../../utils/desktopConnection', () => ({ getRawActiveWorkspaceId: () => 'local' }))
vi.mock('../../stores/useConnectedWorkStore', () => ({ hasDesktopConnections: () => true, useConnectedWorkStore: {} }))
vi.mock('../../utils/remoteSessions', () => ({ registerRemoteSession: vi.fn() }))

it('loads only admitted teammates through each unopened Mac Crew own binding', async () => {
  vi.mocked(api.get).mockResolvedValue({ data: { binding: { connection_id: 'saved', peer_project_id: 'online', side: 'local' } } } as never)
  const fetch = vi.fn().mockResolvedValue({ ok: true, json: async () => [
    { conversation_key: 'online:chat:member', session_id: 'member-session', can_write: false, tab_owner_id: 'member', name: 'Main' },
    { conversation_key: 'online:chat:own', session_id: 'own-session', can_write: true, tab_owner_id: 'owner' },
    { conversation_key: 'online:chat:copy', session_id: 'copy-session', can_write: false, on_your_mac: true, tab_owner_id: 'member' },
  ] })
  vi.stubGlobal('fetch', fetch)
  try {
    const rows = await teamSideChats('work', 'unopened')
    expect(rows.map(row => row.conversation_key)).toEqual(['unopened:team:online:chat:member'])
    expect(fetch.mock.calls[0][0]).toContain('/saved/api/agent-profiles/work/side-chats?project_id=online')
  } finally { vi.unstubAllGlobals() }
})

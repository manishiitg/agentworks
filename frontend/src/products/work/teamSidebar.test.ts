import { expect, it, vi } from 'vitest'
import api from '../../services/api'
import { readTeamTabState, teamSideChats } from './teamApi'
vi.mock('../../services/api', () => ({ default: { get: vi.fn(), request: vi.fn() }, getAuthToken: () => 'test', getDesktopApiBaseUrl: () => 'http://desktop', getLocalApiBaseUrl: () => 'http://local' }))
vi.mock('../../utils/desktopConnection', () => ({ getRawActiveWorkspaceId: () => 'local' }))
vi.mock('../../stores/useConnectedWorkStore', () => ({ hasDesktopConnections: () => true, useConnectedWorkStore: {} }))
vi.mock('../../utils/remoteSessions', async original => ({ ...await original<typeof import('../../utils/remoteSessions')>(), registerRemoteSession: vi.fn() }))

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

// PLAT-746-1025: a restored online-home panel requested /home/online-items (403) instead of the source Team.
it('uses the server-admitted source Team on restored home tabs without falling back on refusal', async () => {
  const home = { team: { project_id: 'home', roster_source: 'source', folder: { id: 'home-folder' } } }
  const source = { team: { project_id: 'source', folder: { id: 'source-folder' } } }
  vi.mocked(api.request).mockResolvedValueOnce({ data: home } as never).mockResolvedValueOnce({ data: source } as never)
  const state = await readTeamTabState({ project: 'home', folderId: 'home-folder' })
  expect(state.target).toEqual({ project: 'source', folderId: 'source-folder' })
  expect(state.view).toBe(source)
  expect(vi.mocked(api.request).mock.calls.slice(-2).map(([request]) => request.url)).toEqual(['/api/crew-teams/home/tab?folder_id=home-folder', '/api/crew-teams/source/tab'])
  vi.mocked(api.request).mockResolvedValueOnce({ data: home } as never).mockRejectedValueOnce(new Error('No longer a member'))
  await expect(readTeamTabState({ project: 'home' })).rejects.toThrow('No longer a member')
})

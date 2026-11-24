// @vitest-environment happy-dom
import { expect, it } from 'vitest'
import { registerRemoteProject, registerRemoteSession, remoteRequestConnection, remoteSessionConnection, setRemoteOpenProjectLookup, setRemoteSessionProjectLookup } from './remoteSessions'

// A server project opened in place (#3383) sends only its own requests to its server; everything that does not name
// it, its chats or (while it is open) its folders stays on this Mac. A Mac copy can share the folder name.
it('routes only requests that name a server project, its chats or its open folders', () => {
  let open: string | null = 'remote-crew'
  setRemoteOpenProjectLookup(() => open)
  setRemoteSessionProjectLookup(session => session === 'remote-chat' ? 'remote-crew' : undefined)
  registerRemoteProject('remote-crew', 'srv', 'Crew/shared-name', 'Files/shared-name')
  registerRemoteSession('teammate-chat', 'srv')

  const route = (request: Parameters<typeof remoteRequestConnection>[0]) => remoteRequestConnection(request)
  expect(route({ url: '/api/agent-profiles/work/side-chats', params: { project_id: 'remote-crew' } })).toBe('srv')
  expect(route({ url: '/api/agent-profiles/work/projects/remote-crew/shell/stream' })).toBe('srv')
  expect(route({ url: '/api/crew-teams/remote-crew/tab' })).toBe('srv')
  expect(route({ url: '/api/crews/remote-crew/panel' })).toBe('srv')
  expect(route({ url: '/api/chat-history/sessions/remote-chat' })).toBe('srv')
  expect(route({ url: '/api/query', headers: { 'X-Session-ID': 'remote-chat' } })).toBe('srv')
  expect(route({ url: '/api/agent-profiles/work/conversation', data: JSON.stringify({ conversation_key: 'remote-crew:chat:helper' }) })).toBe('srv')
  expect(route({ url: `/api/documents/${encodeURIComponent('Crew/shared-name/product.json')}` })).toBe('srv')
  expect(route({ url: '/api/sessions/teammate-chat/events' })).toBe('srv')
  // An upload to one of its folders names the folder in form data.
  const upload = new FormData(); upload.append('folder_path', 'Files/shared-name/inbox')
  expect(route({ url: '/api/upload', data: upload })).toBe('srv')
  expect(remoteSessionConnection('remote-chat')).toBe('srv')

  // This Mac's own work and app-wide reads stay here.
  expect(route({ url: '/api/agent-profiles/work/side-chats', params: { project_id: 'mac-crew' } })).toBeUndefined()
  expect(route({ url: '/api/sessions/mac-chat/events' })).toBeUndefined()
  // A server chat being open must not pull this Mac's own project with it (its X-Session-ID rides every request).
  expect(route({ url: '/api/agent-profiles/work/side-chats', params: { project_id: 'mac-crew' }, headers: { 'X-Session-ID': 'remote-chat' } })).toBeUndefined()
  // A list request never follows the open chat (this Mac's project list was read from the server).
  expect(route({ url: '/api/workspaces', headers: { 'X-Session-ID': 'remote-chat' } })).toBeUndefined()
  // ...while a request for that chat's own project still follows it.
  expect(route({ url: '/api/agent-profiles/work/side-chats', params: { project_id: 'remote-crew' }, headers: { 'X-Session-ID': 'remote-chat' } })).toBe('srv')
  expect(route({ url: '/api/header-summary' })).toBeUndefined()
  expect(route({ url: '/api/documents', params: { folder: 'Crew/other' } })).toBeUndefined()
  // Once a Mac project is open, the same-named Mac folder is read here again.
  open = 'mac-crew'
  expect(route({ url: `/api/documents/${encodeURIComponent('Crew/shared-name/product.json')}` })).toBeUndefined()
})


// One rule (owner, 24 Nov): a project goes to the server whose own list has it; this window's own list always wins.
it('routes a project by the lists that contain it, the window list first', async () => {
  const { setServerProjectList, setWindowProjectList, remoteRequestConnection: route } = await import('./remoteSessions')
  setServerProjectList('excellence', ['server-crew', 'paired-crew'])
  setWindowProjectList(['mac-crew', 'paired-crew'])
  const request = (project_id: string) => route({ url: '/api/agent-profiles/work/side-chats', params: { project_id } })
  expect(request('server-crew')).toBe('excellence')
  expect(request('paired-crew')).toBeUndefined()
  expect(request('mac-crew')).toBeUndefined()
  // A server's next list replaces what it said before.
  setServerProjectList('excellence', ['paired-crew'])
  expect(request('server-crew')).toBeUndefined()
})

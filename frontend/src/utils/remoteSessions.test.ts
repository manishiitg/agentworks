// @vitest-environment happy-dom
import { expect, it } from 'vitest'
import { registerRemoteSession, remoteRequestConnection, remoteSessionConnection } from './remoteSessions'

// A Team's teammate or online-project chat sends only its own requests to its server; projects never leave this Mac
// (#3779: the Mac app lists only this Mac's projects).
it('routes only requests for a teammate chat to its server', () => {
  registerRemoteSession('teammate-chat', 'srv')
  const route = (request: Parameters<typeof remoteRequestConnection>[0]) => remoteRequestConnection(request)
  expect(route({ url: '/api/sessions/teammate-chat/events' })).toBe('srv')
  expect(route({ url: '/api/chat-history/sessions/teammate-chat' })).toBe('srv')
  expect(route({ url: '/api/query', headers: { 'X-Session-ID': 'teammate-chat' } })).toBe('srv')
  expect(route({ url: '/api/stop', data: JSON.stringify({ session_id: 'teammate-chat' }) })).toBe('srv')
  expect(remoteSessionConnection('teammate-chat')).toBe('srv')
  // #4121: typing into your own online agent's tab sends the agent-profile query to its server.
  expect(route({ url: '/api/agent-profiles/work/query', headers: { 'X-Session-ID': 'teammate-chat' } })).toBe('srv')

  expect(route({ url: '/api/sessions/mac-chat/events' })).toBeUndefined()
  // The open teammate chat's X-Session-ID rides every request: a project or list read must stay on this Mac.
  expect(route({ url: '/api/agent-profiles/work/side-chats', params: { project_id: 'mac-crew' }, headers: { 'X-Session-ID': 'teammate-chat' } })).toBeUndefined()
  expect(route({ url: '/api/workspaces', headers: { 'X-Session-ID': 'teammate-chat' } })).toBeUndefined()
})

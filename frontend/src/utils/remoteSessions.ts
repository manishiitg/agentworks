/** Chats this Mac app shows from one of its servers without reloading the window: a Team member's agent and an online
 * project's agents inside a Mac project's Team (owner, 5 Nov; #3664), including the person's own online agents, which
 * they type into (#4121). Their requests go to that server through this
 * Mac's saved sign-in, /api/connections/{id}/…, and the server checks who may read or change them each time. Server
 * projects themselves open on their website, never in this window (owner, 24 Nov, #3779). */
const sessions = new Map<string, string>()

export function registerRemoteSession(sessionId: string, connectionId: string) { sessions.set(sessionId, connectionId) }

/** The connection a session's requests go through, or undefined for this app's own chats. */
export function remoteSessionConnection(sessionId?: string | null): string | undefined {
  return sessionId ? sessions.get(sessionId) : undefined
}

const SESSION_PATH = /^\/api\/(?:sessions|chat-history\/sessions)\/([^/?]+)/
const CHAT_REQUEST_PATH = /^\/api\/(?:query|stop|steer|cancel|chat|sessions|chat-history|agent-profiles\/[^/]+\/(?:conversation|query))(?:\/|$|\?)/

function decoded(value: string): string { try { return decodeURIComponent(value) } catch { return value } }

/** Which connection a request belongs to, from the teammate chat it names; undefined keeps it on the window's own
 * server. Requests that name no such chat (projects, menu, settings, account) never leave this Mac. */
export function remoteRequestConnection(request: { url?: string; params?: unknown; headers?: unknown; data?: unknown }): string | undefined {
  if (!sessions.size) return undefined
  const url = request.url || ''
  const path = url.split('?')[0]
  const query = new URLSearchParams(url.includes('?') ? url.slice(url.indexOf('?') + 1) : '')
  const params = (request.params && typeof request.params === 'object' ? request.params : {}) as Record<string, unknown>
  const headers = (request.headers && typeof request.headers === 'object' ? request.headers : {}) as Record<string, unknown>
  let data = request.data
  if (typeof data === 'string') { try { data = JSON.parse(data) } catch { data = undefined } }
  const body = (data && typeof data === 'object' && !(typeof FormData !== 'undefined' && data instanceof FormData) ? data : {}) as Record<string, unknown>
  const sessionId = [params.session_id, query.get('session_id'), body.session_id].find(value => typeof value === 'string' && value) as string | undefined
  const session = SESSION_PATH.exec(path)?.[1]
  // Every request carries the open chat's X-Session-ID, so it routes only a chat request (send, stop, steer, the chat's
  // own reads): a list such as this Mac's projects must never follow an open teammate chat to its server.
  return (session && remoteSessionConnection(decoded(session)))
    || remoteSessionConnection(sessionId)
    || (typeof headers['X-Session-ID'] === 'string' && CHAT_REQUEST_PATH.test(path) ? remoteSessionConnection(headers['X-Session-ID']) : undefined)
}

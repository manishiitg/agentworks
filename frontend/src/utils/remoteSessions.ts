/** Chats this Mac app shows from one of its servers without holding them (owner, 5 Nov: a Team member's agent opens
 * inside the Mac Crew): their reads go to that server through this Mac's saved sign-in, /api/connections/{id}/…, and
 * the server checks who may read them each time. Watch only: nothing is sent to them from here. */
const remote = new Map<string, string>()

export function registerRemoteSession(sessionId: string, connectionId: string) { remote.set(sessionId, connectionId) }

/** The connection a session's reads go through, or undefined for this app's own chats. */
export function remoteSessionConnection(sessionId?: string | null): string | undefined { return sessionId ? remote.get(sessionId) : undefined }

/** /api/sessions/<id>/… of a remote session, sent through its connection; any other path is unchanged. */
export function remoteSessionPath(path: string): string {
  const match = /^\/api\/sessions\/([^/?]+)/.exec(path)
  const connection = match ? remote.get(decodeURIComponent(match[1])) : undefined
  return connection ? `/api/connections/${encodeURIComponent(connection)}${path}` : path
}

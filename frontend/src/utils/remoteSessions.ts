/** Projects and chats this Mac app shows from one of its servers without reloading the window: a whole server project
 * opened in place (#3383; again since 27 Nov, #4331), a Team member's agent and an online project's agents inside a Mac
 * project's Team (#3664), including the person's own online agents (#4121). Their requests go to that server through
 * this Mac's saved sign-in, /api/connections/{id}/…, and the server checks who may read or change them each time.
 * Everything else (the menu, settings, the look) stays on this Mac. */
const sessions = new Map<string, string>()
const projects = new Map<string, string>()
// A server project's workspace and files folders (Crew/<name>): file reads name a path, not the project ID. A Mac copy
// and its online copy can share a folder name, so folders route only while that project is the one open.
const folders = new Map<string, string[]>()
let sessionProject: (sessionId: string) => string | undefined = () => undefined
let openProject: () => string | null | undefined = () => undefined

export function registerRemoteSession(sessionId: string, connectionId: string) { sessions.set(sessionId, connectionId) }

/** A project that lives on another connection and opens in this window, with its workspace and files folders. */
export function registerRemoteProject(projectId: string, connectionId: string, ...paths: Array<string | undefined>) {
  projects.set(projectId, connectionId)
  const named = paths.filter((path): path is string => Boolean(path)).map(path => path.replace(/^\/+|\/+$/g, ''))
  if (named.length) folders.set(projectId, named)
}

/** Opening a project from this window's own server: a same-ID online copy opened in place earlier must not keep
 * routing it to that server (#3749 follow-up: the Mac's ai-work went to the server after its online copy was opened). */
export function unregisterRemoteProject(projectId: string) {
  projects.delete(projectId)
  folders.delete(projectId)
}

// Where each project lives, from the lists themselves (owner, 24 Nov: one rule, no guessing). The window's own list
// says "here"; each other server's own list says "there". The window's list wins a clash, and the lists reload with the
// app, so a restart routes exactly as before it.
const windowListed = new Set<string>()
const serverListed = new Map<string, string>()

/** The window's own project list (this Mac's, in the Mac app): these projects are always answered here. */
export function setWindowProjectList(ids: readonly string[]) {
  windowListed.clear()
  for (const id of ids) windowListed.add(id)
}

/** One other server's own project list, as this Mac reads it through its saved sign-in. */
export function setServerProjectList(connectionId: string, ids: readonly string[]) {
  for (const [id, connection] of serverListed) if (connection === connectionId) serverListed.delete(id)
  for (const id of ids) if (!serverListed.has(id)) serverListed.set(id, connectionId)
}

export function remoteProjectConnection(projectId?: string | null): string | undefined {
  if (!projectId || windowListed.has(projectId)) return undefined
  return projects.get(projectId) ?? serverListed.get(projectId)
}

/** The chat store says which project a chat tab belongs to; set once by the store (no import cycle). */
export function setRemoteSessionProjectLookup(lookup: (sessionId: string) => string | undefined) { sessionProject = lookup }

/** The product surface says which project is open; set once by its store. */
export function setRemoteOpenProjectLookup(lookup: () => string | null | undefined) { openProject = lookup }

/** The connection a session's requests go through, or undefined for this app's own chats. */
export function remoteSessionConnection(sessionId?: string | null): string | undefined {
  if (!sessionId) return undefined
  return sessions.get(sessionId) ?? (projects.size || serverListed.size ? remoteProjectConnection(sessionProject(sessionId)) : undefined)
}

/** The connection a workspace path belongs to: a folder of the server project open in place, else none. */
export function folderConnection(path: unknown): string | undefined {
  if (typeof path !== 'string' || !path) return undefined
  const open = openProject()
  const connection = remoteProjectConnection(open)
  if (!open || !connection) return undefined
  const clean = path.replace(/^\/+/, '')
  return folders.get(open)?.some(folder => clean === folder || clean.startsWith(`${folder}/`)) ? connection : undefined
}

const PROJECT_PATH = /\/(?:projects|shared-projects|crews|crew-teams|crew-sync)\/([^/?]+)/
const SESSION_PATH = /^\/api\/(?:sessions|chat-history\/sessions)\/([^/?]+)/
const DOCUMENT_PATH = /^\/api\/documents\/(.+?)(?:\?|$)/
const CHAT_REQUEST_PATH = /^\/api\/(?:query|stop|steer|cancel|chat|sessions|chat-history|agent-profiles\/[^/]+\/(?:conversation|query))(?:\/|$|\?)/

function decoded(value: string): string { try { return decodeURIComponent(value) } catch { return value } }

/** Which connection a request belongs to, from the project, chat or folder it names; undefined keeps it on the
 * window's own server. Requests that name none of them (menu, settings, account) never leave this Mac. */
export function remoteRequestConnection(request: { url?: string; params?: unknown; headers?: unknown; data?: unknown }): string | undefined {
  if (!sessions.size && !projects.size && !serverListed.size) return undefined
  const url = request.url || ''
  const path = url.split('?')[0]
  const query = new URLSearchParams(url.includes('?') ? url.slice(url.indexOf('?') + 1) : '')
  const params = (request.params && typeof request.params === 'object' ? request.params : {}) as Record<string, unknown>
  const headers = (request.headers && typeof request.headers === 'object' ? request.headers : {}) as Record<string, unknown>
  let data = request.data
  if (typeof data === 'string') { try { data = JSON.parse(data) } catch { data = undefined } }
  // An upload's fields are form data (a file to a server project's folder, #3383).
  const form = typeof FormData !== 'undefined' && data instanceof FormData ? data : undefined
  const body = (data && typeof data === 'object' && !form ? data : {}) as Record<string, unknown>
  const read = (key: string) => [params[key], query.get(key), body[key], form?.get(key)].find(value => typeof value === 'string' && value) as string | undefined
  const session = SESSION_PATH.exec(path)?.[1]
  const project = PROJECT_PATH.exec(path)?.[1]
  const document = DOCUMENT_PATH.exec(path)?.[1]
  const conversation = read('conversation_key')
  const workspace = read('workspace_id')
  const sessionConnection = session && remoteSessionConnection(decoded(session))
  if (sessionConnection) return sessionConnection
  // A request that names its project goes to that project's own server: this window's own project stays here even
  // while a server chat is open, whose X-Session-ID would otherwise send it there (a Mac project then answered 422).
  const namedProject = (project && decoded(project)) || read('project_id')
  if (namedProject) {
    const own = remoteProjectConnection(namedProject)
    if (own) return own
    // A server project restored before it was registered still follows its own chat, never another project's.
    const chat = read('session_id') || (typeof headers['X-Session-ID'] === 'string' ? headers['X-Session-ID'] : undefined)
    return chat && (chat.includes(namedProject) || sessionProject(chat) === namedProject) ? remoteSessionConnection(chat) : undefined
  }
  // Every request carries the open chat's X-Session-ID, so it routes only a chat request (send, stop, steer, the chat's
  // own reads): a list such as this Mac's projects must never follow an open server chat to its server.
  return remoteSessionConnection(read('session_id'))
    || (typeof headers['X-Session-ID'] === 'string' && CHAT_REQUEST_PATH.test(path) ? remoteSessionConnection(headers['X-Session-ID']) : undefined)
    || (conversation ? remoteProjectConnection(conversation.split(':')[0]) : undefined)
    || (workspace?.startsWith('work:') ? remoteProjectConnection(workspace.slice(5)) : undefined)
    || (document ? folderConnection(decoded(document)) : undefined)
    || folderConnection(read('folder')) || folderConnection(read('workspace')) || folderConnection(read('folder_path')) || folderConnection(read('path')) || folderConnection(read('filepath')) || folderConnection(read('workspace_path'))
}

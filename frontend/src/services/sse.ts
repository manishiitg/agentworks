import { captureChatAccessScope, denyChatAccess } from './chatAccess'
// AgentWorks' session stream: the shared client with this app's base URL,
// token and logger supplied. Same constructor the store has always used.
import { connectionApiBase, getApiBaseUrl, getAuthToken } from './api'
import { getRawActiveWorkspaceId } from '../utils/desktopConnection'
import { remoteSessionConnection } from '../utils/remoteSessions'
import { logger } from '../utils/logger'
import { SSEConnection as SharedSSEConnection, type SSECallbacks } from '../../shared/session/sse'

export type { SSECallbacks }

export class SSEConnection extends SharedSSEConnection {
  constructor(sessionId: string, sinceIndex: number, callbacks: SSECallbacks, durableChat = true) {
    // A Team member's chat or a server project's chat shown in the Mac app streams from its server through this Mac's sign-in.
    const identity = captureChatAccessScope(sessionId)
    const remote = remoteSessionConnection(sessionId)
    super({ sessionId, sinceIndex, callbacks: { ...callbacks, onForbidden: () => { denyChatAccess(sessionId, identity); callbacks.onForbidden?.() } }, baseUrl: remote && remote !== getRawActiveWorkspaceId() ? connectionApiBase(remote) : getApiBaseUrl(), token: getAuthToken, durableChat, log: logger })
  }
}

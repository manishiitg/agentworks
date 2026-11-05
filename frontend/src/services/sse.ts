// AgentWorks' session stream: the shared client with this app's base URL,
// token and logger supplied. Same constructor the store has always used.
import { getApiBaseUrl, getAuthToken } from './api'
import { remoteSessionConnection } from '../utils/remoteSessions'
import { logger } from '../utils/logger'
import { SSEConnection as SharedSSEConnection, type SSECallbacks } from '../../shared/session/sse'

export type { SSECallbacks }

export class SSEConnection extends SharedSSEConnection {
  constructor(sessionId: string, sinceIndex: number, callbacks: SSECallbacks, durableChat = true) {
    // A Team member's chat shown in the Mac app streams from its server through this Mac's sign-in.
    const remote = remoteSessionConnection(sessionId)
    super({ sessionId, sinceIndex, callbacks, baseUrl: remote ? `${getApiBaseUrl()}/api/connections/${encodeURIComponent(remote)}` : getApiBaseUrl(), token: getAuthToken, durableChat, log: logger })
  }
}

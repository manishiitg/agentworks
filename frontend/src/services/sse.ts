// AgentWorks' session stream: the shared client with this app's base URL,
// token and logger supplied. Same constructor the store has always used.
import { getApiBaseUrl, getAuthToken } from './api'
import { logger } from '../utils/logger'
import { SSEConnection as SharedSSEConnection, type SSECallbacks } from '../../shared/session/sse'

export type { SSECallbacks }

export class SSEConnection extends SharedSSEConnection {
  constructor(sessionId: string, sinceIndex: number, callbacks: SSECallbacks, durableChat = true) {
    super({ sessionId, sinceIndex, callbacks, baseUrl: getApiBaseUrl(), token: getAuthToken, durableChat, log: logger })
  }
}

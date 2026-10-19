import { create } from 'zustand'
import { captureChatIdentity, isChatIdentityCurrent } from './chatIdentity'

export type ServiceChatSurface = 'knowledgebase' | 'mcp-gateway'
// An explicit navigation request, never persisted across accounts or servers.
let nextRequest = 0
type Request = { token: number; surface: ServiceChatSurface; tabId?: string; identity: number }
export const useServiceChatNavigation = create<{ request: Request | null }>(() => ({ request: null }))
export function requestServiceChat(surface: ServiceChatSurface, tabId?: string) {
  useServiceChatNavigation.setState({ request: { token: ++nextRequest, surface, tabId, identity: captureChatIdentity() } })
}
export function currentServiceChatRequest(request: Request | null, surface: ServiceChatSurface) {
  return request?.surface === surface && isChatIdentityCurrent(request.identity) ? request : null
}

export function clearServiceChatRequest(request: Request) {
  useServiceChatNavigation.setState(state => state.request === request ? { request: null } : state)
}

import { create } from 'zustand'
import { captureChatIdentity, isChatIdentityCurrent } from './chatIdentity'

export type ServiceChatSurface = 'knowledgebase' | 'mcp-gateway'
// An explicit navigation request, never persisted across accounts or servers.
type Request = { surface: ServiceChatSurface; tabId?: string; identity: number }
export const useServiceChatNavigation = create<{ request: Request | null }>(() => ({ request: null }))
export function requestServiceChat(surface: ServiceChatSurface, tabId?: string) {
  useServiceChatNavigation.setState({ request: { surface, tabId, identity: captureChatIdentity() } })
}
export function currentServiceChatRequest(request: Request | null, surface: ServiceChatSurface) {
  return request?.surface === surface && isChatIdentityCurrent(request.identity) ? request : null
}

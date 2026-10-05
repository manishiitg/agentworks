import { useCallback, useState } from 'react'
import type { PollingEvent } from '../services/api-types'
import { captureChatIdentity, isChatIdentityCurrent } from '../utils/chatIdentity'

export type OlderConversationPage = {
  events: PollingEvent[]
  loading: boolean
  error?: string
  pagination?: { hasMore: boolean; nextOffset: number; compact?: boolean }
}
const emptyPage: OlderConversationPage = { events: [], loading: false }

// A response for an inactive chat updates only that chat's cache. Previously
// one local history slot let A's late response replace B's already-loaded page.
export function useConversationOlderPages(sessionId?: string | null) {
  const identity = captureChatIdentity()
  const [cache, setCache] = useState(() => ({ identity, pages: {} as Record<string, OlderConversationPage> }))
  const pages = cache.identity === identity ? cache.pages : {}
  const updatePage = useCallback((id: string, update: (page: OlderConversationPage) => OlderConversationPage) => {
    if (!isChatIdentityCurrent(identity)) return
    setCache(current => {
      const pages = current.identity === identity ? current.pages : {}
      return { identity, pages: { ...pages, [id]: update(pages[id] ?? emptyPage) } }
    })
  }, [identity])
  return { page: sessionId ? pages[sessionId] ?? emptyPage : emptyPage, updatePage }
}

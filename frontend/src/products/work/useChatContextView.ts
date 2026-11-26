import { useCallback, useEffect, useState } from 'react'
import { useChatStore } from '../../stores/useChatStore'
import { agentApi } from '../../services/api'
import type { ChatContextView } from '../../services/api-types'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'

const CHAT_CONTEXT_DEDUPE_MS = 3_000
const contextCache = new Map<string, { at: number; view: ChatContextView }>()
const contextInFlight = new Map<string, Promise<ChatContextView>>()
let contextCacheIdentity = captureChatIdentity()

function ensureCurrentContextCache(): number {
  const identity = captureChatIdentity()
  if (identity !== contextCacheIdentity) {
    contextCacheIdentity = identity
    contextCache.clear()
    contextInFlight.clear()
  }
  return identity
}

/** Shared deduplicated read of /api/chat-context so ChatModeMenu, ContextRing, and ComposerModelChips do not double-fetch. */
export function fetchChatContextShared(sessionId: string, force = false): Promise<ChatContextView> {
  const identity = ensureCurrentContextCache()
  const key = `${identity}:${sessionId}`
  const cached = contextCache.get(key)
  if (!force && cached && Date.now() - cached.at < CHAT_CONTEXT_DEDUPE_MS) {
    return Promise.resolve(cached.view)
  }
  const pending = contextInFlight.get(key)
  if (pending) return pending
  const promise = agentApi.getChatContext(sessionId).then(view => {
    if (isChatIdentityCurrent(identity)) contextCache.set(key, { at: Date.now(), view })
    return view
  }).finally(() => {
    if (contextInFlight.get(key) === promise) contextInFlight.delete(key)
  })
  contextInFlight.set(key, promise)
  return promise
}

// Display refresh only: the server remains the source for context and account
// usage. A compaction can happen without the foreground turn ending.
export function useChatContextView(sessionId: string, busy = false, enabled = true) {
  const revision = useChatStore(state => {
    if (!enabled || !sessionId) return undefined
    const events = state.tabEvents[sessionId] || []
    for (let i = events.length - 1; i >= 0; i--) {
      const event = events[i]
      if (['status_line', 'token_usage', 'context_compaction', 'unified_completion', 'conversation_end', 'conversation_error'].includes(event.type || '')) return event.id
    }
    return undefined
  })
  const [view, setView] = useState<ChatContextView | null>(() => {
    if (!enabled || !sessionId) return null
    const identity = ensureCurrentContextCache()
    return contextCache.get(`${identity}:${sessionId}`)?.view ?? null
  })

  const refresh = useCallback((force = false, isCancelled?: () => boolean) => {
    if (!enabled || !sessionId || document.visibilityState === 'hidden') return
    const identity = captureChatIdentity()
    void fetchChatContextShared(sessionId, force).then(next => {
      if (!isCancelled?.() && isChatIdentityCurrent(identity)) setView(next)
    }).catch(() => undefined)
  }, [enabled, sessionId])

  useEffect(() => {
    if (!enabled || !sessionId) return
    let cancelled = false
    const isCancelled = () => cancelled
    refresh(false, isCancelled)
    const changed = (event: Event) => {
      const next = (event as CustomEvent<ChatContextView>).detail
      if (next?.session_id === sessionId) {
        const identity = ensureCurrentContextCache()
        contextCache.set(`${identity}:${sessionId}`, { at: Date.now(), view: next })
        setView(next)
      }
    }
    const onVisibility = () => { if (document.visibilityState !== 'hidden') refresh(false, isCancelled) }
    const interval = busy ? window.setInterval(() => refresh(true, isCancelled), 30000) : undefined
    window.addEventListener('agentworks:chat-context-changed', changed)
    document.addEventListener('visibilitychange', onVisibility)
    return () => {
      cancelled = true
      if (interval !== undefined) window.clearInterval(interval)
      window.removeEventListener('agentworks:chat-context-changed', changed)
      document.removeEventListener('visibilitychange', onVisibility)
    }
  }, [sessionId, busy, enabled, refresh])

  // Trigger a refresh when a relevant context/usage event lands without tearing down the interval or listeners.
  useEffect(() => {
    if (!enabled || !sessionId || !revision) return
    let cancelled = false
    refresh(true, () => cancelled)
    return () => { cancelled = true }
  }, [enabled, sessionId, revision, refresh])

  return view?.session_id === sessionId ? view : null
}

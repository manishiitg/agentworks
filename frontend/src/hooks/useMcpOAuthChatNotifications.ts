import { useEffect } from 'react'
import { agentApi } from '../services/api'
import type { PollingEvent } from '../services/api-types'
import { useChatStore } from '../stores/useChatStore'
import { sendWorkspacePaneMessageToChat } from '../utils/workspacePaneChat'
import { MCP_OAUTH_STARTED_EVENT, isWatchingMcpOAuth, finishMcpOAuthNotifications } from '../utils/mcpOAuthNotification'

const inFlight = new Set<string>()

/** Deliver OAuth outcomes through the same durable queue as every right-pane action. */
export async function deliverMcpOAuthChatNotifications(tabId: string, sessionId: string) {
  const key = `${tabId}:${sessionId}`
  if (inFlight.has(key)) return
  inFlight.add(key)
  try {
    const response = await agentApi.getSessionEvents(sessionId, undefined, { limit: 50, offset: 0, workingSet: 'all' })
    const tab = useChatStore.getState().getTab(tabId)
    if (tab?.sessionId !== sessionId) return
    let delivered = 0
    for (const event of response.events ?? []) {
      if (event.type !== 'synthetic_turn_ready' || event.session_id !== sessionId) continue
      const wrapper = event.data as { data?: { agent_id?: string; name?: string; status?: string; message?: string } }
      const fields = wrapper?.data
      if (!fields || !/^(vault|private)-oauth:/.test(fields.agent_id ?? '') || !event.id) continue
      if (fields.status !== 'completed' && fields.status !== 'failed') continue
      if (useChatStore.getState().getTab(tabId)?.config.mcpOAuthNotificationIDs?.includes(event.id)) continue
      // Older callbacks may already have injected their model turn. Do not repeat it.
      const alreadySent = (response.events ?? []).some((item: PollingEvent) => {
        if (item.type !== 'user_message' || !fields.message) return false
        const data = item.data as { content?: string; data?: { content?: string } }
        return (data?.data?.content ?? data?.content ?? '').includes(fields.message)
      })
      const vault = fields.agent_id?.startsWith('vault-oauth:')
      const message = `[AUTO-NOTIFICATION] [MCP sign-in ${event.id}] ${fields.name || 'MCP server'} ${fields.status === 'completed'
        ? (vault ? 'is signed in to Vault and its tools were discovered. No group access was assigned. Tell me briefly that it is connected.' : 'finished private sign-in. Verify its connection through the API bridge and tell me briefly what I can do with it. This does not grant Vault group access.')
        : 'did not finish signing in. Tell me briefly and offer to retry.'}`
      useChatStore.getState().addTabEvents(sessionId, [event])
      if (alreadySent) {
        const store = useChatStore.getState()
        const receipts = store.getTabConfig(tabId)?.mcpOAuthNotificationIDs ?? []
        store.setTabConfig(tabId, { mcpOAuthNotificationIDs: [...receipts, event.id].slice(-100) })
      } else {
        await sendWorkspacePaneMessageToChat({ tabId, message, notificationId: event.id })
      }
      delivered++
    }
    finishMcpOAuthNotifications(sessionId, delivered)
  } finally {
    inFlight.delete(key)
  }
}

export function useMcpOAuthChatNotifications(tabId: string | null | undefined) {
  const sessionId = useChatStore(state => tabId ? state.chatTabs[tabId]?.sessionId : undefined)
  useEffect(() => {
    if (!tabId || !sessionId) return
    let disposed = false
    let timer: ReturnType<typeof setInterval> | undefined
    const refresh = () => {
      if (!disposed) void deliverMcpOAuthChatNotifications(tabId, sessionId).catch(error => console.warn('[MCP OAuth] Could not deliver chat notification', error))
    }
    let focusWatchUntil = 0
    const watch = () => {
      if ((!isWatchingMcpOAuth(sessionId) && focusWatchUntil < Date.now()) || timer) return
      timer = setInterval(() => {
        refresh()
        if (!isWatchingMcpOAuth(sessionId) && focusWatchUntil < Date.now() && timer) { clearInterval(timer); timer = undefined }
      }, 2000)
    }
    const started = (event: Event) => { if ((event as CustomEvent).detail === sessionId) watch() }
    // Initial load/focus also recover a completed callback from an idle or restored chat.
    refresh()
    watch()
    const onFocus = () => { focusWatchUntil = Date.now() + 30_000; refresh(); watch() }
    window.addEventListener('focus', onFocus)
    window.addEventListener(MCP_OAUTH_STARTED_EVENT, started)
    return () => {
      disposed = true
      if (timer) clearInterval(timer)
      window.removeEventListener('focus', onFocus)
      window.removeEventListener(MCP_OAUTH_STARTED_EVENT, started)
    }
  }, [tabId, sessionId])
}

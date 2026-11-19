import { CHAT_INSERT_QUOTE_EVENT, withInsert, type InsertQuoteDetail } from '../components/chat/ReplyQuote'
import { useChatStore } from '../stores/useChatStore'
import { CHAT_FOCUS_COMPOSER_EVENT } from './workspacePaneChat'

/** Puts a line in this chat's message box (the open composer, else the saved draft); sends nothing. */
export function startChatMessage(sessionId: string, text: string) {
  const store = useChatStore.getState()
  const tabId = Object.entries(store.chatTabs).find(([, tab]) => tab?.sessionId === sessionId)?.[0] || store.activeTabId
  if (!tabId) return
  const detail: InsertQuoteDetail = { tabId, text, plain: true }
  window.dispatchEvent(new CustomEvent<InsertQuoteDetail>(CHAT_INSERT_QUOTE_EVENT, { detail }))
  if (!detail.handled) {
    store.setTabConfig(tabId, { inputText: withInsert(store.getTabConfig(tabId)?.inputText || '', detail) })
    window.setTimeout(() => window.dispatchEvent(new CustomEvent(CHAT_FOCUS_COMPOSER_EVENT)), 30)
  }
}

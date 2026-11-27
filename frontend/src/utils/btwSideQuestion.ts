import { useMemo } from 'react'
import { create } from 'zustand'
import api, { agentApi } from '../services/api'
import { useChatStore } from '../stores/useChatStore'
import { buildCleanConversationItems } from './cleanConversation'
import { forkChatTab } from './forkChatTab'
import type { PollingEvent } from '../services/api-types'

// /btw side questions. The server answers each on a hidden copy of the chat (one read-only turn), so the chat itself
// never sees the question and keeps working. The answer shows in a panel above the message box; the person dismisses
// it, opens it as its own tab, or adds it to the chat. Answers stay listed for `/btw` alone until the app reloads.

export type BtwStatus = 'starting' | 'answering' | 'done' | 'failed'
export interface BtwItem {
  id: string
  question: string
  status: BtwStatus
  answer: string
  error?: string
  at: number
  // The hidden copy while it exists; cleared once dismissed or kept as a tab.
  conversationKey?: string
  sessionId?: string
  name?: string
}
interface BtwTab { items: BtwItem[]; open: string | null }
type BtwView = { kind: 'item'; item: BtwItem } | { kind: 'history'; items: BtwItem[] } | null

export const useBtwStore = create<{ byTab: Record<string, BtwTab> }>(() => ({ byTab: {} }))

const EMPTY: BtwTab = { items: [], open: null }
const HISTORY = 'history'
const MAX_KEPT = 20

function updateTab(tabId: string, change: (tab: BtwTab) => BtwTab) {
  useBtwStore.setState(state => ({ byTab: { ...state.byTab, [tabId]: change(state.byTab[tabId] ?? EMPTY) } }))
}
function updateItem(tabId: string, id: string, change: Partial<BtwItem>) {
  updateTab(tabId, tab => ({ ...tab, items: tab.items.map(item => item.id === id ? { ...item, ...change } : item) }))
}

export function btwView(tab: BtwTab | undefined): BtwView {
  if (!tab?.open) return null
  if (tab.open === HISTORY) return { kind: 'history', items: tab.items }
  const item = tab.items.find(entry => entry.id === tab.open)
  return item ? { kind: 'item', item } : null
}
// Select the tab's stored state (a stable reference) and derive the view from it. Building the view inside the
// selector returned a new object on every read, which re-rendered the composer forever.
export function useBtwView(tabId: string | null): BtwView {
  const tab = useBtwStore(state => tabId ? state.byTab[tabId] : undefined)
  return useMemo(() => btwView(tab), [tab])
}

export function openBtwItem(tabId: string, id: string | null) { updateTab(tabId, tab => ({ ...tab, open: id })) }
/** Lists this chat's earlier side questions; false when there are none. */
export function showBtwHistory(tabId: string): boolean {
  const tab = useBtwStore.getState().byTab[tabId]
  if (!tab?.items.length) return false
  updateTab(tabId, current => ({ ...current, open: HISTORY }))
  return true
}

function sourceTab(tabId: string) {
  const tab = useChatStore.getState().chatTabs[tabId]
  const usable = tab?.metadata?.agentProfileId === 'work' && tab.metadata.agentProfileConversationKey && tab.metadata.agentProfileTabCanWrite !== false && !tab.metadata.isViewOnly && !tab.metadata.isScheduledRun && !tab.metadata.isBotRun
  return usable ? tab : undefined
}
export function btwUnavailableReason(tabId: string): string | null {
  return sourceTab(tabId) ? null : 'Open one of your own project chats to ask a side question.'
}

function errorText(cause: unknown, fallback: string): string {
  const detail = (cause as { response?: { data?: { error?: string | { message?: string } } } })?.response?.data?.error
  const message = typeof detail === 'string' ? detail : detail?.message
  return message || fallback
}

export async function askBtw(tabId: string, question: string): Promise<void> {
  const text = question.trim()
  const sourceKey = sourceTab(tabId)?.metadata?.agentProfileConversationKey
  if (!text || !sourceKey) return
  const id = `btw-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
  const item: BtwItem = { id, question: text, status: 'starting', answer: '', at: Date.now() }
  updateTab(tabId, tab => ({ items: [item, ...tab.items].slice(0, MAX_KEPT), open: id }))
  try {
    const { data } = await api.post<{ conversation_key: string; session_id: string; name: string }>('/api/agent-profiles/work/conversation/fork', { conversation_key: sourceKey, side_question: text })
    updateItem(tabId, id, { status: 'answering', conversationKey: data.conversation_key, sessionId: data.session_id, name: data.name })
  } catch (cause) {
    updateItem(tabId, id, { status: 'failed', error: errorText(cause, 'Could not ask the side question. Try again.') })
  }
}

/** The copy's reply to the side question: the assistant text after its last user message. */
export function btwAnswerFromEvents(events: PollingEvent[]): { answer: string; error: string } {
  const items = buildCleanConversationItems(events)
  let lastUser = -1
  items.forEach((item, index) => { if (item.role === 'user') lastUser = index })
  const after = items.slice(lastUser + 1)
  const answer = after.filter(item => item.role === 'assistant').map(item => item.content.trim()).filter(Boolean).join('\n\n')
  const error = after.filter(item => item.role === 'error').map(item => item.content.trim()).filter(Boolean).at(-1) ?? ''
  return { answer, error }
}

const RUNNING = new Set(['active', 'running'])

/** One read of the copy. Returns true while it is still answering. */
export async function pollBtwItem(tabId: string, item: BtwItem): Promise<boolean> {
  if (!item.sessionId || item.status !== 'answering') return false
  const response = await agentApi.getRecentSessionEvents(item.sessionId)
  const { answer, error } = btwAnswerFromEvents(response.events ?? [])
  const running = RUNNING.has(String(response.session_status ?? ''))
  if (!running && (answer || error)) {
    updateItem(tabId, item.id, answer ? { status: 'done', answer } : { status: 'failed', error })
    return false
  }
  if (answer && answer !== item.answer) updateItem(tabId, item.id, { answer })
  return true
}

async function removeCopy(conversationKey: string, sessionId?: string) {
  if (sessionId) await agentApi.stopSession(sessionId).catch(() => undefined)
  for (let attempt = 0; attempt < 6; attempt += 1) {
    try {
      const preview = await agentApi.previewAgentRemoval('work', conversationKey)
      await agentApi.closeAgentProfileSideChat('work', conversationKey, preview.confirmation, {})
      return
    } catch {
      // A copy still stopping answers 409 for a moment; try again shortly.
      await new Promise(resolve => setTimeout(resolve, 1000))
    }
  }
}

/** Dismiss closes the panel and removes the hidden copy; the question and answer stay in /btw history. */
export function dismissBtw(tabId: string, id: string) {
  const item = useBtwStore.getState().byTab[tabId]?.items.find(entry => entry.id === id)
  updateTab(tabId, tab => ({ ...tab, open: tab.open === id ? null : tab.open }))
  if (!item?.conversationKey) return
  const { conversationKey, sessionId } = item
  updateItem(tabId, id, { conversationKey: undefined, sessionId: undefined, ...(item.status === 'answering' ? { status: 'failed', error: 'Dismissed before it answered.' } : {}) })
  void removeCopy(conversationKey, item.status === 'answering' ? sessionId : undefined)
}

/** Open as tab: fork the hidden copy into a normal chat under this one, then remove the copy. */
export async function keepBtwAsTab(tabId: string, id: string): Promise<void> {
  const item = useBtwStore.getState().byTab[tabId]?.items.find(entry => entry.id === id)
  if (!item?.conversationKey || item.status !== 'done') return
  const kept = await forkChatTab(tabId, { conversationKey: item.conversationKey, name: item.name || `btw: ${item.question.slice(0, 40)}` })
  if (kept) dismissBtw(tabId, id)
}

/** The text "Add to chat" sends to the chat (queued behind a running turn). */
export function btwChatNote(item: Pick<BtwItem, 'question' | 'answer'>): string {
  const quote = (text: string) => text.trim().split('\n').map(line => `> ${line}`).join('\n')
  return `By the way, I asked this on the side:\n${quote(item.question)}\n\nThe answer was:\n${quote(item.answer)}`
}

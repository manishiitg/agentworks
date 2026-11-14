import api from '../services/api'
import { useChatStore } from '../stores/useChatStore'
import type { FileContextItem } from '../stores/types'
import { captureChatIdentity, isChatIdentityCurrent } from './chatIdentity'

export type ChatContextDestination = { identity: number; tabId: string; projectId: string; conversationKey: string; sessionId: string }
export type BrowserContextMetadata = { browser_session?: string; document_token: string; url: string; width: number; height: number; selector?: string; text?: string; rect: { x: number; y: number; width: number; height: number } }
export type CapturedChatContext = { source: 'browser'; capture: BrowserContextMetadata; files: { name: string; path: string; type: 'file' }[]; preview_path: string }

export function captureChatContextDestination(tabId: string): ChatContextDestination | undefined {
  const store = useChatStore.getState(), tab = store.getTab(tabId)
  const meta = tab?.metadata
  if (store.activeTabId !== tabId || !tab?.sessionId || meta?.isViewOnly || meta?.agentProfileId !== 'work' || !meta.agentProfileProjectId || !meta.agentProfileConversationKey) return
  return { identity: captureChatIdentity(), tabId, projectId: meta.agentProfileProjectId, conversationKey: meta.agentProfileConversationKey, sessionId: tab.sessionId }
}

export function isChatContextDestinationCurrent(destination: ChatContextDestination): boolean {
  if (!isChatIdentityCurrent(destination.identity)) return false
  const current = captureChatContextDestination(destination.tabId)
  return Boolean(current && current.projectId === destination.projectId && current.conversationKey === destination.conversationKey && current.sessionId === destination.sessionId)
}

export async function requestBrowserContext<T extends BrowserContextMetadata | CapturedChatContext>(destination: ChatContextDestination, browserSession: string, pick?: { document_token: string; x: number; y: number }, signal?: AbortSignal): Promise<T> {
  if (!isChatContextDestinationCurrent(destination)) throw new Error('The chat changed. Add the selection again in the intended chat.')
  const result = await api.post<T>(`/api/agent-profiles/work/projects/${encodeURIComponent(destination.projectId)}/chat-context`, {
    source: 'browser', operation: pick ? 'add' : 'probe', conversation_key: destination.conversationKey, session_id: destination.sessionId, browser_session: browserSession, ...pick,
  }, { signal, timeout: 20000 })
  if (!isChatContextDestinationCurrent(destination)) throw new Error('The chat changed. Add the selection again in the intended chat.')
  return result.data
}

// This is the only draft mutation. It appends ordinary completed files to the
// latest draft; it never inserts paths in a live CLI, calls Send or queues work.
export function stageCapturedChatContext(destination: ChatContextDestination, captured: CapturedChatContext): boolean {
  if (!isChatContextDestinationCurrent(destination) || captured.source !== 'browser' || !captured.files.some(file => file.path === captured.preview_path)) return false
  const store = useChatStore.getState(), tab = store.getTab(destination.tabId)!
  const files: FileContextItem[] = captured.files.map(file => ({ ...file, ...(file.path.endsWith('.json') ? { context: { kind: 'browser' as const, label: 'Page element', previewPath: captured.preview_path } } : {}) }))
  const fileContext = [...tab.config.fileContext]
  for (const file of files) if (!fileContext.some(current => current.path === file.path)) fileContext.push(file)
  store.setTabConfig(destination.tabId, { fileContext, autoRun: false })
  store.setTabViewMode(destination.tabId, 'formatted')
  return true
}

export function contextAttachmentRemoval(files: FileContextItem[], path: string): Set<string> {
  const selected = files.find(file => file.path === path)
  return new Set([path, ...(selected?.context?.previewPath ? [selected.context.previewPath] : [])])
}

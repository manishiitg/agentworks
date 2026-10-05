import { conversationToRestoredEvents } from '../../shared/session/restore'
import { agentApi } from '../services/api'
import type { PollingEvent } from '../services/api-types'
import { useChatStore } from '../stores/useChatStore'
import { resolveLiveInputConfirmations } from './liveInputReceipt'
import { appendRestoredLiveTail } from './sessionRestore'
import { captureChatIdentity, assertChatIdentityCurrent } from './chatIdentity'

export type ExecutionConversationState = {
  status: string
  hasRunningBackgroundAgents: boolean
  isSyntheticTurn: boolean
  canSteer: boolean
  restoredEvents: PollingEvent[]
}

const pendingHydrations = new Map<string, Promise<ExecutionConversationState>>()

// Execution histories are diagnostic artifacts, not interactive chats. Keep
// their JSON reader explicit so it can never become a fallback in ChatArea's
// canonical SQLite restore path.
export function hydrateExecutionConversation(
  sessionId: string,
  workspacePath?: string,
): Promise<ExecutionConversationState> {
  const identity = captureChatIdentity()
  const key = JSON.stringify([identity, sessionId, workspacePath])
  const pending = pendingHydrations.get(key)
  if (pending) return pending
  const request = restoreExecutionConversation(sessionId, workspacePath, identity)
    .finally(() => { pendingHydrations.delete(key) })
  pendingHydrations.set(key, request)
  return request
}

async function restoreExecutionConversation(sessionId: string, workspacePath: string | undefined, identity: number): Promise<ExecutionConversationState> {
  const startingIDs = new Set(useChatStore.getState().getTabEvents(sessionId).map(event => event.id))
  const [conversation, runtime] = await Promise.all([
    agentApi.getChatHistoryResumeConversation(sessionId, workspacePath, 100, 0, true),
    agentApi.getSessionEvents(sessionId, undefined, { limit: 1 }).catch(() => null),
  ])
  assertChatIdentityCurrent(identity)
  const events = resolveLiveInputConfirmations(conversationToRestoredEvents(conversation))
  const chatStore = useChatStore.getState()
  const concurrent = chatStore.getTabEvents(sessionId).filter(event => event.id && !startingIDs.has(event.id))
  chatStore.setTabEvents(sessionId, events)
  if (concurrent.length > 0) appendRestoredLiveTail(sessionId, concurrent)
  // Establish the volatile event cursor before connecting SSE, just like the
  // interactive restore. Otherwise every revisit starts another full restore.
  if (runtime?.last_processed_index !== undefined) chatStore.setTabLastEventIndex(sessionId, runtime.last_processed_index)
  chatStore.setTabHasMoreOlderEvents(sessionId, conversation.history_pagination?.has_more ?? false)
  chatStore.setTabHistoryPagination(sessionId, conversation.history_pagination
    ? { hasMore: conversation.history_pagination.has_more, nextOffset: conversation.history_pagination.next_offset }
    : null)
  return {
    status: runtime?.session_status || 'completed',
    hasRunningBackgroundAgents: runtime?.has_running_background_agents ?? false,
    isSyntheticTurn: runtime?.is_synthetic_turn ?? false,
    canSteer: runtime?.can_steer ?? false,
    restoredEvents: events,
  }
}

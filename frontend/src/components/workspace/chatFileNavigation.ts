import { useChatStore } from '../../stores/useChatStore'
import { setWorkspaceViewTarget } from '../../hooks/useWorkspaceViewTarget'
import type { ReplyFileChangesRef } from '../../utils/replyFileChanges'
import type { FileChange } from './chatFileReviewData'

export type SavedFileSelection = { reference: ReplyFileChangesRef; change: FileChange; query?: Record<string, string> }
export const chatFilesNavigationEvent = 'agentworks:open-chat-files'

// Navigation only. Saved bytes and line anchors still pass the server's chat
// and file grants. Never apply a click from an old chat to the current chat.
export function openChatFiles(sessionId: string, selection?: SavedFileSelection, checkoutId?: string) {
  openFilesTarget(sessionId, selection ? `saved:${JSON.stringify(selection)}` : checkoutId ? `review-project:${checkoutId}` : 'review-project')
}

export function openReplyChanges(reference: ReplyFileChangesRef) {
  openFilesTarget(reference.sessionId, `reply:${JSON.stringify(reference)}`)
}

function openFilesTarget(sessionId: string, target: string) {
  const state = useChatStore.getState()
  if (state.chatTabs[state.activeTabId || '']?.sessionId !== sessionId) {
    state.addToast('Open the original chat to view these changes.', 'error')
    return
  }
  setWorkspaceViewTarget('files', target)
  window.dispatchEvent(new CustomEvent(chatFilesNavigationEvent, { detail: { sessionId } }))
}

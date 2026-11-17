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

/** The chat's pull request panel; the server checks the PR belongs to this chat. */
export function openPullRequest(sessionId: string, repository: string, number: number) {
  openFilesTarget(sessionId, `pr:${JSON.stringify({ repository, number })}`)
}

/** A PR link's click: opens the panel; Cmd/Ctrl-click still opens GitHub. */
export function pullRequestClick(sessionId: string | undefined, repository: string | undefined, number: number | undefined) {
  return (event: { metaKey: boolean; ctrlKey: boolean; shiftKey: boolean; button: number; preventDefault: () => void }) => {
    if (!sessionId || !repository || !number || event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return
    event.preventDefault()
    openPullRequest(sessionId, repository, number)
  }
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

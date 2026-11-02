import { useEffect, useState } from 'react'
import type { ReplyFileChangesRef } from '../../utils/replyFileChanges'
import type { SavedFileSelection } from '../../components/workspace/chatFileNavigation'

export type WorkFilesView =
  | { kind: 'browse' }
  | { kind: 'changes'; project?: boolean; checkoutId?: string; outsideGit?: boolean; uncommitted?: boolean }
  | { kind: 'reply'; reference: ReplyFileChangesRef }
  | { kind: 'saved'; selection: SavedFileSelection }

/** One file-panel selection per chat. New chats inherit Changes/Browse only;
 * exact saved versions never move to another session or access scope. */
export function useWorkFilesView(scope: string, workspacePath: string, sessionId: string, visible = true) {
  const [state, setState] = useState<{ scope: string; chats: Record<string, WorkFilesView>; changes: boolean }>({ scope, chats: {}, changes: false })
  useEffect(() => {
    if (!visible || !sessionId) return
    setState(previous => {
      const current = previous.scope === scope ? previous : { scope, chats: {}, changes: false }
      if (current.chats[sessionId]) return current
      return { ...current, chats: { ...current.chats, [sessionId]: { kind: current.changes ? 'changes' : 'browse' } } }
    })
  }, [scope, sessionId, visible])
  const view: WorkFilesView = state.scope === scope
    ? state.chats[sessionId] ?? { kind: state.changes ? 'changes' : 'browse' }
    : { kind: 'browse' }
  const select = (target: string) => {
    let next: WorkFilesView = { kind: 'browse' }
    if (target === 'review') next = { kind: 'changes' }
    else if (target === 'review-project' || target.startsWith('review-project:')) next = { kind: 'changes', project: true, checkoutId: target.slice('review-project:'.length) }
    else if (target === 'review-outside-git') next = { kind: 'changes', outsideGit: true }
    else if (target === 'review-uncommitted') next = { kind: 'changes', uncommitted: true }
    else if (target.startsWith('reply:') || target.startsWith('saved:')) {
      try {
        if (target.startsWith('reply:')) {
          const reference = JSON.parse(target.slice(6)) as ReplyFileChangesRef
          if (reference.sessionId !== sessionId || reference.workspacePath !== workspacePath || !reference.snapshotId) return
          next = { kind: 'reply', reference }
        } else {
          const selection = JSON.parse(target.slice(6)) as SavedFileSelection
          if (selection.reference?.sessionId !== sessionId || selection.reference.workspacePath !== workspacePath || !selection.change?.path) return
          next = { kind: 'saved', selection }
        }
      } catch { return }
    }
    setState(previous => ({ scope, chats: { ...(previous.scope === scope ? previous.chats : {}), [sessionId]: next }, changes: next.kind !== 'browse' }))
  }
  return [view, select] as const
}

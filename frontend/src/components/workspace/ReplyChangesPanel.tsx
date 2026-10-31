import type { ReplyFileChangesRef } from '../../utils/replyFileChanges'
import { ChatFileReview } from './ChatFileReview'

/** Render the exact reply's saved diffs through the same authorized viewer as chat review. */
export function ReplyChangesPanel({ reference, tabId, onBrowse }: { reference: ReplyFileChangesRef; tabId: string; onBrowse: () => void }) {
  return <ChatFileReview reply={reference} workspacePath={reference.workspacePath} sessionId={reference.sessionId} tabId={tabId} onBrowse={onBrowse} />
}

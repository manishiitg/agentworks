import type { PollingEvent } from '../services/api-types'
import { normalizeTranscriptChunkEvents } from './transcriptChunkUpdates'

export interface ReplyFileChangesRef {
  snapshotId: string
  workspacePath: string
  sessionId: string
}

// Capture finishes asynchronously, sometimes after the next reply. Bind by
// server sequence bounds, never by arrival position or the latest message.
export function replyFileChangesByMessage(events: PollingEvent[], replyIds: ReadonlySet<string>): Map<string, ReplyFileChangesRef> {
  const replies = normalizeTranscriptChunkEvents(events).filter(event => replyIds.has(event.id))
  const result = new Map<string, ReplyFileChangesRef>()
  for (const event of events) {
    if (event.type !== 'reply_file_changes') continue
    const data = event.data?.data as Record<string, unknown> | undefined
    if (!data || typeof data.snapshot_id !== 'string' || typeof data.workspace_path !== 'string'
      || typeof data.start_sequence !== 'number' || typeof data.end_sequence !== 'number') continue
    const reply = replies.filter(candidate => candidate.session_id === event.session_id
      && typeof candidate.sequence === 'number' && candidate.sequence > (data.start_sequence as number)
      && candidate.sequence <= (data.end_sequence as number))
      .sort((a, b) => (b.sequence ?? 0) - (a.sequence ?? 0))[0]
    if (reply) result.set(reply.id, { snapshotId: data.snapshot_id, workspacePath: data.workspace_path, sessionId: event.session_id || '' })
  }
  return result
}

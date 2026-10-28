import type { PollingEvent } from '../services/api-types'
import { normalizeTranscriptChunkEvents } from './transcriptChunkUpdates'

type ReplyFileChange = { path: string; status: string; added?: number; removed?: number; binary?: boolean }
export type ReplyPullRequest = { number?: number; url?: string; state: string; checks?: string; review?: string; failing_checks?: string[]; conflicts?: boolean }
// next_step is decided by the server: the one Git step this checkout needs now (sent by the lazy groups read, not the event).
export type ReplyFileGroup = { kind: 'repo' | 'crew_files'; root: string; name: string; branch?: string; github_repo?: string; new_checkout: boolean; files: ReplyFileChange[]; total_files: number; more_count: number; pr?: ReplyPullRequest; next_step?: 'share_pr' | 'update_pr' | 'get_latest' | 'none' }
// Computed by the server when the reply's files were captured; the card renders from it without a request.
export type ReplyFileSummary = { files: number; added: number; removed: number; groups: ReplyFileGroup[] }

export interface ReplyFileChangesRef {
  snapshotId: string
  workspacePath: string
  sessionId: string
  latestSnapshotId?: string
  summary?: ReplyFileSummary
  // The chat's newest review point (a file_reviewed event), if any.
  reviewedSnapshotId?: string
}

const summaryOf = (value: unknown): ReplyFileSummary | undefined => {
  const summary = value as ReplyFileSummary | undefined
  return summary && typeof summary.files === 'number' && Array.isArray(summary.groups) ? summary : undefined
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
    if (reply) result.set(reply.id, { snapshotId: data.snapshot_id, workspacePath: data.workspace_path, sessionId: event.session_id || '', summary: summaryOf(data.summary) })
  }
  const newest = (type: string) => {
    const found = new Map<string, { id: string; end: number }>()
    for (const event of events) {
      if (event.type !== type) continue
      const data = event.data?.data as Record<string, unknown> | undefined
      if (!data || typeof data.snapshot_id !== 'string' || typeof data.end_sequence !== 'number') continue
      const session = event.session_id || ''
      if (data.end_sequence > (found.get(session)?.end ?? -1)) found.set(session, { id: data.snapshot_id, end: data.end_sequence })
    }
    return found
  }
  const latest = newest('reply_file_changes'), reviewed = newest('file_reviewed')
  for (const ref of result.values()) {
    ref.latestSnapshotId = latest.get(ref.sessionId)?.id
    ref.reviewedSnapshotId = reviewed.get(ref.sessionId)?.id
  }
  return result
}

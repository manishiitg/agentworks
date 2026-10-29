import type { PollingEvent } from '../services/api-types'
import { normalizeTranscriptChunkEvents } from './transcriptChunkUpdates'

type ReplyFileChange = { path: string; status: string; added?: number; removed?: number; binary?: boolean }
export type ReplyPullRequest = { number?: number; url?: string; state: string; checks?: string; review?: string; failing_checks?: string[]; conflicts?: boolean }
export type ReplyFileGroup = { kind: 'repo' | 'crew_files'; root: string; name: string; branch?: string; github_repo?: string; new_checkout: boolean; files: ReplyFileChange[]; total_files: number; more_count: number; pr?: ReplyPullRequest }
// One card action, decided by the server for the viewer. icon is "octicon:<id>" or a Lucide name;
// prompt is sent to the chat's agent as is (Review opens Review; Add button puts it in the composer).
export type ReplyCardAction = { id: string; kind: 'builtin' | 'git_step' | 'fix' | 'command'; label: string; aria_label?: string; description?: string; icon?: string; placement: 'row' | 'more' | 'pr_line'; prompt?: string; group?: string; editors_only?: boolean }
// Computed by the server when the reply's files were captured; the card renders from it without a request.
// actions are the ones every viewer gets, shown until the card's own request returns this viewer's list.
export type ReplyFileSummary = { files: number; added: number; removed: number; groups: ReplyFileGroup[]; actions?: ReplyCardAction[] }

/** A Crew's own files live under _files/; that folder name is noise on screen. */
export const shownFilePath = (path: string) => path.startsWith('_files/') ? path.slice('_files/'.length) : path

export interface ReplyFileChangesRef {
  snapshotId: string
  workspacePath: string
  sessionId: string
  latestSnapshotId?: string
  summary?: ReplyFileSummary
  // The chat's newest review point (a file_reviewed event), if any.
  reviewedSnapshotId?: string
  // The server's one line when this reply's file changes were not saved (no snapshot).
  unavailable?: string
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
    const unavailable = data?.status === 'unavailable' && typeof data.message === 'string' ? data.message : undefined
    if (!data || (typeof data.snapshot_id !== 'string' && !unavailable) || typeof data.workspace_path !== 'string'
      || typeof data.start_sequence !== 'number' || typeof data.end_sequence !== 'number') continue
    const reply = replies.filter(candidate => candidate.session_id === event.session_id
      && typeof candidate.sequence === 'number' && candidate.sequence > (data.start_sequence as number)
      && candidate.sequence <= (data.end_sequence as number))
      .sort((a, b) => (b.sequence ?? 0) - (a.sequence ?? 0))[0]
    if (reply && unavailable) result.set(reply.id, { snapshotId: '', workspacePath: data.workspace_path, sessionId: event.session_id || '', unavailable })
    else if (reply) result.set(reply.id, { snapshotId: data.snapshot_id as string, workspacePath: data.workspace_path, sessionId: event.session_id || '', summary: summaryOf(data.summary) })
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

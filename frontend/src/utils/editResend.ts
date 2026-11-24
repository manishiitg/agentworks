import api from '../services/api'
import type { PollingEvent } from '../services/api-types'
import type { ReplyFileChangesRef } from './replyFileChanges'

// Edit and resend (#3134): the server takes the chat back to just before one
// of the person's messages; the screen then sends the edited text as usual.

export type RewindPreview = { ok: boolean; later_messages: number; provider: string; native: boolean; /** The chat is copied for the Team; that copy keeps the earlier messages. */ team_copy?: boolean }
export type RewindResult = RewindPreview & { mode?: 'native' | 'fresh' }

const rewindPath = '/api/agent-profiles/work/conversation/rewind'

export async function previewRewind(conversationKey: string, eventId: string): Promise<RewindPreview> {
  const { data } = await api.post<RewindPreview>(rewindPath, { conversation_key: conversationKey, event_id: eventId, dry_run: true })
  return data
}

export async function rewindChat(conversationKey: string, eventId: string): Promise<RewindResult> {
  const { data } = await api.post<RewindResult>(rewindPath, { conversation_key: conversationKey, event_id: eventId })
  return data
}

/** The server's plain-words reason and code, or the fallback. */
export function rewindError(cause: unknown, fallback: string): { message: string; code: string } {
  const data = (cause as { response?: { data?: { error?: unknown } } })?.response?.data
  const error = data?.error
  if (error && typeof error === 'object') {
    const { message, code } = error as { message?: unknown; code?: unknown }
    return { message: typeof message === 'string' && message.trim() ? message : fallback, code: typeof code === 'string' ? code : '' }
  }
  return { message: typeof error === 'string' && error.trim() ? error : fallback, code: '' }
}

/** File changes saved for replies after the message at sequence, newest first. */
export function fileChangesAfter(events: PollingEvent[], sequence: number): ReplyFileChangesRef[] {
  const found: Array<ReplyFileChangesRef & { end: number }> = []
  for (const event of events) {
    if (event.type !== 'reply_file_changes') continue
    const data = event.data?.data as Record<string, unknown> | undefined
    if (!data || typeof data.snapshot_id !== 'string' || !data.snapshot_id || typeof data.workspace_path !== 'string'
      || typeof data.start_sequence !== 'number' || typeof data.end_sequence !== 'number') continue
    if (data.start_sequence < sequence) continue
    found.push({ snapshotId: data.snapshot_id, workspacePath: data.workspace_path, sessionId: event.session_id || '', end: data.end_sequence })
  }
  return found.sort((a, b) => b.end - a.end).map(({ end: _end, ...ref }) => ref)
}

/** Changed file names across those replies, for the checkbox's line. */
export function changedFileNames(events: PollingEvent[], sequence: number): string[] {
  const names = new Set<string>()
  for (const event of events) {
    if (event.type !== 'reply_file_changes') continue
    const data = event.data?.data as Record<string, unknown> | undefined
    if (!data || typeof data.start_sequence !== 'number' || data.start_sequence < sequence) continue
    const groups = (data.summary as { groups?: Array<{ files?: Array<{ path?: string }> }> } | undefined)?.groups ?? []
    for (const group of groups) for (const file of group.files ?? []) if (file.path) names.add(file.path.split('/').pop() || file.path)
  }
  return [...names]
}

type UndoPlan = { restore: unknown[]; skipped: unknown[] }

/** Reverts each reply's saved changes, newest first, with the per-reply undo. */
export async function revertFileChanges(refs: ReplyFileChangesRef[]): Promise<{ restored: number; skipped: number }> {
  let restored = 0
  let skipped = 0
  for (const ref of refs) {
    const { data } = await api.post<{ undo: UndoPlan }>('/api/workspace-snapshots', {}, { params: { workspace_path: ref.workspacePath, snapshot_id: ref.snapshotId, session_id: ref.sessionId, op: 'snapshot_undo' } })
    restored += data.undo?.restore?.length ?? 0
    skipped += data.undo?.skipped?.length ?? 0
  }
  if (refs.length > 0) window.dispatchEvent(new Event('chat-file-review-changed'))
  return { restored, skipped }
}

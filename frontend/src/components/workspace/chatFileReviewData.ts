import { useEffect, useState } from 'react'
import api from '../../services/api'

export type FileChange = { path: string; status: string; added?: number; removed?: number; binary?: boolean }
export type ReviewSummary = { files: number; added: number; removed: number }
export type ReviewHistory = {
  replies: { snapshot_id: string; end_sequence: number }[]
  reviews: { snapshot_id: string; reviewed_at: string }[]
  available: boolean; start_message?: { text: string; at?: string }; last_snapshot_id: string; latest_snapshot_id: string
  baseline_snapshot_id: string; can_review: boolean; note?: string
}
export type ReviewResult = {
  history: ReviewHistory
  comparison?: { uncaptured_changes: number; changes: FileChange[]; diff?: string; truncated?: boolean; snapshot: { status: string }; note: string; skipped_count: number }
  summary?: ReviewSummary; since_review?: ReviewSummary
  total_changes?: number; next_offset?: number
}
export const reviewEndpoint = '/api/workspace-snapshots'
const reviewChanged = 'chat-file-review-changed'
export function useReviewRevision() {
  const [revision, refresh] = useState(0)
  useEffect(() => { const update = () => refresh(value => value + 1); window.addEventListener(reviewChanged, update); return () => window.removeEventListener(reviewChanged, update) }, [])
  return [revision, () => refresh(value => value + 1)] as const
}

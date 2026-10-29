import { useEffect, useState } from 'react'
import { FileDiff } from 'lucide-react'
import api from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { WorkspaceContextRow } from './WorkspaceContextCard'
import { reviewEndpoint, useReviewRevision, type ReviewResult } from './chatFileReviewData'

export function ChatChangesRow({ workspacePath, sessionId, onOpen }: { workspacePath: string; sessionId: string; onOpen: () => void }) {
  const [result, setResult] = useState<ReviewResult | null>(null)
  const [revision] = useReviewRevision()
  const savedEvents = useChatStore(state => (state.tabEvents[sessionId] ?? []).filter(event => event.type === 'reply_file_changes' || event.type === 'file_reviewed').map(event => event.id).join(','))
  useEffect(() => {
    const controller = new AbortController()
    setResult(null)
    void api.get<ReviewResult>(reviewEndpoint, { params: { workspace_path: workspacePath, session_id: sessionId, op: 'snapshot_compare' }, signal: controller.signal })
      .then(({ data }) => { if (!controller.signal.aborted) setResult(data) }).catch(() => undefined)
    return () => controller.abort()
  }, [workspacePath, sessionId, revision, savedEvents])
  return <WorkspaceContextRow icon={FileDiff} onClick={onOpen} detail={result?.summary && <><span>{result.summary.files} files </span><span className="text-diff-add">+{result.summary.added}</span> <span className="text-diff-del">−{result.summary.removed}</span></>}>Changes in this chat</WorkspaceContextRow>
}

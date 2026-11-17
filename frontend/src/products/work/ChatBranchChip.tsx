import { GitBranch } from 'lucide-react'
import { useChatContextView } from './useChatContextView'

/**
 * Names the chat's own Git branch in the chat box's bottom row (#2922), only when its agent started one. The
 * server lists the branches in /api/chat-context; the full list is the hover text.
 */
export default function ChatBranchChip({ sessionId, busy }: { sessionId: string; busy: boolean }) {
  const view = useChatContextView(sessionId, busy)
  const branches = view?.session_id === sessionId ? view.branches ?? [] : []
  if (branches.length === 0) return null
  const full = branches.map(b => `${b.branch} in ${b.repo}`).join(', ')
  return <span data-testid="chat-branch-chip" title={`Working on branch ${full}`} aria-label={`Working on branch ${full}`}
    className="ml-1 inline-flex min-w-0 max-w-44 items-center gap-1 overflow-hidden whitespace-nowrap px-1 py-0.5 text-[11px] text-muted-foreground">
    <GitBranch className="h-3 w-3 shrink-0" aria-hidden="true" />
    <span className="min-w-0 truncate">{branches[0].branch}{branches.length > 1 ? ` +${branches.length - 1}` : ''}</span>
  </span>
}

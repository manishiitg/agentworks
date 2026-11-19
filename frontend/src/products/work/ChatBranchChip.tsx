import * as Menu from '@radix-ui/react-dropdown-menu'
import { GitBranch, GitMerge, Trash2 } from 'lucide-react'
import { useChatContextView } from './useChatContextView'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'

type Branch = { repo: string; branch: string }

export function bringBackMessage(branches: Branch[]) {
  const list = branches.map(b => `${b.branch} in ${b.repo}`).join(', ')
  return `Bring the changes in my own copy back (${list}): commit what is not committed yet there, merge the branch into the branch it started from, and tell me what changed. If it clashes with edits in the shared folder, tell me and suggest how to settle it before changing anything.`
}

export function discardMessage(branches: Branch[]) {
  const list = branches.map(b => `${b.branch} in ${b.repo}`).join(', ')
  return `Discard my own copy (${list}): remove its folder and branch without bringing anything back. The shared folder stays as it is.`
}

/**
 * This chat's own copy of a Git repository (#2922), only when its agent started one. The chip names it in the chat
 * box's bottom row; its menu asks the chat's agent to bring the changes back or to discard the copy. The server lists
 * the branches in /api/chat-context.
 */
export default function ChatBranchChip({ sessionId, tabId, busy }: { sessionId: string; tabId?: string; busy: boolean }) {
  const view = useChatContextView(sessionId, busy)
  const branches = view?.session_id === sessionId ? view.branches ?? [] : []
  if (branches.length === 0) return null
  const full = branches.map(b => `${b.branch} in ${b.repo}`).join(', ')
  const send = (message: string) => { if (tabId) void sendWorkspacePaneMessageToChat({ tabId, message }) }
  const item = 'flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-xs outline-none data-[highlighted]:bg-muted'
  return <Menu.Root modal={false}>
    <Menu.Trigger type="button" data-testid="chat-branch-chip" title={`This chat works in its own copy: ${full}. Other chats don't see its edits until you bring them back.`} aria-label={`Own copy: ${full}`}
      className="ml-1 inline-flex min-w-0 max-w-48 items-center gap-1 overflow-hidden whitespace-nowrap rounded px-1 py-0.5 text-[11px] text-emerald-700 hover:bg-muted dark:text-emerald-400">
      <GitBranch className="h-3 w-3 shrink-0" aria-hidden="true" />
      <span className="min-w-0 truncate">Own copy · {branches[0].branch}{branches.length > 1 ? ` +${branches.length - 1}` : ''}</span>
    </Menu.Trigger>
    <Menu.Portal><Menu.Content side="top" align="start" sideOffset={8} collisionPadding={12} className="z-[100] w-[min(260px,calc(100vw-24px))] rounded-xl border border-border bg-popover p-1.5 text-popover-foreground shadow-lg">
      <p className="px-2 py-1 text-[11px] text-muted-foreground">Edits stay in {full} until you bring them back.</p>
      <Menu.Item disabled={!tabId} className={item} onSelect={() => send(bringBackMessage(branches))}><GitMerge className="h-3.5 w-3.5" aria-hidden="true" />Bring changes back</Menu.Item>
      <Menu.Item disabled={!tabId} className={item} onSelect={() => { if (window.confirm(`Discard ${full}? Its edits are deleted; the shared folder is not touched.`)) send(discardMessage(branches)) }}><Trash2 className="h-3.5 w-3.5" aria-hidden="true" />Discard copy</Menu.Item>
    </Menu.Content></Menu.Portal>
  </Menu.Root>
}

import { useEffect, useRef, useState } from 'react'
import { ChevronDown, Loader2 } from 'lucide-react'
import { gitActionError, workspaceGitApi, type GitRepo } from '../../services/workspaceGit'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { useGitDelivery } from './GitDeliveryControls'
import type { AskAgent } from './GitExtras'
import { gitAgentPrompts } from '../../utils/gitAgentPrompts'

type Step = 'commit' | 'push' | 'pr'
const ACTIONS: Record<string, Step[]> = { Commit: ['commit'], 'Commit and push': ['commit', 'push'], 'Commit, push and open a pull request': ['commit', 'push', 'pr'] }
const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? '' : 's'}`

/** Commit, push and pull request on the Changes tab (owner, 20 Nov). The same
 * server operations as the file list's Git view: commit, push, draft_commit,
 * prepare_pr/open_pr. A step that cannot run says why, from the server's note. */
export function ChangesCommit({ workspacePath, repo, onDone, onAsk }: { workspacePath: string; repo: GitRepo | null; onDone: () => void; onAsk?: AskAgent }) {
  const [message, setMessage] = useState('')
  const [action, setAction] = useState<string | null>(null)
  const [menu, setMenu] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const delivery = useGitDelivery(workspacePath, repo?.root ?? '', repo?.branch ?? '', !!repo, setMessage)
  const menuRef = useRef<HTMLDivElement>(null)
  useEffect(() => { setAction(null); setMessage(''); setError('') }, [repo?.root, repo?.branch])
  useEffect(() => {
    if (!menu) return
    const close = (event: MouseEvent) => { if (!menuRef.current?.contains(event.target as Node)) setMenu(false) }
    window.addEventListener('mousedown', close)
    return () => window.removeEventListener('mousedown', close)
  }, [menu])
  if (!repo) return null

  const files = repo.files
  const staged = repo.files.filter(file => file.index_status)
  const conflicts = repo.files.some(file => file.worktree_status === 'conflict' || file.index_status === 'conflict')
  const canPush = !!delivery.capability?.can_push
  const pushNote = delivery.capability?.note || 'Pushing needs GitHub connected for this project.'
  const commitBlocked = conflicts ? 'Fix the merge conflicts first.' : files.length === 0 ? 'Nothing to commit: every file matches the latest commit.' : ''
  const where = `${repo.root || workspacePath.split('/').filter(Boolean).pop() || 'this project'}${repo.branch ? ` on ${repo.branch}` : ''}`

  const open = (name: string) => {
    setMenu(false); setError(''); setAction(name)
    // The agent suggests a message (one small request); people can edit it.
    if (!message.trim() && delivery.capability?.can_draft) void delivery.draftCommit(staged.length === 0)
  }
  const run = async (steps: Step[]) => {
    const identity = captureChatIdentity()
    setBusy(true); setError('')
    try {
      if (steps.includes('commit')) await workspaceGitApi.act(workspacePath, repo.root, { op: 'commit', message: message.trim(), all: staged.length === 0 })
      if (steps.includes('push')) await workspaceGitApi.act(workspacePath, repo.root, { op: 'push' })
      if (!isChatIdentityCurrent(identity)) return
      setAction(null); setMessage('')
      onDone()
      if (steps.includes('pr')) void delivery.preparePR()
    } catch (cause) {
      if (isChatIdentityCurrent(identity)) { setError(gitActionError(cause)); onDone() }
    } finally { setBusy(false) }
  }

  const item = (label: string, detail: string, onClick: () => void, disabledReason = '') => <button type="button" role="menuitem" disabled={!!disabledReason} onClick={onClick}
    className="block w-full px-3 py-1.5 text-left text-xs hover:bg-muted disabled:cursor-not-allowed disabled:opacity-50">
    {label}<span className="block text-[11px] text-muted-foreground">{disabledReason || detail}</span>
  </button>
  const mainLabel = files.length === 0 && repo.ahead > 0 ? 'Push' : 'Commit'
  const mainDisabled = mainLabel === 'Push' ? !canPush || busy : !!commitBlocked || busy
  const mainTitle = mainLabel === 'Push' ? (canPush ? `Send ${plural(repo.ahead, 'commit')} to GitHub` : pushNote) : commitBlocked || `Save ${plural(files.length, 'file')} in ${where}`

  return <>
    <div ref={menuRef} className="relative ml-auto flex">
      <button type="button" disabled={mainDisabled} title={mainTitle} onClick={() => mainLabel === 'Push' ? void run(['push']) : open('Commit')}
        className="inline-flex items-center gap-1 rounded-l-md bg-primary px-2.5 py-1 text-xs font-medium text-primary-foreground hover:bg-primary/90 disabled:cursor-not-allowed disabled:opacity-50">
        {busy && !action && <Loader2 className="h-3 w-3 animate-spin" />}{mainLabel}
      </button>
      <button type="button" aria-label="More commit actions" aria-haspopup="menu" aria-expanded={menu} onClick={() => setMenu(value => !value)}
        className="rounded-r-md border-l border-primary-foreground/30 bg-primary px-1.5 text-primary-foreground hover:bg-primary/90"><ChevronDown className="h-3.5 w-3.5" /></button>
      {menu && <div role="menu" className="absolute right-0 top-full z-30 mt-1 w-64 overflow-hidden rounded-md border border-border bg-popover py-1 shadow-md">
        {item('Commit', `Save ${plural(files.length, 'file')} in ${where}`, () => open('Commit'), commitBlocked)}
        {item('Commit and push', 'Then send them to GitHub', () => open('Commit and push'), commitBlocked || (canPush ? '' : pushNote))}
        {item('Commit, push and open a pull request', 'Then write the pull request', () => open('Commit, push and open a pull request'), commitBlocked || (canPush ? '' : pushNote))}
        {item('Push', `Send ${plural(repo.ahead, 'commit')} to GitHub`, () => { setMenu(false); void run(['push']) }, !canPush ? pushNote : repo.ahead === 0 ? 'Nothing to push: no commits ahead of GitHub.' : '')}
        {item('Open a pull request', 'For what is already pushed', () => { setMenu(false); void delivery.preparePR() }, canPush ? '' : pushNote)}
        {/* Pull goes through the agent, which can sort out anything it conflicts with. */}
        {onAsk && item('Pull', repo.behind > 0 ? `Get ${plural(repo.behind, 'new commit')} from GitHub` : 'Get the latest from GitHub', () => { setMenu(false); void onAsk(gitAgentPrompts.pull(repo.root)) })}
      </div>}
    </div>
    {(action || error) && <div className="basis-full rounded-md border border-border bg-muted/30 p-2 text-xs">
      {action && <>
        <p className="mb-1.5"><strong>{action}</strong> {plural(files.length, 'file')} in <strong>{where}</strong></p>
        <textarea aria-label="Commit message" value={message} rows={2} disabled={busy || delivery.busy} onChange={event => setMessage(event.target.value)}
          onKeyDown={event => { if ((event.metaKey || event.ctrlKey) && event.key === 'Enter' && message.trim()) { event.preventDefault(); void run(ACTIONS[action]) } }}
          placeholder={delivery.busy ? 'The agent is writing a message…' : 'What changed?'}
          className="block w-full resize-y rounded border border-input bg-background p-1.5 text-[13px] text-foreground placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring" />
        {delivery.capability?.can_draft && <p className="mt-1 text-[11px] text-muted-foreground">Message suggested by the agent. Edit it if you like.</p>}
        <div className="mt-2 flex justify-end gap-2">
          <button type="button" disabled={busy} onClick={() => { setAction(null); setError('') }} className="rounded border border-border px-2.5 py-1 hover:bg-muted">Cancel</button>
          <button type="button" disabled={busy || !message.trim()} onClick={() => void run(ACTIONS[action])} className="inline-flex items-center gap-1 rounded bg-primary px-2.5 py-1 font-medium text-primary-foreground disabled:opacity-50">{busy && <Loader2 className="h-3 w-3 animate-spin" />}{action}</button>
        </div>
      </>}
      {error && <p role="alert" className="mt-1 text-destructive">{error}</p>}
    </div>}
    {delivery.prResult && <div className="basis-full">{delivery.prResult}</div>}
    {delivery.chooser}
  </>
}

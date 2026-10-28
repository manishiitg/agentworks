import { useEffect, useState } from 'react'
import api from '../../services/api'
import { workspaceGitApi, type GitRepo } from '../../services/workspaceGit'
import { useChatStore } from '../../stores/useChatStore'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'
import { captureChatIdentity, assertChatIdentityCurrent } from '../../utils/chatIdentity'
import DiffRenderer, { type DiffLineAnchor } from '../ui/DiffRenderer'
import { SavedFile } from '../chat/ReplyFileChanges'
import { FileDiffView } from './FileDiffView'

type Reply = { snapshot_id: string; end_sequence: number }
type History = { replies: Reply[]; available: boolean; last_snapshot_id: string; note?: string }
type Change = { path: string; status: string; added?: number; removed?: number; binary?: boolean }
type Comparison = { uncaptured_changes: number; changes: Change[]; diff?: string; truncated?: boolean; snapshot: { status: string }; note: string; skipped_count: number }
type Result = { history: History; comparison?: Comparison; file_replies?: string[]; reply_history_incomplete?: boolean; total_changes?: number; next_offset?: number }
const endpoint = '/api/workspace-snapshots'

/** Mounted with an account/server/chat key by its owner. All requests and sends
 * stay with that chat; the server resolves and authorizes its saved boundaries. */
export function ChatFileReview({ workspacePath, sessionId, tabId, onBrowse }: {
  workspacePath: string; sessionId: string; tabId: string; onBrowse: () => void
}) {
  const [scope, setScope] = useState('chat')
  const [result, setResult] = useState<Result | null>(null)
  const [file, setFile] = useState('')
  const [diff, setDiff] = useState<Result | null>(null)
  const [repos, setRepos] = useState<GitRepo[]>([])
  const [repo, setRepo] = useState('')
  const [revision, refresh] = useState(0)
  const [error, setError] = useState('')
  const [anchor, setAnchor] = useState<DiffLineAnchor | null>(null)
  const [comment, setComment] = useState('')
  const [sending, setSending] = useState(false)
  const [sent, setSent] = useState(false)
  const [preview, setPreview] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  const snapshotId = scope === 'chat' || scope === 'uncommitted' ? '' : scope
  const params = { workspace_path: workspacePath, session_id: sessionId, snapshot_id: snapshotId }
  useEffect(() => {
    const controller = new AbortController()
    setResult(null); setDiff(null); setFile(''); setAnchor(null); setError(''); setPreview(false)
    if (scope === 'uncommitted') return () => controller.abort()
    void api.get<Result>(endpoint, { params: { workspace_path: workspacePath, session_id: sessionId, snapshot_id: snapshotId, op: 'snapshot_compare' }, signal: controller.signal })
      .then(({ data }) => { if (!controller.signal.aborted) setResult(data) })
      .catch(() => { if (!controller.signal.aborted) setError('Could not load this saved comparison. Refresh or choose another reply.') })
    return () => controller.abort()
  }, [workspacePath, sessionId, scope, snapshotId, revision])
  useEffect(() => {
    let active = true
    void workspaceGitApi.status(workspacePath).then(value => {
      if (active) { setRepos(value); setRepo(current => value.some(item => item.root === current) ? current : value[0]?.root ?? '') }
    }).catch(() => { if (active) setRepos([]) })
    return () => { active = false }
  }, [workspacePath, revision])
  const through = result?.history.last_snapshot_id
  useEffect(() => {
    const controller = new AbortController()
    setDiff(null); setAnchor(null); setPreview(false); setSent(false)
    if (!file || scope === 'uncommitted' || !through) return () => controller.abort()
    void api.get<Result>(endpoint, { params: { workspace_path: workspacePath, session_id: sessionId, snapshot_id: snapshotId, through_snapshot_id: through, op: 'snapshot_compare', file }, signal: controller.signal })
      .then(({ data }) => { if (!controller.signal.aborted) setDiff(data) })
      .catch(() => { if (!controller.signal.aborted) setError('This saved file is unavailable. Refresh the comparison.') })
    return () => controller.abort()
  }, [file, scope, snapshotId, through, workspacePath, sessionId])
  // Preserve the list while viewing Uncommitted, without using it as a baseline.
  const [replies, setReplies] = useState<Reply[]>([])
  useEffect(() => { if (result?.history.replies) setReplies(result.history.replies) }, [result])
  const loadMore = async () => {
    if (!result?.next_offset || !result.comparison) return
    const shown = result
    setLoadingMore(true)
    try {
      const { data } = await api.get<Result>(endpoint, { params: { ...params, op: 'snapshot_compare', through_snapshot_id: shown.history.last_snapshot_id, offset: shown.next_offset } })
      setResult(current => current === shown && data.comparison ? { ...data, comparison: { ...data.comparison, changes: [...shown.comparison!.changes, ...data.comparison.changes] } } : current)
    } catch { setError('Could not load more files. Refresh and try again.') }
    finally { setLoadingMore(false) }
  }
  const currentRepo = repos.find(item => item.root === repo)
  const changes: Change[] = scope === 'uncommitted'
    ? (currentRepo?.files.filter(item => item.status !== 'untracked').map(item => ({ path: item.path, status: item.status })) ?? [])
    : result?.comparison?.changes ?? []
  const selected = changes.find(change => change.path === file)
  const send = async (looksGood = false) => {
    const identity = captureChatIdentity()
    setSending(true); setError(''); setSent(false)
    try {
      let message = 'Looks good.'
      if (!looksGood) {
        if (!anchor || !comment.trim() || !through) return
        const { data } = await api.get<{ context: string }>(endpoint, { params: {
          ...params, through_snapshot_id: through, op: 'snapshot_compare', file, side: anchor.side, line: anchor.line,
        } })
        if (!data.context) throw new Error('This saved line is no longer available. Refresh the comparison.')
        message = `${data.context}\n\n${comment.trim()}`
      }
      assertChatIdentityCurrent(identity)
      if (useChatStore.getState().chatTabs[tabId]?.sessionId !== sessionId) throw new Error('This chat changed. Reopen Review changes before sending.')
      await sendWorkspacePaneMessageToChat({ tabId, message })
      setComment(''); setAnchor(null); setSent(true)
    } catch (cause) { setError(cause instanceof Error ? cause.message : 'Could not send to this chat.') }
    finally { setSending(false) }
  }
  return <section className="flex h-full min-h-0 flex-col" aria-label="Review changes">
    <header className="flex flex-wrap items-center gap-2 border-b border-border p-3">
      <strong className="mr-auto">Review changes</strong>
      <button type="button" className="text-xs underline" onClick={() => refresh(value => value + 1)}>Refresh</button>
      <button type="button" className="text-xs underline" onClick={onBrowse}>Browse files</button>
      <label className="flex w-full items-center gap-2 text-xs">Compare
        <select aria-label="Review scope" className="min-w-0 rounded border bg-background p-1" value={scope} onChange={event => { setScope(event.target.value); setFile('') }}>
          <option value="chat">Whole chat</option>
          {replies.map((reply, index) => <option key={reply.snapshot_id} value={reply.snapshot_id}>Saved reply {index + 1}</option>)}
          {repos.length > 0 && <option value="uncommitted">Uncommitted</option>}
        </select>
        {scope === 'uncommitted' && <select aria-label="Repository" value={repo} onChange={event => { setRepo(event.target.value); setFile('') }}>{repos.map(item => <option key={item.root} value={item.root}>{item.root || 'Workspace'}</option>)}</select>}
      </label>
    </header>
    {error && <p role="alert" className="p-3 text-sm text-destructive">{error}</p>}
    <div className="min-h-0 flex-1 overflow-auto">
      {scope !== 'uncommitted' && !result && !error && <p className="p-3 text-sm">Loading saved comparison…</p>}
      {scope !== 'uncommitted' && result && !result.comparison && <p className="p-3 text-sm">{result.history.note}</p>}
      {(result?.comparison?.uncaptured_changes ?? 0) > 0 && <p role="status" className="p-3 text-xs text-muted-foreground">{result?.comparison?.uncaptured_changes} {result?.comparison?.uncaptured_changes === 1 ? 'file may' : 'files may'} have changed but could not be compared.</p>}
      <div className="grid gap-1 border-b border-border p-2">
        {scope !== 'uncommitted' && result?.comparison && <p className="p-2 text-xs text-muted-foreground">{result.total_changes ?? changes.length} changed files</p>}
        {changes.map(change => <button type="button" key={change.path} aria-pressed={file === change.path} className={`flex gap-2 rounded px-2 py-1 text-left text-xs ${file === change.path ? 'bg-muted' : 'hover:bg-muted/50'}`} onClick={() => { setFile(change.path); setError('') }}>
          <span className="min-w-0 flex-1 break-all">{change.path}</span><span>{change.binary ? 'Binary' : change.added !== undefined ? `+${change.added} −${change.removed ?? 0}` : change.status}</span>
        </button>)}
        {!!result?.next_offset && scope !== 'uncommitted' && <button type="button" disabled={loadingMore} className="p-2 text-xs underline" onClick={() => void loadMore()}>{loadingMore ? 'Loading…' : 'Show more files'}</button>}
        {(result?.comparison || scope === 'uncommitted') && changes.length === 0 && <p className="p-2 text-sm">{scope === 'uncommitted' ? 'No tracked changes against HEAD. New untracked files have no diff yet.' : 'No net changes in the captured files.'}</p>}
      </div>
      {selected && <div className="p-2">
        <h3 className="mb-2 break-all text-sm font-medium">{file}</h3>
        {scope === 'uncommitted' ? <FileDiffView key={`${repo}:${file}:${revision}`} workspacePath={workspacePath} file={file} repositoryFile={{ repo, file }} />
          : !diff ? <p className="text-sm">Loading file…</p>
          : !diff.comparison ? <p className="text-sm">{diff.history.note}</p>
          : selected.binary ? <button type="button" className="rounded border px-2 py-1 text-sm" onClick={() => setPreview(true)}>Open saved version</button>
          : <><p className="mb-2 text-xs text-muted-foreground">Select a line number to ask for a change.</p><DiffRenderer content={diff.comparison.diff || ''} onSelectLine={setAnchor} maxHeightClassName="max-h-none" />{diff.comparison.truncated && <p className="text-xs">Only the start of this large comparison is shown.</p>}</>}
        {scope !== 'uncommitted' && !!diff?.file_replies?.length && <div className="mt-3 flex flex-wrap gap-2 text-xs"><span>Observed in:</span>{diff.file_replies.map(id => <button type="button" key={id} className="underline" onClick={() => setScope(id)}>Saved reply {replies.findIndex(reply => reply.snapshot_id === id) + 1}</button>)}</div>}
        {diff?.reply_history_incomplete && <p className="mt-2 text-xs text-muted-foreground">Some older reply comparisons are no longer retained.</p>}
        {preview && through && <SavedFile reference={{ workspacePath, sessionId, snapshotId }} query={{ session_id: sessionId, through_snapshot_id: through }} change={selected} view="preview" close={() => setPreview(false)} />}
      </div>}
      {anchor && <div className="space-y-2 border-t p-3"><p className="text-xs">{file} · {anchor.side} line {anchor.line}</p><textarea aria-label="Request a change" className="min-h-20 w-full rounded border bg-background p-2 text-sm" value={comment} onChange={event => setComment(event.target.value)} placeholder="What should change here?" /><button type="button" disabled={sending || !comment.trim()} className="rounded border px-3 py-1 text-sm disabled:opacity-50" onClick={() => void send()}>Send to this chat</button></div>}
      <p className="p-3 text-xs text-muted-foreground">{scope === 'uncommitted' ? 'Current tracked files against HEAD, including staged and unstaged changes.' : result?.comparison?.note}</p>
    </div>
    <footer className="flex items-center gap-2 border-t p-3 text-xs"><span className="mr-auto">{sent ? 'Sent to this chat.' : 'Posts a message in this chat.'}</span><button type="button" disabled={sending || !changes.length} className="rounded border px-3 py-1 disabled:opacity-50" onClick={() => void send(true)}>Looks good</button></footer>
  </section>
}

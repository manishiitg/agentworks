import { useEffect, useState, type ReactNode } from 'react'
import { FolderOpen, RefreshCw } from 'lucide-react'
import { WorkspaceViewHeader } from '../workflow/WorkspaceViewHeader'
import { ReplyFileViewer } from './ReplyFileViewer'
import { workspaceGitApi, type GitBranchCompare, type GitChangedFile, type GitRepo } from '../../services/workspaceGit'

type Scope = 'uncommitted' | 'branch' | 'chat'
type Row = { repo: string; path: string; label: string; added?: number; removed?: number; binary?: boolean }

const iconButton = 'rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
const select = 'max-w-44 rounded border border-border bg-background px-1.5 py-0.5 text-xs text-foreground'
const uncommittedLabel: Record<GitChangedFile['status'], string> = { untracked: 'New', added: 'Added', deleted: 'Deleted', renamed: 'Renamed', conflict: 'Conflict', modified: '' }
const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? '' : 's'}`
const scopeLabel: Record<Scope, string> = { uncommitted: 'Uncommitted', branch: 'Branch / PR', chat: 'This chat' }

/** Git review of a Crew folder (PLAT-746-200). Uncommitted: working files vs the
 * latest commit. Branch / PR: what the branch adds on top of its base, as a pull
 * request shows it. The server picks the base and pins both commits; this view
 * only shows what it returns. When the Crew folder itself is not a repository,
 * "This chat" shows the chat's saved-copy changes (renderChat), so files outside
 * the repositories are not hidden. */
export function GitReview({ workspacePath, sessionId, tabId, repos, onRefresh, onBrowse, renderChat }: {
  workspacePath: string; sessionId: string; tabId: string; repos: GitRepo[]; onRefresh: () => void; onBrowse: () => void
  renderChat?: (bar: ReactNode) => ReactNode
}) {
  const [scope, setScope] = useState<Scope>('uncommitted')
  const [repoRoot, setRepoRoot] = useState(repos[0].root)
  const [base, setBase] = useState('')
  const [branches, setBranches] = useState<string[]>([])
  const [compare, setCompare] = useState<GitBranchCompare | null>(null)
  const [selected, setSelected] = useState<{ repo: string; path: string } | null>(null)
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)
  const folder = workspacePath.split('/').filter(Boolean).pop() || 'Project'
  const repoName = (root: string) => root || folder
  const repo = repos.find(item => item.root === repoRoot) ?? repos[0]

  useEffect(() => {
    if (scope !== 'branch') return
    let live = true
    setCompare(null); setError('')
    void workspaceGitApi.branchCompare(workspacePath, repo.root, base)
      .then(data => { if (live) setCompare(data) })
      .catch(() => { if (live) setError('Could not compare this branch. Refresh and try again.') })
    void workspaceGitApi.branches(workspacePath, repo.root)
      .then(list => { if (live) setBranches(list.filter(item => !item.current).map(item => item.name)) })
      .catch(() => { if (live) setBranches([]) })
    return () => { live = false }
  }, [scope, workspacePath, repo.root, base, revision])

  const rows: Row[] = scope === 'uncommitted'
    ? repos.flatMap(item => item.files.map(file => ({ repo: item.root, path: file.path, label: file.path.endsWith('/') ? 'New folder' : uncommittedLabel[file.status] ?? '' })))
    : (compare?.files ?? []).map(file => ({ repo: repo.root, path: file.path, label: file.status === 'added' ? 'Added' : file.status === 'deleted' ? 'Deleted' : '', added: file.added, removed: file.removed, binary: file.binary }))
  const current = rows.find(row => row.repo === selected?.repo && row.path === selected?.path) ?? rows[0]
  const baseSha = compare?.base_sha, headSha = compare?.head_sha

  const refresh = () => { setError(''); onRefresh(); setRevision(value => value + 1) }
  const repoLabel = (root: string) => root || 'the Crew folder'
  // Changes for the selected file, and how a clicked line is named in the
  // message to this chat (repo, file, side, line and which comparison).
  const diffSource = current && !current.path.endsWith('/') ? {
    load: () => scope === 'branch' && baseSha && headSha
      ? workspaceGitApi.branchDiff(workspacePath, current.repo, current.path, baseSha, headSha)
      : workspaceGitApi.diff(workspacePath, current.repo, current.path),
    lineContext: ({ side, line }: { side: string; line: number }) => `In ${repoLabel(current.repo)}, file ${current.path}, ${side} line ${line} (${scope === 'branch' && baseSha && headSha
      ? `branch ${compare?.branch || 'this version'} compared with ${compare?.base}, ${baseSha.slice(0, 7)}..${headSha.slice(0, 7)}`
      : 'changes not committed yet, compared with the latest commit'}):`,
  } : undefined
  const uncommittedCount = repos.reduce((sum, item) => sum + item.files.length, 0)
  const note = scope === 'uncommitted'
    ? uncommittedCount === 0 ? 'Nothing to commit: every file matches the latest commit.'
      : `${repos.length === 1 ? `${repos[0].branch || 'This version'} · ` : ''}${plural(uncommittedCount, 'file')} not committed yet (staged, unstaged and new)`
    : !compare ? '' : compare.note || [
      `${compare.branch || 'This version'} → ${compare.base}`,
      compare.pr ? `PR #${compare.pr.number}` : '',
      compare.unpushed ? `${plural(compare.unpushed, 'commit')} not pushed yet` : '',
    ].filter(Boolean).join(' · ')
  const baseOptions = Array.from(new Set([compare?.base ?? '', ...branches].filter(Boolean)))
  const grouped = scope === 'uncommitted' && repos.length > 1
  const scopeSwitch = <div role="group" aria-label="What to review" className="flex overflow-hidden rounded border border-border text-xs">
    {(renderChat ? ['uncommitted', 'branch', 'chat'] as const : ['uncommitted', 'branch'] as const).map(value => <button type="button" key={value} aria-pressed={scope === value} onClick={() => { setScope(value); setSelected(null) }}
      className={`px-2 py-0.5 ${scope === value ? 'bg-muted text-foreground' : 'text-muted-foreground hover:text-foreground'}`}>{scopeLabel[value]}</button>)}
  </div>
  if (scope === 'chat' && renderChat) return renderChat(<div className="flex flex-wrap items-center gap-2 border-b border-border px-3 py-2">{scopeSwitch}</div>)

  return <section className="flex h-full min-h-0 flex-col" aria-label="Review changes">
    <WorkspaceViewHeader title="Files" subtitle="Review changes" actions={<>
      <button type="button" aria-label="Refresh changes" title="Refresh" onClick={refresh} className={iconButton}><RefreshCw className="h-3.5 w-3.5" aria-hidden="true" /></button>
      <button type="button" aria-label="Browse files" title="Browse files" onClick={onBrowse} className={iconButton}><FolderOpen className="h-3.5 w-3.5" aria-hidden="true" /></button>
    </>} />
    <div className="flex flex-wrap items-center gap-2 border-b border-border px-3 py-2">
      {scopeSwitch}
      {scope === 'branch' && <>
        {repos.length > 1 && <select aria-label="Project" value={repo.root} className={select} onChange={event => { setRepoRoot(event.target.value); setBase(''); setSelected(null) }}>
          {repos.map(item => <option key={item.root} value={item.root}>{repoName(item.root)}</option>)}
        </select>}
        <span className="flex-1" />
        {baseOptions.length > 0 && <label className="flex items-center gap-1 text-xs text-muted-foreground">Compare with
          <select aria-label="Compare with" value={compare?.base ?? base} className={select} onChange={event => { setBase(event.target.value); setSelected(null) }}>
            {!(compare?.base ?? base) && <option value="">Choose a branch</option>}
            {baseOptions.map(name => <option key={name} value={name}>{name}</option>)}
          </select>
        </label>}
      </>}
    </div>
    {note && <p className="border-b border-border px-3 py-1.5 text-xs text-muted-foreground">{note}</p>}
    {error && <p role="alert" className="p-3 text-sm text-destructive">{error}</p>}
    <div className="max-h-[35%] shrink-0 overflow-auto border-b border-border p-2 sm:max-h-56">
      {scope === 'branch' && !compare && !error && <p className="p-2 text-sm">Loading…</p>}
      {(scope === 'uncommitted' ? repos.map(item => item.root) : [repo.root]).map(root => {
        const items = rows.filter(row => row.repo === root)
        if (!items.length) return null
        return <div key={root} className="grid gap-0.5">
          {grouped && <p className="px-2 pt-1 text-[11px] uppercase tracking-wide text-muted-foreground">{repoName(root)}{repos.find(item => item.root === root)?.branch ? ` · ${repos.find(item => item.root === root)?.branch}` : ''}</p>}
          {items.map(row => <button type="button" key={`${row.repo}:${row.path}`} aria-pressed={current === row} onClick={() => setSelected({ repo: row.repo, path: row.path })}
            className={`flex gap-2 rounded px-2 py-1 text-left text-xs hover:bg-muted/50 ${current === row ? 'bg-muted font-medium' : ''}`}>
            <span className="min-w-0 flex-1 break-all">{row.path}</span>
            {row.label && <span className="text-muted-foreground">{row.label}</span>}
            {row.binary ? <span className="text-muted-foreground">Binary</span> : row.added !== undefined && <><span className="text-diff-add">+{row.added}</span><span className="text-diff-del">−{row.removed}</span></>}
          </button>)}
        </div>
      })}
      {compare?.truncated && <p className="p-2 text-xs text-muted-foreground">Only the first files are listed.</p>}
    </div>
    {current && <div className="min-h-0 flex-1 border-t border-border">
      {diffSource ? <ReplyFileViewer key={`${scope}:${current.repo}:${current.path}:${baseSha}:${headSha}:${revision}`} embedded diffSource={diffSource} tabId={tabId} onBack={onBrowse}
        selection={{ reference: { workspacePath, sessionId, snapshotId: '' }, change: { path: current.repo ? `${current.repo}/${current.path}` : current.path, status: current.label, binary: current.binary } }} />
        : <p className="p-3 text-sm">A new folder. Open it with Browse files to see what is inside.</p>}
    </div>}
  </section>
}

import { useEffect, useState, type ReactNode } from 'react'
import { ArrowLeft, ArrowDown, ArrowUp, Check, ChevronDown, ChevronRight, FileText, Files, GitBranch, GitCommit, Loader2, Minus, Plus, RefreshCw, Sparkles, Undo2 } from 'lucide-react'
import DiffRenderer from '../ui/DiffRenderer'
import { gitActionError, workspaceGitApi, type GitChangedFile, type GitCommit as GitCommitInfo, type GitRepo } from '../../services/workspaceGit'
import { gitFullPath, useWorkspaceGitStore, type GitPanel } from '../../stores/useWorkspaceGitStore'
import { openWorkspaceFile } from '../../utils/openWorkspaceFile'
import { FileTypeIcon } from './fileTypeIcon'
import { BranchPicker, ConflictSection, GitBlamePanel, StashSection, type AskAgent, type RunAction } from './GitExtras'
import { gitAgentPrompts } from '../../utils/gitAgentPrompts'

const STATUS_STYLE = {
  modified: { letter: 'M', text: 'text-amber-500', title: 'Modified' },
  added: { letter: 'A', text: 'text-emerald-500', title: 'Added' },
  untracked: { letter: 'U', text: 'text-emerald-500', title: 'Untracked' },
  deleted: { letter: 'D', text: 'text-destructive', title: 'Deleted' },
  renamed: { letter: 'R', text: 'text-sky-500', title: 'Renamed' },
  conflict: { letter: '!', text: 'text-destructive', title: 'Merge conflict' },
} as const

function repoLabel(repo: GitRepo): string {
  return repo.root || 'workspace'
}

function BranchChip({ repo, showName }: { repo: GitRepo; showName: boolean }) {
  return (
    <span className="inline-flex min-w-0 items-center gap-1 text-muted-foreground" title={repo.upstream ? `Tracking ${repo.upstream}` : 'No upstream branch'}>
      <GitBranch aria-hidden="true" className="h-3.5 w-3.5 shrink-0" />
      {showName && <span className="truncate">{repoLabel(repo)}:</span>}
      <span className="truncate font-medium text-foreground">{repo.detached ? 'detached HEAD' : repo.branch}</span>
      {repo.ahead > 0 && <span className="inline-flex items-center text-emerald-500" title={`${repo.ahead} commit${repo.ahead === 1 ? '' : 's'} to push`}><ArrowUp className="h-3 w-3" />{repo.ahead}</span>}
      {repo.behind > 0 && <span className="inline-flex items-center text-amber-500" title={`${repo.behind} commit${repo.behind === 1 ? '' : 's'} to pull`}><ArrowDown className="h-3 w-3" />{repo.behind}</span>}
    </span>
  )
}

/** Branch strip above the tree (VS Code's status-bar branch); the rail switches to Source Control. */
export function GitBar() {
  const repos = useWorkspaceGitStore(state => state.repos)
  if (repos.length === 0) return null
  return (
    <div className="flex h-8 shrink-0 items-center gap-2 border-b border-border bg-muted/30 px-2 text-xs">
      <div className="flex min-w-0 flex-1 items-center gap-x-3 overflow-hidden">
        {repos.slice(0, 2).map(repo => <BranchChip key={repo.root} repo={repo} showName={repos.length > 1} />)}
        {repos.length > 2 && <span className="text-muted-foreground">+{repos.length - 2}</span>}
      </div>
    </div>
  )
}

/** VS Code-style activity rail: Explorer and Source Control (with a change-count badge). */
export function ActivityRail({ view, onChange }: { view: 'files' | 'scm'; onChange: (view: 'files' | 'scm') => void }) {
  const changed = useWorkspaceGitStore(state => state.repos.reduce((sum, repo) => sum + repo.files.length, 0))
  const item = (id: 'files' | 'scm', label: string, icon: ReactNode, badge?: number) => (
    <button
      key={id}
      type="button"
      role="tab"
      aria-selected={view === id}
      aria-label={label}
      title={label}
      onClick={() => onChange(id)}
      className={`relative flex h-10 w-full items-center justify-center border-l-2 ${view === id ? 'border-primary text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}`}
    >
      {icon}
      {badge ? <span className="absolute bottom-1 right-1 min-w-4 rounded-full bg-primary px-1 text-[10px] font-semibold leading-4 text-primary-foreground">{badge > 99 ? '99+' : badge}</span> : null}
    </button>
  )
  return (
    <div role="tablist" aria-label="Views" aria-orientation="vertical" className="flex w-10 shrink-0 flex-col border-r border-border bg-muted/30">
      {item('files', 'Explorer', <Files className="h-5 w-5" />)}
      {item('scm', 'Source Control', <GitBranch className="h-5 w-5" />, changed)}
    </div>
  )
}

function FileRow({ repo, file, staged, workspacePath, busy, run }: {
  repo: GitRepo
  file: GitChangedFile
  staged: boolean
  workspacePath: string
  busy: boolean
  run: RunAction
}) {
  const openPanel = useWorkspaceGitStore(state => state.openPanel)
  const activePanel = useWorkspaceGitStore(state => state.panel)
  const [confirming, setConfirming] = useState(false)
  const status = (staged ? file.index_status : file.worktree_status) ?? file.status
  const style = STATUS_STYLE[status]
  const bare = file.path.replace(/\/$/, '')
  const base = bare.split('/').pop() || bare
  const dir = bare.includes('/') ? bare.slice(0, bare.lastIndexOf('/')) : ''
  const isFolder = file.path.endsWith('/')
  const active = activePanel?.kind === 'diff' && activePanel.repo === repo.root && activePanel.file === bare
  const full = gitFullPath(workspacePath, repo.root, bare)
  const iconButton = 'rounded p-1 text-muted-foreground hover:bg-background hover:text-foreground disabled:opacity-40'

  if (confirming) {
    return (
      <div className="flex items-center gap-2 bg-destructive/10 px-3 py-1.5 text-xs">
        <span className="min-w-0 flex-1 truncate text-foreground">Discard changes to <strong>{base}</strong>? This cannot be undone.</span>
        <button type="button" disabled={busy} onClick={() => { setConfirming(false); void run(repo, { op: 'discard', files: [file.path] }) }} className="rounded bg-destructive px-2 py-0.5 font-medium text-destructive-foreground">Discard</button>
        <button type="button" onClick={() => setConfirming(false)} className="rounded border border-border px-2 py-0.5">Cancel</button>
      </div>
    )
  }
  return (
    <div className={`group/row flex h-7 items-center gap-1.5 px-3 hover:bg-muted ${active ? 'bg-primary/15' : ''}`}>
      <button
        type="button"
        title={full}
        onClick={() => { if (!isFolder) openPanel({ kind: 'diff', repo: repo.root, file: bare }) }}
        className="flex min-w-0 flex-1 items-center gap-1.5 text-left text-[13px]"
      >
        <FileTypeIcon name={base} folder={isFolder} />
        <span className={`truncate ${status === 'deleted' ? 'text-muted-foreground line-through' : 'text-foreground'}`}>{base}</span>
        {dir && <span className="truncate text-xs text-muted-foreground">{dir}</span>}
      </button>
      <span className="hidden shrink-0 items-center group-hover/row:flex group-focus-within/row:flex">
        {!isFolder && status !== 'deleted' && (
          <button type="button" title="Open file" aria-label={`Open ${base}`} onClick={() => { void openWorkspaceFile(full) }} className={iconButton}><FileText className="h-3.5 w-3.5" /></button>
        )}
        {!staged && (
          <button type="button" disabled={busy} title={status === 'untracked' ? 'Delete file' : 'Discard changes'} aria-label={`Discard ${base}`} onClick={() => setConfirming(true)} className={iconButton}><Undo2 className="h-3.5 w-3.5" /></button>
        )}
        <button
          type="button"
          disabled={busy}
          title={staged ? 'Unstage' : 'Stage'}
          aria-label={`${staged ? 'Unstage' : 'Stage'} ${base}`}
          onClick={() => { void run(repo, { op: staged ? 'unstage' : 'stage', files: [file.path] }) }}
          className={iconButton}
        >
          {staged ? <Minus className="h-3.5 w-3.5" /> : <Plus className="h-3.5 w-3.5" />}
        </button>
      </span>
      <span className={`w-3 shrink-0 text-center text-[11px] font-semibold ${style.text}`} title={style.title}>{style.letter}</span>
    </div>
  )
}

function Section({ title, count, action, children }: { title: string; count: number; action?: ReactNode; children: ReactNode }) {
  const [open, setOpen] = useState(true)
  if (count === 0) return null
  return (
    <div>
      <div className="group/section flex h-7 items-center gap-1 px-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
        <button type="button" onClick={() => setOpen(value => !value)} aria-expanded={open} className="flex min-w-0 flex-1 items-center gap-1 text-left">
          {open ? <ChevronDown className="h-3.5 w-3.5 shrink-0" /> : <ChevronRight className="h-3.5 w-3.5 shrink-0" />}
          <span className="truncate">{title}</span>
          <span className="rounded-full bg-muted px-1.5 text-[10px] font-medium normal-case leading-4 text-foreground">{count}</span>
        </button>
        <span className="hidden group-hover/section:flex group-focus-within/section:flex">{action}</span>
      </div>
      {open && children}
    </div>
  )
}

function refBadge(ref: string): { label: string; className: string } | null {
  const name = ref.replace(/^HEAD -> /, '')
  if (ref.startsWith('tag: ')) return { label: ref.slice(5), className: 'bg-amber-500/15 text-amber-600 dark:text-amber-300' }
  if (name.startsWith('origin/') || name.includes('/')) return { label: name, className: 'bg-violet-500/20 text-violet-600 dark:text-violet-300' }
  return { label: name, className: ref.startsWith('HEAD -> ') ? 'bg-primary/20 text-primary' : 'bg-muted text-foreground' }
}

/** Recent commits of the repo, newest first, with branch and tag badges. */
function GitGraph({ workspacePath, repo, reloadKey }: { workspacePath: string; repo: GitRepo; reloadKey: number }) {
  const commits = useAsync(() => workspaceGitApi.log(workspacePath, repo.root), [workspacePath, repo.root, repo.branch, repo.ahead, repo.behind, reloadKey])
  const list = commits.value ?? []
  return (
    <Section title="Graph" count={list.length}>
      {commits.loading && list.length === 0 ? <Loading />
        : commits.error ? <p className="px-3 py-1 text-xs text-destructive">{commits.error}</p>
          : list.length === 0 ? <p className="px-3 py-1 text-xs text-muted-foreground">No commits yet.</p>
            : (
              <ol className="pb-1">
                {list.map((entry, index) => {
                  const badges = (entry.refs ?? []).map(refBadge).filter((badge): badge is NonNullable<typeof badge> => badge !== null).slice(0, 3)
                  return (
                    <li key={entry.hash} title={`${entry.subject}\n${entry.author} · ${new Date(entry.date).toLocaleString()} · ${entry.hash.slice(0, 7)}`} className="relative flex min-h-7 items-center gap-2 py-0.5 pl-4 pr-2 hover:bg-muted">
                      <span aria-hidden="true" className={`absolute left-[1.05rem] w-px bg-border ${index === 0 ? 'top-1/2' : 'top-0'} ${index === list.length - 1 ? 'bottom-1/2' : 'bottom-0'}`} />
                      <span aria-hidden="true" className={`relative z-10 h-2.5 w-2.5 shrink-0 rounded-full border-2 ${index === 0 ? 'border-primary bg-primary' : 'border-violet-400 bg-background'}`} />
                      <span className="min-w-0 flex-1 truncate text-[13px] text-foreground">{entry.subject}</span>
                      {badges.map(badge => <span key={badge.label} className={`shrink-0 truncate rounded-full px-2 py-0.5 text-[11px] font-medium ${badge.className}`}>{badge.label}</span>)}
                    </li>
                  )
                })}
              </ol>
            )}
    </Section>
  )
}

function RepoSection({ repo, workspacePath, showName, onAsk, run, busy, error, reloadKey }: {
  repo: GitRepo
  workspacePath: string
  showName: boolean
  onAsk?: AskAgent
  run: RunAction
  busy: boolean
  error: string | null
  /** Bumped by the view's refresh so the graph and stashes reload with the status. */
  reloadKey: number
}) {
  const [message, setMessage] = useState('')
  const [menuOpen, setMenuOpen] = useState(false)
  const [graphKey, setGraphKey] = useState(0)
  const staged = repo.files.filter(file => file.index_status)
  const unstaged = repo.files.filter(file => file.worktree_status && file.worktree_status !== 'conflict')
  const canCommit = message.trim() !== '' && (staged.length > 0 || unstaged.length > 0) && !busy
  const commit = async (options: { all?: boolean; push?: boolean } = {}) => {
    if (!canCommit) return
    setMenuOpen(false)
    const all = options.all ?? staged.length === 0
    if (await run(repo, { op: 'commit', message, all })) {
      setMessage('')
      setGraphKey(key => key + 1)
      if (options.push && onAsk) void onAsk(gitAgentPrompts.push(repo.root))
    }
  }
  const headerButton = 'inline-flex items-center gap-1 rounded px-1.5 py-1 text-xs text-muted-foreground hover:bg-muted hover:text-foreground'
  const menuItem = 'flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-foreground hover:bg-muted disabled:opacity-50'
  return (
    <section className="border-b border-border pb-1">
      <div className="flex h-9 items-center gap-1 px-2 text-xs">
        <div className="flex min-w-0 flex-1 items-center gap-1.5">
          {showName && <span className="truncate text-muted-foreground">{repoLabel(repo)}:</span>}
          <BranchPicker workspacePath={workspacePath} repo={repo} run={run} busy={busy} />
          {repo.ahead > 0 && <span className="inline-flex items-center text-emerald-500" title={`${repo.ahead} to push`}><ArrowUp className="h-3 w-3" />{repo.ahead}</span>}
          {repo.behind > 0 && <span className="inline-flex items-center text-amber-500" title={`${repo.behind} to pull`}><ArrowDown className="h-3 w-3" />{repo.behind}</span>}
        </div>
        {onAsk && (
          <>
            <button type="button" title="Ask the agent to pull" aria-label="Pull" onClick={() => { void onAsk(gitAgentPrompts.pull(repo.root)) }} className={headerButton}><ArrowDown className="h-3.5 w-3.5" />{repo.behind > 0 ? repo.behind : ''}</button>
            <button type="button" title="Ask the agent to push" aria-label="Push" onClick={() => { void onAsk(gitAgentPrompts.push(repo.root)) }} className={headerButton}><ArrowUp className="h-3.5 w-3.5" />{repo.ahead > 0 ? repo.ahead : ''}</button>
          </>
        )}
      </div>
      <div className="px-2 pb-2">
        <div className="relative">
          <textarea
            value={message}
            onChange={event => setMessage(event.target.value)}
            onKeyDown={event => { if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') { event.preventDefault(); void commit() } }}
            placeholder="Message (Ctrl+Enter to commit)"
            aria-label="Commit message"
            rows={2}
            className={`block w-full resize-y rounded-md border border-input bg-muted/40 py-1.5 pl-2 text-[13px] text-foreground placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring ${onAsk ? 'pr-8' : 'pr-2'}`}
          />
          {onAsk && (
            <button
              type="button"
              title="Ask the agent to write the message and commit"
              aria-label="Ask the agent to write the commit message"
              disabled={repo.files.length === 0}
              onClick={() => { void onAsk(gitAgentPrompts.commit(repo.root, staged.length > 0)) }}
              className="absolute right-1.5 top-1.5 rounded p-1 text-muted-foreground hover:bg-background hover:text-foreground disabled:opacity-40"
            >
              <Sparkles className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
        <div className="relative mt-1.5 flex">
          <button
            type="button"
            disabled={!canCommit}
            onClick={() => { void commit() }}
            className="inline-flex flex-1 items-center justify-center gap-1.5 rounded-l-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:bg-primary/90 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {busy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Check className="h-3.5 w-3.5" />}
            {staged.length > 0 ? `Commit ${staged.length} staged` : unstaged.length > 0 ? `Commit all ${unstaged.length}` : 'Commit'}
          </button>
          <button
            type="button"
            disabled={!canCommit}
            aria-label="More commit actions"
            aria-haspopup="menu"
            aria-expanded={menuOpen}
            onClick={() => setMenuOpen(open => !open)}
            className="rounded-r-md border-l border-primary-foreground/30 bg-primary px-2 text-primary-foreground hover:bg-primary/90 disabled:cursor-not-allowed disabled:opacity-50"
          >
            <ChevronDown className="h-3.5 w-3.5" />
          </button>
          {menuOpen && (
            <div role="menu" className="absolute right-0 top-full z-20 mt-1 w-52 overflow-hidden rounded-md border border-border bg-popover shadow-md">
              <button role="menuitem" type="button" className={menuItem} onClick={() => { void commit({ all: staged.length === 0 }) }}>Commit</button>
              {staged.length > 0 && unstaged.length > 0 && <button role="menuitem" type="button" className={menuItem} onClick={() => { void commit({ all: true }) }}>Commit All (stage everything)</button>}
              {onAsk && <button role="menuitem" type="button" className={menuItem} onClick={() => { void commit({ all: staged.length === 0, push: true }) }}>Commit &amp; Push (agent)</button>}
            </div>
          )}
        </div>
        {error && <p role="alert" className="mt-1.5 text-xs text-destructive">{error}</p>}
      </div>
      {repo.files.length === 0 && (
        <p className="flex items-center gap-1.5 px-3 py-2 text-xs text-muted-foreground"><Check className="h-3.5 w-3.5 text-emerald-500" />No changes. The working tree is clean.</p>
      )}
      <ConflictSection repo={repo} run={run} busy={busy} onAsk={onAsk} />
      <Section
        title="Staged Changes"
        count={staged.length}
        action={<button type="button" disabled={busy} title="Unstage all" aria-label="Unstage all" onClick={() => { void run(repo, { op: 'unstage', all: true }) }} className="rounded p-1 hover:bg-background hover:text-foreground"><Minus className="h-3.5 w-3.5" /></button>}
      >
        {staged.map(file => <FileRow key={`s:${file.path}`} repo={repo} file={file} staged workspacePath={workspacePath} busy={busy} run={run} />)}
      </Section>
      <Section
        title="Changes"
        count={unstaged.length}
        action={<button type="button" disabled={busy} title="Stage all" aria-label="Stage all" onClick={() => { void run(repo, { op: 'stage', all: true }) }} className="rounded p-1 hover:bg-background hover:text-foreground"><Plus className="h-3.5 w-3.5" /></button>}
      >
        {unstaged.map(file => <FileRow key={`u:${file.path}`} repo={repo} file={file} staged={false} workspacePath={workspacePath} busy={busy} run={run} />)}
      </Section>
      {repo.truncated && <p className="px-3 py-1 text-xs text-muted-foreground">Showing the first changes only.</p>}
      <StashSection workspacePath={workspacePath} repo={repo} run={run} busy={busy} reloadKey={graphKey + reloadKey} />
      <GitGraph workspacePath={workspacePath} repo={repo} reloadKey={graphKey + reloadKey} />
    </section>
  )
}

/** Source Control view: commit box and staged / unstaged changes, per repo. */
export function GitChangesList({ workspacePath, onAsk }: { workspacePath: string; onAsk?: AskAgent }) {
  const repos = useWorkspaceGitStore(state => state.repos)
  const refresh = useWorkspaceGitStore(state => state.refresh)
  const [busyRepo, setBusyRepo] = useState<string | null>(null)
  const [reloadKey, setReloadKey] = useState(0)
  const [errors, setErrors] = useState<Record<string, string | null>>({})

  const run: RunAction = async (repo, action) => {
    setBusyRepo(repo.root)
    setErrors(current => ({ ...current, [repo.root]: null }))
    try {
      await workspaceGitApi.act(workspacePath, repo.root, action)
      await refresh(workspacePath)
      return true
    } catch (error) {
      setErrors(current => ({ ...current, [repo.root]: gitActionError(error) }))
      await refresh(workspacePath)
      return false
    } finally {
      setBusyRepo(null)
    }
  }

  return (
    <div className="flex h-full min-h-0 flex-col text-[13px]" aria-label="Source control">
      <div className="flex h-9 shrink-0 items-center justify-between border-b border-border px-3">
        <h3 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">Source Control</h3>
        <button type="button" aria-label="Refresh source control" title="Refresh" onClick={() => { void refresh(workspacePath); setReloadKey(key => key + 1) }} className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"><RefreshCw className="h-3.5 w-3.5" /></button>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
      {repos.map(repo => (
        <RepoSection
          key={repo.root}
          repo={repo}
          workspacePath={workspacePath}
          showName={repos.length > 1}
          onAsk={onAsk}
          run={run}
          busy={busyRepo === repo.root}
          error={errors[repo.root] ?? null}
          reloadKey={reloadKey}
        />
      ))}
      </div>
    </div>
  )
}

function useAsync<T>(load: () => Promise<T>, deps: unknown[]) {
  const [state, setState] = useState<{ loading: boolean; error: string | null; value: T | null }>({ loading: true, error: null, value: null })
  useEffect(() => {
    let cancelled = false
    setState({ loading: true, error: null, value: null })
    load().then(
      value => { if (!cancelled) setState({ loading: false, error: null, value }) },
      () => { if (!cancelled) setState({ loading: false, error: 'Could not load this from git.', value: null }) },
    )
    return () => { cancelled = true }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)
  return state
}

function DiffBody({ diff, truncated, empty }: { diff: string; truncated?: boolean; empty: string }) {
  if (!diff.trim()) return <p className="p-4 text-sm text-muted-foreground">{empty}</p>
  return (
    <div className="p-2">
      <DiffRenderer content={diff} maxHeightClassName="max-h-none" />
      {truncated && <p className="mt-2 text-xs text-muted-foreground">This diff is large; only the start is shown.</p>}
    </div>
  )
}

/** Right-hand panel: a file's diff against HEAD, or its commit history. */
export function GitFilePanel({ workspacePath, panel, onClose, onAsk }: { workspacePath: string; panel: GitPanel; onClose: () => void; onAsk?: AskAgent }) {
  const openPanel = useWorkspaceGitStore(state => state.openPanel)
  const [commit, setCommit] = useState<GitCommitInfo | null>(null)
  useEffect(() => setCommit(null), [panel.kind, panel.repo, panel.file])
  const fullPath = gitFullPath(workspacePath, panel.repo, panel.file)

  const diff = useAsync(
    () => panel.kind === 'diff' ? workspaceGitApi.diff(workspacePath, panel.repo, panel.file) : Promise.resolve(null),
    [workspacePath, panel.kind, panel.repo, panel.file],
  )
  const log = useAsync(
    () => panel.kind === 'history' ? workspaceGitApi.log(workspacePath, panel.repo, panel.file) : Promise.resolve([] as GitCommitInfo[]),
    [workspacePath, panel.kind, panel.repo, panel.file],
  )
  const shown = useAsync(
    () => panel.kind === 'history' && commit ? workspaceGitApi.show(workspacePath, panel.repo, panel.file, commit.hash) : Promise.resolve(null),
    [workspacePath, panel.kind, panel.repo, panel.file, commit?.hash],
  )

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <div className="flex shrink-0 items-center gap-2 border-b border-border px-3 py-2">
        <button type="button" onClick={onClose} aria-label="Close" title="Close" className="rounded p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground"><ArrowLeft className="h-4 w-4" /></button>
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-medium text-foreground" title={fullPath}>{panel.file.split('/').pop()}</div>
          <div className="truncate text-xs text-muted-foreground">{panel.kind === 'diff' ? 'Changes since the last commit' : panel.kind === 'blame' ? 'Blame' : 'History'} · {panel.repo || 'workspace'}/{panel.file}</div>
        </div>
        <div className="flex shrink-0 items-center gap-0.5 text-xs">
          <button type="button" onClick={() => openPanel({ kind: 'diff', repo: panel.repo, file: panel.file })} aria-pressed={panel.kind === 'diff'} className={`rounded px-2 py-1 ${panel.kind === 'diff' ? 'bg-primary/15 text-foreground' : 'text-muted-foreground hover:bg-muted'}`}>Changes</button>
          <button type="button" onClick={() => openPanel({ kind: 'history', repo: panel.repo, file: panel.file })} aria-pressed={panel.kind === 'history'} className={`rounded px-2 py-1 ${panel.kind === 'history' ? 'bg-primary/15 text-foreground' : 'text-muted-foreground hover:bg-muted'}`}>History</button>
          <button type="button" onClick={() => openPanel({ kind: 'blame', repo: panel.repo, file: panel.file })} aria-pressed={panel.kind === 'blame'} className={`rounded px-2 py-1 ${panel.kind === 'blame' ? 'bg-primary/15 text-foreground' : 'text-muted-foreground hover:bg-muted'}`}>Blame</button>
          {onAsk && panel.kind === 'diff' && <button type="button" title="Ask the agent to explain these changes" onClick={() => { void onAsk(gitAgentPrompts.explainChanges(panel.repo, panel.file)) }} className="inline-flex items-center gap-1 rounded px-2 py-1 text-muted-foreground hover:bg-muted"><Sparkles className="h-3 w-3" />Explain</button>}
          <button type="button" onClick={() => { void openWorkspaceFile(fullPath) }} className="rounded px-2 py-1 text-muted-foreground hover:bg-muted">Open file</button>
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {panel.kind === 'blame' && <GitBlamePanel workspacePath={workspacePath} repo={panel.repo} file={panel.file} onAsk={onAsk} />}
        {panel.kind === 'diff' && (diff.loading
          ? <Loading />
          : diff.error ? <p className="p-4 text-sm text-destructive">{diff.error}</p>
            : <DiffBody diff={diff.value?.diff ?? ''} truncated={diff.value?.truncated} empty="No changes against the last commit (a new untracked file has no diff yet)." />)}
        {panel.kind === 'history' && (
          <div className="flex h-full min-h-0 flex-col">
            <div className="max-h-56 shrink-0 overflow-y-auto border-b border-border">
              {log.loading ? <Loading /> : log.error ? <p className="p-4 text-sm text-destructive">{log.error}</p>
                : (log.value ?? []).length === 0 ? <p className="p-4 text-sm text-muted-foreground">No commits for this file yet.</p>
                  : (log.value ?? []).map(entry => (
                    <button key={entry.hash} type="button" onClick={() => setCommit(entry)} className={`flex w-full items-start gap-2 px-3 py-1.5 text-left hover:bg-muted ${commit?.hash === entry.hash ? 'bg-primary/15' : ''}`}>
                      <GitCommit aria-hidden="true" className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                      <span className="min-w-0 flex-1">
                        <span className="block truncate text-[13px] text-foreground">{entry.subject}</span>
                        <span className="block truncate text-xs text-muted-foreground">{entry.author} · {new Date(entry.date).toLocaleDateString()} · {entry.hash.slice(0, 7)}</span>
                      </span>
                    </button>
                  ))}
            </div>
            {commit && onAsk && (
              <div className="flex shrink-0 items-center gap-2 border-b border-border px-3 py-1.5 text-xs">
                <span className="min-w-0 flex-1 truncate text-muted-foreground">{commit.hash.slice(0, 7)} · {commit.subject}</span>
                <button type="button" onClick={() => { void onAsk(gitAgentPrompts.explainCommit(panel.repo, commit.hash, commit.subject, panel.file)) }} className="inline-flex items-center gap-1 rounded border border-border px-2 py-1 hover:bg-muted"><Sparkles className="h-3 w-3" />Explain this commit</button>
              </div>
            )}
            <div className="min-h-0 flex-1 overflow-y-auto">
              {!commit ? <p className="p-4 text-sm text-muted-foreground">Select a commit to see what it changed in this file.</p>
                : shown.loading ? <Loading /> : shown.error ? <p className="p-4 text-sm text-destructive">{shown.error}</p>
                  : <DiffBody diff={shown.value?.diff ?? ''} truncated={shown.value?.truncated} empty="This commit did not change this file." />}
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

function Loading() {
  return <div className="flex items-center gap-2 p-4 text-sm text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" />Loading…</div>
}

import { useEffect, useState } from 'react'
import { ArrowLeft, ArrowDown, ArrowUp, GitBranch, GitCommit, Loader2, RefreshCw } from 'lucide-react'
import DiffRenderer from '../ui/DiffRenderer'
import { workspaceGitApi, type GitCommit as GitCommitInfo, type GitRepo } from '../../services/workspaceGit'
import { gitFullPath, useWorkspaceGitStore, type GitPanel } from '../../stores/useWorkspaceGitStore'
import { openWorkspaceFile } from '../../utils/openWorkspaceFile'

const STATUS_STYLE = {
  modified: { letter: 'M', text: 'text-amber-500' },
  added: { letter: 'A', text: 'text-emerald-500' },
  untracked: { letter: 'U', text: 'text-emerald-500' },
  deleted: { letter: 'D', text: 'text-destructive' },
  renamed: { letter: 'R', text: 'text-sky-500' },
  conflict: { letter: '!', text: 'text-destructive' },
} as const

function repoLabel(repo: GitRepo): string {
  return repo.root || 'workspace'
}

/** Branch and change summary above the tree; the Changes button swaps the tree for the changed-file list. */
export function GitBar({ workspacePath, changesOpen, onToggleChanges }: {
  workspacePath: string
  changesOpen: boolean
  onToggleChanges: () => void
}) {
  const repos = useWorkspaceGitStore(state => state.repos)
  const refresh = useWorkspaceGitStore(state => state.refresh)
  if (repos.length === 0) return null
  const changed = repos.reduce((sum, repo) => sum + repo.files.length, 0)
  return (
    <div className="flex shrink-0 items-center gap-2 border-b border-border bg-muted/30 px-2 py-1 text-xs">
      <div className="flex min-w-0 flex-1 flex-wrap items-center gap-x-3 gap-y-0.5">
        {repos.map(repo => (
          <span key={repo.root} className="inline-flex min-w-0 items-center gap-1 text-muted-foreground" title={repo.upstream ? `Tracking ${repo.upstream}` : 'No upstream branch'}>
            <GitBranch aria-hidden="true" className="h-3.5 w-3.5 shrink-0" />
            {repos.length > 1 && <span className="truncate">{repoLabel(repo)}:</span>}
            <span className="truncate font-medium text-foreground">{repo.detached ? 'detached HEAD' : repo.branch}</span>
            {repo.ahead > 0 && <span className="inline-flex items-center text-emerald-500" title={`${repo.ahead} to push`}><ArrowUp className="h-3 w-3" />{repo.ahead}</span>}
            {repo.behind > 0 && <span className="inline-flex items-center text-amber-500" title={`${repo.behind} to pull`}><ArrowDown className="h-3 w-3" />{repo.behind}</span>}
          </span>
        ))}
      </div>
      <button
        type="button"
        onClick={onToggleChanges}
        aria-pressed={changesOpen}
        className={`shrink-0 rounded px-1.5 py-0.5 font-medium ${changesOpen ? 'bg-primary/15 text-foreground' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}
      >
        Changes{changed > 0 ? ` (${changed})` : ''}
      </button>
      <button type="button" aria-label="Refresh git status" title="Refresh git status" onClick={() => { void refresh(workspacePath) }} className="shrink-0 rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground">
        <RefreshCw className="h-3.5 w-3.5" />
      </button>
    </div>
  )
}

/** Changed files, grouped by repo. A click opens the diff; actions go to the agent. */
export function GitChangesList({ workspacePath, onAsk }: { workspacePath: string; onAsk?: (message: string) => void | Promise<unknown> }) {
  const repos = useWorkspaceGitStore(state => state.repos)
  const openPanel = useWorkspaceGitStore(state => state.openPanel)
  const activePanel = useWorkspaceGitStore(state => state.panel)
  return (
    <div className="h-full overflow-y-auto py-1 text-[13px]" aria-label="Changes">
      {repos.map(repo => {
        const name = repoLabel(repo)
        return (
          <section key={repo.root} className="mb-3">
            <div className="flex items-center gap-2 px-2 py-1 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
              <GitBranch aria-hidden="true" className="h-3.5 w-3.5" />
              <span className="truncate">{name} · {repo.detached ? 'detached HEAD' : repo.branch}</span>
            </div>
            {onAsk && (
              <div className="flex flex-wrap gap-1 px-2 pb-1">
                {repo.files.length > 0 && <button type="button" onClick={() => { void onAsk(`Commit the current changes in ${repo.root ? `the \`${repo.root}\` repo` : 'this repo'} with a clear commit message. Show me what you will commit first.`) }} className="rounded border border-border px-2 py-0.5 text-xs hover:bg-muted">Commit…</button>}
                <button type="button" onClick={() => { void onAsk(`Pull the latest changes for ${repo.root ? `the \`${repo.root}\` repo` : 'this repo'} and tell me what changed.`) }} className="rounded border border-border px-2 py-0.5 text-xs hover:bg-muted">Pull…</button>
                {repo.ahead > 0 && <button type="button" onClick={() => { void onAsk(`Push ${repo.root ? `the \`${repo.root}\` repo` : 'this repo'}'s commits to its remote.`) }} className="rounded border border-border px-2 py-0.5 text-xs hover:bg-muted">Push…</button>}
              </div>
            )}
            {repo.files.length === 0 && <p className="px-3 py-1 text-xs text-muted-foreground">No changes.</p>}
            {repo.files.map(file => {
              const style = STATUS_STYLE[file.status]
              const active = activePanel?.kind === 'diff' && activePanel.repo === repo.root && activePanel.file === file.path
              const base = file.path.split('/').pop() || file.path
              const dir = file.path.includes('/') ? file.path.slice(0, file.path.lastIndexOf('/')) : ''
              return (
                <button
                  key={`${file.status}:${file.path}`}
                  type="button"
                  title={gitFullPath(workspacePath, repo.root, file.path)}
                  onClick={() => openPanel({ kind: 'diff', repo: repo.root, file: file.path })}
                  className={`flex w-full items-center gap-2 px-3 py-1 text-left hover:bg-muted ${active ? 'bg-primary/15' : ''}`}
                >
                  <span className="min-w-0 flex-1 truncate"><span className="text-foreground">{base}</span>{dir && <span className="ml-1.5 text-xs text-muted-foreground">{dir}</span>}</span>
                  <span className={`w-3 shrink-0 text-center text-[11px] font-semibold ${style.text}`} title={file.status}>{style.letter}</span>
                </button>
              )
            })}
            {repo.truncated && <p className="px-3 py-1 text-xs text-muted-foreground">Showing the first changes only.</p>}
          </section>
        )
      })}
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
export function GitFilePanel({ workspacePath, panel, onClose }: { workspacePath: string; panel: GitPanel; onClose: () => void }) {
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
          <div className="truncate text-xs text-muted-foreground">{panel.kind === 'diff' ? 'Changes since the last commit' : 'History'} · {panel.repo || 'workspace'}/{panel.file}</div>
        </div>
        <div className="flex shrink-0 items-center gap-0.5 text-xs">
          <button type="button" onClick={() => openPanel({ kind: 'diff', repo: panel.repo, file: panel.file })} aria-pressed={panel.kind === 'diff'} className={`rounded px-2 py-1 ${panel.kind === 'diff' ? 'bg-primary/15 text-foreground' : 'text-muted-foreground hover:bg-muted'}`}>Changes</button>
          <button type="button" onClick={() => openPanel({ kind: 'history', repo: panel.repo, file: panel.file })} aria-pressed={panel.kind === 'history'} className={`rounded px-2 py-1 ${panel.kind === 'history' ? 'bg-primary/15 text-foreground' : 'text-muted-foreground hover:bg-muted'}`}>History</button>
          <button type="button" onClick={() => { void openWorkspaceFile(fullPath) }} className="rounded px-2 py-1 text-muted-foreground hover:bg-muted">Open file</button>
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
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

import { useFileGit, useFileGitStore } from './FileGitContext'
import { useEffect, useMemo, useRef, useState } from 'react'
import { Archive, ArrowLeft, ChevronDown, Cloud, GitBranch as GitBranchIcon, Loader2, Plus, Search, Sparkles, Trash2 } from 'lucide-react'
import { type GitAction, type GitBlameLine, type GitBranch, type GitChangedFile, type GitRepo, type GitStash } from '../../services/workspaceGit'
import { gitFullPath } from '../../stores/useWorkspaceGitStore'
import { gitAgentPrompts } from '../../utils/gitAgentPrompts'
import { FileTypeIcon } from './fileTypeIcon'

export type RunAction = (repo: GitRepo, action: GitAction) => Promise<boolean>
export type AskAgent = (message: string) => void | Promise<unknown>

const iconButton = 'rounded p-1 text-muted-foreground hover:bg-background hover:text-foreground disabled:opacity-40'

function useLoaded<T>(load: () => Promise<T>, deps: unknown[], initial: T) {
  const [value, setValue] = useState<T>(initial)
  const [loading, setLoading] = useState(true)
  useEffect(() => {
    let cancelled = false
    setLoading(true)
    load().then(next => { if (!cancelled) { setValue(next); setLoading(false) } }, () => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)
  return { value, loading }
}

/** The branch name as a button that opens the branch switcher (VS Code's status-bar branch picker). */
export function BranchPicker({ workspacePath, repo, run, busy }: { workspacePath: string; repo: GitRepo; run: RunAction; busy: boolean }) {
  const git = useFileGit()
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [creating, setCreating] = useState(false)
  const [newName, setNewName] = useState('')
  const [deleting, setDeleting] = useState<string | null>(null)
  const rootRef = useRef<HTMLDivElement>(null)
  const branches = useLoaded<GitBranch[]>(() => open ? git.api.branches(workspacePath, repo.root) : Promise.resolve([]), [open, workspacePath, repo.root, repo.branch], [])

  useEffect(() => {
    if (!open) return
    const onDown = (event: MouseEvent) => { if (!rootRef.current?.contains(event.target as Node)) setOpen(false) }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open])

  const shown = useMemo(() => branches.value.filter(branch => branch.name.toLowerCase().includes(query.trim().toLowerCase())), [branches.value, query])
  const close = () => { setOpen(false); setQuery(''); setCreating(false); setNewName(''); setDeleting(null) }
  const switchTo = async (branch: GitBranch) => {
    if (branch.current) return close()
    if (await run(repo, { op: 'checkout', branch: branch.name, remote: branch.remote })) close()
  }
  const create = async () => {
    if (!newName.trim()) return
    if (await run(repo, { op: 'create_branch', branch: newName.trim() })) close()
  }

  return (
    <div ref={rootRef} className="relative min-w-0">
      <button
        type="button"
        onClick={() => setOpen(value => !value)}
        aria-haspopup="listbox"
        aria-expanded={open}
        title="Switch or create a branch"
        className="inline-flex min-w-0 items-center gap-1 rounded px-1 py-0.5 text-muted-foreground hover:bg-muted hover:text-foreground"
      >
        <GitBranchIcon aria-hidden="true" className="h-3.5 w-3.5 shrink-0" />
        <span className="truncate font-medium text-foreground">{repo.detached ? 'detached HEAD' : repo.branch}</span>
        <ChevronDown aria-hidden="true" className="h-3 w-3 shrink-0" />
      </button>
      {open && (
        <div className="absolute left-0 top-full z-30 mt-1 w-64 overflow-hidden rounded-md border border-border bg-popover shadow-md">
          <div className="flex items-center gap-1.5 border-b border-border px-2 py-1.5">
            <Search aria-hidden="true" className="h-3.5 w-3.5 text-muted-foreground" />
            <input autoFocus value={query} onChange={event => setQuery(event.target.value)} placeholder="Select a branch" aria-label="Filter branches" className="min-w-0 flex-1 bg-transparent text-xs text-foreground outline-none placeholder:text-muted-foreground" />
          </div>
          <div className="max-h-64 overflow-y-auto py-1" role="listbox" aria-label="Branches">
            {creating ? (
              <div className="flex items-center gap-1 px-2 py-1">
                <input
                  autoFocus
                  value={newName}
                  onChange={event => setNewName(event.target.value)}
                  onKeyDown={event => { if (event.key === 'Enter') void create(); if (event.key === 'Escape') setCreating(false) }}
                  placeholder="New branch name"
                  aria-label="New branch name"
                  className="min-w-0 flex-1 rounded border border-input bg-background px-2 py-1 text-xs text-foreground outline-none focus-visible:ring-1 focus-visible:ring-ring"
                />
                <button type="button" disabled={busy || !newName.trim()} onClick={() => { void create() }} className="rounded bg-primary px-2 py-1 text-xs font-medium text-primary-foreground disabled:opacity-50">Create</button>
              </div>
            ) : (
              <button type="button" onClick={() => setCreating(true)} className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-foreground hover:bg-muted"><Plus className="h-3.5 w-3.5" />Create new branch…</button>
            )}
            {branches.loading && branches.value.length === 0 && <div className="flex items-center gap-2 px-3 py-2 text-xs text-muted-foreground"><Loader2 className="h-3.5 w-3.5 animate-spin" />Loading…</div>}
            {shown.map(branch => deleting === branch.name ? (
              <div key={branch.name} className="flex items-center gap-2 bg-destructive/10 px-3 py-1.5 text-xs">
                <span className="min-w-0 flex-1 truncate">Delete <strong>{branch.name}</strong>?</span>
                <button type="button" disabled={busy} onClick={() => { setDeleting(null); void run(repo, { op: 'delete_branch', branch: branch.name }) }} className="rounded bg-destructive px-2 py-0.5 font-medium text-destructive-foreground">Delete</button>
                <button type="button" onClick={() => setDeleting(null)} className="rounded border border-border px-2 py-0.5">Cancel</button>
              </div>
            ) : (
              <div key={branch.name} role="option" aria-selected={!!branch.current} className={`group/branch flex items-center gap-2 px-3 py-1.5 text-xs hover:bg-muted ${branch.current ? 'bg-primary/10' : ''}`}>
                <button type="button" disabled={busy} onClick={() => { void switchTo(branch) }} className="flex min-w-0 flex-1 items-center gap-2 text-left">
                  {branch.remote ? <Cloud className="h-3.5 w-3.5 shrink-0 text-violet-500" /> : <GitBranchIcon className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />}
                  <span className="truncate text-foreground">{branch.name}</span>
                  {branch.current && <span className="ml-auto shrink-0 text-[10px] uppercase text-muted-foreground">current</span>}
                </button>
                {!branch.current && !branch.remote && (
                  <button type="button" aria-label={`Delete ${branch.name}`} title="Delete branch" onClick={() => setDeleting(branch.name)} className={`${iconButton} hidden group-hover/branch:block`}><Trash2 className="h-3.5 w-3.5" /></button>
                )}
              </div>
            ))}
            {!branches.loading && shown.length === 0 && <p className="px-3 py-2 text-xs text-muted-foreground">No matching branches.</p>}
          </div>
        </div>
      )}
    </div>
  )
}

/** Stashed work: stash everything now, apply/pop/drop earlier stashes. */
export function StashSection({ workspacePath, repo, run, busy, reloadKey }: { workspacePath: string; repo: GitRepo; run: RunAction; busy: boolean; reloadKey: number }) {
  const git = useFileGit()
  const [open, setOpen] = useState(false)
  const [message, setMessage] = useState('')
  const [dropping, setDropping] = useState<string | null>(null)
  const stashes = useLoaded<GitStash[]>(() => git.api.stashes(workspacePath, repo.root), [workspacePath, repo.root, repo.files.length, reloadKey], [])
  const hasChanges = repo.files.length > 0
  if (!hasChanges && stashes.value.length === 0) return null
  return (
    <div className="border-t border-border">
      <button type="button" onClick={() => setOpen(value => !value)} aria-expanded={open} className="flex h-7 w-full items-center gap-1 px-2 text-left text-xs font-semibold uppercase tracking-wide text-muted-foreground hover:text-foreground">
        <Archive aria-hidden="true" className="h-3.5 w-3.5" />
        <span className="flex-1">Stashes</span>
        {stashes.value.length > 0 && <span className="rounded-full bg-muted px-1.5 text-[10px] font-medium normal-case leading-4 text-foreground">{stashes.value.length}</span>}
      </button>
      {open && (
        <div className="pb-2">
          {hasChanges && (
            <div className="flex items-center gap-1 px-2 pb-1">
              <input value={message} onChange={event => setMessage(event.target.value)} placeholder="Stash message (optional)" aria-label="Stash message" className="min-w-0 flex-1 rounded border border-input bg-background px-2 py-1 text-xs text-foreground outline-none placeholder:text-muted-foreground focus-visible:ring-1 focus-visible:ring-ring" />
              <button type="button" disabled={busy} onClick={() => { void run(repo, { op: 'stash', message: message.trim() || undefined }).then(ok => { if (ok) setMessage('') }) }} className="rounded border border-border px-2 py-1 text-xs hover:bg-muted disabled:opacity-50">Stash</button>
            </div>
          )}
          {stashes.value.map(stash => dropping === stash.ref ? (
            <div key={stash.ref} className="flex items-center gap-2 bg-destructive/10 px-3 py-1.5 text-xs">
              <span className="min-w-0 flex-1 truncate">Drop {stash.ref}? This cannot be undone.</span>
              <button type="button" disabled={busy} onClick={() => { setDropping(null); void run(repo, { op: 'stash_drop', ref: stash.ref }) }} className="rounded bg-destructive px-2 py-0.5 font-medium text-destructive-foreground">Drop</button>
              <button type="button" onClick={() => setDropping(null)} className="rounded border border-border px-2 py-0.5">Cancel</button>
            </div>
          ) : (
            <div key={stash.ref} className="group/stash flex h-7 items-center gap-1.5 px-3 text-[13px] hover:bg-muted" title={`${stash.ref} · ${new Date(stash.date).toLocaleString()}`}>
              <span className="min-w-0 flex-1 truncate text-foreground">{stash.message.replace(/^On [^:]+: /, '') || stash.ref}</span>
              <span className="hidden shrink-0 items-center group-hover/stash:flex">
                <button type="button" disabled={busy} onClick={() => { void run(repo, { op: 'stash_apply', ref: stash.ref }) }} className="rounded px-1.5 py-0.5 text-xs text-muted-foreground hover:bg-background hover:text-foreground">Apply</button>
                <button type="button" disabled={busy} onClick={() => { void run(repo, { op: 'stash_pop', ref: stash.ref }) }} className="rounded px-1.5 py-0.5 text-xs text-muted-foreground hover:bg-background hover:text-foreground">Pop</button>
                <button type="button" disabled={busy} aria-label={`Drop ${stash.ref}`} onClick={() => setDropping(stash.ref)} className={iconButton}><Trash2 className="h-3.5 w-3.5" /></button>
              </span>
            </div>
          ))}
          {stashes.value.length === 0 && !stashes.loading && <p className="px-3 py-1 text-xs text-muted-foreground">No stashes yet.</p>}
        </div>
      )}
    </div>
  )
}

/** Files with merge conflicts: take one side or both here, or hand the rest to the agent. */
export function ConflictSection({ repo, run, busy, onAsk }: { repo: GitRepo; run: RunAction; busy: boolean; onAsk?: AskAgent }) {
  const conflicts = repo.files.filter((file: GitChangedFile) => file.worktree_status === 'conflict')
  if (conflicts.length === 0) return null
  const choose = (file: GitChangedFile, choice: 'ours' | 'theirs' | 'both') => { void run(repo, { op: 'resolve', files: [file.path], choice }) }
  const button = 'rounded border border-border px-2 py-0.5 text-[11px] text-foreground hover:bg-muted disabled:opacity-50'
  return (
    <div className="border-b border-border bg-destructive/5 pb-1">
      <div className="flex h-8 items-center gap-2 px-3 text-xs font-semibold uppercase tracking-wide text-destructive">
        <span className="flex-1">Merge Conflicts</span>
        <span className="rounded-full bg-destructive px-1.5 text-[10px] font-medium leading-4 text-destructive-foreground">{conflicts.length}</span>
        {onAsk && <button type="button" onClick={() => { void onAsk(gitAgentPrompts.resolveConflict(repo.root)) }} className="inline-flex items-center gap-1 rounded border border-border bg-background px-2 py-0.5 text-[11px] font-medium normal-case text-foreground hover:bg-muted"><Sparkles className="h-3 w-3" />Resolve all with agent</button>}
      </div>
      {conflicts.map(file => {
        const name = file.path.split('/').pop() || file.path
        return (
          <div key={file.path} className="px-3 py-1">
            <div className="flex items-center gap-1.5 text-[13px]">
              <FileTypeIcon name={name} />
              <span className="min-w-0 flex-1 truncate text-destructive" title={file.path}>{name}</span>
              <span className="w-3 shrink-0 text-center text-[11px] font-semibold text-destructive" title="Merge conflict">!</span>
            </div>
            <div className="mt-1 flex flex-wrap gap-1 pl-6">
              <button type="button" disabled={busy} title="Keep your side (current branch)" onClick={() => choose(file, 'ours')} className={button}>Accept Current</button>
              <button type="button" disabled={busy} title="Take their side (incoming branch)" onClick={() => choose(file, 'theirs')} className={button}>Accept Incoming</button>
              <button type="button" disabled={busy} title="Keep both, remove the markers" onClick={() => choose(file, 'both')} className={button}>Accept Both</button>
              {onAsk && <button type="button" title="Ask the agent to resolve this file" onClick={() => { void onAsk(gitAgentPrompts.resolveConflict(repo.root, file.path)) }} className={`${button} inline-flex items-center gap-1`}><Sparkles className="h-3 w-3" />Agent</button>}
            </div>
          </div>
        )
      })}
    </div>
  )
}

function relativeTime(seconds: number): string {
  const diff = Math.max(0, Date.now() / 1000 - seconds)
  const units: Array<[number, string]> = [[31536000, 'y'], [2592000, 'mo'], [86400, 'd'], [3600, 'h'], [60, 'm']]
  for (const [size, label] of units) if (diff >= size) return `${Math.floor(diff / size)}${label} ago`
  return 'just now'
}

/** Line-by-line blame: who last changed each line, grouped by commit. */
export function GitBlamePanel({ workspacePath, repo, file, onAsk }: { workspacePath: string; repo: string; file: string; onAsk?: AskAgent }) {
  const git = useFileGit()
  const openPanel = useFileGitStore(state => state.openPanel)
  const [selected, setSelected] = useState<string | null>(null)
  const full = gitFullPath(workspacePath, repo, file)
  const data = useLoaded<{ lines: GitBlameLine[]; text: string[]; error: string | null; truncated: boolean }>(async () => {
    try {
      const [blame, content] = await Promise.all([git.api.blame(workspacePath, repo, file), git.readFile(full)])
      const text = content.split('\n')
      return { lines: blame.lines, text, error: null, truncated: blame.truncated }
    } catch {
      return { lines: [], text: [], error: 'This file has no git history yet, so there is nothing to blame.', truncated: false }
    }
  }, [workspacePath, repo, file], { lines: [], text: [], error: null, truncated: false })

  // Consecutive lines from one commit share a gutter cell.
  const groups = useMemo(() => {
    const list: Array<{ start: number; end: number; blame: GitBlameLine }> = []
    data.value.lines.forEach((blame, index) => {
      const last = list[list.length - 1]
      if (last && last.blame.hash === blame.hash) last.end = index
      else list.push({ start: index, end: index, blame })
    })
    return list
  }, [data.value.lines])

  if (data.loading) return <div className="flex items-center gap-2 p-4 text-sm text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" />Loading blame…</div>
  if (data.value.error) return <p className="p-4 text-sm text-muted-foreground">{data.value.error}</p>
  return (
    <div className="font-mono text-xs leading-5">
      {groups.map(group => {
        const { blame } = group
        const active = selected === blame.hash
        return (
          <div key={`${blame.hash}:${group.start}`} className={`flex ${active ? 'bg-primary/10' : ''}`}>
            <button
              type="button"
              onClick={() => setSelected(active ? null : blame.hash)}
              title={`${blame.summary}\n${blame.author}${blame.time ? ` · ${new Date(blame.time * 1000).toLocaleString()}` : ''}${blame.uncommitted ? '' : ` · ${blame.hash.slice(0, 7)}`}`}
              className="w-56 shrink-0 self-start truncate border-r border-border px-2 text-left text-muted-foreground hover:bg-muted"
            >
              {blame.uncommitted ? <span className="italic">You · uncommitted</span> : <><span className="text-foreground">{blame.author}</span> · {relativeTime(blame.time)} · {blame.summary}</>}
            </button>
            <pre className="min-w-0 flex-1 overflow-x-auto whitespace-pre px-2">{data.value.text.slice(group.start, group.end + 1).map((line, offset) => `${String(group.start + offset + 1).padStart(4, ' ')}  ${line}`).join('\n')}</pre>
          </div>
        )
      })}
      {data.value.truncated && <p className="p-2 text-muted-foreground">Only the first lines are shown.</p>}
      {selected && onAsk && (() => {
        const group = groups.find(item => item.blame.hash === selected)
        if (!group || group.blame.uncommitted) return null
        return (
          <div className="sticky bottom-0 flex items-center gap-2 border-t border-border bg-background px-3 py-2 font-sans text-xs">
            <span className="min-w-0 flex-1 truncate text-muted-foreground">{group.blame.hash.slice(0, 7)} · {group.blame.summary}</span>
            <button type="button" onClick={() => { void onAsk(gitAgentPrompts.explainBlame(repo, file, group.start + 1, group.end + 1, group.blame.hash, group.blame.summary)) }} className="inline-flex items-center gap-1 rounded border border-border px-2 py-1 hover:bg-muted"><Sparkles className="h-3 w-3" />Explain these lines</button>
            <button type="button" onClick={() => openPanel({ kind: 'history', repo, file })} className="rounded border border-border px-2 py-1 hover:bg-muted"><ArrowLeft className="mr-1 inline h-3 w-3" />File history</button>
          </div>
        )
      })()}
    </div>
  )
}

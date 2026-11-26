import { useState, type ReactNode } from 'react'
import { ChevronRight, Clock, FileDiff, FileText, Folder, FolderOpen, Github, GitBranch, SquareTerminal, TriangleAlert } from 'lucide-react'
import api from '../../services/api'
import type { CrewFileLimit, CrewPanelSection, CrewPanelRow } from '../../products/work/useCrewPanel'
import { usePersistentTab } from '../../hooks/usePersistentTab'
import { WorkspaceContextRow } from './WorkspaceContextCard'

const states = ['open', 'closed'] as const
const DAY = 24 * 60 * 60

/** "now", "5m ago", "3h ago", "4d ago", "5w ago", "7mo ago" from Unix seconds. */
export function gitAgo(at: number, now = Date.now() / 1000) {
  const s = Math.max(0, now - at)
  if (s < 60) return 'now'
  if (s < 3600) return `${Math.floor(s / 60)}m ago`
  if (s < DAY) return `${Math.floor(s / 3600)}h ago`
  if (s < 14 * DAY) return `${Math.floor(s / DAY)}d ago`
  if (s < 60 * DAY) return `${Math.floor(s / (7 * DAY))}w ago`
  return `${Math.floor(s / (30 * DAY))}mo ago`
}

/** The remote check is worth mentioning once it is more than a day old. */
export function gitStaleDays(fetchedAt?: number, now = Date.now() / 1000) {
  if (!fetchedAt) return 0
  const days = Math.floor((now - fetchedAt) / DAY)
  return days >= 1 ? days : 0
}

/** "12 changed files", "1 changed file"; "" for none. */
export function changedLabel(n: number | undefined, words: 'changed' | 'changed files'): string {
  if (!n) return ''
  return words === 'changed' ? `${n} changed` : `${n} changed ${n === 1 ? 'file' : 'files'}`
}

/** Repositories most worth a look first: those with edits not committed yet,
 * then the most recent commit. A repository's checkouts (its `group`) stay
 * together in the server's order, main checkout first. */
export function sortGitRows(rows: CrewPanelRow[]): CrewPanelRow[] {
  const groups: CrewPanelRow[][] = []
  const byKey = new Map<string, CrewPanelRow[]>()
  for (const row of rows) {
    const key = row.group ? `group:${row.group}` : `row:${row.id}`
    let list = byKey.get(key)
    if (!list) { list = []; byKey.set(key, list); groups.push(list) }
    list.push(row)
  }
  const changed = (g: CrewPanelRow[]) => g.some(r => r.changed || r.changed_files) ? 1 : 0
  const latest = (g: CrewPanelRow[]) => Math.max(0, ...g.map(r => r.commit_at || 0))
  return groups.map((g, i) => ({ g, i })).sort((a, b) => changed(b.g) - changed(a.g) || latest(b.g) - latest(a.g) || a.i - b.i).flatMap(x => x.g)
}

/** The project's Git map: every checkout with its branch and its state against
 * its remote, as the server labels them, grouped under their repository
 * (`group`). File counts are git's own (files not committed yet), never
 * credited to a chat: reply cards show what each reply changed. The
 * Git title only shows or hides this list. Each checkout also shows when its
 * last commit was made and its message, and how old the remote check is once
 * it is more than a day old (the app never fetches); a repository row (with its file
 * icon) opens that checkout's files. Open or closed is remembered per project
 * in this browser. */
const fileCount = (n: number) => n.toLocaleString('en-US')
// The server's refusals say what to do instead; show them as they are.
const errorText = (cause: unknown, fallback: string) => (cause as { response?: { data?: { error?: { message?: string } } } })?.response?.data?.error?.message || fallback

/** Too many files to track every reply's changes (#3878, approved mockup): the
 * server's stored verdict, the biggest folders, and the fixes. The server
 * decides and checks again; this only shows it and sends the click. */
/** The chat message Move out sends: the person and the agent decide together
 * (owner, 25 Nov), instead of the click moving the folder at once. */
export function moveOutMessage(folder: { path: string; files: number }, limit: CrewFileLimit) {
  return `This project folder has about ${fileCount(limit.files)} files, more than the ${fileCount(limit.limit)} replies can track, so not all changes are tracked. The biggest folder is "${folder.path}" (about ${fileCount(folder.files)} files).\n\nI'm thinking of moving "${folder.path}" out of the project. Before doing anything, check what it is and whether anything uses it (scripts, Git worktrees, running work), then suggest what to do: move it next to the project (files action=file_limit, operation=move), delete it if it is scratch, or leave it. Don't move or delete anything until I say so.`
}

export function FileLimitBox({ limit, workspacePath, onAskAgent, onOpenFolder }: { limit: CrewFileLimit; workspacePath?: string; onAskAgent?: (message: string) => Promise<void>; onOpenFolder?: (path: string) => void }) {
  const [busy, setBusy] = useState('')
  const [note, setNote] = useState<{ text: string; failed?: boolean } | null>(null)
  const [report, setReport] = useState(limit)
  const askAgent = async (folder: { path: string; files: number }) => {
    if (!onAskAgent) return
    setBusy(folder.path); setNote(null)
    try {
      await onAskAgent(moveOutMessage(folder, report))
      setNote({ text: `Asked the agent about ${folder.path} in the chat. Nothing moves until you agree there.` })
    } catch {
      setNote({ text: 'Could not open the chat. Try again.', failed: true })
    } finally { setBusy('') }
  }
  // The desktop app shows the folder in Finder or File Explorer; the server resolves it inside this project.
  const canReveal = Boolean(workspacePath) && typeof window.electronAPI?.openEditor === 'function'
  const revealLabel = /Windows/i.test(navigator.userAgent) ? 'File Explorer' : 'Finder'
  const reveal = async (folder: { path: string }) => {
    if (!canReveal) return
    setNote(null)
    try {
      const { data } = await api.post<{ launch?: { reveal?: string }; request_id?: string }>('/api/workspace-editor', { workspace_path: workspacePath, op: 'reveal', file: folder.path }, { skipSessionContext: true })
      if (!data.launch?.reveal || !data.request_id) throw new Error('This folder could not be opened.')
      const result = await window.electronAPI!.openEditor!({ reveal: data.launch.reveal, requestId: data.request_id })
      if (!result.ok) throw new Error(result.error)
    } catch (cause) {
      setNote({ text: cause instanceof Error && !('response' in cause) ? cause.message : errorText(cause, 'This folder could not be opened.'), failed: true })
    }
  }
  // Ignore writes /<folder>/ into the project's own .gitignore on the server and counts again (#3878).
  const ignore = async (folder: { path: string }) => {
    if (!workspacePath) return
    setBusy(`ignore:${folder.path}`); setNote(null)
    try {
      const { data } = await api.post<{ report: CrewFileLimit }>('/api/workspace-snapshots', {}, { params: { workspace_path: workspacePath, op: 'file_limit_ignore', folder: folder.path } })
      setReport(current => ({ ...current, ...data.report }))
      setNote({ text: `${folder.path} is in the project's .gitignore now; file tracking leaves it out. Remove the line to track it again.` })
    } catch (cause) {
      setNote({ text: errorText(cause, `Could not ignore ${folder.path}. Try again.`), failed: true })
    } finally { setBusy('') }
  }
  const checkAgain = async () => {
    if (!workspacePath) return
    setBusy('check'); setNote(null)
    try {
      const { data } = await api.post<{ report: CrewFileLimit }>('/api/workspace-snapshots', {}, { params: { workspace_path: workspacePath, op: 'file_limit_check' } })
      setReport(current => ({ ...current, ...data.report }))
      setNote({ text: data.report.over ? 'Checked: still too many files.' : 'Checked: all changes are tracked again.' })
    } catch (cause) {
      setNote({ text: errorText(cause, 'Could not check again. Try again.'), failed: true })
    } finally { setBusy('') }
  }
  const folders = report.folders ?? []
  if (!report.over) return note ? <p role="status" className="ml-3 mt-1 text-[11px] text-emerald-700 dark:text-emerald-400">{note.text}</p> : null
  return <section aria-label="Changes not fully tracked" className="ml-3 mt-1 space-y-2 rounded-lg border border-border bg-amber-500/10 p-2.5 text-xs">
    <h4 className="font-medium">Not all changes are tracked</h4>
    <p>This project folder has about <b>{fileCount(report.files)}</b> files; replies track up to {fileCount(report.limit)}. Changes in its repositories are still tracked, but edits to files outside them are not shown on replies and can't be undone.</p>
    {folders.length > 0 && <div>
      <p className="text-[11px] text-muted-foreground">Biggest folders</p>
      <ul className="mt-1 divide-y divide-border">{folders.map(folder => <li key={folder.path} className="flex items-center gap-2 py-1.5">
        {/* The folder opens in Files so the person can see what fills it before moving it. */}
        <button type="button" disabled={!onOpenFolder} onClick={() => onOpenFolder?.(folder.path)} title={`Open ${folder.path} in Files`} className="group/folder flex min-w-0 flex-1 items-center gap-2 rounded text-left enabled:hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <Folder className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
        <span className="min-w-0 flex-1"><span className="block truncate font-medium group-enabled/folder:group-hover/folder:underline">{folder.path}</span><span className="text-[11px] text-muted-foreground">about {fileCount(folder.files)} files</span></span>
        </button>
        {canReveal && <button type="button" title={`Show ${folder.path} in ${revealLabel}`} aria-label={`Show ${folder.path} in ${revealLabel}`} onClick={() => void reveal(folder)} className="flex h-6 w-6 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-background hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><FolderOpen className="h-3.5 w-3.5" aria-hidden="true" /></button>}
        {report.can_fix && onAskAgent && <button type="button" disabled={Boolean(busy)} title="Ask the agent in chat to check this folder and suggest what to do" onClick={() => void askAgent(folder)} className="shrink-0 rounded-md border border-border bg-background px-2 py-0.5 text-[11px] hover:bg-muted disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">{busy === folder.path ? 'Asking…' : 'Move out'}</button>}
        {report.can_fix && workspacePath && <button type="button" disabled={Boolean(busy)} title={`Add /${folder.path}/ to the project's .gitignore so file tracking leaves it out`} onClick={() => void ignore(folder)} className="shrink-0 rounded-md border border-border bg-background px-2 py-0.5 text-[11px] hover:bg-muted disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">{busy === `ignore:${folder.path}` ? 'Ignoring…' : 'Ignore'}</button>}
      </li>)}</ul>
    </div>}
    {note && <p role={note.failed ? 'alert' : 'status'} className={note.failed ? 'text-destructive' : 'text-muted-foreground'}>{note.text}</p>}
    {report.can_fix
      ? <div className="flex flex-wrap items-center gap-2"><button type="button" disabled={Boolean(busy)} onClick={() => void checkAgain()} className="rounded-md border border-border bg-background px-2 py-0.5 text-[11px] hover:bg-muted disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">{busy === 'check' ? 'Checking…' : 'Check again'}</button><span className="text-[11px] text-muted-foreground">Move out asks the agent in chat first. Ignore adds the folder to the project's .gitignore.</span></div>
      : <p className="text-[11px] text-muted-foreground">Only the project owner can move or ignore folders, on their Mac. Ask them, or ask the agent "why aren't all my changes tracked?".</p>}
  </section>
}

/** Returns true if active GitHub login/orgs reach repo's owner, false if orgs are known and don't match, or null when unknown. */
export function isGitHubRepoReachable(githubRepo?: string, login?: string, orgs?: string[]): boolean | null {
  if (!githubRepo || !login) return null
  const owner = githubRepo.split('/')[0]?.trim().toLowerCase()
  if (!owner) return null
  if (owner === login.trim().toLowerCase()) return true
  if (!orgs || orgs.length === 0) return null
  return orgs.some(org => org.trim().toLowerCase() === owner)
}

export type ChatChangesGitHub = {
  connected: boolean
  login?: string
  orgs?: string[]
  onOpen?: () => void
}

export function ChatChangesRow({ section, storageKey, onOpen, onTerminal, workspacePath, onAskAgent, github }: { section?: CrewPanelSection; storageKey: string; onOpen: (repoRoot?: string, changed?: boolean) => void; onTerminal?: () => void; workspacePath?: string; onAskAgent?: (message: string) => Promise<void>; github?: ChatChangesGitHub }) {
  const [state, setState] = usePersistentTab(storageKey, 'open', states)
  if (!section || (!section.error && !section.rows.length)) return null
  const open = state === 'open'
  const stale = (repo: CrewPanelRow) => gitStaleDays(repo.fetched_at)
  const repoRow = (repo: CrewPanelRow) => {
    return <button type="button" key={repo.id} onClick={() => onOpen(repo.path || '', Boolean(repo.changed) || !repo.path)} title={repo.changed ? `Review the edits not committed yet in ${repo.path || repo.label}` : `Open the folder ${repo.path || repo.label}`}
      className={`group/repo flex w-full min-w-0 items-start gap-2 rounded-lg py-2 pr-1.5 text-left hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${repo.group ? 'pl-5' : 'pl-1.5'}`}>
      <GitBranch className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
      <span className="min-w-0 flex-1 space-y-0.5">
        {/* Line 1: name and its state; lines 2 and 3 use the full width so names and messages are not squeezed. */}
        <span className="flex min-w-0 items-baseline gap-2">
          <span className="min-w-0 flex-1 truncate text-xs font-medium">{repo.label}</span>
          {(repo.count || Boolean(repo.changed_files)) && <span title={[changedLabel(repo.changed_files, 'changed'), repo.count].filter(Boolean).join(' · ')} className="shrink-0 whitespace-nowrap text-[11px] text-muted-foreground">
            {repo.changed_files ? <span className="font-medium text-amber-600 underline-offset-2 group-hover/repo:underline dark:text-amber-400">{changedLabel(repo.changed_files, 'changed')}</span> : null}
            {repo.changed_files && repo.count ? ' · ' : ''}{repo.count}
          </span>}
        </span>
        {(repo.detail || repo.commit_at || stale(repo) > 0) && <span className="flex min-w-0 items-center gap-1 text-[11px] text-muted-foreground">
          <span className="min-w-0 truncate">{[repo.detail, repo.commit_at ? gitAgo(repo.commit_at) : ''].filter(Boolean).join(' · ')}</span>
          {stale(repo) > 0 && <span title={`Last fetched from its remote (such as GitHub) ${stale(repo)} ${stale(repo) === 1 ? 'day' : 'days'} ago, so this may be out of date. Ask the agent to fetch to refresh it.`} className="ml-auto flex shrink-0 items-center gap-0.5 whitespace-nowrap text-amber-600 dark:text-amber-400"><Clock className="h-2.5 w-2.5" aria-hidden="true" />checked {stale(repo)}d ago</span>}
        </span>}
        {repo.commit && <span title={repo.commit} className="block truncate text-[11px] italic text-muted-foreground/80">“{repo.commit}”</span>}
      </span>
      {/* A checkout with edits opens their review, so it shows a diff icon; a clean one opens its files. */}
      {repo.changed ? <FileDiff className="mt-0.5 h-3.5 w-3.5 shrink-0 text-amber-600 dark:text-amber-400" aria-label="Changes" />
        : <FileText className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground/60" aria-label="Files" />}
    </button>
  }
  const githubStrip = github && <div className="ml-3 mt-1 flex items-center justify-between gap-2 rounded-md border border-border bg-muted/40 px-2 py-1 text-[11px] text-muted-foreground">
    <span className="flex min-w-0 items-center gap-1.5 truncate">
      <span aria-hidden="true" className={`h-1.5 w-1.5 shrink-0 rounded-full ${github.connected ? 'bg-emerald-500' : 'bg-muted-foreground/50'}`} />
      {github.connected && github.login
        ? <span title={`GitHub: @${github.login}`} className="truncate">GitHub: <strong className="font-medium text-foreground">@{github.login}</strong></span>
        : <span className="truncate">GitHub: Not connected</span>}
    </span>
    {github.onOpen && <button type="button" onClick={github.onOpen} className="shrink-0 rounded px-1.5 py-0.5 text-[11px] font-medium text-foreground hover:bg-background/70 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">{github.connected ? 'Switch' : 'Connect'}</button>}
  </div>
  const rows: ReactNode[] = []
  const sorted = sortGitRows(section.rows)
  sorted.forEach((repo, i) => {
    if (repo.group && repo.group !== sorted[i - 1]?.group) {
      rows.push(<p key={`group:${repo.group}`} className="flex items-center gap-2 px-1.5 pt-2 text-xs font-semibold"><GitBranch className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />{repo.group}</p>)
    }
    rows.push(repoRow(repo))
  })
  // The Git line shows or hides the list; its arrow only shows which. The changed count next to it opens the review.
  const detail = section.detail || (section.error ? 'Unavailable' : '')
  const changed = changedLabel(section.changed, 'changed files')
  // The terminal sits on the Git line (owner, 20 Nov: "it should be in the git row only"), not as a row of its own.
  return <div>
    <div className="flex items-center">
    <div className="min-w-0 flex-1"><WorkspaceContextRow icon={Github} hint="This project’s repositories and worktrees, each with its branch and whether it is pushed. Click a repository to see its files." onClick={() => setState(open ? 'closed' : 'open')}
      detail={<span className="inline-flex max-w-full items-center justify-end gap-1.5"><span className="min-w-0 truncate" title={detail}>{detail}</span>
      <ChevronRight className={`h-3 w-3 shrink-0 transition-transform ${open ? 'rotate-90' : ''}`} aria-label={open ? 'Hide repositories' : 'Show repositories'} /></span>}>Git</WorkspaceContextRow></div>
    {/* The count is its own button: it opens one review of every file not committed yet, across the project. */}
    {/* Compact like a diff stat ("±12"); the full wording is its name and tooltip. */}
    {/* The warning opens the Git list, where the box explains it and offers Check again and Move out. */}
    {section.file_limit?.over && <button type="button" onClick={() => setState(open ? 'closed' : 'open')} aria-expanded={open} title="Changes are not fully tracked: too many files in this project folder. Click to see why and what to do." className="flex shrink-0 items-center gap-1 whitespace-nowrap rounded px-1 py-0.5 text-[11px] font-medium text-amber-600 underline-offset-2 hover:bg-background/40 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring dark:text-amber-400"><TriangleAlert className="h-3 w-3" aria-hidden="true" />not fully tracked</button>}
    {changed && !section.file_limit?.over && <button type="button" onClick={() => onOpen(undefined, true)} aria-label={`${changed}: review them`} title={`${changed} not committed yet. Click to review them.`} className="shrink-0 whitespace-nowrap rounded px-1 py-0.5 font-mono text-[11px] font-medium tabular-nums text-amber-600 underline-offset-2 hover:bg-background/40 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring dark:text-amber-400">±{section.changed}</button>}
    {onTerminal && <button type="button" onClick={onTerminal} aria-label="Open terminal" title="Open a terminal in this project folder (Ctrl+`)" className="flex h-8 w-7 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-background/40 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><SquareTerminal className="h-3.5 w-3.5" aria-hidden="true" /></button>}
    </div>
    {open && section.file_limit?.over && <FileLimitBox key={`${section.file_limit.checked_at}:${section.file_limit.can_fix}`} limit={section.file_limit} workspacePath={workspacePath} onAskAgent={onAskAgent} onOpenFolder={path => onOpen(path, false)} />}
    {open && githubStrip}
    {open && rows.length > 0 && <div className="mt-1 space-y-0.5 pl-3">{rows}</div>}
  </div>
}


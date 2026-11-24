import type { ReactNode } from 'react'
import { ChevronRight, Clock, FileDiff, FileText, Github, GitBranch, SquareTerminal } from 'lucide-react'
import type { CrewPanelSection, CrewPanelRow } from '../../products/work/useCrewPanel'
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
export function ChatChangesRow({ section, storageKey, onOpen, onTerminal }: { section?: CrewPanelSection; storageKey: string; onOpen: (repoRoot?: string, changed?: boolean) => void; onTerminal?: () => void }) {
  const [state, setState] = usePersistentTab(storageKey, 'open', states)
  if (!section || (!section.error && !section.rows.length)) return null
  const open = state === 'open'
  const stale = (repo: CrewPanelRow) => gitStaleDays(repo.fetched_at)
  const repoRow = (repo: CrewPanelRow) => <button type="button" key={repo.id} onClick={() => onOpen(repo.path || '', Boolean(repo.changed) || !repo.path)} title={repo.changed ? `Review the edits not committed yet in ${repo.path || repo.label}` : `Open the folder ${repo.path || repo.label}`}
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
    {changed && <button type="button" onClick={() => onOpen(undefined, true)} aria-label={`${changed}: review them`} title={`${changed} not committed yet. Click to review them.`} className="shrink-0 whitespace-nowrap rounded px-1 py-0.5 font-mono text-[11px] font-medium tabular-nums text-amber-600 underline-offset-2 hover:bg-background/40 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring dark:text-amber-400">±{section.changed}</button>}
    {onTerminal && <button type="button" onClick={onTerminal} aria-label="Open terminal" title="Open a terminal in this project folder (Ctrl+`)" className="flex h-8 w-7 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-background/40 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><SquareTerminal className="h-3.5 w-3.5" aria-hidden="true" /></button>}
    </div>
    {open && rows.length > 0 && <div className="mt-1 space-y-0.5 pl-3">{rows}</div>}
  </div>
}


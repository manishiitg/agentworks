import type { ReactNode } from 'react'
import { ChevronRight, Clock, FileText, Github, GitBranch } from 'lucide-react'
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

/** The project's Git map: every checkout with its branch and its state against
 * its remote, as the server labels them, grouped under their repository
 * (`group`). No file counts: reply cards show what each reply changed. The
 * Git title only shows or hides this list. Each checkout also shows when its
 * last commit was made and its message, and how old the remote check is once
 * it is more than a day old (the app never fetches); a repository row (with its file
 * icon) opens that checkout's files. Open or closed is remembered per project
 * in this browser. */
export function ChatChangesRow({ section, storageKey, onOpen }: { section?: CrewPanelSection; storageKey: string; onOpen: (repoRoot?: string, changed?: boolean) => void }) {
  const [state, setState] = usePersistentTab(storageKey, 'open', states)
  if (!section || (!section.error && !section.rows.length)) return null
  const open = state === 'open'
  const stale = (repo: CrewPanelRow) => gitStaleDays(repo.fetched_at)
  const repoRow = (repo: CrewPanelRow) => <button type="button" key={repo.id} onClick={() => onOpen(repo.path || '', Boolean(repo.changed) || !repo.path)} title={repo.changed ? `Review the edits not committed yet in ${repo.path || repo.label}` : `Open the folder ${repo.path || repo.label}`}
    className={`flex w-full min-w-0 items-start gap-2 rounded-lg py-2 pr-1.5 text-left hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${repo.group ? 'pl-5' : 'pl-1.5'}`}>
    <GitBranch className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
    <span className="min-w-0 flex-1 space-y-0.5">
      {/* Line 1: name and its state; lines 2 and 3 use the full width so names and messages are not squeezed. */}
      <span className="flex min-w-0 items-baseline gap-2">
        <span className="min-w-0 flex-1 truncate text-xs font-medium">{repo.label}</span>
        {repo.count && <span title={repo.count} className="shrink-0 whitespace-nowrap text-[11px] text-muted-foreground">{repo.count}</span>}
      </span>
      {(repo.detail || repo.commit_at || stale(repo) > 0) && <span className="flex min-w-0 items-center gap-1 text-[11px] text-muted-foreground">
        <span className="min-w-0 truncate">{[repo.detail, repo.commit_at ? gitAgo(repo.commit_at) : ''].filter(Boolean).join(' · ')}</span>
        {stale(repo) > 0 && <span title={`Last fetched from its remote (such as GitHub) ${stale(repo)} ${stale(repo) === 1 ? 'day' : 'days'} ago, so this may be out of date. Ask the agent to fetch to refresh it.`} className="ml-auto flex shrink-0 items-center gap-0.5 whitespace-nowrap text-amber-600 dark:text-amber-400"><Clock className="h-2.5 w-2.5" aria-hidden="true" />checked {stale(repo)}d ago</span>}
      </span>}
      {repo.commit && <span title={repo.commit} className="block truncate text-[11px] italic text-muted-foreground/80">“{repo.commit}”</span>}
    </span>
    <FileText className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground/60" aria-label="Files" />
  </button>
  const rows: ReactNode[] = []
  section.rows.forEach((repo, i) => {
    if (repo.group && repo.group !== section.rows[i - 1]?.group) {
      rows.push(<p key={`group:${repo.group}`} className="flex items-center gap-2 px-1.5 pt-2 text-xs font-semibold"><GitBranch className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />{repo.group}</p>)
    }
    rows.push(repoRow(repo))
  })
  // One place to click: the whole Git line shows or hides the list; its arrow only shows which.
  const detail = section.detail || (section.error ? 'Unavailable' : '')
  return <div>
    <WorkspaceContextRow icon={Github} hint="This project’s repositories and worktrees, each with its branch and whether it is pushed. Click a repository to see its files." onClick={() => setState(open ? 'closed' : 'open')}
      detail={<span className="inline-flex max-w-full items-center justify-end gap-1.5"><span className="truncate">{detail}</span><ChevronRight className={`h-3 w-3 shrink-0 transition-transform ${open ? 'rotate-90' : ''}`} aria-label={open ? 'Hide repositories' : 'Show repositories'} /></span>}>Git</WorkspaceContextRow>
    {open && rows.length > 0 && <div className="mt-1 space-y-0.5 pl-3">{rows}</div>}
  </div>
}

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
export function ChatChangesRow({ section, storageKey, onOpen }: { section?: CrewPanelSection; storageKey: string; onOpen: (repoRoot?: string) => void }) {
  const [state, setState] = usePersistentTab(storageKey, 'open', states)
  if (!section || (!section.error && !section.rows.length)) return null
  const open = state === 'open'
  const stale = (repo: CrewPanelRow) => gitStaleDays(repo.fetched_at)
  const repoRow = (repo: CrewPanelRow) => <button type="button" key={repo.id} onClick={() => onOpen(repo.path || '')} title={`Open the files of ${repo.path || repo.label}`}
    className={`flex w-full min-w-0 items-start gap-2 rounded-lg py-1.5 pr-2 text-left hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${repo.group ? 'pl-6' : 'pl-2'}`}>
    <GitBranch className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
    <span className="min-w-[3rem] flex-1">
      <span className="block truncate text-xs">{repo.label}</span>
      {(repo.detail || repo.commit_at) && <span className="block truncate text-[11px] text-muted-foreground">{[repo.detail, repo.commit_at ? gitAgo(repo.commit_at) : ''].filter(Boolean).join(' · ')}</span>}
      {repo.commit && <span title={repo.commit} className="block truncate text-[11px] italic text-muted-foreground/80">“{repo.commit}”</span>}
    </span>
    {(repo.count || stale(repo) > 0) && <span className="flex min-w-0 max-w-[50%] flex-col items-end text-right text-[11px]">
      {repo.count && <span title={repo.count} className="max-w-full truncate text-muted-foreground">{repo.count}</span>}
      {stale(repo) > 0 && <span title={`Last fetched from its remote (such as GitHub) ${stale(repo)} ${stale(repo) === 1 ? 'day' : 'days'} ago, so this may be out of date. Ask the agent to fetch to refresh it.`} className="flex items-center gap-0.5 whitespace-nowrap text-amber-600 dark:text-amber-400"><Clock className="h-2.5 w-2.5" aria-hidden="true" />checked {stale(repo)}d ago</span>}
    </span>}
    <FileText className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground/70" aria-label="Files" />
  </button>
  const rows: ReactNode[] = []
  section.rows.forEach((repo, i) => {
    if (repo.group && repo.group !== section.rows[i - 1]?.group) {
      rows.push(<p key={`group:${repo.group}`} className="flex items-center gap-2 px-2 pt-1.5 text-xs font-medium"><GitBranch className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />{repo.group}</p>)
    }
    rows.push(repoRow(repo))
  })
  return <div>
    <div className="flex items-center">
      <div className="min-w-0 flex-1">
        <WorkspaceContextRow icon={Github} hint="This project’s repositories and worktrees, each with its branch and whether it is pushed. Click a repository to see its files." onClick={() => setState(open ? 'closed' : 'open')} detail={section.detail || (section.error ? 'Unavailable' : undefined)}>Git</WorkspaceContextRow>
      </div>
      <button type="button" aria-expanded={open} aria-label={open ? 'Collapse repositories' : 'Expand repositories'} onClick={() => setState(open ? 'closed' : 'open')}
        className="flex h-8 w-6 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <ChevronRight className={`h-3 w-3 transition-transform ${open ? 'rotate-90' : ''}`} aria-hidden="true" />
      </button>
    </div>
    {open && rows.length > 0 && <div className="mt-0.5 pl-4">{rows}</div>}
  </div>
}

import { ChevronRight, FileDiff, GitBranch } from 'lucide-react'
import type { CrewPanelSection, CrewPanelRow } from '../../products/work/useCrewPanel'
import { WorkspaceContextRow } from './WorkspaceContextCard'

/** The server reads current Git state and checks access to private-chat usage
 * hints. Opening this row does not start another request or polling timer. */
export function ChatChangesRow({ section, onOpen }: { section?: CrewPanelSection; onOpen: (repoRoot?: string) => void }) {
  if (!section || (!section.error && !section.rows.length)) return null
  const used = section.rows.filter(repo => repo.used_in_chat)
  const others = section.rows.filter(repo => !repo.used_in_chat)
  const repoRow = (repo: CrewPanelRow) => <button type="button" key={repo.id} onClick={() => onOpen(repo.path || '')} title={repo.path}
    className="flex w-full min-w-0 items-center gap-2 rounded-lg px-2 py-2 text-left hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
    <GitBranch className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
    <span className="min-w-0 flex-1"><span className="block truncate text-sm">{repo.label}</span><span className="block truncate text-xs text-muted-foreground">{repo.detail}</span></span>
  </button>
  return <div>
    <WorkspaceContextRow icon={FileDiff} hint="Current staged, unstaged and new files in this project's Git repositories. Untracked dependencies and caches are excluded. Committing clears these changes; Branch / PR shows committed work." onClick={() => onOpen()} detail={section.detail || (section.error ? 'Unavailable' : undefined)}>{section.title}</WorkspaceContextRow>
    {used.length > 0 && <div className="mt-1"><p className="px-2 py-1 text-xs text-muted-foreground">Used in this chat</p>{used.map(repoRow)}</div>}
    {others.length > 0 && <details className="mt-1 group/other"><summary className="flex cursor-pointer list-none items-center gap-1 px-2 py-2 text-xs text-muted-foreground"><ChevronRight className="h-3 w-3 group-open/other:rotate-90" aria-hidden="true" />Other repositories <span className="ml-auto">{others.length}</span></summary>{others.map(repoRow)}</details>}
  </div>
}

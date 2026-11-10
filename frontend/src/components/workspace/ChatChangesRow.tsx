import type { ReactNode } from 'react'
import { ChevronRight, FileDiff, GitBranch } from 'lucide-react'
import type { CrewPanelSection, CrewPanelRow } from '../../products/work/useCrewPanel'
import { usePersistentTab } from '../../hooks/usePersistentTab'
import { WorkspaceContextRow } from './WorkspaceContextCard'

const states = ['open', 'closed'] as const

/** Current uncommitted Git changes in this project. The server lists only
 * checkouts with changes, labels them and names the repository a row sits
 * under (`group`); nothing changed means no rows and no section. The arrow
 * opens and closes the list, remembered per project in this browser. */
export function ChatChangesRow({ section, storageKey, onOpen }: { section?: CrewPanelSection; storageKey: string; onOpen: (repoRoot?: string) => void }) {
  const [state, setState] = usePersistentTab(storageKey, 'open', states)
  if (!section || (!section.error && !section.rows.length)) return null
  const open = state === 'open'
  const repoRow = (repo: CrewPanelRow) => <button type="button" key={repo.id} onClick={() => onOpen(repo.path || '')} title={repo.path}
    className={`flex w-full min-w-0 items-start gap-2 rounded-lg py-1.5 pr-2 text-left hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${repo.group ? 'pl-6' : 'pl-2'}`}>
    <GitBranch className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
    <span className="min-w-0 flex-1"><span className="block truncate text-xs">{repo.label}</span>{repo.detail && <span className="block truncate text-[11px] text-muted-foreground">{repo.detail}</span>}</span>
    {repo.count && <span className="shrink-0 text-[11px] text-muted-foreground">{repo.count}</span>}
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
      <button type="button" aria-expanded={open} aria-label={open ? 'Hide changed repositories' : 'Show changed repositories'} onClick={() => setState(open ? 'closed' : 'open')}
        className="flex h-6 w-5 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <ChevronRight className={`h-3 w-3 transition-transform ${open ? 'rotate-90' : ''}`} aria-hidden="true" />
      </button>
      <div className="min-w-0 flex-1">
        <WorkspaceContextRow icon={FileDiff} hint="Uncommitted files in this project's Git repositories. Untracked dependencies and caches are excluded. Committing clears these changes; Branch / PR shows committed work." onClick={() => onOpen()} detail={section.detail || (section.error ? 'Unavailable' : undefined)}>{section.title}</WorkspaceContextRow>
      </div>
    </div>
    {open && rows.length > 0 && <div className="mt-0.5 pl-4">{rows}</div>}
  </div>
}

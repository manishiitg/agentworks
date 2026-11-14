import type { ReactNode } from 'react'
import { ChevronRight, Github, GitBranch } from 'lucide-react'
import type { CrewPanelSection, CrewPanelRow } from '../../products/work/useCrewPanel'
import { usePersistentTab } from '../../hooks/usePersistentTab'
import { WorkspaceContextRow } from './WorkspaceContextCard'

const states = ['open', 'closed'] as const

/** The project's Git map: every checkout with its branch and its state against
 * its remote, as the server labels them, grouped under their repository
 * (`group`). No file counts: reply cards show what each reply changed. The
 * arrow is remembered per project in this browser. */
export function ChatChangesRow({ section, storageKey, onOpen }: { section?: CrewPanelSection; storageKey: string; onOpen: (repoRoot?: string) => void }) {
  const [state, setState] = usePersistentTab(storageKey, 'open', states)
  if (!section || (!section.error && !section.rows.length)) return null
  const open = state === 'open'
  const repoRow = (repo: CrewPanelRow) => <button type="button" key={repo.id} onClick={() => onOpen(repo.path || '')} title={repo.path}
    className={`flex w-full min-w-0 items-start gap-2 rounded-lg py-1.5 pr-2 text-left hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${repo.group ? 'pl-6' : 'pl-2'}`}>
    <GitBranch className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
    <span className="min-w-[3rem] flex-1"><span className="block truncate text-xs">{repo.label}</span>{repo.detail && <span className="block truncate text-[11px] text-muted-foreground">{repo.detail}</span>}</span>
    {repo.count && <span title={repo.count} className="min-w-0 max-w-[60%] truncate text-right text-[11px] text-muted-foreground">{repo.count}</span>}
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
        <WorkspaceContextRow icon={Github} hint="This project’s repositories and worktrees, each with its branch and whether it is pushed. Open one to see its uncommitted files." onClick={() => onOpen()} detail={section.detail || (section.error ? 'Unavailable' : undefined)}>Git</WorkspaceContextRow>
      </div>
      <button type="button" aria-expanded={open} aria-label={open ? 'Collapse repositories' : 'Expand repositories'} onClick={() => setState(open ? 'closed' : 'open')}
        className="flex h-8 w-6 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <ChevronRight className={`h-3 w-3 transition-transform ${open ? 'rotate-90' : ''}`} aria-hidden="true" />
      </button>
    </div>
    {open && rows.length > 0 && <div className="mt-0.5 pl-4">{rows}</div>}
  </div>
}

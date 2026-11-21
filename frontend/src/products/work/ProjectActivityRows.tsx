import { Activity, ExternalLink, FolderOpen, ListTodo, Mail, MessageSquare, type LucideIcon } from 'lucide-react'
import { WorkspaceCompactGroup } from '../../components/workspace/WorkspaceCompactGroup'
import type { CrewPanelSection } from './useCrewPanel'

// Project activity (#3476, approved mockup 21 Nov): one row per feed Watcher the
// person's agent set up. The server decides the rows and their state; the feed is
// the person's own even in a shared Crew.
const appIcon: Record<string, LucideIcon> = { slack: MessageSquare, mail: Mail, drive: FolderOpen, tasks: ListTodo }

export const projectActivitySetupMessage = 'Set up Project activity for this project. Check which apps this project can reach (Slack, Gmail, Google Drive, GitHub or another task tracker), ask me which channels, mail, folders and task lists matter, then create one feed Watcher for each. Follow the project-activity guide in the automations skill.'
export const projectActivityChangeMessage = 'I want to change what Project activity watches for this project. Show me what it watches now and ask what to add, change or remove.'

function ago(at: number): string {
  const minutes = Math.max(0, Math.round((Date.now() / 1000 - at) / 60))
  if (minutes < 1) return 'now'
  if (minutes < 60) return `${minutes}m`
  const hours = Math.round(minutes / 60)
  return hours < 24 ? `${hours}h` : `${Math.round(hours / 24)}d`
}

export function ProjectActivityRows({ section, onAgent, onWatchers }: {
  section?: CrewPanelSection
  onAgent?: (message: string) => void
  onWatchers: () => void
}) {
  if (!section) return null
  const rows = section.rows
  if (!rows.length) {
    if (!onAgent) return null
    return <div>
      <div className="flex min-h-8 items-center gap-2.5 px-1 text-xs text-foreground/85"><Activity aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-muted-foreground"/><span>Project activity</span></div>
      <div className="flex items-center gap-2 pb-1 pl-7 pr-1 text-[10px] text-muted-foreground">
        <span className="min-w-0 flex-1">Slack, mail, Drive and tasks for this project</span>
        <button type="button" onClick={() => onAgent(projectActivitySetupMessage)} className="shrink-0 rounded-full border border-border px-2 py-0.5 text-[10px] font-medium text-primary hover:bg-background/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">Set up with agent</button>
      </div>
    </div>
  }
  return <div>
    {rows.map(row => <WorkspaceCompactGroup key={row.id} title={row.label} icon={appIcon[row.group || ''] || Activity}
      detail={<span className={row.attention ? 'text-amber-600 dark:text-amber-400' : row.count ? 'font-medium text-foreground' : undefined}>{row.attention ? row.detail : row.count || row.detail}</span>}>
      {row.attention && <button type="button" onClick={onWatchers} className="block w-full rounded px-1 py-0.5 text-left text-[11px] text-amber-600 hover:bg-background/40 dark:text-amber-400">{row.detail} · open Watchers</button>}
      {(row.items || []).map((item, index) => {
        const body = <><span className="block truncate text-foreground">{item.title}</span>
          <span className="flex gap-1 text-[10px] text-muted-foreground">{item.sub && <span className="min-w-0 flex-1 truncate">{item.sub}</span>}<span className="ml-auto shrink-0">{ago(item.at)}</span></span></>
        return item.url
          ? <a key={index} href={item.url} target="_blank" rel="noopener noreferrer" className="block rounded px-1 py-0.5 text-[11px] hover:bg-background/40">{body}</a>
          : <div key={index} className="px-1 py-0.5 text-[11px]">{body}</div>
      })}
      {!row.items?.length && !row.attention && <p className="px-1 py-0.5 text-[11px] text-muted-foreground">{row.detail}</p>}
      {row.path && <a href={row.path} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 px-1 py-0.5 text-[10px] text-primary hover:underline">Open<ExternalLink aria-hidden="true" className="h-2.5 w-2.5"/></a>}
    </WorkspaceCompactGroup>)}
    {onAgent && <button type="button" onClick={() => onAgent(projectActivityChangeMessage)} className="pl-7 text-[10px] text-muted-foreground hover:text-foreground">Change with agent</button>}
  </div>
}

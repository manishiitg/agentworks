import { useEffect, useRef, useState } from 'react'
import { Activity, ExternalLink, FolderOpen, ListTodo, Mail, MessageSquare, MoreHorizontal, type LucideIcon } from 'lucide-react'
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

type ActivityItem = NonNullable<CrewPanelSection['rows'][number]['items']>[number]

// #3530 (approved mockup 22 Nov): each item can be handed to the agent. A choice
// only drafts a message in the Crew's own chat; the person edits and sends it,
// and the agent does the work with the task tools it already has.
export function projectActivityItemMessage(kind: 'ask' | 'mine' | 'team', source: string, item: ActivityItem): string {
  const what = `${source}: "${item.title}"${item.url ? ` (${item.url})` : ''}`
  const issue = item.title.match(/#\d+/)?.[0]
  if (kind === 'ask') return `Look at this from ${what}. Tell me what it needs and what you suggest; don't change anything yet.`
  if (kind === 'mine') return `Add this to my tasks: ${what}.${issue ? ` Link it to ${issue}.` : ''}`
  return `Make a Team task for this so a teammate's agent can take it: ${what}.${issue ? ` Put it under ${issue}.` : ''}`
}

const itemChoices = [
  { kind: 'ask', label: 'Ask my agent', detail: 'What does this need?' },
  { kind: 'mine', label: 'Add to my tasks', detail: 'Shows in Team › Tasks, by you' },
  { kind: 'team', label: 'Make a Team task', detail: "Any teammate's agent can take it" },
] as const

function ItemMenu({ onPick }: { onPick: (kind: 'ask' | 'mine' | 'team') => void }) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const down = (event: MouseEvent) => { if (ref.current && !ref.current.contains(event.target as Node)) setOpen(false) }
    const key = (event: KeyboardEvent) => { if (event.key === 'Escape') setOpen(false) }
    document.addEventListener('mousedown', down)
    document.addEventListener('keydown', key)
    return () => { document.removeEventListener('mousedown', down); document.removeEventListener('keydown', key) }
  }, [open])
  return <div ref={ref} className="relative shrink-0">
    <button type="button" aria-label="Hand this to your agent" aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen(value => !value)}
      className={`rounded p-0.5 text-muted-foreground hover:bg-background/60 hover:text-foreground focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring [@media(hover:none)]:opacity-100 ${open ? 'opacity-100' : 'opacity-0 group-hover:opacity-100'}`}>
      <MoreHorizontal aria-hidden="true" className="h-3.5 w-3.5"/>
    </button>
    {open && <div role="menu" className="absolute right-0 top-5 z-20 w-52 rounded-md border border-border bg-popover p-1 text-popover-foreground shadow-lg">
      {itemChoices.map(choice => <button key={choice.kind} type="button" role="menuitem" onClick={() => { setOpen(false); onPick(choice.kind) }}
        className="block w-full rounded px-2 py-1 text-left text-[11px] hover:bg-muted focus-visible:bg-muted focus-visible:outline-none">
        {choice.label}<span className="block text-[10px] text-muted-foreground">{choice.detail}</span>
      </button>)}
    </div>}
  </div>
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
        const content = item.url
          ? <a href={item.url} target="_blank" rel="noopener noreferrer" className="block min-w-0 flex-1 px-1 py-0.5">{body}</a>
          : <div className="min-w-0 flex-1 px-1 py-0.5">{body}</div>
        return <div key={index} className="group flex items-start gap-0.5 rounded text-[11px] hover:bg-background/40 focus-within:bg-background/40">
          {content}
          {onAgent && <ItemMenu onPick={kind => onAgent(projectActivityItemMessage(kind, row.label, item))}/>}
        </div>
      })}
      {!row.items?.length && !row.attention && <p className="px-1 py-0.5 text-[11px] text-muted-foreground">{row.detail}</p>}
      {row.path && <a href={row.path} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 px-1 py-0.5 text-[10px] text-primary hover:underline">Open<ExternalLink aria-hidden="true" className="h-2.5 w-2.5"/></a>}
    </WorkspaceCompactGroup>)}
    {onAgent && <button type="button" onClick={() => onAgent(projectActivityChangeMessage)} className="pl-7 text-[10px] text-muted-foreground hover:text-foreground">Change with agent</button>}
  </div>
}

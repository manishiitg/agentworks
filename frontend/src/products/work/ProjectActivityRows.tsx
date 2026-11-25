import { useEffect, useRef, useState } from 'react'
import { Activity, ExternalLink, FolderOpen, ListChecks, ListTodo, Mail, MessageSquare, MoreHorizontal, type LucideIcon } from 'lucide-react'
import { WorkspaceCompactGroup } from '../../components/workspace/WorkspaceCompactGroup'
import { SetupPrompt } from '../../components/workspace/SetupPrompt'
import type { CrewPanelFeedItem, CrewPanelRow, CrewPanelSection } from './useCrewPanel'
import { ProjectTasksRow, projectActivityChangeMessage } from './ProjectTasksRow'

// Project activity (#3476, approved mockup 21 Nov): one row per feed Watcher the
// person's agent set up. The server decides the rows and their state; the feed is
// the person's own even in a shared Crew.
const appIcon: Record<string, LucideIcon> = { slack: MessageSquare, mail: Mail, drive: FolderOpen, tasks: ListTodo, 'project-tasks': ListChecks }

export const projectActivityLine = 'Slack, mail, Drive and tasks for this project'

export const projectActivitySetupMessage = 'Set up Project activity for this project. Check which apps this project can reach (Slack, Gmail, Google Drive, GitHub or another task tracker), ask me which channels, mail, folders and task lists matter, then set up Project tasks: a dedicated lead agent that keeps one shared task list and hands out the work, fed by a Watcher or a schedule per source, whichever fits best. Follow the project-activity guide in the automations skill.'

function ago(at: number): string {
  const minutes = Math.max(0, Math.round((Date.now() / 1000 - at) / 60))
  if (minutes < 1) return 'now'
  if (minutes < 60) return `${minutes}m`
  const hours = Math.round(minutes / 60)
  return hours < 24 ? `${hours}h` : `${Math.round(hours / 24)}d`
}

type ActivityItem = CrewPanelFeedItem

// #3530 (approved mockup 22 Nov): each item can be handed to the agent. A choice
// only drafts a message in the Crew's own chat; the person edits and sends it,
// and the agent does the work with the task tools it already has. #3625: the
// draft names the row and item so the agent links its task and moves the item
// through the row's steps.
export function projectActivityItemMessage(kind: 'ask' | 'mine' | 'team', source: string, item: ActivityItem, rowID = ''): string {
  const what = `${source}: "${item.title}"${item.url ? ` (${item.url})` : ''}`
  const issue = item.title.match(/#\d+/)?.[0]
  const where = rowID && item.id ? ` Link the task to this Project activity item and move it through the row's steps (row ${rowID.replace(/^feed:/, '')}, item ${item.id}).` : ''
  if (kind === 'ask') return `Look at this from ${what}. Tell me what it needs and what you suggest; don't change anything yet.`
  if (kind === 'mine') return `Add this to my tasks: ${what}.${issue ? ` Link it to ${issue}.` : ''}${where}`
  return `Make a Team task for this so a teammate's agent can take it: ${what}.${issue ? ` Put it under ${issue}.` : ''}${where}`
}


const taskWords: Record<string, string> = { open: 'asked', in_progress: 'working', blocked: 'blocked', needs_you: 'waiting on you', stalled: 'stalled', done: 'done' }
const taskTone = (state: string) => state === 'done' ? 'text-emerald-600 dark:text-emerald-400' : state === 'in_progress' || state === 'open' ? 'text-sky-600 dark:text-sky-400' : 'font-medium text-amber-600 dark:text-amber-400'

function ItemDetail({ item, steps }: { item: ActivityItem; steps: string[] }) {
  const at = Math.max(0, steps.indexOf(item.step || steps[0]))
  return <div className="space-y-1.5 px-1 pb-1.5 pt-0.5 text-[10px]">
    <ol aria-label="Steps" className="flex flex-wrap items-center gap-1">
      {steps.map((step, index) => <li key={step} aria-current={index === at ? 'step' : undefined}
        className={`rounded px-1.5 ${index === at ? (item.done ? 'bg-emerald-500/15 font-medium text-emerald-700 dark:text-emerald-300' : 'bg-sky-500/15 font-medium text-foreground') : index < at ? 'text-emerald-700 dark:text-emerald-400' : 'text-muted-foreground'}`}>{index < at ? '✓ ' : ''}{step}</li>)}
    </ol>
    {item.tasks?.length ? <ul aria-label="Tasks" className="space-y-0.5">
      {item.tasks.map((task, index) => <li key={index} className="flex gap-1.5 rounded border border-border px-1.5 py-0.5"><span className="min-w-0 flex-1 truncate text-foreground">{task.title}<span className="block text-muted-foreground">{task.agent}</span></span><span className={`shrink-0 self-center ${taskTone(task.state)}`}>{taskWords[task.state] || task.state}</span></li>)}
    </ul> : <p className="text-muted-foreground">No task yet. Use ⋯ to hand it to your agent.</p>}
    {item.history?.length ? <ul aria-label="History" className="space-y-0.5 border-l border-border pl-2">
      {item.history.map((move, index) => <li key={index}><span className="text-foreground">{move.by || 'Your agent'}{move.step ? ` → ${move.step}` : ''}</span> <span className="text-muted-foreground">{ago(move.at)}</span>{move.note && <span className="block text-muted-foreground">{move.note}</span>}</li>)}
    </ul> : null}
    {item.url && <a href={item.url} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 text-primary hover:underline">Open<ExternalLink aria-hidden="true" className="h-2.5 w-2.5"/></a>}
  </div>
}

function ItemRow({ item, row, onAgent }: { item: ActivityItem; row: CrewPanelRow; onAgent?: (message: string) => void }) {
  const [open, setOpen] = useState(false)
  const steps = row.steps?.length ? row.steps : []
  const task = item.tasks?.find(t => t.state !== 'done')
  return <div className="group rounded text-[11px] hover:bg-background/40 focus-within:bg-background/40">
    <div className="flex items-start gap-0.5">
      <button type="button" aria-expanded={open} onClick={() => setOpen(value => !value)} className="block min-w-0 flex-1 px-1 py-0.5 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <span className="block truncate text-foreground">{item.title}</span>
        <span className="flex items-center gap-1 whitespace-nowrap text-[10px] text-muted-foreground">
          {item.step && <span className={item.done ? 'text-emerald-600 dark:text-emerald-400' : 'rounded bg-muted px-1 text-foreground'}>{item.done ? '✓ ' : ''}{item.step}</span>}
          {task ? <span className={`min-w-0 truncate ${taskTone(task.state)}`}>· {task.agent} {taskWords[task.state] || task.state}</span>
            : !item.done && !item.tasks?.length ? <span className="min-w-0 truncate">· {item.sub || 'not picked up'}</span> : null}
          <span className="ml-auto shrink-0">{ago(item.at)}</span>
        </span>
      </button>
      {onAgent && <ItemMenu onPick={kind => onAgent(projectActivityItemMessage(kind, row.label, item, row.id))}/>}
    </div>
    {open && steps.length > 0 && <ItemDetail item={item} steps={steps}/>}
  </div>
}

function RowItems({ row, onAgent }: { row: CrewPanelRow; onAgent?: (message: string) => void }) {
  const [showDone, setShowDone] = useState(false)
  const items = row.items || []
  const working = items.filter(item => !item.done), done = items.filter(item => item.done)
  const last = row.steps?.[row.steps.length - 1] || 'done'
  return <>
    {row.steps?.length ? <p className="px-1 pb-0.5 text-[10px] text-muted-foreground">{row.steps.join(' → ')}</p> : null}
    {working.map((item, index) => <ItemRow key={item.id || index} item={item} row={row} onAgent={onAgent}/>)}
    {row.done_count ? <button type="button" aria-expanded={showDone} onClick={() => setShowDone(value => !value)} className="px-1 py-0.5 text-[10px] text-muted-foreground hover:text-foreground">✓ {row.done_count} {last.toLowerCase()} {showDone ? '▾' : '▸'}</button> : null}
    {showDone && done.map((item, index) => <ItemRow key={item.id || index} item={item} row={row} onAgent={onAgent}/>)}
  </>
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

export function ProjectActivityRows({ section, onAgent, onWatchers, hidden, onHide, onOpenTask }: {
  section?: CrewPanelSection
  onAgent?: (message: string) => void
  onWatchers: () => void
  hidden: boolean
  onHide: () => void
  onOpenTask: (id: string) => void
}) {
  if (!section) return null
  const rows = section.rows
  if (!rows.length) {
    if (!onAgent || hidden) return null
    return <SetupPrompt icon={Activity} title="Project activity" line={projectActivityLine} action="Set up" onAction={() => onAgent(projectActivitySetupMessage)} onHide={onHide}
      what="One shared task list for this project, kept up to date for you."
      points={['It keeps an eye on the Slack channels, mail and task lists you pick', 'New requests become tasks, and an agent picks each one up', 'You see who is on what; only real decisions come to you']}/>
  }
  return <div>
    {rows.map(row => <WorkspaceCompactGroup key={row.id} title={row.label} icon={appIcon[row.group || ''] || Activity}
      detail={<span className={row.attention ? 'text-amber-600 dark:text-amber-400' : row.count ? 'font-medium text-foreground' : undefined}>
        {!row.attention && row.needs_you_count ? <span className="text-amber-600 dark:text-amber-400">{row.needs_you_count} need{row.needs_you_count === 1 ? 's' : ''} you</span> : null}
        {/* "3 need you" alone when every open task needs you; the count only adds what the amber part doesn't say. */}
        {row.attention ? row.detail : row.needs_you_count && row.needs_you_count === row.open_count ? null : <>{!row.attention && row.needs_you_count ? ' · ' : ''}{row.count || row.detail}</>}</span>}>
      {row.attention && <button type="button" onClick={onWatchers} className="block w-full rounded px-1 py-0.5 text-left text-[11px] text-amber-600 hover:bg-background/40 dark:text-amber-400">{row.detail} · open Watchers</button>}
      {row.group === 'project-tasks' ? <ProjectTasksRow row={row} onAgent={onAgent} onOpenTask={onOpenTask}/> : <>
        <RowItems row={row} onAgent={onAgent}/>
        {!row.items?.length && !row.attention && <p className="px-1 py-0.5 text-[11px] text-muted-foreground">{row.detail}</p>}
      </>}
      <div className="flex items-center gap-3 px-1 py-0.5 text-[10px]">
        {row.path && <a href={row.path} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 text-primary hover:underline">Open<ExternalLink aria-hidden="true" className="h-2.5 w-2.5"/></a>}
        {onAgent && row.group !== 'project-tasks' && <button type="button" onClick={() => onAgent(projectActivityChangeMessage)} className="text-muted-foreground hover:text-foreground">Change setup</button>}
      </div>
    </WorkspaceCompactGroup>)}
  </div>
}

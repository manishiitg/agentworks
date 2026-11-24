import { useState } from 'react'
import { ExternalLink, Pause, Play } from 'lucide-react'
import type { CrewPanelRow, ProjectTask, ProjectTaskRef } from './useCrewPanel'

// Project tasks (approved mockup 24 Nov): one shared, curated list per project.
// Watchers send what they find to the lead agent, which creates, merges or drops;
// each task shows where it came from, what is linked to it, what is happening on
// those links and who moved it. The server decides the list; this only shows it.
// #3732: the lead line (auto or ask first, last sorted, Pause), a Needs you group
// the server decides, and Reassign / Approve / Hold per task. Like the activity
// item menu, each control drafts a message in the project chat; the agent does
// the change with the same task actions.
// #3756: a checklist inside a task (small steps an agent ticks off itself), shown
// as "2/3" on the line and as a list when the task is open.

type Draft = (message: string) => void

export function projectTaskMessage(kind: 'approve' | 'reassign' | 'hold' | 'unhold', task: ProjectTask): string {
  const what = `Project task ${task.id} "${task.title}"`
  if (kind === 'approve') return `Approve handing ${what} to ${task.proposed}: set them as assignee, send them the ask and link it to the task.`
  if (kind === 'reassign') return `Reassign ${what}${task.assignee ? ` (now with ${task.assignee})` : ''}. Suggest the best-fitting agent by purpose and ask me which one, then hand it over, link the new ask and tell the old agent.`
  if (kind === 'hold') return `Put ${what} on hold (task_update held=true) and tell whoever has it to stop for now.`
  return `Take ${what} off hold (task_update held=false) and let whoever has it carry on.`
}

export function projectLeadMessage(kind: 'pause' | 'resume' | 'auto' | 'ask', lead = 'the lead'): string {
  if (kind === 'pause') return `Pause the Project tasks lead (${lead}): task_settings paused=true. New events wait until I resume it.`
  if (kind === 'resume') return `Resume the Project tasks lead (${lead}): task_settings paused=false.`
  if (kind === 'ask') return `Switch Project tasks to ask me first: task_settings mode=ask. ${lead} should propose an agent for each new task and wait for my approval.`
  return `Switch Project tasks to auto: task_settings mode=auto. ${lead} hands new tasks out by itself.`
}

const kindLetter: Record<string, string> = { slack: 'S', mail: 'M', tasks: 'T', github: 'G', notion: 'N', drive: 'D', other: '•' }
const kindTone: Record<string, string> = { slack: 'bg-purple-500/15 text-purple-700 dark:text-purple-300', mail: 'bg-blue-500/15 text-blue-700 dark:text-blue-300', tasks: 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-300', github: 'bg-foreground/10 text-foreground', notion: 'bg-foreground/10 text-foreground', drive: 'bg-amber-500/15 text-amber-700 dark:text-amber-300', other: 'bg-muted text-muted-foreground' }
const kindName: Record<string, string> = { slack: 'Slack', mail: 'Mail', tasks: 'Task tracker', github: 'GitHub', notion: 'Notion', drive: 'Drive', other: 'Other' }

function Kind({ kind }: { kind: string }) {
  return <span title={kindName[kind] || kind} aria-label={kindName[kind] || kind}
    className={`mt-px flex h-3.5 w-3.5 shrink-0 items-center justify-center rounded-[3px] text-[8px] font-bold ${kindTone[kind] || kindTone.other}`}>{kindLetter[kind] || kindLetter.other}</span>
}

function ago(at: number): string {
  const minutes = Math.max(0, Math.round((Date.now() / 1000 - at) / 60))
  if (minutes < 1) return 'now'
  if (minutes < 60) return `${minutes}m`
  const hours = Math.round(minutes / 60)
  return hours < 24 ? `${hours}h` : `${Math.round(hours / 24)}d`
}

const taskWords: Record<string, string> = { open: 'asked', asked: 'asked', in_progress: 'working', blocked: 'blocked', needs_you: 'waiting on you', stalled: 'stalled', done: 'done' }

function Ref({ item }: { item: ProjectTaskRef }) {
  const body = <><Kind kind={item.kind}/><span className="min-w-0 flex-1 truncate text-foreground">{item.title}</span>{item.by && <span className="shrink-0 text-muted-foreground">{item.by}</span>}</>
  return item.url
    ? <a href={item.url} target="_blank" rel="noopener noreferrer" className="flex items-center gap-1.5 rounded border border-border px-1.5 py-0.5 hover:bg-background/40">{body}<ExternalLink aria-hidden="true" className="h-2.5 w-2.5 shrink-0 text-primary"/></a>
    : <div className="flex items-center gap-1.5 rounded border border-border px-1.5 py-0.5">{body}</div>
}

function TaskActions({ task, onAgent }: { task: ProjectTask; onAgent?: Draft }) {
  if (!onAgent || task.done) return null
  const button = 'rounded border border-border px-1.5 py-px hover:bg-background/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
  return <div className="flex flex-wrap gap-1 pt-0.5">
    {task.proposed && !task.held && <button type="button" onClick={() => onAgent(projectTaskMessage('approve', task))} className={`${button} font-medium text-primary`}>Approve → {task.proposed}</button>}
    <button type="button" onClick={() => onAgent(projectTaskMessage('reassign', task))} className={`${button} text-foreground`}>Reassign</button>
    <button type="button" onClick={() => onAgent(projectTaskMessage(task.held ? 'unhold' : 'hold', task))} className={`${button} text-muted-foreground`}>{task.held ? 'Take off hold' : 'Hold'}</button>
  </div>
}

function TaskDetail({ task, steps, onAgent }: { task: ProjectTask; steps: string[]; onAgent?: Draft }) {
  const at = Math.max(0, steps.indexOf(task.step || steps[0]))
  const head = 'pt-1 text-[10px] font-medium text-foreground'
  return <div className="space-y-1 px-1 pb-1.5 pt-0.5 text-[10px]">
    <ol aria-label="Steps" className="flex flex-wrap items-center gap-1">
      {steps.map((step, index) => <li key={step} aria-current={index === at ? 'step' : undefined}
        className={`rounded px-1.5 ${index === at ? (task.done ? 'bg-emerald-500/15 font-medium text-emerald-700 dark:text-emerald-300' : 'bg-sky-500/15 font-medium text-foreground') : index < at ? 'text-emerald-700 dark:text-emerald-400' : 'text-muted-foreground'}`}>{index < at ? '✓ ' : ''}{step}</li>)}
    </ol>
    {task.needs_you && <p className="font-medium text-amber-600 dark:text-amber-400">{task.needs_you}</p>}
    <TaskActions task={task} onAgent={onAgent}/>
    {task.tasks?.map((t, index) => <p key={index} className="text-muted-foreground"><span className="text-foreground">{t.agent}</span> · {taskWords[t.state] || t.state}{t.title ? ` · ${t.title}` : ''}</p>)}
    {task.checklist?.length ? <><p className={head}>Checklist</p><ul aria-label="Checklist" className="space-y-0.5">
      {task.checklist.map((c, i) => <li key={i} className="flex items-start gap-1.5"><span aria-hidden="true" className={`shrink-0 ${c.done ? 'text-emerald-600 dark:text-emerald-400' : 'text-muted-foreground'}`}>{c.done ? '✓' : '○'}</span><span className={`min-w-0 flex-1 ${c.done ? 'text-muted-foreground line-through' : 'text-foreground'}`}>{c.text}<span className="sr-only">{c.done ? ' (done)' : ' (to do)'}</span></span>{c.done && c.by ? <span className="shrink-0 text-muted-foreground">{c.by}</span> : null}</li>)}
    </ul></> : null}
    {task.sources?.length ? <><p className={head}>From</p><div className="space-y-0.5">{task.sources.map((s, i) => <Ref key={i} item={s}/>)}</div></> : null}
    <p className={head}>Linked</p>
    {task.links?.length ? <div className="space-y-0.5">{task.links.map((s, i) => <Ref key={i} item={s}/>)}</div>
      : <p className="text-muted-foreground">Nothing linked yet. Agents add tickets, docs, threads and pull requests as they work.</p>}
    {task.activity?.length ? <><p className={head}>What&rsquo;s happening</p><ul className="space-y-0.5">
      {[...task.activity].reverse().map((a, i) => <li key={i} className="flex items-center gap-1.5"><Kind kind={a.kind}/><span className="min-w-0 flex-1 truncate text-foreground">{a.url ? <a href={a.url} target="_blank" rel="noopener noreferrer" className="hover:underline">{a.text}</a> : a.text}</span><span className="shrink-0 text-muted-foreground">{ago(a.at)}</span></li>)}
    </ul></> : null}
    {task.history?.length ? <><p className={head}>History</p><ul aria-label="History" className="space-y-0.5 border-l border-border pl-2">
      {task.history.map((move, index) => <li key={index}><span className="text-foreground">{move.by || 'An agent'}{move.step ? ` → ${move.step}` : ''}</span> <span className="text-muted-foreground">{ago(move.at)}</span>{move.note && <span className="block text-muted-foreground">{move.note}</span>}</li>)}
    </ul></> : null}
  </div>
}

function TaskLine({ task, steps, onAgent }: { task: ProjectTask; steps: string[]; onAgent?: Draft }) {
  const [open, setOpen] = useState(false)
  const kind = task.sources?.[0]?.kind || 'other'
  const waiting = !!task.needs_you
  const state = task.tasks?.length ? taskWords[task.tasks[task.tasks.length - 1].state] : ''
  const who = task.held ? 'on hold' : task.proposed ? `suggest ${task.proposed}` : task.assignee ? `${task.assignee}${state ? ` · ${state}` : ''}` : 'not handed out yet'
  const day = Date.now() / 1000 - 24 * 3600
  const fresh = task.activity?.filter(a => a.at >= day).length || 0
  return <div className="rounded text-[11px] hover:bg-background/40 focus-within:bg-background/40">
    <button type="button" aria-expanded={open} onClick={() => setOpen(value => !value)} className="flex w-full items-start gap-1.5 px-1 py-0.5 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
      <Kind kind={kind}/>
      <span className="min-w-0 flex-1">
        <span className="block truncate text-foreground">{task.title}</span>
        <span className="flex items-center gap-1 whitespace-nowrap text-[10px] text-muted-foreground">
          <span className={task.done ? 'text-emerald-600 dark:text-emerald-400' : 'rounded bg-muted px-1 text-foreground'}>{task.done ? '✓ ' : ''}{task.step}</span>
          {!task.done && <span className={`min-w-0 truncate ${waiting ? 'font-medium text-amber-600 dark:text-amber-400' : task.assignee ? 'text-sky-600 dark:text-sky-400' : ''}`}>· {who}</span>}
          {task.checklist?.length ? <span className="shrink-0" title="Checklist items done">· ☑ {task.checklist.filter(c => c.done).length}/{task.checklist.length}</span> : null}
          {task.links?.length ? <span className="shrink-0">· {task.links.length} linked</span> : null}
          {fresh ? <span className="shrink-0 text-sky-600 dark:text-sky-400">· {fresh} new</span> : null}
          <span className="ml-auto shrink-0">{ago(task.at)}</span>
        </span>
      </span>
    </button>
    {open && <TaskDetail task={task} steps={steps} onAgent={onAgent}/>}
  </div>
}

// Short enough for the side panel: the mode is a small chip you click to switch,
// and Pause is an icon button.
function LeadLine({ row, onAgent }: { row: CrewPanelRow; onAgent?: Draft }) {
  if (!row.lead) return null
  const ask = row.tasks_mode === 'ask'
  const chip = 'shrink-0 rounded px-1 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
  const mode = row.paused
    ? <span className={`${chip} bg-amber-500/15 font-medium text-amber-700 dark:text-amber-300`}>Paused</span>
    : onAgent
      ? <button type="button" onClick={() => onAgent(projectLeadMessage(ask ? 'auto' : 'ask', row.lead))} title={ask ? 'Asks you before handing work out. Click to let it hand work out by itself' : 'Hands work out by itself. Click to approve each hand-out first'} className={`${chip} bg-muted text-foreground hover:bg-background/60`}>{ask ? 'Ask first' : 'Auto'}</button>
      : <span className={`${chip} bg-muted text-foreground`}>{ask ? 'Ask first' : 'Auto'}</span>
  return <div className="flex items-center gap-1 px-1 pb-0.5 text-[10px] text-muted-foreground">
    <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${row.paused ? 'bg-amber-500' : 'bg-emerald-500'}`} aria-hidden="true"/>
    <span className="min-w-0 truncate text-foreground">{row.lead}</span>
    {mode}
    {row.last_sorted ? <span className="shrink-0">{ago(row.last_sorted)}</span> : null}
    {onAgent && <button type="button" onClick={() => onAgent(projectLeadMessage(row.paused ? 'resume' : 'pause', row.lead))} aria-label={row.paused ? 'Resume the lead' : 'Pause the lead'} title={row.paused ? 'Resume the lead' : 'Pause the lead. New events wait until you resume'}
      className="ml-auto flex h-4 w-4 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-background/40 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
      {row.paused ? <Play aria-hidden="true" className="h-2.5 w-2.5"/> : <Pause aria-hidden="true" className="h-2.5 w-2.5"/>}</button>}
  </div>
}

export function ProjectTasksRow({ row, onAgent }: { row: CrewPanelRow; onAgent?: Draft }) {
  const [showDone, setShowDone] = useState(false)
  const [showSorted, setShowSorted] = useState(false)
  const steps = row.steps || []
  const tasks = row.tasks || []
  const open = tasks.filter(t => !t.done), done = tasks.filter(t => t.done)
  const needsYou = open.filter(t => t.needs_you), others = open.filter(t => !t.needs_you)
  const sorted = row.sorted || []
  const dropped = sorted.filter(s => s.kind === 'dropped').length, added = sorted.filter(s => s.kind === 'added').length
  return <>
    <LeadLine row={row} onAgent={onAgent}/>
    {steps.length ? <p className="px-1 pb-0.5 text-[10px] text-muted-foreground">{steps.join(' → ')}</p> : null}
    {needsYou.length ? <section aria-label="Needs you" className="mb-1 rounded border border-amber-500/30 bg-amber-500/5 py-0.5">
      <p className="px-1 text-[10px] font-medium text-amber-700 dark:text-amber-300">Needs you · {needsYou.length}</p>
      {needsYou.map(task => <TaskLine key={task.id} task={task} steps={steps} onAgent={onAgent}/>)}
    </section> : null}
    {others.map(task => <TaskLine key={task.id} task={task} steps={steps} onAgent={onAgent}/>)}
    {!open.length && <p className="px-1 py-0.5 text-[11px] text-muted-foreground">Nothing open. New work shows here once the lead sorts it.</p>}
    {row.done_count ? <button type="button" aria-expanded={showDone} onClick={() => setShowDone(value => !value)} className="px-1 py-0.5 text-[10px] text-muted-foreground hover:text-foreground">✓ {row.done_count} {(steps[steps.length - 1] || 'done').toLowerCase()} {showDone ? '▾' : '▸'}</button> : null}
    {showDone && done.map(task => <TaskLine key={task.id} task={task} steps={steps} onAgent={onAgent}/>)}
    {sorted.length ? <button type="button" aria-expanded={showSorted} onClick={() => setShowSorted(value => !value)} className="block px-1 py-0.5 text-left text-[10px] text-primary hover:underline">
      {sorted.length} sorted today{dropped ? `: ${dropped} dropped` : ''}{added ? `${dropped ? ',' : ':'} ${added} added to tasks` : ''} {showSorted ? '▾' : '▸'}</button> : null}
    {showSorted && <ul aria-label="What the lead sorted" className="space-y-0.5 px-1 pb-1 text-[10px]">
      {[...sorted].reverse().map((s, i) => <li key={i} className="flex gap-1.5"><span className="shrink-0 rounded bg-muted px-1">{s.kind === 'created' ? 'new task' : s.kind}</span><span className="min-w-0 flex-1 truncate text-foreground">{s.url ? <a href={s.url} target="_blank" rel="noopener noreferrer" className="hover:underline">{s.text}</a> : s.text}</span><span className="shrink-0 text-muted-foreground">{ago(s.at)}</span></li>)}
    </ul>}
  </>
}

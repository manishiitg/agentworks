import { useEffect, useRef, useState } from 'react'
import { ExternalLink, MoreHorizontal } from 'lucide-react'
import type { CrewPanelRow, ProjectTask, ProjectTaskRef } from './useCrewPanel'
import { CodingCliIcon } from '../../components/chat/CodingCliIcon'
import { formatRelativeSeconds } from '../../utils/relativeTime'

// Project tasks (approved mockups 24 Nov; tidy list and task page #3788): one
// shared, curated list per project. The list shows Needs you first, then the
// rest, each task in two lines; the lead, Pause, the mode and setup sit in one
// footer with ⋯. Clicking a task opens its page in the side panel: what you need
// to do (with the agent's answer buttons), the checklist, who is working on it,
// what is linked, what is happening and history. The server decides all of it;
// every control drafts a message in the project chat and the agent makes the
// change with the task actions.

type Draft = (message: string) => void

type TaskMessage = 'approve' | 'reassign' | 'hold' | 'unhold' | 'move' | 'reply'
export function projectTaskMessage(kind: TaskMessage, task: ProjectTask): string {
  const what = `Project task ${task.id} "${task.title}"`
  if (kind === 'approve') return `Approve handing ${what} to ${task.proposed}: set them as assignee, send them the ask and link it to the task.`
  if (kind === 'reassign') return `Reassign ${what}${task.assignee ? ` (now with ${task.assignee})` : ''}. Suggest the best-fitting agent by purpose and ask me which one, then hand it over, link the new ask and tell the old agent.`
  if (kind === 'hold') return `Put ${what} on hold (task_update held=true) and tell whoever has it to stop for now.`
  if (kind === 'unhold') return `Take ${what} off hold (task_update held=false) and let whoever has it carry on.`
  if (kind === 'move') return `Move ${what} (now at ${task.step}) to the step I name, with a short note: `
  return `About ${what}${task.needs_you ? ` ("${task.needs_you}")` : ''}: `
}

export function projectTaskAnswer(task: ProjectTask, answer: string): string {
  return `My answer on Project task ${task.id} "${task.title}" ("${task.needs_you}"): ${answer}. Pass it to whoever asked, then clear waiting.`
}

export function projectTaskTick(task: ProjectTask, index: number, done: boolean): string {
  const item = task.checklist?.[index]
  return `On Project task ${task.id} "${task.title}", ${done ? 'tick' : 'untick'} checklist item ${index + 1} "${item?.text || ''}" (task_update ${done ? 'checklist_done' : 'checklist_open'}=${index + 1}).`
}

export function projectLeadMessage(kind: 'pause' | 'resume' | 'auto' | 'ask', lead = 'the lead'): string {
  if (kind === 'pause') return `Pause the Project tasks lead (${lead}): task_settings paused=true. New events wait until I resume it.`
  if (kind === 'resume') return `Resume the Project tasks lead (${lead}): task_settings paused=false.`
  if (kind === 'ask') return `Switch Project tasks to ask me first: task_settings mode=ask. ${lead} should propose an agent for each new task and wait for my approval.`
  return `Switch Project tasks to auto: task_settings mode=auto. ${lead} hands new tasks out by itself.`
}

export const projectActivityChangeMessage = 'I want to change Project activity for this project. Show me what it watches now, the lead agent, and the steps and rules of the Project tasks list, then ask what to add, change or remove.'

const kindLetter: Record<string, string> = { slack: 'S', mail: 'M', tasks: 'T', github: 'G', notion: 'N', drive: 'D', other: '•' }
const kindTone: Record<string, string> = { slack: 'bg-purple-500/15 text-purple-700 dark:text-purple-300', mail: 'bg-blue-500/15 text-blue-700 dark:text-blue-300', tasks: 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-300', github: 'bg-foreground/10 text-foreground', notion: 'bg-foreground/10 text-foreground', drive: 'bg-amber-500/15 text-amber-700 dark:text-amber-300', other: 'bg-muted text-muted-foreground' }
const kindName: Record<string, string> = { slack: 'Slack', mail: 'Mail', tasks: 'Task tracker', github: 'GitHub', notion: 'Notion', drive: 'Drive', other: 'Other' }

function Kind({ kind }: { kind: string }) {
  return <span title={kindName[kind] || kind} aria-label={kindName[kind] || kind}
    className={`mt-px flex h-3.5 w-3.5 shrink-0 items-center justify-center rounded-[3px] text-[8px] font-bold ${kindTone[kind] || kindTone.other}`}>{kindLetter[kind] || kindLetter.other}</span>
}

const taskWords: Record<string, string> = { open: 'asked', asked: 'asked', in_progress: 'working', blocked: 'blocked', needs_you: 'waiting on you', stalled: 'stalled', done: 'done' }
const taskTone: Record<string, string> = { in_progress: 'bg-sky-500/15 text-sky-700 dark:text-sky-300', blocked: 'bg-amber-500/15 text-amber-700 dark:text-amber-300', needs_you: 'bg-amber-500/15 text-amber-700 dark:text-amber-300', stalled: 'bg-amber-500/15 text-amber-700 dark:text-amber-300', done: 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-300' }
const ring = 'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
const button = `rounded-md border border-border px-2 py-0.5 text-[11px] hover:bg-background/40 ${ring}`

function who(task: ProjectTask): string {
  if (task.held) return 'on hold'
  if (task.proposed) return `suggest ${task.proposed}`
  if (!task.assignee) return 'not handed out yet'
  const last = task.tasks?.[task.tasks.length - 1]
  return `${task.assignee}${last && taskWords[last.state] ? ` · ${taskWords[last.state]}` : ''}`
}

function TaskLine({ task, onOpen }: { task: ProjectTask; onOpen: () => void }) {
  const day = Date.now() / 1000 - 24 * 3600
  const fresh = task.activity?.filter(a => a.at >= day).length || 0
  const checks = task.checklist || []
  return <button type="button" onClick={onOpen} className={`block w-full rounded px-1 py-1 text-left hover:bg-background/40 ${ring}`}>
    <span className="flex items-start gap-1.5 text-[11.5px]">
      <Kind kind={task.sources?.[0]?.kind || 'other'}/>
      <span className="line-clamp-2 min-w-0 flex-1 leading-snug text-foreground">{task.title}</span>
      <span className="shrink-0 pt-px text-[10px] text-muted-foreground">{formatRelativeSeconds(task.at)}</span>
    </span>
    <span className="mt-0.5 flex flex-wrap items-center gap-x-1.5 pl-5 text-[10.5px]">
      {task.done ? <span className="text-emerald-600 dark:text-emerald-400">✓ {task.step}</span>
        : task.needs_you ? <span className="min-w-0 font-medium text-amber-600 dark:text-amber-400">{task.needs_you}</span>
        : <span className="inline-flex min-w-0 items-center gap-1 text-muted-foreground">{task.step} ·{task.assignee && !task.held && !task.proposed ? <CodingCliIcon provider={task.assignee_cli} className="inline-flex h-3 w-3 shrink-0 items-center"/> : null}<span className={task.assignee && !task.held ? 'text-sky-600 dark:text-sky-400' : ''}>{who(task)}</span></span>}
      {checks.length ? <span className="rounded bg-muted px-1 text-[10px] text-muted-foreground" title="Checklist done">☑ {checks.filter(c => c.done).length}/{checks.length}</span> : null}
      {task.links?.length ? <span className="text-muted-foreground">{task.links.length} linked</span> : null}
      {fresh ? <span className="text-sky-600 dark:text-sky-400">{fresh} new</span> : null}
    </span>
  </button>
}

function LeadMenu({ row, onAgent }: { row: CrewPanelRow; onAgent: Draft }) {
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
  const lead = row.lead || 'the lead'
  const ask = row.tasks_mode === 'ask'
  const items = [
    ...(row.lead ? [
      { label: row.paused ? `Resume ${lead}` : `Pause ${lead}`, detail: row.paused ? 'Waiting work comes in again' : 'New work waits until you resume', message: projectLeadMessage(row.paused ? 'resume' : 'pause', row.lead) },
      { label: ask ? 'Let it hand work out by itself' : 'Ask me before handing work out', detail: ask ? `Now: ${lead} asks you first` : `Now: ${lead} hands work out by itself`, message: projectLeadMessage(ask ? 'auto' : 'ask', row.lead) },
    ] : []),
    { label: 'Change setup', detail: 'What it watches, steps and rules', message: projectActivityChangeMessage },
  ]
  return <div ref={ref} className="relative shrink-0">
    <button type="button" aria-label="Project tasks options" aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen(value => !value)}
      className={`flex h-4 w-5 items-center justify-center rounded text-muted-foreground hover:bg-background/40 hover:text-foreground ${ring}`}><MoreHorizontal aria-hidden="true" className="h-3.5 w-3.5"/></button>
    {open && <div role="menu" className="absolute bottom-5 right-0 z-20 w-56 rounded-md border border-border bg-popover p-1 text-popover-foreground shadow-lg">
      {items.map(item => <button key={item.label} type="button" role="menuitem" onClick={() => { setOpen(false); onAgent(item.message) }}
        className="block w-full rounded px-2 py-1 text-left text-[11px] hover:bg-muted focus-visible:bg-muted focus-visible:outline-none">
        {item.label}<span className="block text-[10px] text-muted-foreground">{item.detail}</span>
      </button>)}
    </div>}
  </div>
}

export function ProjectTasksRow({ row, onAgent, onOpenTask }: { row: CrewPanelRow; onAgent?: Draft; onOpenTask: (id: string) => void }) {
  const [showDone, setShowDone] = useState(false)
  const tasks = row.tasks || []
  const open = tasks.filter(t => !t.done), done = tasks.filter(t => t.done)
  const needsYou = open.filter(t => t.needs_you), others = open.filter(t => !t.needs_you)
  const line = (task: ProjectTask) => <TaskLine key={task.id} task={task} onOpen={() => task.id && onOpenTask(task.id)}/>
  const heading = 'px-1 pb-0.5 pt-1.5 text-[10px] font-semibold uppercase tracking-wide'
  return <>
    {needsYou.length ? <section aria-label="Needs you"><p className={`${heading} text-amber-600 dark:text-amber-400`}>Needs you</p>{needsYou.map(line)}</section> : null}
    {others.length ? <section aria-label="In progress">{needsYou.length ? <p className={`${heading} text-muted-foreground`}>In progress</p> : null}{others.map(line)}</section> : null}
    {!open.length && <p className="px-1 py-0.5 text-[11px] text-muted-foreground">Nothing open. New work shows here once {row.lead || 'the lead'} sorts it.</p>}
    <div className="mt-1.5 flex items-center gap-2.5 border-t border-dashed border-border px-1 pt-1 text-[10.5px] text-muted-foreground">
      <span className="flex min-w-0 flex-1 items-center gap-1 truncate">
        {row.lead ? <><span aria-hidden="true" className={`h-1.5 w-1.5 shrink-0 rounded-full ${row.paused ? 'bg-amber-500' : 'bg-emerald-500'}`}/>
          <span className="truncate">{row.paused ? `${row.lead} is paused` : `${row.lead} sorts new work`}</span></> : null}
      </span>
      {row.done_count ? <button type="button" aria-expanded={showDone} onClick={() => setShowDone(value => !value)} className="shrink-0 hover:text-foreground">✓ {row.done_count} done {showDone ? '▾' : '▸'}</button> : null}
      {onAgent && <LeadMenu row={row} onAgent={onAgent}/>}
    </div>
    {showDone && done.map(line)}
  </>
}

function Ref({ item }: { item: ProjectTaskRef }) {
  const body = <><Kind kind={item.kind}/><span className="min-w-0 flex-1"><span className="block truncate text-foreground">{item.title}</span><span className="block text-[10px] text-muted-foreground">{kindName[item.kind] || item.kind}{item.by ? ` · added by ${item.by}` : ''}</span></span></>
  return item.url
    ? <a href={item.url} target="_blank" rel="noopener noreferrer" className="flex items-center gap-2 rounded-md border border-border px-2 py-1 hover:bg-background/40">{body}<ExternalLink aria-hidden="true" className="h-3 w-3 shrink-0 text-muted-foreground"/></a>
    : <div className="flex items-center gap-2 rounded-md border border-border px-2 py-1">{body}</div>
}

// One task's page; it takes the whole side panel so nothing is squeezed.
export function ProjectTaskPage({ row, task, onAgent }: { row: CrewPanelRow; task: ProjectTask; onAgent?: Draft }) {
  const steps = row.steps || []
  const at = Math.max(0, steps.indexOf(task.step || steps[0]))
  const checks = task.checklist || []
  const ticked = checks.filter(c => c.done).length
  const head = 'mb-1 mt-3 flex items-baseline gap-1.5 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground'
  const source = task.sources?.[0]
  return <div className="space-y-0.5 px-1 pb-2 text-[11.5px]">
    <h3 className="text-[13px] font-semibold leading-snug text-foreground">{task.title}</h3>
    <p className="flex flex-wrap items-center gap-1 text-[10.5px] text-muted-foreground">
      {source && <><Kind kind={source.kind}/><span>From {kindName[source.kind] || source.kind}</span><span>·</span></>}
      <span>{task.done ? 'finished' : task.assignee ? `with ${task.assignee}` : task.proposed ? `suggested: ${task.proposed}` : 'not handed out yet'}</span><span>·</span><span>{formatRelativeSeconds(task.at, { style: 'long' })}</span>
      {task.held && <span className="rounded bg-amber-500/15 px-1 text-amber-700 dark:text-amber-300">on hold</span>}
    </p>
    <ol aria-label="Steps" className="flex flex-wrap gap-1 pt-1.5 text-[10.5px]">
      {steps.map((step, index) => <li key={step} aria-current={index === at ? 'step' : undefined}
        className={`rounded px-1.5 ${index === at ? (task.done ? 'bg-emerald-500/15 font-medium text-emerald-700 dark:text-emerald-300' : 'bg-amber-500/15 font-medium text-foreground') : index < at ? 'text-emerald-700 dark:text-emerald-400' : 'text-muted-foreground'}`}>{index < at ? '✓ ' : ''}{step}</li>)}
    </ol>
    {task.needs_you && <div className="mt-2 rounded-lg border border-amber-500/40 bg-amber-500/5 px-2.5 py-2">
      <p><span className="font-semibold text-amber-600 dark:text-amber-400">Needs you:</span> <span className="text-foreground">{task.needs_you}</span></p>
      {onAgent && <div className="mt-1.5 flex flex-wrap gap-1.5">
        {task.proposed && !task.held && <button type="button" onClick={() => onAgent(projectTaskMessage('approve', task))} className={`${button} border-transparent bg-primary font-medium text-primary-foreground hover:bg-primary/90`}>Approve → {task.proposed}</button>}
        {task.choices?.map((choice, i) => <button key={choice} type="button" onClick={() => onAgent(projectTaskAnswer(task, choice))} className={i === 0 ? `${button} border-transparent bg-primary font-medium text-primary-foreground hover:bg-primary/90` : button}>{choice}</button>)}
        <button type="button" onClick={() => onAgent(projectTaskMessage('reply', task))} className={button}>Reply…</button>
      </div>}
    </div>}
    {checks.length ? <>
      <p className={head}>Checklist <span className="font-normal normal-case tracking-normal">{ticked} of {checks.length}</span></p>
      <div className="mb-1 h-1 overflow-hidden rounded-full bg-muted"><div className="h-full rounded-full bg-emerald-500" style={{ width: `${Math.round(ticked / checks.length * 100)}%` }}/></div>
      <ul aria-label="Checklist" className="space-y-0.5">
        {checks.map((c, i) => <li key={i} className="flex items-start gap-2">
          <button type="button" role="checkbox" aria-checked={!!c.done} aria-label={`${c.done ? 'Untick' : 'Tick'} ${c.text}`} disabled={!onAgent} onClick={() => onAgent?.(projectTaskTick(task, i, !c.done))}
            className={`mt-0.5 flex h-3.5 w-3.5 shrink-0 items-center justify-center rounded-[3px] border text-[9px] ${c.done ? 'border-emerald-500 bg-emerald-500 text-background' : 'border-border hover:border-foreground/50'} ${ring}`}>{c.done ? '✓' : ''}</button>
          <span className={`min-w-0 flex-1 ${c.done ? 'text-muted-foreground line-through' : 'text-foreground'}`}>{c.text}</span>
          {c.done && c.by ? <span className="shrink-0 text-[10px] text-muted-foreground">{c.by}</span> : null}
        </li>)}
      </ul>
    </> : null}
    {task.tasks?.length ? <>
      <p className={head}>Who is working on it</p>
      <ul className="space-y-1">{task.tasks.map((t, i) => <li key={i}>
        <span className="flex items-center gap-2"><CodingCliIcon provider={t.cli} className="inline-flex h-3.5 w-3.5 shrink-0 items-center"/><span className="min-w-0 flex-1 truncate text-foreground">{t.agent}</span><span className={`shrink-0 rounded-full px-1.5 text-[10px] ${taskTone[t.state] || 'bg-muted text-muted-foreground'}`}>{taskWords[t.state] || t.state}</span></span>
        {t.title ? <span className="block truncate text-[10.5px] text-muted-foreground">{t.title}</span> : null}
      </li>)}</ul>
    </> : null}
    <p className={head}>Linked {task.links?.length ? <span className="font-normal normal-case tracking-normal">{task.links.length}</span> : null}</p>
    {task.links?.length || task.sources?.length
      ? <div className="space-y-1">{[...(task.links || []), ...(task.sources || [])].map((s, i) => <Ref key={i} item={s}/>)}</div>
      : <p className="text-[10.5px] text-muted-foreground">Nothing linked yet. Agents add pull requests, tickets, docs and threads as they work.</p>}
    {task.activity?.length ? <>
      <p className={head}>What&rsquo;s happening</p>
      <ul className="space-y-1 border-l border-border pl-2.5 text-[11px]">
        {[...task.activity].reverse().map((a, i) => <li key={i}><span className="text-foreground">{a.url ? <a href={a.url} target="_blank" rel="noopener noreferrer" className="hover:underline">{a.text}</a> : a.text}</span> <span className="text-[10px] text-muted-foreground">{formatRelativeSeconds(a.at)}</span></li>)}
      </ul>
    </> : null}
    {task.history?.length ? <>
      <p className={head}>History</p>
      <ul aria-label="History" className="space-y-1 border-l border-border pl-2.5 text-[11px]">
        {task.history.map((move, index) => <li key={index}><span className="text-foreground">{move.by || 'An agent'}{move.step ? ` → ${move.step}` : ''}</span> <span className="text-[10px] text-muted-foreground">{formatRelativeSeconds(move.at)}</span>{move.note && <span className="block text-muted-foreground">{move.note}</span>}</li>)}
      </ul>
    </> : null}
    {onAgent && !task.done && <div className="flex flex-wrap gap-1.5 pt-3">
      <button type="button" onClick={() => onAgent(projectTaskMessage('reassign', task))} className={button}>Reassign</button>
      <button type="button" onClick={() => onAgent(projectTaskMessage(task.held ? 'unhold' : 'hold', task))} className={button}>{task.held ? 'Take off hold' : 'Hold'}</button>
      <button type="button" onClick={() => onAgent(projectTaskMessage('move', task))} className={button}>Move…</button>
    </div>}
  </div>
}

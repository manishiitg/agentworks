import { useEffect, useRef, useState } from 'react'
import { WorkspaceCompactGroup } from '../../components/workspace/WorkspaceCompactGroup'
import { ChevronRight, ExternalLink, ListChecks, Maximize2, MoreHorizontal, Pin } from 'lucide-react'
import type { CrewPanelRow, ProjectTask, ProjectTaskGroup, ProjectTaskMove, ProjectTaskRef } from './useCrewPanel'
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
  if (kind === 'hold') return `Don't chase ${what} (task_update held=true): it stays on the list, and the lead stops handing it out, nudging or reassigning it.`
  if (kind === 'unhold') return `Let the lead chase ${what} again (task_update held=false).`
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

export function projectLeadMessage(kind: 'pause' | 'resume' | 'auto' | 'ask' | 'off', lead = 'the lead'): string {
  if (kind === 'off') return `Switch Project tasks to off: task_settings mode=off. Keep it a plain list: ${lead} still adds and tidies tasks, but doesn't hand work out, nudge or reassign.`
  if (kind === 'pause') return `Pause the Project tasks lead (${lead}): task_settings paused=true. New events wait until I resume it.`
  if (kind === 'resume') return `Resume the Project tasks lead (${lead}): task_settings paused=false.`
  if (kind === 'ask') return `Switch Project tasks to ask me first: task_settings mode=ask. ${lead} should propose an agent for each new task and wait for my approval, and ask me before reassigning stuck work.`
  return `Switch Project tasks to auto: task_settings mode=auto. ${lead} hands new tasks out by itself and chases them until they're done.`
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
  if (task.held) return 'not chased'
  if (task.proposed) return `suggest ${task.proposed}`
  if (!task.assignee) return 'not handed out yet'
  const last = task.tasks?.[task.tasks.length - 1]
  return `${task.assignee}${last && taskWords[last.state] ? ` · ${taskWords[last.state]}` : ''}`
}

// Due dates read as a short day: "today", "Fri", "Dec 4". Overdue comes from the server.
function dueLabel(due: string): string {
  const [y, m, d] = due.split('-').map(Number)
  const date = new Date(y, m - 1, d)
  const today = new Date(); today.setHours(0, 0, 0, 0)
  const days = Math.round((date.getTime() - today.getTime()) / 86400000)
  if (days === 0) return 'today'
  if (days === 1) return 'tomorrow'
  if (days > 1 && days < 7) return date.toLocaleDateString(undefined, { weekday: 'short' })
  return date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}

// One task in two lines, full width: the title, then who has it (with their
// CLI logo) or what you need to do, the checklist, due date and counts.
function TaskLine({ task, onOpen, onAgent, answers = false }: { task: ProjectTask; onOpen: () => void; onAgent?: Draft; answers?: boolean }) {
  const day = Date.now() / 1000 - 24 * 3600
  const fresh = task.activity?.filter(a => a.at >= day).length || 0
  const checks = task.checklist || []
  const ticked = checks.filter(c => c.done).length
  const quick = answers && onAgent && task.needs_you && (task.choices?.length || (task.proposed && !task.held))
  return <div className="rounded-md hover:bg-background/40">
    <button type="button" onClick={onOpen} className={`block w-full rounded-md px-1.5 py-1 text-left ${ring}`}>
      <span className="flex items-start gap-1.5 text-[11.5px]">
        {task.pinned && <Pin aria-label="Pinned" className="mt-0.5 h-3 w-3 shrink-0 text-muted-foreground"/>}
        <span className="line-clamp-2 min-w-0 flex-1 leading-snug text-foreground">{task.title}</span>
        <span className="shrink-0 pt-px text-[10px] text-muted-foreground">{formatRelativeSeconds(task.at)}</span>
      </span>
      <span className="mt-0.5 flex flex-wrap items-center gap-x-1.5 gap-y-0.5 text-[10.5px]">
        {task.done ? <span className="text-emerald-600 dark:text-emerald-400">✓ {task.step}</span>
          : task.needs_you ? <span className="min-w-0 font-medium text-amber-600 dark:text-amber-400">{task.needs_you}</span>
          : <span className="inline-flex min-w-0 items-center gap-1 text-muted-foreground">{task.step} ·{task.assignee && !task.held && !task.proposed ? <CodingCliIcon provider={task.assignee_cli} className="inline-flex h-3 w-3 shrink-0 items-center"/> : null}<span className={task.assignee && !task.held ? 'text-sky-600 dark:text-sky-400' : ''}>{who(task)}</span></span>}
        {!task.done && !task.needs_you && task.chase ? <span className={task.chase === 'stuck' ? 'font-medium text-red-600 dark:text-red-400' : 'text-amber-600 dark:text-amber-400'}>{task.chase === 'stuck' ? 'Stuck' : '⏰ Nudged'}{task.chase_at ? ` ${formatRelativeSeconds(task.chase_at)}` : ''}</span>
          : !task.done && !task.needs_you && task.from ? <span className="text-muted-foreground">↻ from {task.from}</span> : null}
        {task.due && !task.done ? <span className={`rounded px-1 text-[10px] ${task.overdue ? 'bg-amber-500/15 font-medium text-amber-700 dark:text-amber-300' : 'bg-muted text-muted-foreground'}`} title={`Due ${task.due}`}>{task.overdue ? 'overdue · ' : 'due '}{dueLabel(task.due)}</span> : null}
        {checks.length ? <span className="inline-flex items-center gap-1 text-[10px] text-muted-foreground" title="Checklist done">☑ {ticked}/{checks.length}<span className="h-1 w-6 overflow-hidden rounded-full bg-muted"><span className="block h-full rounded-full bg-emerald-500" style={{ width: `${Math.round(ticked / checks.length * 100)}%` }}/></span></span> : null}
        {task.links?.length ? <span className="text-muted-foreground">{task.links.length} linked</span> : null}
        {fresh ? <span className="text-sky-600 dark:text-sky-400">{fresh} new</span> : null}
      </span>
    </button>
    {quick ? <div className="flex flex-wrap gap-1 px-1.5 pb-1">
      {task.proposed && !task.held && <button type="button" onClick={() => onAgent!(projectTaskMessage('approve', task))} className={`${button} border-transparent bg-primary font-medium text-primary-foreground hover:bg-primary/90`}>Approve</button>}
      {task.choices?.map((choice, i) => <button key={choice} type="button" onClick={() => onAgent!(projectTaskAnswer(task, choice))} className={i === 0 && !task.proposed ? `${button} border-transparent bg-primary font-medium text-primary-foreground hover:bg-primary/90` : button}>{choice}</button>)}
      <button type="button" onClick={() => onAgent!(projectTaskMessage('reply', task))} className={button}>Reply…</button>
    </div> : null}
  </div>
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
  // How the list is run and Pause sit in the switch and strip above (owner, 27 Nov).
  const items = [
    { label: 'Change setup', detail: 'What it watches, the lead, steps and rules', message: projectActivityChangeMessage },
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

// In the side panel: the usual hide/show row (owner, 26 Nov). Expanding it
// shows the list in place; a small icon opens it wide, like Team, for those
// who want more room.
export function ProjectTasksSummary({ row, storageKey, onAgent, onOpenTask, onOpenTasks }: { row: CrewPanelRow; storageKey: string; onAgent?: Draft; onOpenTask: (id: string) => void; onOpenTasks: () => void }) {
  const needs = (row.tasks || []).filter(t => !t.done && t.needs_you).length
  const open = row.open_count || 0
  return <WorkspaceCompactGroup flush title="Project tasks" icon={ListChecks}
    detail={<>{needs ? <span className="text-amber-600 dark:text-amber-400">{needs} need{needs === 1 ? 's' : ''} you</span> : null}
      {needs && open > needs ? ' · ' : ''}{open > needs || !needs ? `${open} open` : ''}</>}
    preview={<button type="button" onClick={onOpenTasks} aria-label="Open Project tasks wide" title="Open wide"
      className={`flex h-6 w-6 items-center justify-center rounded text-muted-foreground hover:bg-background/40 hover:text-foreground ${ring}`}>
      <Maximize2 aria-hidden="true" className="h-3 w-3"/>
    </button>}>
    <ProjectTasksPanel row={row} storageKey={storageKey} onAgent={onAgent} onOpenTask={onOpenTask}/>
  </WorkspaceCompactGroup>
}

function useFolded(storageKey: string, groups: ProjectTaskGroup[]) {
  const [folded, setFolded] = useState<Record<string, boolean>>(() => {
    try { return JSON.parse(localStorage.getItem(storageKey) || '{}') } catch { return {} }
  })
  const isFolded = (name: string) => folded[name] ?? !!groups.find(g => g.name === name)?.folded
  const toggle = (name: string) => setFolded(current => {
    const next = { ...current, [name]: !isFolded(name) }
    try { localStorage.setItem(storageKey, JSON.stringify(next)) } catch { /* this visit only */ }
    return next
  })
  return [isFolded, toggle] as const
}

// The full Project tasks panel (#4294): the lead's focus line, Needs you, then
// the lead's own groups in its order (Other for the rest), each foldable with a
// done bar; filters by agent and step; the done list; the lead's controls.
export function ProjectTasksPanel({ row, storageKey, onAgent, onOpenTask, wide = false }: { row: CrewPanelRow; storageKey: string; onAgent?: Draft; onOpenTask: (id: string) => void; wide?: boolean }) {
  const [agent, setAgent] = useState('')
  const [step, setStep] = useState('')
  const [showDone, setShowDone] = useState(false)
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})
  const groups = row.task_groups || []
  const [isFolded, toggle] = useFolded(storageKey, groups)
  const all = row.tasks || []
  const agents = [...new Set(all.filter(t => !t.done && t.assignee).map(t => t.assignee!))].sort()
  const match = (t: ProjectTask) => (!agent || t.assignee === agent) && (!step || t.step === step)
  const open = all.filter(t => !t.done && match(t)), done = all.filter(t => t.done && match(t))
  const needsYou = open.filter(t => t.needs_you), rest = open.filter(t => !t.needs_you)
  const line = (task: ProjectTask, answers = false) => <TaskLine key={task.id} task={task} onAgent={onAgent} answers={answers} onOpen={() => task.id && onOpenTask(task.id)}/>
  const sections = [...groups.map(g => ({ name: g.name, note: g.note })), { name: 'Other', note: '' }]
    .map(g => ({ ...g, tasks: rest.filter(t => (t.group || 'Other') === g.name), all: all.filter(t => (t.group || 'Other') === g.name && match(t)) }))
    .filter(g => g.tasks.length)
  const select = `rounded-md border border-border bg-transparent px-1.5 py-0.5 text-[10.5px] text-foreground ${ring}`
  return <div className="space-y-1.5 px-0.5 pb-2 text-[11.5px]">
    <Drive row={row} onAgent={onAgent}/>
    {row.focus ? <p className="rounded-md bg-muted/60 px-2 py-1 text-[11.5px] text-foreground">{row.focus}</p> : null}
    {agents.length > 1 || (row.steps?.length || 0) > 2 ? <div className="flex flex-wrap items-center gap-1.5 px-1">
      {agents.length > 1 && <select aria-label="Filter by agent" value={agent} onChange={e => setAgent(e.target.value)} className={select}><option value="">Everyone</option>{agents.map(a => <option key={a} value={a}>{a}</option>)}</select>}
      <select aria-label="Filter by step" value={step} onChange={e => setStep(e.target.value)} className={select}><option value="">All steps</option>{(row.steps || []).map(s => <option key={s} value={s}>{s}</option>)}</select>
      {agent || step ? <button type="button" onClick={() => { setAgent(''); setStep('') }} className="text-[10.5px] text-muted-foreground hover:text-foreground">Clear</button> : null}
    </div> : null}
    {needsYou.length ? <section aria-label="Needs you" className="rounded-lg border border-amber-500/40 bg-amber-500/5 p-1">
      <p className="px-1.5 pb-0.5 pt-0.5 text-[10px] font-semibold uppercase tracking-wide text-amber-600 dark:text-amber-400">Needs you <span className="font-normal">{needsYou.length}</span></p>
      {needsYou.map(task => line(task, true))}
    </section> : null}
    <div className={wide && groups.length ? 'gap-x-4 gap-y-2 lg:grid lg:grid-cols-2' : ''}>
    {sections.map(g => {
      const folded = groups.length ? isFolded(g.name) : false
      const finished = g.all.filter(t => t.done).length
      const shown = expanded[g.name] ? g.tasks : g.tasks.slice(0, 8)
      return <section key={g.name} aria-label={g.name}>
        {groups.length ? <button type="button" aria-expanded={!folded} onClick={() => toggle(g.name)} className={`flex w-full items-center gap-1.5 rounded-md px-1 py-1 text-left text-[11px] hover:bg-background/40 ${ring}`}>
          <ChevronRight aria-hidden="true" className={`h-3 w-3 shrink-0 text-muted-foreground transition-transform ${folded ? '' : 'rotate-90'}`}/>
          <span className="shrink-0 font-semibold text-foreground">{g.name}</span>
          <span className="shrink-0 text-muted-foreground">{g.tasks.length} open</span>
          <span className="min-w-0 flex-1 truncate text-[10.5px] text-muted-foreground">{g.note}</span>
          {g.all.length ? <span title={`${finished} of ${g.all.length} done`} className="h-1 w-10 shrink-0 overflow-hidden rounded-full bg-muted"><span className="block h-full rounded-full bg-emerald-500" style={{ width: `${Math.round(finished / g.all.length * 100)}%` }}/></span> : null}
        </button> : null}
        {!folded && shown.map(task => line(task))}
        {!folded && g.tasks.length > shown.length ? <button type="button" onClick={() => setExpanded(current => ({ ...current, [g.name]: true }))} className="px-1.5 text-[10.5px] text-muted-foreground hover:text-foreground">{g.tasks.length - shown.length} more</button> : null}
      </section>
    })}
    </div>
    {!open.length && <p className="px-1 py-0.5 text-[11px] text-muted-foreground">{agent || step ? 'Nothing open matches.' : `Nothing open. New work shows here once ${row.lead || 'the lead'} sorts it.`}</p>}
    {row.done_count ? <div className="border-t border-dashed border-border pt-1">
      <button type="button" aria-expanded={showDone} onClick={() => setShowDone(value => !value)} className="px-1 text-[10.5px] text-muted-foreground hover:text-foreground">✓ {row.done_count} done {showDone ? '▾' : '▸'}</button>
      {showDone && done.map(task => line(task))}
    </div> : null}
    {row.tasks_mode !== 'off' && row.task_moves?.length ? <LeadMoves row={row} shown={wide ? 8 : 3}/> : null}
  </div>
}

const modes = [{ id: 'off', label: 'Off', title: 'A plain list: nobody hands work out or chases it' }, { id: 'ask', label: 'Ask me', title: 'The lead suggests who does what; you approve' }, { id: 'auto', label: 'Auto', title: 'The lead hands work out and chases it until done' }] as const

// Organise vs drive (owner, 27 Nov): the list is always there; whether an
// agent runs it is one switch. The strip under it says what the lead is doing.
function Drive({ row, onAgent }: { row: CrewPanelRow; onAgent?: Draft }) {
  const mode = row.tasks_mode || 'auto'
  const lead = row.lead || 'The lead'
  const strip = 'flex items-center gap-2 rounded-md px-2 py-1 text-[10.5px]'
  const link = 'shrink-0 text-[10.5px] text-primary hover:underline'
  return <>
    <div className="flex items-center gap-2 px-1 text-[10.5px] text-muted-foreground">
      <span className="min-w-0 flex-1 truncate">Run by an agent</span>
      {onAgent ? <span role="radiogroup" aria-label="How the list is run" className="inline-flex shrink-0 overflow-hidden rounded-md border border-border">
        {modes.map(m => <button key={m.id} type="button" role="radio" aria-checked={mode === m.id} title={m.title} onClick={() => mode !== m.id && onAgent(projectLeadMessage(m.id, row.lead))}
          className={`border-border px-2 py-0.5 text-[10.5px] [&:not(:first-child)]:border-l ${mode === m.id ? 'bg-foreground text-background' : 'hover:bg-background/40'} ${ring}`}>{m.label}</button>)}
      </span> : <span className="shrink-0">{modes.find(m => m.id === mode)?.label}</span>}
      {onAgent && <LeadMenu row={row} onAgent={onAgent}/>}
    </div>
    {mode === 'off'
      ? <div className={`${strip} border border-dashed border-border text-muted-foreground`}><span className="min-w-0 flex-1">Not driven: a shared list you and agents keep up to date.</span>
        {onAgent && <button type="button" onClick={() => onAgent(row.lead ? projectLeadMessage('ask', row.lead) : projectActivityChangeMessage)} className={link}>Let an agent run it</button>}</div>
      : row.paused
        ? <div className={`${strip} border border-border bg-muted/60`}><span aria-hidden="true" className="h-1.5 w-1.5 shrink-0 rounded-full bg-muted-foreground"/><span className="min-w-0 flex-1 text-foreground">{lead} is paused. Nothing is handed out or chased.</span>
          {onAgent && <button type="button" onClick={() => onAgent(projectLeadMessage('resume', row.lead))} className={link}>Resume</button>}</div>
        : <div className={`${strip} border border-sky-500/35 bg-sky-500/5`}><span aria-hidden="true" className="h-1.5 w-1.5 shrink-0 animate-pulse rounded-full bg-sky-500"/>
          <span className="min-w-0 flex-1 text-foreground">{row.lead ? <b className="font-semibold">{row.lead}</b> : 'No lead yet;'} {mode === 'ask' ? 'suggests who does what; you approve' : 'hands out work and chases it'}</span>
          {onAgent && row.lead && <button type="button" onClick={() => onAgent(projectLeadMessage('pause', row.lead))} className={link}>Pause</button>}</div>}
  </>
}

const moveMark: Record<string, string> = { gave: '→', moved: '↻', nudged: '⏰', stuck: '!', suggested: '?', asked: '?', closed: '✓', step: '▸' }

// "What the lead did": the server records each hand-out, nudge, move, question and close.
function LeadMoves({ row, shown }: { row: CrewPanelRow; shown: number }) {
  const [all, setAll] = useState(false)
  const moves: ProjectTaskMove[] = row.task_moves || []
  return <section aria-label="What the lead did" className="border-t border-border pt-1">
    <p className="px-1 pb-0.5 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">What the lead did</p>
    <ul className="space-y-0.5">{(all ? moves : moves.slice(0, shown)).map((m, i) => <li key={i} className="flex items-baseline gap-1.5 px-1 text-[11px]">
      <span aria-hidden="true" className={`w-3 shrink-0 text-center ${m.kind === 'stuck' ? 'text-red-600 dark:text-red-400' : m.kind === 'closed' ? 'text-emerald-600 dark:text-emerald-400' : 'text-muted-foreground'}`}>{moveMark[m.kind] || '•'}</span>
      <span className="min-w-0 flex-1 text-foreground">{m.text}{m.by && m.by !== row.lead ? <span className="text-muted-foreground"> · {m.by}</span> : null}</span>
      <span className="shrink-0 text-[10px] text-muted-foreground">{formatRelativeSeconds(m.at)}</span>
    </li>)}</ul>
    {!all && moves.length > shown ? <button type="button" onClick={() => setAll(true)} className="px-1 text-[10.5px] text-muted-foreground hover:text-foreground">{moves.length - shown} more</button> : null}
  </section>
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
      {task.held && <span className="rounded bg-muted px-1 text-muted-foreground">not chased</span>}
      {!task.done && task.chase && <span className={`rounded px-1 ${task.chase === 'stuck' ? 'bg-red-500/15 text-red-700 dark:text-red-300' : 'bg-amber-500/15 text-amber-700 dark:text-amber-300'}`}>{task.chase === 'stuck' ? 'stuck' : 'nudged'}{task.chase_at ? ` ${formatRelativeSeconds(task.chase_at)}` : ''}</span>}
      {task.from && <span>· moved from {task.from}</span>}
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
      <button type="button" onClick={() => onAgent(projectTaskMessage(task.held ? 'unhold' : 'hold', task))} className={button} title={task.held ? 'The lead hands out, nudges and reassigns it again' : 'Stays on the list; the lead leaves it alone'}>{task.held ? 'Let the lead chase it' : "Don't chase"}</button>
      <button type="button" onClick={() => onAgent(projectTaskMessage('move', task))} className={button}>Move…</button>
    </div>}
  </div>
}

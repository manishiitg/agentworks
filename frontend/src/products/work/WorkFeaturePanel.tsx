import { useEffect, useState, type ReactNode } from 'react'
import { ChevronRight } from 'lucide-react'
import { useChatStore } from '../../stores/useChatStore'
import { useCrewAgentsStore } from '../../components/chat/AgentCard'
import { featureCall, useCrewFeatures, type FeatureEvent, type FeatureTask, type FeatureView } from './featuresApi'
import { TaskStateIcon } from './TeamTasks'

const ago = (at?: string) => {
  const elapsed = at ? Math.max(0, Date.now() - Date.parse(at)) : NaN
  return !Number.isFinite(elapsed) ? '' : elapsed < 3_600_000 ? `${Math.max(1, Math.floor(elapsed / 60_000))}m` : elapsed < 86_400_000 ? `${Math.floor(elapsed / 3_600_000)}h` : `${Math.floor(elapsed / 86_400_000)}d`
}

/** The agent's work as the server writes it: the feature it is in (one picked when several), or its own tasks. */
function useAgentWork(projectId: string, tabId?: string) {
  const crew = useCrewAgentsStore()
  const activeTabId = useChatStore(state => state.activeTabId)
  const tab = tabId || activeTabId || undefined
  const agent = crew.projectId === projectId ? crew.agents.find(row => row.tabId === tab) : undefined
  const main = !!agent?.main
  const { target, forTab, chatKeyFor, error } = useCrewFeatures(projectId)
  const mine = forTab(tab, main)
  const [picked, setPicked] = useState('')
  const feature = mine.find(item => item.id === picked) || mine[0]
  const chat = chatKeyFor(tab, main)
  const [view, setView] = useState<{ key: string; feature?: FeatureView; tasks?: FeatureTask[]; error: string }>()
  const key = `${target?.connection || ''}\x00${target?.project || ''}\x00${feature?.id || ''}\x00${feature?.changed_at || ''}\x00${feature?.open_tasks}\x00${feature?.done_tasks}\x00${chat}`
  useEffect(() => {
    if (!target || (!feature && !chat)) return
    let live = true
    const job = feature
      ? featureCall<FeatureView>(target, { op: 'feature', feature: feature.id }).then(out => ({ key, feature: out, error: '' }))
      : featureCall<{ tasks: FeatureTask[] }>(target, { op: 'tasks', mine: true, state: 'all', limit: 30, chat_key: chat }).then(out => ({ key, tasks: out.tasks || [], error: '' }))
    job.then(out => { if (live) setView(out) }).catch(e => { if (live) setView({ key, error: e instanceof Error ? e.message : 'Work is unavailable' }) })
    return () => { live = false }
    // key names target, feature and chat.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])
  const shown = view?.key === key ? view : undefined
  return { target, agent, mine, feature, setPicked, shown, failed: shown?.error || error }
}

/**
 * Work, in the right panel (#3210). An agent in a feature shows the feature: what is happening now and who is on it,
 * what is blocked or waiting on a person, decisions with why, its story and its agents. An agent in no feature shows
 * its own tasks. The server writes all of it.
 */
export function WorkFeaturePanel({ projectId, tabId }: { projectId: string; tabId?: string }) {
  const { target, agent, mine, feature, setPicked, shown, failed } = useAgentWork(projectId, tabId)
  if (!target) return <Empty text="Work shows here once this Crew is open." />
  return <div className="h-full overflow-y-auto p-3 text-xs">
    {mine.length > 1 && <div role="tablist" aria-label="Features" className="mb-2 flex flex-wrap gap-1">
      {mine.map(item => <button key={item.id} type="button" role="tab" aria-selected={item.id === feature?.id} onClick={() => setPicked(item.id)}
        className={`rounded-full border px-2 py-0.5 text-[11px] ${item.id === feature?.id ? 'border-primary/40 bg-primary/10 text-primary' : 'border-border text-muted-foreground hover:text-foreground'}`}>◈ {item.name}</button>)}
    </div>}
    {failed && <p className="mb-2 text-[11px] text-destructive">{failed}</p>}
    {feature ? <FeatureBody feature={feature.name} link={feature.link} view={shown?.feature} />
      : <AgentWork name={agent?.name || 'This agent'} tasks={shown?.tasks} />}
  </div>
}

function Empty({ text }: { text: string }) {
  return <p className="p-3 text-xs text-muted-foreground">{text}</p>
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return <section className="mb-3"><h3 className="mb-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">{title}</h3>{children}</section>
}

function TaskLine({ task }: { task: FeatureTask }) {
  return <li className="flex min-w-0 items-start gap-1.5 py-0.5">
    <TaskStateIcon state={task.state} />
    <span className="min-w-0 flex-1"><span className="block truncate font-medium">{task.title}</span>
      <span className="block truncate text-[10px] text-muted-foreground">{[task.agent || task.person || 'nobody yet', task.last].filter(Boolean).join(' · ')}</span></span>
    {task.issue && <span className="shrink-0 rounded bg-primary/10 px-1 text-[10px] text-primary">{task.issue}</span>}
  </li>
}

function EventLine({ event }: { event: FeatureEvent }) {
  const mark = event.kind === 'decision' ? '◆' : event.kind === 'done' ? '✓' : '·'
  return <li className="flex min-w-0 items-start gap-1.5 py-0.5">
    <span aria-hidden="true" className="shrink-0 text-primary">{mark}</span>
    <span className="min-w-0 flex-1"><span className="block break-words">{event.text}</span>
      {event.why && <span className="block break-words text-[10px] text-muted-foreground">Why: {event.why}</span>}
      <span className="block text-[10px] text-muted-foreground">{[event.by, ago(event.at)].filter(Boolean).join(' · ')}</span></span>
  </li>
}

function FeatureBody({ feature, link, view }: { feature: string; link?: string; view?: FeatureView }) {
  return <>
    <div className="mb-3 flex min-w-0 items-center gap-1.5"><span aria-hidden="true" className="text-primary">◈</span><h2 className="min-w-0 truncate text-sm font-semibold">{feature}</h2>{link && <span className="shrink-0 text-[11px] text-muted-foreground">{link}</span>}</div>
    {!view ? <Empty text="Loading…" /> : <>
      <Section title="Now">
        {view.now.length ? <ol>{view.now.map(task => <TaskLine key={task.id} task={task} />)}</ol> : <p className="text-muted-foreground">Nothing open right now.</p>}
        {view.last_done && <p className="mt-1 truncate text-[11px] text-muted-foreground">Last finished: {view.last_done.text}{view.last_done.by ? ` (${view.last_done.by})` : ''}</p>}
      </Section>
      {view.waiting_on_a_person.length > 0 && <Section title="Waiting on a person"><ol>{view.waiting_on_a_person.map(task => <TaskLine key={task.id} task={task} />)}</ol></Section>}
      {view.blocked.length > 0 && <Section title="Blocked"><ol>{view.blocked.map(task => <TaskLine key={task.id} task={task} />)}</ol></Section>}
      <Section title="Decisions">{view.decisions.length ? <ol>{view.decisions.map(event => <EventLine key={event.id} event={event} />)}</ol> : <p className="text-muted-foreground">No decisions yet. Agents record them with a short why.</p>}</Section>
      <Section title="Story"><ol>{view.story.filter(event => event.kind !== 'decision').map(event => <EventLine key={event.id} event={event} />)}</ol></Section>
      <Section title="Agents"><ul className="flex flex-wrap gap-1">{view.agents.map(item => <li key={item.chat_key} className="rounded-full border border-border px-2 py-0.5 text-[11px]">{item.name}{item.person ? ` · ${item.person}` : ''}</li>)}</ul></Section>
    </>}
  </>
}

function AgentWork({ name, tasks }: { name: string; tasks?: FeatureTask[] }) {
  if (!tasks) return <Empty text="Loading…" />
  const open = tasks.filter(task => task.state !== 'done'), done = tasks.filter(task => task.state === 'done')
  return <>
    <h2 className="mb-3 truncate text-sm font-semibold">{name}'s work</h2>
    <Section title="Open">{open.length ? <ol>{open.map(task => <TaskLine key={task.id} task={task} />)}</ol> : <p className="text-muted-foreground">No open tasks.</p>}</Section>
    {done.length > 0 && <Section title="Finished"><ol>{done.map(task => <TaskLine key={task.id} task={task} />)}</ol></Section>}
    <p className="text-[11px] text-muted-foreground">Finished tasks show here for about two weeks. Put this agent in a feature with ◈ on its top bar to keep the whole story.</p>
  </>
}

/**
 * The small right menu's ◈ block (#3210): the open agent's feature at a glance (what is open now, what waits on a
 * person, the latest decision), or its own open tasks. A click opens the full Work view.
 */
export function FeatureMenuBlock({ projectId, onOpen }: { projectId: string; onOpen: () => void }) {
  const { target, agent, feature, shown, failed } = useAgentWork(projectId)
  if (!target || !agent) return null
  const view = shown?.feature
  const open = feature ? view?.now : shown?.tasks?.filter(task => task.state !== 'done')
  const waiting = view?.waiting_on_a_person.length || 0
  const title = feature ? feature.name : `${agent.name}'s work`
  const count = feature ? feature.open_tasks : open?.length
  return <div className="mb-1 border-b border-border pb-1 text-xs">
    <button type="button" onClick={onOpen} aria-label={`Open ${title}`} className="flex w-full min-w-0 items-center gap-1.5 rounded px-1 py-1 text-left hover:bg-muted">
      <span aria-hidden="true" className={feature ? 'text-primary' : 'text-muted-foreground'}>◈</span>
      <span className="min-w-0 flex-1 truncate font-semibold">{title}</span>
      {feature?.link && <span className="shrink-0 text-[10px] text-muted-foreground">{feature.link}</span>}
      <span className="shrink-0 text-[11px] text-muted-foreground">{count ? `${count} open` : 'Nothing open'}{waiting ? ` · ${waiting} waiting` : ''}</span>
      <ChevronRight aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
    </button>
    {failed ? <p className="px-1 text-[11px] text-destructive">{failed}</p> : <div className="px-1">
      {open && open.length > 0 && <ol>{open.slice(0, 3).map(task => <TaskLine key={task.id} task={task} />)}</ol>}
      {view?.decisions[0] && <ol><EventLine event={view.decisions[0]} /></ol>}
      {!feature && <p className="py-0.5 text-[10px] text-muted-foreground">Not in a feature yet: use ◈ on the agent's top bar.</p>}
    </div>}
  </div>
}

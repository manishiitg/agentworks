import { teamError, type TeamTabPerson } from './teamApi'
import type { TeamNeed, TeamNeedAnswer } from './TeamActivity'
import { CodingCliIcon } from '../../components/chat/CodingCliIcon'
import { AgentPlaceMark } from '../../components/chat/AgentIcon'
import { Avatar, type AvatarFields } from '../../components/ui/Avatar'
import { useEffect, useRef, useState, type ReactNode } from 'react'
import { ArrowLeft, CalendarClock, ChevronRight, Pencil, Users, X } from 'lucide-react'

/** All rows and live lines are already admitted/filtered by the server. */
export type TeamPanelAgent = AvatarFields & {
 purpose?: string; examples?: string[]
 connection?: string; id: string; name: string; person: string; person_id?: string; mine: boolean; team_agent?: boolean
 client_build?:string; update_needed?:boolean; cli?: string; engine?: string; model_id?: string; reasoning_effort?: string
 online: boolean; place: string; activity?: string; last_message?: string; last_message_at?: string; typing?: boolean
 task_state?: string; offline_copy_ready?: boolean
}
export type TeamPanelShared = { key: string; name: string; canChange: boolean; chosen?: number }
export type TeamPanelEvent = { id: string; kind: string; at: string; title: string; verb: string; line?: string; private?: boolean; unseen?:boolean;today?:boolean; bridge?:{thread_id:string;can_open:boolean}; task?:string; task_title?:string; task_state?:string }
export type TeamPanelRoutine = { id: string; name: string; line?: string; enabled: boolean; last_run_at?: string; last_status?: string; next_run_at?: string; chat_id?: string }
export type TeamPanelView = 'live' | 'agents' | 'shared' | 'items' | 'activity' | 'ask'
type Artifact = { id: string; title: string; kind: string; addedBy?: string }
const titles: Record<TeamPanelView,string> = {live:'Team', agents:'All agents', shared:'Shared by you', items:'On the Team', activity:'All activity', ask:'Team agent'}
const activityOpen=(scope:string)=>{try{return localStorage.getItem(`agentworks.team-activity:${scope}`)!=='minimized'}catch{return true}}
const time = (at?:string) => at ? Date.parse(at) || 0 : 0
function age(at?:string) {
 const elapsed = at ? Math.max(0,Date.now()-Date.parse(at)) : NaN
 return !Number.isFinite(elapsed) ? '' : elapsed<60_000 ? 'now' : elapsed<3_600_000 ? `${Math.floor(elapsed/60_000)}m` : elapsed<86_400_000 ? `${Math.floor(elapsed/3_600_000)}h` : `${Math.floor(elapsed/86_400_000)}d`
}
const name = (row:TeamPanelAgent) => row.team_agent ? 'Team agent' : row.name
const working = (row:TeamPanelAgent) => row.activity==='running'||!row.team_agent&&row.activity==='waiting'
const person = (row:TeamPanelAgent) => row.team_agent?'Team':row.person
const status = (row:TeamPanelAgent) => row.task_state==='needs_owner' ? 'needs owner' : row.task_state==='blocked' ? 'blocked' : row.task_state==='stalled' ? 'stalled' : row.typing ? 'typing…' : row.activity==='waiting' ? `waiting for ${row.mine?'you':row.person}` : row.activity==='running' ? 'running' : row.mine||row.online ? 'idle' : 'offline'
const runtime = (row:TeamPanelAgent, includeHelper=false) => [person(row),row.place==='mac'?'Mac':'Server',row.cli||row.engine,includeHelper&&row.client_build&&`AgentWorks ${row.client_build}${row.update_needed?' · Update needed':''}`,row.model_id,row.reasoning_effort].filter(Boolean).join(' · ')

/** Shared Mac/web live panel; detail views replace the right panel, not inline disclosures. */
export function TeamPanel({scope,agents,events,collapsedItem,needs=[],onAnswer,onAsk,unseen=0,today=0,onActivitySeen,onActivityEvent,people=[],selectedPerson,onPersonSelect,personAction,onNavigate,view='live',shared=[],onShared,artifacts=[],onArtifact,routines=[],fullActivity,teamAgentAsk,error,askLabel}: {
 collapsedItem?:TeamPanelEvent|null;
 unseen?:number;today?:number;onActivitySeen?:(through:string)=>void;onActivityEvent?:(event:TeamPanelEvent)=>void
 people?:TeamTabPerson[]; selectedPerson?:string; onPersonSelect?:(id?:string)=>void; personAction?:ReactNode
 scope:string; agents:TeamPanelAgent[]; events:TeamPanelEvent[]; onAsk:(row:TeamPanelAgent)=>void
 needs?:TeamNeed[]; onAnswer?:(need:string,button:string,helper?:string)=>Promise<TeamNeedAnswer>
 view?:TeamPanelView; onNavigate:(view:TeamPanelView)=>void; shared?:TeamPanelShared[]; onShared?:(row:TeamPanelShared)=>void
 artifacts?:Artifact[]; onArtifact?:(id:string)=>void; routines?:TeamPanelRoutine[]; fullActivity?:ReactNode; teamAgentAsk?:ReactNode; error?:string; askLabel?:string
}) {
 const [query,setQuery]=useState('')
 const [recentState,setRecentState]=useState({scope,open:activityOpen(scope)})
 const recentOpen=recentState.scope===scope?recentState.open:activityOpen(scope)
 const seen=useRef('')
 useEffect(()=>{setQuery('')},[scope,view])
 const toggleRecent=()=>{const open=!recentOpen;setRecentState({scope,open});try{localStorage.setItem(`agentworks.team-activity:${scope}`,open?'open':'minimized')}catch{}}

 const rows=agents.filter(row=>row.team_agent||!row.mine)
 const hasHome=rows.some(row=>row.team_agent)
 const busy=rows.filter(working)
 const selected=people.find(person=>person.user_id===selectedPerson&&person.note!=='invited')
 const personal=selected?[...agents.filter(row=>row.person_id===selected.user_id)].sort((a,b)=>Number(working(b))-Number(working(a))):[]
 const recent=events.filter(item=>!item.private&&item.kind!=='question').sort((a,b)=>time(b.at)-time(a.at)).slice(0,4)
 // The server labels each item with its work-log task; items about the same task show together under it.
 const groups=recent.reduce<{task?:string;title?:string;state?:string;items:TeamPanelEvent[]}[]>((out,item)=>{const group=item.task?out.find(g=>g.task===item.task):undefined;if(group)group.items.push(item);else out.push({task:item.task,title:item.task_title,state:item.task_state,items:[item]});return out},[])
 useEffect(()=>{
  const id=recent[0]?.id,key=`${scope}:${id}`
  if(view==='live'&&!selected&&recentOpen&&id&&seen.current!==key&&onActivitySeen){seen.current=key;onActivitySeen(id)}
 },[scope,view,selectedPerson,recentOpen,recent[0]?.id,onActivitySeen])
 const recentItem=(item:TeamPanelEvent)=><li key={item.id}><button type="button" title={`${item.title} ${item.verb} ${item.line||''}`} onClick={()=>onActivityEvent?onActivityEvent(item):onNavigate('activity')} className="flex w-full min-w-0 items-start gap-1.5 rounded px-1 py-1 text-left hover:bg-background/40">
  <span aria-hidden="true" className="shrink-0 text-primary">{item.kind==='message'?'↗':item.kind==='decision'?'◆':item.kind==='finding'?'●':item.kind==='task'&&item.verb==='started'?'▸':item.kind==='task'&&item.verb==='added'?'+':'✓'}</span><span className="min-w-0 flex-1"><span className="block truncate font-medium">{item.unseen&&<span aria-label="Unseen" className="mr-1 inline-block h-1 w-1 rounded-full bg-primary"/>}{item.title} {item.verb}</span>{item.line&&<span className="block truncate text-muted-foreground">{item.line}</span>}</span><span className="shrink-0 text-[10px] text-muted-foreground">{age(item.at)}</span>
 </button></li>
 const button='flex w-full min-w-0 items-start gap-2 rounded-md px-1 py-1.5 text-left hover:bg-background/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
 const agent=(row:TeamPanelAgent)=><button key={row.id} type="button" aria-label={`Open ${row.team_agent?'Team agent':`${row.person}'s ${row.name}`}`} onClick={()=>onAsk(row)} className={button} title={runtime(row,true)}>
  <Avatar {...row} name={person(row)} className="mt-0.5 h-6 w-6 shrink-0 text-[10px]"/><AgentPlaceMark place={row.place==='server'?'server':'mac'}/>
  <span className="min-w-0 flex-1"><span className="block truncate text-xs font-medium">{name(row)}{row.update_needed&&<span> · Update needed</span>}</span>
   {row.purpose && <span className="block text-xs text-muted-foreground">{row.purpose}</span>}
   {!!row.examples?.length && <span className="block text-xs text-muted-foreground">Ask me to: {row.examples.join(' · ')}</span>}
   <span className="block truncate text-[10px] text-muted-foreground">{runtime(row)}</span>
   <span className={`block truncate text-[11px] ${row.activity==='waiting'?'text-amber-600 dark:text-amber-400':working(row)?'text-emerald-600 dark:text-emerald-400':'text-muted-foreground'}`}>
    {status(row)}
    {row.last_message&&<span className="text-muted-foreground"> · {!working(row)&&age(row.last_message_at)?`${age(row.last_message_at)} · `:''}{row.last_message}</span>}
   </span>
   {!row.online&&!working(row)&&<span className="block text-[10px] text-muted-foreground">{row.offline_copy_ready?'Its copy answers':hasHome?'New asks go to the Team agent':'No Team agent available'}</span>}
  </span>
 </button>
 const compactAgent=(row:TeamPanelAgent,showPerson=true)=>{
  const state=row.activity==='waiting'?'waiting':working(row)?'running':'idle'
  return <button key={row.id} type="button" aria-label={`Open ${row.team_agent?'Team agent':`${row.person}'s ${row.name}`}`} onClick={()=>onAsk(row)} title={[runtime(row,true),status(row),row.update_needed&&'Update needed',row.last_message].filter(Boolean).join(' · ')} className={`flex h-6 w-full min-w-0 items-center gap-1.5 rounded px-1 text-left text-[11px] hover:bg-background/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${state==='idle'?'opacity-50':''}`}>
   <CodingCliIcon provider={row.cli||row.engine||'terminal'} className="inline-flex h-3.5 w-3.5 shrink-0 items-center"/>
   <span title={name(row)} className="max-w-[120px] shrink-0 truncate font-medium">{name(row)}</span>
   <span role="img" aria-label={state} title={state} className={`shrink-0 ${state==='waiting'?'text-amber-600 dark:text-amber-400':state==='running'?'text-emerald-600 dark:text-emerald-400':'text-muted-foreground'}`}>{state==='running'?'●':state==='waiting'?'◐':'○'}</span>
   <span className="min-w-0 flex-1 truncate text-muted-foreground">{row.last_message||(state==='idle'?'idle':status(row))}</span>
   <AgentPlaceMark place={row.place==='server'?'server':'mac'}/>
   {showPerson&&<span className="shrink-0" title={person(row)}><Avatar {...row} name={person(row)} connection={row.connection} className="h-3.5 w-3.5 shrink-0 text-[8px]"/></span>}
  </button>
 }
 const event=(item:TeamPanelEvent)=><li key={item.id} className="py-1 text-[11px] text-muted-foreground"><span className="font-medium text-foreground">{item.title}</span> {item.verb}{item.line&&<>: {item.line}</>} <span className="text-[10px]">· {age(item.at)}</span>{item.private&&<span> · Only you</span>}</li>
 const ordered=[...busy,...rows.filter(row=>!working(row)&&row.online),...rows.filter(row=>!working(row)&&!row.online)]
 const results=ordered.filter(row=>[name(row),runtime(row),status(row)].join(' ').toLowerCase().includes(query.trim().toLowerCase()))
 return <section aria-label={view==='live'?'Team live panel':titles[view]} className={`min-w-0 ${view==='live'?'space-y-1 text-[11px]':'space-y-2 text-sm'}`}>
  {view!=='live'&&<header className="sticky top-0 z-10 space-y-2 bg-surface-panel pb-2"><button type="button" onClick={()=>onNavigate('live')} className="flex items-center gap-1 text-xs text-primary"><ArrowLeft aria-hidden="true" className="h-3 w-3"/>Team</button><h2 className="text-sm font-semibold">{view === 'ask' ? askLabel || titles.ask : titles[view]}</h2></header>}
  {view==='live'&&<>
   {selected?<section aria-label={`${selected.name}'s agents`}>
    <div className="flex items-center gap-1.5 border-t border-border px-1 py-1"><Avatar {...selected} name={selected.name} className="h-4 w-4 shrink-0 aspect-square leading-none text-[8px]"/><span className="min-w-0 truncate font-medium">{selected.name}</span><span className="shrink-0 text-muted-foreground">· {personal.length} agents</span><button type="button" aria-label="Back to working agents" onClick={()=>onPersonSelect?.(undefined)} className="ml-auto"><X className="h-3 w-3"/></button></div>
    {personal.map(row=>compactAgent(row,false))}
    {!personal.length&&<p className="px-1 py-2 text-xs text-muted-foreground">{selected.name} hasn't added an agent to this Team yet.</p>}
    {personal.length>0&&personAction}
   </section>:busy.length>0?<section aria-label="Working now">{busy.map(row=>compactAgent(row))}</section>:null}
   {!selected&&needs.length>0&&<section aria-label="Needs you" className="mt-1 border-t border-border pt-1"><ol className="space-y-1">{needs.map(need=><NeedCard key={need.id} need={need} onAnswer={onAnswer}/>)}</ol></section>}
   {!selected&&<section aria-label="Just happened" className="mt-1 border-t border-border pt-1">
    <div className="flex min-w-0 items-center gap-1 px-1"><button type="button" aria-label="Toggle Just happened" aria-expanded={recentOpen} onClick={toggleRecent} className="flex h-6 min-w-0 flex-1 items-center gap-1 text-left text-[10px] text-muted-foreground"><ChevronRight aria-hidden="true" className={`h-3 w-3 shrink-0 ${recentOpen?'rotate-90':''}`}/>{recentOpen?<span>Just happened</span>:<span className="min-w-0 truncate">{collapsedItem?`${collapsedItem.title} ${collapsedItem.verb} ${collapsedItem.line||''}`:'Just happened · Nothing new'}</span>}</button>{unseen>0&&<span className="shrink-0 rounded-full bg-primary px-1.5 text-[10px] text-primary-foreground">{unseen} new</span>}{recentOpen&&<button type="button" onClick={()=>onNavigate('activity')} className="shrink-0 text-[10px] text-primary">All ›</button>}</div>
    {recentOpen&&<><ol>{groups.map(group=>group.task?<li key={`task:${group.task}`} className="mb-1 rounded border border-border/60"><div className="flex min-w-0 items-center gap-1 px-1 pt-1 text-[10px]"><span className="shrink-0 rounded bg-primary/10 px-1 font-semibold text-primary">{group.task}</span><span className="min-w-0 flex-1 truncate font-medium">{group.title}</span>{group.state&&<span className="shrink-0 text-muted-foreground">{group.state}</span>}</div><ol>{group.items.map(recentItem)}</ol></li>:recentItem(group.items[0]))}</ol>{Math.max(0,today-recent.filter(item=>item.today).length)>0&&<button type="button" onClick={()=>onNavigate('activity')} className="px-1 text-[11px] text-primary">▸ {Math.max(0,today-recent.filter(item=>item.today).length)} more today</button>}{!recent.length&&<p className="px-1 text-[10px] text-muted-foreground">Nothing new</p>}</>}
   </section>}
  </>}
  {view==='agents'&&<><input type="search" aria-label="Search Team agents" placeholder="Search agents or people…" value={query} onChange={e=>setQuery(e.target.value)} className="w-full rounded border border-border bg-background px-2 py-1.5 text-xs"/><div aria-label="All Team agents" className="space-y-0.5">{results.map(row=>agent(row))}{!results.length&&<p className="text-xs text-muted-foreground">No matching agents.</p>}</div></>}
  {view==='shared'&&<div aria-label="Your shared agents">{shared.map(row=><button key={row.key} type="button" onClick={()=>onShared?.(row)} className={button}><Users aria-hidden="true" className="h-4 w-4 shrink-0 text-muted-foreground"/><span className="min-w-0 flex-1"><span className="block truncate text-xs">{row.name}</span><span className="block text-[10px] text-muted-foreground">{row.canChange?'Can make changes':'Answers only'} · {row.chosen===undefined?'Whole team':`${row.chosen} chosen`}</span></span><Pencil aria-hidden="true" className="h-3 w-3 text-muted-foreground"/></button>)}</div>}
  {view==='items'&&<div aria-label="Team artifacts and automations">{artifacts.map(row=><button key={row.id} type="button" onClick={()=>onArtifact?.(row.id)} className={button}><span className="min-w-0"><span className="block truncate text-xs">{row.title}</span><span className="block text-[10px] text-muted-foreground">{row.kind}{row.addedBy?` · Added by ${row.addedBy}`:''}</span></span></button>)}{routines.map(row=><div key={row.id} className="flex gap-2 px-1 py-1.5"><CalendarClock aria-hidden="true" className="h-4 w-4 shrink-0 text-muted-foreground"/><span className="min-w-0"><span className="block truncate text-xs">{row.name}</span><span className="block text-[10px] text-muted-foreground">{row.line}{!row.enabled?' · off':''}</span></span></div>)}</div>}
  {view==='activity'&&(fullActivity||<ol>{events.map(event)}</ol>)}
  {view==='ask'&&teamAgentAsk}
  {error&&<p role="alert" className="text-[11px] text-destructive">{error}</p>}
 </section>
}

/** One pinned Needs-you card. Only the person asked gets buttons; everyone else sees who it waits on. */
function NeedCard({need,onAnswer}:{need:TeamNeed;onAnswer?:(need:string,button:string,helper?:string)=>Promise<TeamNeedAnswer>}) {
 const [busy,setBusy]=useState('')
 const [note,setNote]=useState('')
 const [choose,setChoose]=useState<TeamNeedAnswer['choose_agent']>()
 const click=async(button:string,helper?:string)=>{
  if(!onAnswer)return
  setBusy(button);setNote('')
  try {const out=await onAnswer(need.id,button,helper);setChoose(out.choose_agent);if(!out.choose_agent)setNote(out.note||'')}
  catch(e){setNote(teamError(e))}
  finally{setBusy('')}
 }
 return <li className="rounded border border-amber-500/50 bg-amber-500/10 px-1.5 py-1 text-[11px]">
  <div className="flex min-w-0 items-center gap-1 text-[10px]"><span className="shrink-0 font-semibold text-amber-700 dark:text-amber-300">{need.for_me?'Needs you':`Needs ${need.for}`}</span><span className="min-w-0 truncate text-muted-foreground">· {need.person}'s {need.agent} · {age(need.at)}</span></div>
  {need.task&&<div className="mt-0.5 flex min-w-0 items-center gap-1 text-[10px]"><span className="shrink-0 rounded bg-primary/10 px-1 font-semibold text-primary">{need.task}</span><span className="min-w-0 truncate">{need.task_title}</span></div>}
  <p className="mt-0.5 break-words">{need.line}</p>
  {need.link&&<a href={need.link} target="_blank" rel="noreferrer" className="text-[10px] text-primary">Open link ›</a>}
  {need.for_me?<div className="mt-1 flex flex-wrap gap-1">{need.buttons.map(button=><button key={button} type="button" disabled={!!busy} onClick={()=>void click(button)} className="rounded border border-border bg-background px-1.5 py-0.5 text-[10px] hover:bg-muted disabled:opacity-50">{busy===button?'Sending…':button}</button>)}</div>
   :<p className="mt-0.5 text-[10px] text-muted-foreground">Waiting for {need.for}</p>}
  {choose&&<div className="mt-1 text-[10px]"><p className="text-muted-foreground">Which of your agents should handle requests like this? It's remembered.</p><div className="mt-0.5 flex flex-wrap gap-1">{choose.map(agent=><button key={agent.id} type="button" disabled={!!busy} onClick={()=>void click('Ask my agent',agent.id)} className="rounded border border-border bg-background px-1.5 py-0.5 hover:bg-muted disabled:opacity-50">{agent.name}{!agent.online&&' (offline)'}</button>)}</div></div>}
  {note&&<p className="mt-0.5 text-[10px] text-muted-foreground">{note}</p>}
 </li>
}

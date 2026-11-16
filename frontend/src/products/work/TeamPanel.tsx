import type { TeamTabPerson } from './teamApi'
import { CodingCliIcon } from '../../components/chat/CodingCliIcon'
import { AgentPlaceMark } from '../../components/chat/AgentIcon'
import { Avatar, type AvatarFields } from '../../components/ui/Avatar'
import { useEffect, useState, type ReactNode } from 'react'
import { ArrowLeft, CalendarClock, ChevronRight, Pencil, Users, X } from 'lucide-react'

/** All rows and live lines are already admitted/filtered by the server. */
export type TeamPanelAgent = AvatarFields & {
 connection?: string; id: string; name: string; person: string; person_id?: string; mine: boolean; team_agent?: boolean
 client_build?:string; update_needed?:boolean; cli?: string; engine?: string; model_id?: string; reasoning_effort?: string
 online: boolean; place: string; activity?: string; last_message?: string; last_message_at?: string; typing?: boolean
 offline_copy_ready?: boolean
}
export type TeamPanelShared = { key: string; name: string; canChange: boolean; chosen?: number }
export type TeamPanelEvent = { id: string; kind: string; at: string; title: string; verb: string; line?: string; private?: boolean }
export type TeamPanelRoutine = { id: string; name: string; line?: string; enabled: boolean; last_run_at?: string; last_status?: string; next_run_at?: string; chat_id?: string }
export type TeamPanelView = 'live' | 'agents' | 'shared' | 'items' | 'activity' | 'ask'
type Artifact = { id: string; title: string; kind: string; addedBy?: string }
const titles: Record<TeamPanelView,string> = {live:'Team', agents:'All agents', shared:'Shared by you', items:'On the Team', activity:'All activity', ask:'Team agent'}
const time = (at?:string) => at ? Date.parse(at) || 0 : 0
function age(at?:string) {
 const elapsed = at ? Math.max(0,Date.now()-Date.parse(at)) : NaN
 return !Number.isFinite(elapsed) ? '' : elapsed<60_000 ? 'now' : elapsed<3_600_000 ? `${Math.floor(elapsed/60_000)}m` : elapsed<86_400_000 ? `${Math.floor(elapsed/3_600_000)}h` : `${Math.floor(elapsed/86_400_000)}d`
}
const name = (row:TeamPanelAgent) => row.team_agent ? 'Team agent' : row.name
const working = (row:TeamPanelAgent) => row.activity==='running'||!row.team_agent&&row.activity==='waiting'
const person = (row:TeamPanelAgent) => row.team_agent?'Team':row.person
const status = (row:TeamPanelAgent) => row.typing ? 'typing…' : row.activity==='waiting' ? `waiting for ${row.mine?'you':row.person}` : row.activity==='running' ? 'running' : row.online ? 'idle' : 'offline'
const runtime = (row:TeamPanelAgent, includeHelper=false) => [person(row),row.place==='mac'?'Mac':'Server',row.cli||row.engine,includeHelper&&row.client_build&&`AgentWorks ${row.client_build}${row.update_needed?' · Update needed':''}`,row.model_id,row.reasoning_effort].filter(Boolean).join(' · ')

/** Shared Mac/web live panel; detail views replace the right panel, not inline disclosures. */
export function TeamPanel({scope,agents,events,onAsk,people=[],selectedPerson,onPersonSelect,askFromAgent,onNavigate,view='live',shared=[],onShared,artifacts=[],onArtifact,routines=[],fullActivity,teamAgentAsk,error,askLabel}: {
 people?:TeamTabPerson[]; selectedPerson?:string; onPersonSelect?:(id?:string)=>void; askFromAgent?:(row:TeamPanelAgent)=>ReactNode
 scope:string; agents:TeamPanelAgent[]; events:TeamPanelEvent[]; onAsk:(row:TeamPanelAgent)=>void
 view?:TeamPanelView; onNavigate:(view:TeamPanelView)=>void; shared?:TeamPanelShared[]; onShared?:(row:TeamPanelShared)=>void
 artifacts?:Artifact[]; onArtifact?:(id:string)=>void; routines?:TeamPanelRoutine[]; fullActivity?:ReactNode; teamAgentAsk?:ReactNode; error?:string; askLabel?:string
}) {
 const [query,setQuery]=useState('')
 const [recentOpen,setRecentOpen]=useState(false)
 useEffect(()=>{setQuery('');setRecentOpen(false)},[scope,view])
 const rows=agents.filter(row=>row.team_agent||!row.mine)
 const hasHome=rows.some(row=>row.team_agent)
 const busy=rows.filter(working)
 const selected=people.find(person=>person.user_id===selectedPerson&&person.note!=='invited')
 const personal=selected?[...agents.filter(row=>row.person_id===selected.user_id)].sort((a,b)=>Number(working(b))-Number(working(a))):[]
 const recent=events.filter(item=>!item.private&&item.kind!=='question').sort((a,b)=>time(b.at)-time(a.at)).slice(0,3)
 const button='flex w-full min-w-0 items-start gap-2 rounded-md px-1 py-1.5 text-left hover:bg-background/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
 const agent=(row:TeamPanelAgent)=><button key={row.id} type="button" aria-label={`Open ${row.team_agent?'Team agent':`${row.person}'s ${row.name}`}`} onClick={()=>onAsk(row)} className={button} title={runtime(row,true)}>
  <Avatar {...row} name={person(row)} className="mt-0.5 h-6 w-6 shrink-0 text-[10px]"/><AgentPlaceMark place={row.place==='server'?'server':'mac'}/>
  <span className="min-w-0 flex-1"><span className="block truncate text-xs font-medium">{name(row)}{row.update_needed&&<span> · Update needed</span>}</span>
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
   <CodingCliIcon provider={row.cli||row.engine}/>
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
    <div className="flex items-center gap-1.5 border-t border-border px-1 py-1"><Avatar {...selected} name={selected.name} className="h-4 w-4 text-[8px]"/><span className="min-w-0 truncate font-medium">{selected.name}</span><span className="shrink-0 text-muted-foreground">· {personal.length} agents</span><button type="button" aria-label="Back to working agents" onClick={()=>onPersonSelect?.(undefined)} className="ml-auto"><X className="h-3 w-3"/></button></div>
    {personal.map(row=><div key={row.id}>{compactAgent(row,false)}{askFromAgent?.(row)}</div>)}
   </section>:busy.length>0?<section aria-label="Working now">{busy.map(row=>compactAgent(row))}</section>:null}
   {!selected&&recent.length>0&&<section aria-label="Just happened"><button type="button" aria-expanded={recentOpen} onClick={()=>setRecentOpen(open=>!open)} className="flex h-6 w-full items-center gap-1 px-1 text-[10px] text-muted-foreground"><ChevronRight aria-hidden="true" className={`h-3 w-3 ${recentOpen?'rotate-90':''}`}/><span>Just happened</span><span className="ml-auto">{recent.length} new</span></button>{recentOpen&&<ol>{recent.map(item=>{
    const text=`${item.title} ${item.verb}${item.line?`: ${item.line}`:''}`
    return <li key={item.id}><button type="button" title={text} onClick={()=>onNavigate('activity')} className="flex h-6 w-full min-w-0 items-center gap-1 px-1 text-left text-[11px] text-muted-foreground hover:bg-background/40"><span className="min-w-0 flex-1 truncate">{text}</span><span className="shrink-0 text-[10px]">· {age(item.at)}</span></button></li>
   })}</ol>}</section>}
  </>}
  {view==='agents'&&<><input type="search" aria-label="Search Team agents" placeholder="Search agents or people…" value={query} onChange={e=>setQuery(e.target.value)} className="w-full rounded border border-border bg-background px-2 py-1.5 text-xs"/><div aria-label="All Team agents" className="space-y-0.5">{results.map(row=>agent(row))}{!results.length&&<p className="text-xs text-muted-foreground">No matching agents.</p>}</div></>}
  {view==='shared'&&<div aria-label="Your shared agents">{shared.map(row=><button key={row.key} type="button" onClick={()=>onShared?.(row)} className={button}><Users aria-hidden="true" className="h-4 w-4 shrink-0 text-muted-foreground"/><span className="min-w-0 flex-1"><span className="block truncate text-xs">{row.name}</span><span className="block text-[10px] text-muted-foreground">{row.canChange?'Can make changes':'Answers only'} · {row.chosen===undefined?'Whole team':`${row.chosen} chosen`}</span></span><Pencil aria-hidden="true" className="h-3 w-3 text-muted-foreground"/></button>)}</div>}
  {view==='items'&&<div aria-label="Team artifacts and automations">{artifacts.map(row=><button key={row.id} type="button" onClick={()=>onArtifact?.(row.id)} className={button}><span className="min-w-0"><span className="block truncate text-xs">{row.title}</span><span className="block text-[10px] text-muted-foreground">{row.kind}{row.addedBy?` · Added by ${row.addedBy}`:''}</span></span></button>)}{routines.map(row=><div key={row.id} className="flex gap-2 px-1 py-1.5"><CalendarClock aria-hidden="true" className="h-4 w-4 shrink-0 text-muted-foreground"/><span className="min-w-0"><span className="block truncate text-xs">{row.name}</span><span className="block text-[10px] text-muted-foreground">{row.line}{!row.enabled?' · off':''}</span></span></div>)}</div>}
  {view==='activity'&&(fullActivity||<ol>{events.map(event)}</ol>)}
  {view==='ask'&&teamAgentAsk}
  {error&&<p role="alert" className="text-[11px] text-destructive">{error}</p>}
 </section>
}

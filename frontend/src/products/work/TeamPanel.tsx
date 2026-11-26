import { agentPurposeTooltip } from '../../utils/agentPurposeTooltip'
import type { TeamOnlineProject, TeamTabPerson } from './teamApi'
import { CodingCliIcon } from '../../components/chat/CodingCliIcon'
import { AgentPlaceMark } from '../../components/chat/AgentIcon'
import { Avatar, type AvatarFields } from '../../components/ui/Avatar'
import { useEffect, useState, type ReactNode } from 'react'
import { ArrowLeft, Globe, Lock, Pencil, Users, X } from 'lucide-react'

/** All rows and live lines are already admitted/filtered by the server. */
export type TeamPanelAgent = AvatarFields & {
 purpose?: string; examples?: string[]
 connection?: string; id: string; name: string; person: string; person_id?: string; online_project?: string; mine: boolean
 client_build?:string; update_needed?:boolean; cli?: string; engine?: string; model_id?: string; reasoning_effort?: string
 online: boolean; place: string; activity?: string; last_message?: string; last_message_at?: string; typing?: boolean
 task_state?: string; chat_readable?: boolean; offline_line?: string; chat_share?: string
}
export type TeamPanelShared = { key: string; name: string; chosen?: number }
export type TeamPanelEvent = { id: string; kind: string; at: string; title: string; verb: string; line?: string; private?: boolean; unseen?:boolean;today?:boolean; cli?:string; bridge?:{thread_id:string;can_open:boolean}; task?:string; task_title?:string; task_state?:string }
export type TeamPanelView = 'live' | 'agents' | 'shared' | 'ask'
const titles: Record<TeamPanelView,string> = {live:'Team', agents:'All agents', shared:'Shared by you', ask:'Agent'}
function age(at?:string) {
 const elapsed = at ? Math.max(0,Date.now()-Date.parse(at)) : NaN
 return !Number.isFinite(elapsed) ? '' : elapsed<60_000 ? 'now' : elapsed<3_600_000 ? `${Math.floor(elapsed/60_000)}m` : elapsed<86_400_000 ? `${Math.floor(elapsed/3_600_000)}h` : `${Math.floor(elapsed/86_400_000)}d`
}
const name = (row:TeamPanelAgent) => row.name
const working = (row:TeamPanelAgent) => row.activity==='running'||row.activity==='waiting'
const person = (row:TeamPanelAgent) => row.person
const status = (row:TeamPanelAgent) => row.task_state==='needs_you' ? 'needs you' : row.task_state==='blocked' ? 'blocked' : row.task_state==='stalled' ? 'stalled' : row.typing ? 'typing…' : row.activity==='waiting' ? `waiting for ${row.mine?'you':row.person}` : row.activity==='running' ? 'running' : row.mine||row.online ? 'idle' : 'offline'
const runtime = (row:TeamPanelAgent, includeHelper=false) => [person(row),row.place==='mac'?'Mac':'Server',row.cli||row.engine,includeHelper&&row.client_build&&`AgentWorks ${row.client_build}${row.update_needed?' · Update needed':''}`,row.model_id,row.reasoning_effort].filter(Boolean).join(' · ')

/** Shared Mac/web live panel; detail views replace the right panel, not inline disclosures. */
export function TeamPanel({scope,agents,onAsk,people=[],selectedPerson,onPersonSelect,onNavigate,view='live',shared=[],onShared,teamAgentAsk,askLabel,files,onlineProjects=[],onOpenProject}: {
 people?:TeamTabPerson[]; selectedPerson?:string; onPersonSelect?:(id?:string)=>void
 /** Server projects with this Team in their Users (from the server); their agents are the rows whose online_project is the project. */
 onlineProjects?:TeamOnlineProject[]
 /** Opens the online project on its server's website. */
 onOpenProject?:(id:string)=>void
 scope:string; agents:TeamPanelAgent[]; onAsk:(row:TeamPanelAgent)=>void
 view?:TeamPanelView; onNavigate:(view:TeamPanelView)=>void; shared?:TeamPanelShared[]; onShared?:(row:TeamPanelShared)=>void
 teamAgentAsk?:ReactNode; askLabel?:string
 /** The shared folder's sync row (TeamFilesRow), under the working agents in the live view. */
 files?:ReactNode
}) {
 const [query,setQuery]=useState('')
 const [allOf,setAllOf]=useState<string>()
 useEffect(()=>{setQuery('');setAllOf(undefined)},[scope,view])

 const rows=agents.filter(row=>!row.mine)
 // Online projects' agents show in their own group (below), so Working now lists people's agents only.
 const busy=rows.filter(row=>working(row)&&!row.online_project)
 const selected=people.find(person=>person.user_id===selectedPerson&&person.note!=='invited')
 const personal=selected?[...agents.filter(row=>row.person_id===selected.user_id)].sort((a,b)=>Number(working(b))-Number(working(a))):[]
 const button='flex w-full min-w-0 items-start gap-2 rounded-md px-1 py-1.5 text-left hover:bg-background/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
 const agent=(row:TeamPanelAgent)=><button key={row.id} type="button" aria-label={`Open ${row.person}'s ${row.name}`} onClick={()=>onAsk(row)} className={button} title={agentPurposeTooltip(row,runtime(row,true))}>
  <Avatar {...row} name={person(row)} className="mt-0.5 h-6 w-6 shrink-0 text-[10px]"/><AgentPlaceMark place={row.place==='server'?'server':'mac'}/>
  <span className="min-w-0 flex-1"><span className="block truncate text-xs font-medium">{name(row)}{row.update_needed&&<span> · Update needed</span>}</span>
   {row.purpose && <span className="block text-xs text-muted-foreground">{row.purpose}</span>}
   {!!row.examples?.length && <span className="block text-xs text-muted-foreground">Ask me to: {row.examples.join(' · ')}</span>}
   <span className="block truncate text-[10px] text-muted-foreground">{runtime(row)}</span>
   <span className={`block truncate text-[11px] ${row.activity==='waiting'?'text-amber-600 dark:text-amber-400':working(row)?'text-emerald-600 dark:text-emerald-400':'text-muted-foreground'}`}>
    {status(row)}
    {row.last_message&&<span className="text-muted-foreground"> · {!working(row)&&age(row.last_message_at)?`${age(row.last_message_at)} · `:''}{row.last_message}</span>}
   </span>
   {!row.online&&!working(row)&&row.offline_line&&<span className="block text-[10px] text-muted-foreground">{row.offline_line}</span>}
  </span>
 </button>
 const compactAgent=(row:TeamPanelAgent,showPerson=true)=>{
  const state=row.activity==='waiting'?'waiting':working(row)?'running':'idle'
  return <button key={row.id} type="button" aria-label={`Open ${row.person}'s ${row.name}`} onClick={()=>onAsk(row)} title={agentPurposeTooltip(row,[runtime(row,true),status(row),row.update_needed&&'Update needed',row.last_message].filter(Boolean).join(' · '))} className={`flex h-6 w-full min-w-0 items-center gap-1.5 rounded px-1 text-left text-[11px] hover:bg-background/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${state==='idle'?'opacity-50':''}`}>
   <CodingCliIcon provider={row.cli||row.engine||'terminal'} className="inline-flex h-3.5 w-3.5 shrink-0 items-center"/>
   <span className="max-w-[120px] shrink-0 truncate font-medium">{name(row)}</span>
   {!row.mine&&row.chat_share==='agent'&&<span role="img" aria-label="Chat hidden" title="Chat hidden" className="shrink-0"><Lock aria-hidden="true" className="h-3 w-3 text-muted-foreground"/></span>}
   <span role="img" aria-label={state} title={state} className={`shrink-0 ${state==='waiting'?'text-amber-600 dark:text-amber-400':state==='running'?'text-emerald-600 dark:text-emerald-400':'text-muted-foreground'}`}>{state==='running'?'●':state==='waiting'?'◐':'○'}</span>
   <span className="min-w-0 flex-1 truncate text-muted-foreground">{row.last_message||(state==='idle'?'idle':status(row))}</span>
   <AgentPlaceMark place={row.place==='server'?'server':'mac'}/>
   {showPerson&&<span className="shrink-0" title={person(row)}><Avatar {...row} name={person(row)} connection={row.connection} className="h-3.5 w-3.5 shrink-0 text-[8px]"/></span>}
  </button>
 }
 const ordered=[...busy,...rows.filter(row=>!working(row)&&row.online),...rows.filter(row=>!working(row)&&!row.online)]
 const results=ordered.filter(row=>[name(row),runtime(row),status(row)].join(' ').toLowerCase().includes(query.trim().toLowerCase()))
 return <section aria-label={view==='live'?'Team live panel':titles[view]} className={`min-w-0 ${view==='live'?'space-y-1 text-[11px]':'space-y-2 text-sm'}`}>
  {view!=='live'&&<header className="sticky top-0 z-10 space-y-2 bg-surface-panel pb-2"><button type="button" onClick={()=>onNavigate('live')} className="flex items-center gap-1 text-xs text-primary"><ArrowLeft aria-hidden="true" className="h-3 w-3"/>Team</button><h2 className="text-sm font-semibold">{view === 'ask' ? askLabel || titles.ask : titles[view]}</h2></header>}
  {view==='live'&&<>
   {selected?<section aria-label={`${selected.name}'s agents`}>
    <div className="flex items-center gap-1.5 border-t border-border px-1 py-1"><Avatar {...selected} name={selected.name} className="h-4 w-4 shrink-0 aspect-square leading-none text-[8px]"/><span className="min-w-0 truncate font-medium">{selected.name}</span><button type="button" aria-label="Back to working agents" onClick={()=>onPersonSelect?.(undefined)} className="ml-auto"><X className="h-3 w-3"/></button></div>
    {personal.map(row=>compactAgent(row,false))}
    {!personal.length&&<p className="px-1 py-2 text-xs text-muted-foreground">{selected.name} hasn't added an agent to this Team yet.</p>}
   </section>:busy.length>0?<section aria-label="Working now">{busy.map(row=>compactAgent(row))}</section>:null}
   {!selected&&onlineProjects.length>0&&<section aria-label="Online projects this Team uses" className="pt-1">
    <p className="px-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">Online projects this Team uses</p>
    {onlineProjects.map(mate=>{
     const own=rows.filter(row=>row.online_project===mate.project_id)
     const shown=allOf===mate.id?own:own.slice(0,4)
     return <div key={mate.id}>
      <div className="flex min-w-0 items-center gap-1.5 px-1 py-1" title={`${mate.name} · ${mate.line}`}>
       <span className="relative inline-flex h-4 w-4 shrink-0 items-center justify-center rounded-full bg-emerald-100 text-emerald-700 dark:bg-emerald-900 dark:text-emerald-300"><Globe aria-hidden="true" className="h-2.5 w-2.5"/><span className="absolute -bottom-px -right-px h-1.5 w-1.5 rounded-full border border-background bg-emerald-500"/></span>
       <span className="min-w-0 truncate font-medium">{mate.name}</span><span className="min-w-0 truncate text-[10px] text-muted-foreground">{mate.line}</span>
       {onOpenProject&&<button type="button" onClick={()=>onOpenProject(mate.id)} className="ml-auto shrink-0 text-[11px] text-primary hover:underline">Open ↗</button>}
      </div>
      <div className="pl-3">{shown.map(row=>compactAgent(row,false))}
       {own.length>4&&<button type="button" onClick={()=>setAllOf(allOf===mate.id?undefined:mate.id)} className="px-1 text-[11px] text-primary">{allOf===mate.id?'Show fewer':`+ ${own.length-4} more agents`}</button>}
       {!own.length&&<p className="px-1 text-[11px] text-muted-foreground">No agents shared with the Team yet.</p>}
      </div>
     </div>})}
   </section>}
   {!selected&&files}
  </>}
  {view==='agents'&&<><input type="search" aria-label="Search Team agents" placeholder="Search agents or people…" value={query} onChange={e=>setQuery(e.target.value)} className="w-full rounded border border-border bg-background px-2 py-1.5 text-xs"/><div aria-label="All Team agents" className="space-y-0.5">{results.map(row=>agent(row))}{!results.length&&<p className="text-xs text-muted-foreground">No matching agents.</p>}</div></>}
  {view==='shared'&&<div aria-label="Your shared agents">{shared.map(row=><button key={row.key} type="button" onClick={()=>onShared?.(row)} className={button}><Users aria-hidden="true" className="h-4 w-4 shrink-0 text-muted-foreground"/><span className="min-w-0 flex-1"><span className="block truncate text-xs">{row.name}</span><span className="block text-[10px] text-muted-foreground">{row.chosen===undefined?'Whole team':`${row.chosen} chosen`}</span></span><Pencil aria-hidden="true" className="h-3 w-3 text-muted-foreground"/></button>)}</div>}
  {view==='ask'&&teamAgentAsk}
 </section>
}

import { Avatar, type AvatarFields } from '../../components/ui/Avatar'
import { useEffect, useState, type ReactNode } from 'react'
import { ArrowLeft, CalendarClock, ChevronRight, Cloud, Pencil, Users } from 'lucide-react'

/** All rows and live lines are already admitted/filtered by the server. */
export type TeamPanelAgent = AvatarFields & {
 connection?: string; id: string; name: string; person: string; person_id?: string; mine: boolean; team_agent?: boolean
 cli?: string; engine?: string; model_id?: string; reasoning_effort?: string
 online: boolean; place: string; activity?: string; last_message?: string; last_message_at?: string; typing?: boolean
 offline_copy_ready?: boolean
}
export type TeamPanelShared = { key: string; name: string; canChange: boolean; chosen?: number }
export type TeamPanelEvent = { id: string; kind: string; at: string; title: string; verb: string; line?: string; private?: boolean }
export type TeamPanelRoutine = { id: string; name: string; line?: string; enabled: boolean; last_run_at?: string; last_status?: string; chat_id?: string }
export type TeamPanelView = 'live' | 'agents' | 'shared' | 'items' | 'activity'
type Artifact = { id: string; title: string; kind: string; addedBy?: string }
const titles: Record<TeamPanelView,string> = {live:'Team', agents:'All agents', shared:'Shared by you', items:'On the Team', activity:'All activity'}
const time = (at?:string) => at ? Date.parse(at) || 0 : 0
function age(at?:string) {
 const elapsed = at ? Math.max(0,Date.now()-Date.parse(at)) : NaN
 return !Number.isFinite(elapsed) ? '' : elapsed<60_000 ? 'now' : elapsed<3_600_000 ? `${Math.floor(elapsed/60_000)}m` : elapsed<86_400_000 ? `${Math.floor(elapsed/3_600_000)}h` : `${Math.floor(elapsed/86_400_000)}d`
}
const name = (row:TeamPanelAgent) => row.team_agent ? 'Team agent' : row.name
const working = (row:TeamPanelAgent) => row.activity==='running'||row.activity==='waiting'
const status = (row:TeamPanelAgent) => row.team_agent ? row.typing?'always online · typing…':row.activity==='running'?'always online · running':row.activity==='waiting'?'always online · waiting':'always online' : row.typing ? 'typing…' : row.activity==='waiting' ? `waiting for ${row.person}` : row.activity==='running' ? 'running' : row.online ? 'idle' : 'offline'
const runtime = (row:TeamPanelAgent) => [row.person,row.place==='mac'?'Mac':'Server',row.cli||row.engine,row.model_id,row.reasoning_effort].filter(Boolean).join(' · ')

/** Shared Mac/web live panel; detail views replace the right panel, not inline disclosures. */
export function TeamPanel({scope,agents,events,onAsk,onNavigate,view='live',shared=[],onShared,artifacts=[],onArtifact,routines=[],fullActivity,error}: {
 scope:string; agents:TeamPanelAgent[]; events:TeamPanelEvent[]; onAsk:(row:TeamPanelAgent)=>void
 view?:TeamPanelView; onNavigate:(view:TeamPanelView)=>void; shared?:TeamPanelShared[]; onShared?:(row:TeamPanelShared)=>void
 artifacts?:Artifact[]; onArtifact?:(id:string)=>void; routines?:TeamPanelRoutine[]; fullActivity?:ReactNode; error?:string
}) {
 const [query,setQuery]=useState('')
 useEffect(()=>setQuery(''),[scope,view])
 const rows=agents.filter(row=>row.team_agent||!row.mine)
 const homes=rows.filter(row=>row.team_agent), peers=rows.filter(row=>!row.team_agent)
 const busy=peers.filter(working)
 const people=new Map<string,TeamPanelAgent[]>()
 for(const row of busy) {const key=row.person_id||row.person;people.set(key,[...(people.get(key)||[]),row])}
 const recent=events.filter(item=>!item.private&&item.kind!=='question').sort((a,b)=>time(b.at)-time(a.at)).slice(0,4)
 const latestRun=[...routines].filter(row=>row.last_run_at&&(!row.chat_id||row.chat_id==='main')).sort((a,b)=>time(b.last_run_at)-time(a.last_run_at))[0]
 const button='flex w-full min-w-0 items-start gap-2 rounded-md px-1 py-1.5 text-left hover:bg-background/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
 const agent=(row:TeamPanelAgent,compact=false)=><button key={row.id} type="button" onClick={()=>onAsk(row)} className={`${button} ${compact?'pl-9':''}`} title={runtime(row)}>
  {row.team_agent?<Cloud aria-hidden="true" className="mt-0.5 h-5 w-5 shrink-0 text-primary"/>:!compact?<Avatar {...row} name={row.person} className="mt-0.5 h-6 w-6 shrink-0 text-[10px]"/>:null}
  <span className="min-w-0 flex-1"><span className="block truncate text-xs font-medium">{name(row)}</span>
   {!compact&&!row.team_agent&&<span className="block truncate text-[10px] text-muted-foreground">{runtime(row)}</span>}
   <span className={`block truncate text-[11px] ${row.activity==='waiting'?'text-amber-600 dark:text-amber-400':working(row)?'text-emerald-600 dark:text-emerald-400':'text-muted-foreground'}`}>
    {row.team_agent&&!working(row)&&latestRun?`always online · last ran ${latestRun.name} · ${age(latestRun.last_run_at)}`:status(row)}
    {row.last_message&&<span className="text-muted-foreground"> · {!working(row)&&age(row.last_message_at)?`${age(row.last_message_at)} · `:''}{row.last_message}</span>}
   </span>
   {!row.online&&!working(row)&&<span className="block text-[10px] text-muted-foreground">{row.offline_copy_ready?'Its copy answers':homes.length?'New asks go to the Team agent':'No Team agent available'}</span>}
  </span>
 </button>
 const event=(item:TeamPanelEvent)=><li key={item.id} className="py-1 text-[11px] text-muted-foreground"><span className="font-medium text-foreground">{item.title}</span> {item.verb}{item.line&&<>: {item.line}</>} <span className="text-[10px]">· {age(item.at)}</span>{item.private&&<span> · Only you</span>}</li>
 const link=(destination:TeamPanelView,count?:number)=><button type="button" onClick={()=>onNavigate(destination)} className={`${button} items-center text-xs`}><span className="flex-1">{titles[destination]}</span>{count!==undefined&&<span className="text-[10px] text-muted-foreground">{count}</span>}<ChevronRight aria-hidden="true" className="h-3 w-3 text-muted-foreground"/></button>
 const ordered=[...homes,...peers.filter(working),...peers.filter(row=>!working(row)&&row.online),...peers.filter(row=>!working(row)&&!row.online)]
 const results=ordered.filter(row=>[name(row),runtime(row),status(row)].join(' ').toLowerCase().includes(query.trim().toLowerCase()))
 return <section aria-label={view==='live'?'Team live panel':titles[view]} className="min-w-0 space-y-2 text-sm">
  {view!=='live'&&<header className="sticky top-0 z-10 space-y-2 bg-surface-panel pb-2"><button type="button" onClick={()=>onNavigate('live')} className="flex items-center gap-1 text-xs text-primary"><ArrowLeft aria-hidden="true" className="h-3 w-3"/>Team</button><h2 className="text-sm font-semibold">{titles[view]}</h2></header>}
  {view==='live'&&<>
   {homes.length>0&&<div className="sticky top-0 z-10 bg-surface-panel">{homes.map(row=>agent(row))}</div>}
   {busy.length>0&&<section aria-label="Working now"><h3 className="px-1 text-[10px] uppercase tracking-wide text-muted-foreground">Working now</h3>{[...people.entries()].map(([id,group])=><div key={id} className="mt-1"><div className="flex items-center gap-2 px-1 py-1"><Avatar {...group[0]} name={group[0].person} className="h-6 w-6 text-[10px]"/><span><span className="block text-xs font-medium">{group[0].person}</span><span className="block text-[10px] text-muted-foreground">{[group.some(row=>row.activity==='running')?`${group.filter(row=>row.activity==='running').length} working`:'',group.some(row=>row.activity==='waiting')?`${group.filter(row=>row.activity==='waiting').length} waiting`:''].filter(Boolean).join(' · ')}</span></span></div>{group.map(row=>agent(row,true))}</div>)}</section>}
   {recent.length>0&&<section aria-label="Just happened"><h3 className="px-1 text-[10px] uppercase tracking-wide text-muted-foreground">Just happened</h3><ol className="ml-3 border-l border-border pl-3">{recent.map(event)}</ol></section>}
   {rows.length+shared.length+artifacts.length+routines.length+events.length>0&&<nav aria-label="Team details" className="border-t border-border pt-1">{rows.length>0&&link('agents',rows.length)}{shared.length>0&&link('shared',shared.length)}{artifacts.length+routines.length>0&&link('items',artifacts.length+routines.length)}{events.length>0&&link('activity')}</nav>}
  </>}
  {view==='agents'&&<><input type="search" aria-label="Search Team agents" placeholder="Search agents or people…" value={query} onChange={e=>setQuery(e.target.value)} className="w-full rounded border border-border bg-background px-2 py-1.5 text-xs"/><div aria-label="All Team agents" className="space-y-0.5">{results.map(row=>agent(row))}{!results.length&&<p className="text-xs text-muted-foreground">No matching agents.</p>}</div></>}
  {view==='shared'&&<div aria-label="Your shared agents">{shared.map(row=><button key={row.key} type="button" onClick={()=>onShared?.(row)} className={button}><Users aria-hidden="true" className="h-4 w-4 shrink-0 text-muted-foreground"/><span className="min-w-0 flex-1"><span className="block truncate text-xs">{row.name}</span><span className="block text-[10px] text-muted-foreground">{row.canChange?'Can make changes':'Answers only'} · {row.chosen===undefined?'Whole team':`${row.chosen} chosen`}</span></span><Pencil aria-hidden="true" className="h-3 w-3 text-muted-foreground"/></button>)}</div>}
  {view==='items'&&<div aria-label="Team artifacts and automations">{artifacts.map(row=><button key={row.id} type="button" onClick={()=>onArtifact?.(row.id)} className={button}><span className="min-w-0"><span className="block truncate text-xs">{row.title}</span><span className="block text-[10px] text-muted-foreground">{row.kind}{row.addedBy?` · Added by ${row.addedBy}`:''}</span></span></button>)}{routines.map(row=><div key={row.id} className="flex gap-2 px-1 py-1.5"><CalendarClock aria-hidden="true" className="h-4 w-4 shrink-0 text-muted-foreground"/><span className="min-w-0"><span className="block truncate text-xs">{row.name}</span><span className="block text-[10px] text-muted-foreground">{row.line}{!row.enabled?' · off':''}</span></span></div>)}</div>}
  {view==='activity'&&(fullActivity||<ol>{events.map(event)}</ol>)}
  {error&&<p role="alert" className="text-[11px] text-destructive">{error}</p>}
 </section>
}

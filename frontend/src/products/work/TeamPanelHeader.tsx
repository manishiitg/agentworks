import { ChevronRight } from 'lucide-react'
import { useState } from 'react'
import { Avatar } from '../../components/ui/Avatar'
import type { TeamTabView, TeamReachableAgent } from './teamApi'

type HeaderView = { team: Pick<TeamTabView['team'], 'on' | 'name'>; people: TeamTabView['people']; reachable?: Pick<TeamReachableAgent, 'activity' | 'kind'>[] }

/** Metadata from the same admitted Team read as the panel; no second presence count. */
export function TeamPanelHeader({view,connection,fallback='Team',selectedPerson,onPersonSelect,onOpen,onName,expanded=true,onToggle,needsCount=0}:{onName?:()=>void;expanded?:boolean;onToggle?:()=>void;needsCount?:number;view?:HeaderView;connection?:string;fallback?:string;selectedPerson?:string;onPersonSelect?:(id?:string)=>void;onOpen?:()=>void}) {
 const [more,setMore]=useState(false)
 if(!view?.team.on) return <span className="min-w-0 truncate">{fallback}</span>
 const people=(view.people||[]).filter(person=>person.note!=='invited'&&!person.you&&person.note!=='you')
 const names=people.map(person=>person.name).join(', ')
 const working=(view.reachable||[]).filter(row=>row.activity==='running'||row.activity==='waiting').length
 const toggle=()=>{setMore(false);(onToggle||onOpen)?.()}
 const avatar=(person:HeaderView['people'][number])=><button key={person.user_id} type="button" title={person.presence?.line?`${person.name} · ${person.presence.line}`:person.name} aria-label={`Show ${person.name}'s agents`} aria-pressed={selectedPerson===person.user_id} onClick={()=>onPersonSelect?.(selectedPerson===person.user_id?undefined:person.user_id)} style={{width:20,height:20,minWidth:20,minHeight:20,padding:0}} className={`relative inline-flex shrink-0 items-center justify-center rounded-full focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${selectedPerson===person.user_id?'ring-2 ring-primary':''}`}>
  <Avatar {...person} name={person.name} connection={connection} className="h-4 w-4 shrink-0 aspect-square border border-background leading-none text-[8px]"/>
  {person.presence?.online&&<span role="img" aria-label="Mac online" className="absolute -bottom-px -right-px h-1.5 w-1.5 rounded-full border border-background bg-emerald-500"/>}
 </button>
 return <div className="relative flex min-w-0 items-center gap-1.5 px-1 py-1" title={`${people.length} people: ${names}`}>
  <button type="button" onClick={onName||toggle} aria-expanded={!onName&&onToggle?expanded:undefined} title={onName?`Open ${view.team.name||'Team'}`:view.team.name||'Team'} className="min-w-0 truncate text-left text-xs font-medium hover:text-primary">{view.team.name||'Team'}</button>
  {people.length>0&&<span role="group" aria-label="Team people" className="inline-flex shrink-0 items-center -space-x-1">
   {people.slice(0,3).map(avatar)}
   {people.length>3&&<button type="button" aria-label="More Team people" aria-expanded={more} onClick={()=>setMore(!more)} className="pl-1.5 text-[10px] text-muted-foreground">+{people.length-3}</button>}
  </span>}
  <button type="button" aria-label={expanded?'Collapse Team':'Expand Team'} aria-expanded={expanded} onClick={toggle} className="ml-auto inline-flex shrink-0 items-center gap-1 text-[10px] text-muted-foreground"><span>{working?`${working} working`:'no one working'}</span>{needsCount>0&&<span className="text-amber-700 dark:text-amber-300">· {needsCount} Needs you</span>}{onToggle&&<ChevronRight aria-hidden="true" className={`h-3 w-3 ${expanded?'rotate-90':''}`}/>}</button>
  {more&&<div role="group" aria-label="More Team people" className="absolute left-0 top-full z-20 flex flex-wrap gap-2 rounded-lg border border-border bg-popover p-2">{people.slice(3).map(avatar)}</div>}
 </div>
}

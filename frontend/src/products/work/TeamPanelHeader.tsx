import { useState } from 'react'
import { Avatar } from '../../components/ui/Avatar'
import type { TeamTabView, TeamReachableAgent } from './teamApi'

type HeaderView = { team: Pick<TeamTabView['team'], 'on' | 'name'>; people: TeamTabView['people']; reachable?: Pick<TeamReachableAgent, 'activity' | 'kind'>[] }

/** Metadata from the same admitted Team read as the panel; no second presence count. */
export function TeamPanelHeader({view,connection,fallback='Team',selectedPerson,onPersonSelect,onOpen}:{view?:HeaderView;connection?:string;fallback?:string;selectedPerson?:string;onPersonSelect?:(id?:string)=>void;onOpen?:()=>void}) {
 const [more,setMore]=useState(false)
 if(!view?.team.on) return <span className="min-w-0 truncate">{fallback}</span>
 const people=(view.people||[]).filter(person=>person.note!=='invited')
 const names=people.map(person=>person.name).join(', ')
 const working=(view.reachable||[]).filter(row=>row.activity==='running'||row.kind!=='team_agent'&&row.activity==='waiting').length
 const avatar=(person:HeaderView['people'][number])=><button key={person.user_id} type="button" title={person.name} aria-label={`Show ${person.name}'s agents`} aria-pressed={selectedPerson===person.user_id} onClick={()=>onPersonSelect?.(selectedPerson===person.user_id?undefined:person.user_id)} className={`relative shrink-0 rounded-full focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${selectedPerson===person.user_id?'ring-2 ring-primary':''}`}>
  <Avatar {...person} name={person.name} connection={connection} className="h-4 w-4 border border-background text-[8px]"/>
  {person.presence?.online&&<span role="img" aria-label="Mac online" className="absolute -bottom-px -right-px h-1.5 w-1.5 rounded-full border border-background bg-emerald-500"/>}
 </button>
 return <div className="relative flex min-w-0 items-center gap-1.5 px-1 py-1" title={`${people.length} people: ${names}`}>
  <button type="button" onClick={onOpen} title={view.team.name||'Team'} className="min-w-0 truncate text-left text-xs font-medium">{view.team.name||'Team'}</button>
  {people.length>0&&<span role="group" aria-label="Team people" className="inline-flex shrink-0 items-center -space-x-1">
   {people.slice(0,3).map(avatar)}
   {people.length>3&&<button type="button" aria-label="More Team people" aria-expanded={more} onClick={()=>setMore(!more)} className="pl-1.5 text-[10px] text-muted-foreground">+{people.length-3}</button>}
  </span>}
  <span className="ml-auto shrink-0 text-[10px] text-muted-foreground">{working?`${working} working`:'no one working'}</span>
  {more&&<div role="group" aria-label="More Team people" className="absolute left-0 top-full z-20 flex flex-wrap gap-2 rounded-lg border border-border bg-popover p-2">{people.slice(3).map(avatar)}</div>}
 </div>
}

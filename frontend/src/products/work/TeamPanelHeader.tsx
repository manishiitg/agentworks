import { Avatar } from '../../components/ui/Avatar'
import type { TeamTabView, TeamReachableAgent } from './teamApi'

type HeaderView = { team: Pick<TeamTabView['team'], 'on' | 'name'>; people: TeamTabView['people']; reachable?: Pick<TeamReachableAgent, 'activity' | 'kind'>[] }

/** Metadata from the same admitted Team read as the panel; no second presence count. */
export function TeamPanelHeader({view,connection,fallback='Team'}:{view?:HeaderView;connection?:string;fallback?:string}) {
 if(!view?.team.on) return <span className="min-w-0 truncate">{fallback}</span>
 const people=(view.people||[]).filter(person=>person.note!=='invited')
 const names=people.map(person=>person.name).join(', ')
 const working=(view.reachable||[]).filter(row=>row.activity==='running'||row.kind!=='team_agent'&&row.activity==='waiting').length
 return <span className="flex min-w-0 flex-wrap items-center gap-1.5" title={names}>
  <span className="min-w-0 truncate">{view.team.name||'Team'}</span>
  {people.length>0&&<span role="img" aria-label={names} className="inline-flex shrink-0 items-center -space-x-1">
   {people.slice(0,3).map(person=><Avatar key={person.user_id} {...person} name={person.name} connection={connection} className="h-4 w-4 border border-background text-[8px]"/>)}
   {people.length>3&&<span className="pl-1.5 text-[10px] text-muted-foreground">+{people.length-3}</span>}
  </span>}
  <span className="shrink-0 text-[10px] text-muted-foreground">· {people.length} {people.length===1?'person':'people'} · {working} working</span>
 </span>
}

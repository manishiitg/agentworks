import { ForkTeammateAgent } from '../../products/work/ForkTeammateAgent'
import { openOnServerWebsite } from '../../utils/openOnServerWebsite'
import { TeamMonitorAgentRow } from './TeamMonitorAgentRow'
import { fillMainDraft } from '../../utils/agentPurposeDraft'
import { useChatStore } from '../../stores/useChatStore'
import { Avatar, type AvatarFields } from '../ui/Avatar'
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import api from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { hasDesktopConnections, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { openSidebarProject } from './SidebarServerGroup'
import { openCrewAccess } from '../../products/work/MacSharedAgents'
import type { TeamLiveView, TeamMonitorWork } from './TeamLiveAgents'
import { nowLine, type TeamOverview, type TeamOverviewPerson } from '../../products/work/TeamOverview'
import { timeAgo } from '../scheduler/scheduleRuns/helpers'
import { NeedCard } from '../../products/work/ActivitySection'
import type { TeamNeed, TeamNeedAnswer } from '../../products/work/TeamActivity'
import type { TaskFileContent } from '../../products/work/TaskAttachments'
import { teamRequest, type TeamTarget } from '../../products/work/teamApi'
import { GlobalActivityMonitor } from '../GlobalActivityMonitor'
import { AccountSignInBadge } from '../providers/AccountSignInBadge'

type Crew = { id: string; name: string; team_live?: TeamLiveView }
type Row = { you?:boolean; id?:string; target?:TeamTarget; purpose?: string; examples?: string[]; cli?:string; engine?:string; typing?: boolean; last_message?: string; last_message_at?: string; private?: boolean; key: string; personKey: string; person: string; network: string; agent: string; crew: string; activity: string; doing?: string; waiting?: string; open: () => void }
type Face = AvatarFields & { connection?: string; key: string; name: string; network: string; online: boolean; line: string; working: number; waiting: number; idle: number }


type MonitorTeam={overview?:TeamOverview;openOverview:()=>void;work?:TeamMonitorWork;key:string;name:string;network:string;networkId:string;faces:Face[];rows:Row[];needs:TeamNeed[];target:TeamTarget;open:()=>void}
const workingRow=(row:Row)=>row.activity==='running'||row.activity==='waiting'
const newestTime=(team:MonitorTeam)=>Date.parse(team.work?.last_worked_at||'')||0
const ignoreOwnSummary=()=>{}

/** Every Crew in the person's menu, on this network and each connected one, with its Team as the server words it. */
type MonitorSource={connectionId:string;network:string;networkId:string;crews:Crew[];error?:string}
function useTeamCrews():{sources:MonitorSource[];notice:string;people:TeamOverviewPerson[];tokens:string} {
 const user=useAuthStore(state=>state.user?.id||'')
 const active=getRawActiveWorkspaceId()
 const allowed=(useAuthStore(state=>state.user?.allowed_products)||['work']).includes('work')
 const [loaded,setLoaded]=useState<{identity:number;sources:MonitorSource[];notice:string;people:TeamOverviewPerson[];tokens:string}>()
 const [refresh,setRefresh]=useState(0)
 useLiveRefetch(()=>setRefresh(v=>v+1),{kinds:['sessions','crew_panel'],fallbackMs:60000,safetyMs:60000,minIntervalMs:5000,enabled:allowed&&!!user})
 useEffect(()=>{const reload=()=>setRefresh(v=>v+1);window.addEventListener('agentworks:team-updated',reload);return()=>window.removeEventListener('agentworks:team-updated',reload)},[])
 useEffect(()=>{
  if(!allowed||!user)return
  let disposed=false
  const identity=captureChatIdentity()
  void api.get<{connections:{connection:string;network?:string;network_id?:string;teams:TeamLiveView[];error?:string}[];notice?:string;people?:TeamOverviewPerson[];tokens_label?:string}>('/api/crew-team-monitor').then(({data})=>{
   if(disposed||!isChatIdentityCurrent(identity))return
   setLoaded({identity,notice:data.notice||'',people:data.people||[],tokens:data.tokens_label||'',sources:data.connections.map(source=>({connectionId:source.connection==='local'?active:source.connection,network:source.network||'',networkId:source.network_id||window.location.origin,crews:source.teams.map(live=>({id:live.project_id!,name:live.name||'Team',team_live:live})),error:source.error}))})
  }).catch(()=>{if(!disposed&&isChatIdentityCurrent(identity))setLoaded({identity,sources:[],people:[],tokens:'',notice:'Teams temporarily unavailable. Try again.'})})
  return()=>{disposed=true}
 },[active,user,allowed,refresh])
 return loaded&&isChatIdentityCurrent(loaded.identity)?loaded:{sources:[],notice:'',people:[],tokens:''}
}

function openCrewTeam(connectionId: string, crewId: string, section:'agents'|'overview'='agents') {
  // A Crew on a server opens on that server's website (#3779): the Mac app shows only this Mac's projects.
  if (connectionId !== getRawActiveWorkspaceId()) { void openOnServerWebsite(connectionId, `/crew/projects/${encodeURIComponent(crewId)}`); return }
  openSidebarProject('crew', crewId)
  openCrewAccess(crewId, '', 'team', section)
}

/** The header's Team (owner, 12 Nov, mockup "Monitor with Team" v4): the faces of teammates across every Crew, a
 * ring on those whose agents work or wait, and a panel of who is working where. The server decides every state and
 * word; this only gathers each Crew's reply. The left menu keeps only the person's own agents (PLAT-746-926). */
export function TeamMonitor({ representedSessionIds }: { representedSessionIds?: readonly string[] } = {}) {
 const {sources,notice,people,tokens}=useTeamCrews()
 const [byPerson,setByPerson]=useState(false)
 const [open,setOpen]=useState(false),[only,setOnly]=useState(''),[allTeams,setAllTeams]=useState(false),[revision,setRevision]=useState(0)
 const rootRef=useRef<HTMLDivElement>(null),panelRef=useRef<HTMLDivElement>(null)
 const [position,setPosition]=useState({left:0,top:0})
 const close=useCallback(()=>setOpen(false),[])
 const teams=useMemo(()=>{
  const found=new Map<string,MonitorTeam>()
  for(const source of sources)for(const crew of source.crews){
   const live=crew.team_live;if(!live)continue
   const networkId=(live.network_id||source.networkId).replace(/\/$/,'')
   const key=`${networkId}:${live.project_id||crew.id}`
   if(found.has(key))continue
   const network=source.network
   const faces=new Map<string,Face>()
   const personKey=(id:string,device?:string)=>`${networkId}:${id}:${device||''}`
   for(const p of live.people||[]){if(p.you)continue;const k=personKey(p.user_id,p.device);faces.set(k,{...p,connection:source.connectionId,key:k,name:p.name,network,online:p.online,line:p.line,working:0,waiting:0,idle:0})}
   const rows:Row[]=[]
   for(const agent of [...(live.agents||[]),...(live.idle||[])]){
    const pk=personKey(agent.owner_id,agent.device)
    const face=faces.get(pk)||{...agent,connection:source.connectionId,key:pk,name:agent.person,network,online:false,line:'',working:0,waiting:0,idle:0}
    if(agent.activity==='running')face.working++;else if(agent.activity==='waiting')face.waiting++;else face.idle++
    if(!agent.you)faces.set(pk,face)
    rows.push({...agent,target:{project:live.project_id||crew.id,connection:source.connectionId==='local'?undefined:source.connectionId},key:`${key}:${agent.id}`,personKey:pk,person:agent.person,network,agent:agent.name,crew:live.name||crew.name,activity:agent.activity||'idle',open:()=>openCrewTeam(source.connectionId,crew.id)})
   }
   found.set(key,{overview:live.overview,openOverview:()=>openCrewTeam(source.connectionId,crew.id,'overview'),work:live.work,key,name:live.name||crew.name,network,networkId,faces:[...faces.values()],rows,needs:live.needs||[],target:{project:live.project_id||crew.id,connection:live.connection||source.connectionId},open:()=>openCrewTeam(source.connectionId,crew.id)})
  }
  return [...found.values()].sort((a,b)=>Number((b.work?.working||0)>0)-Number((a.work?.working||0)>0)||newestTime(b)-newestTime(a)||a.name.localeCompare(b.name))
 },[sources,revision])
 const rows=teams.flatMap(team=>team.rows),workingRows=rows.filter(workingRow)
 const faces=[...new Map(teams.flatMap(team=>team.faces).map(face=>[face.key,face])).values()]
 const selected=faces.find(face=>face.key===only)
 const needs=teams.flatMap(team=>team.needs.map(need=>({team,need})))
 const multi=new Set(teams.map(team=>team.networkId)).size>1
 const needsInput=needs.length
 // Non-Team own work stays reachable in its existing component, never reclassified as a Team request.
 const own=<GlobalActivityMonitor representedSessionIds={representedSessionIds} slot={null} onSummary={ignoreOwnSummary} onClose={close}/>
 useEffect(()=>{
  if(!open)return
  const outside=(event:PointerEvent)=>{const target=event.target as Node;if(!rootRef.current?.contains(target)&&!panelRef.current?.contains(target))setOpen(false)}
  const escape=(event:KeyboardEvent)=>{if(event.key==='Escape')setOpen(false)}
  document.addEventListener('pointerdown',outside);document.addEventListener('keydown',escape)
  return()=>{document.removeEventListener('pointerdown',outside);document.removeEventListener('keydown',escape)}
 },[open])
 useLayoutEffect(()=>{
  if(!open)return
  const place=()=>{const trigger=rootRef.current?.getBoundingClientRect(),panel=panelRef.current?.getBoundingClientRect();if(trigger&&panel)setPosition({left:Math.max(12,Math.min(trigger.right-panel.width,window.innerWidth-panel.width-12)),top:trigger.bottom+8})}
  place();window.addEventListener('resize',place);return()=>window.removeEventListener('resize',place)
 },[open,only,allTeams,needs.length,rows.length])
 const avatar=(face:Face)=><button key={face.key} type="button" title={`${face.name} · ${face.line}`} aria-label={`Show ${face.name}'s agents`} onClick={()=>setOnly(face.key)} className="relative inline-flex h-5 w-5 shrink-0 rounded-full"><Avatar {...face} name={face.name} connection={face.connection} className="h-5 w-5 shrink-0 rounded-full text-[9px]"/>{face.online&&<span aria-label="Mac online" className="absolute -bottom-px -right-px h-1.5 w-1.5 rounded-full border border-background bg-emerald-500"/>}</button>
 const rowView=(row:Row)=>{const face=faces.find(face=>face.key===row.personKey);return <div key={row.key} className="flex min-w-0 items-center"><TeamMonitorAgentRow name={row.agent} purpose={row.purpose} examples={row.examples} cli={row.cli||row.engine} activity={row.activity} line={row.last_message||row.doing||row.waiting} team={row.crew} server={multi?row.network:undefined} onOpen={()=>{setOpen(false);row.open()}}/>{!row.you&&row.id&&row.target&&<ForkTeammateAgent target={row.target} agent={row.id} name={row.agent} compact/>}{!selected&&face&&avatar(face)}</div>}
 const askPerson=()=>{if(!selected)return;if(fillMainDraft('',`Ask ${selected.name}’s agents in their Teams: `))setOpen(false);else useChatStore.getState().addToast('Open one of your own agents first.','info')}
 const answer=(team:MonitorTeam)=>async(need:string,button:string,helper?:string,comment?:string)=>{
  const identity=captureChatIdentity()
  const out=await teamRequest<TeamNeedAnswer>(team.target,'/work','POST',{op:'work_answer',need,button,...(helper?{helper}:{}),...(comment?{comment}:{})})
  if(isChatIdentityCurrent(identity)){setRevision(v=>v+1);window.dispatchEvent(new Event('agentworks:team-updated'));if(hasDesktopConnections())void useConnectedWorkStore.getState().refresh(true)}
  return out
 }
 const section='px-2 pb-1 pt-2 text-[10px] font-medium uppercase tracking-wide text-muted-foreground'
 const listed=rows.filter(row=>row.personKey===only).sort((a,b)=>Number(workingRow(b))-Number(workingRow(a)))
 if(!notice&&!teams.length)return <div ref={rootRef} className="flex items-center gap-1.5">{own}<AccountSignInBadge/></div>
 return <div ref={rootRef} className="relative flex items-center gap-1.5" data-theme-team-working={workingRows.length>0?'true':'false'} data-theme-team-waiting={needsInput>0?'true':'false'}>
  {own}
  <AccountSignInBadge/>
  <button type="button" onClick={()=>setOpen(v=>!v)} aria-expanded={open} aria-label="Open Team monitor" className="flex h-8 items-center gap-1.5 rounded-full border border-border px-2 text-xs text-muted-foreground hover:bg-muted">Team · <span>{workingRows.length} working</span>{needsInput>0&&<span className="text-warning">· {needsInput} needs you</span>}</button>
  {open&&createPortal(<div ref={panelRef} role="dialog" aria-label="Team monitor" style={position} className="fixed z-50 flex max-h-[75vh] w-96 max-w-[calc(100vw-1.5rem)] flex-col overflow-y-auto rounded-lg border border-border bg-popover p-2 shadow-xl">
   {selected?<><div className="flex items-center gap-2 border-b border-border px-1 pb-1">{avatar(selected)}<b className="text-xs">{selected.name}</b><span className="text-[10px] text-muted-foreground">{listed.length} agents</span><button type="button" aria-label="Back to all Teams" className="ml-auto text-xs" onClick={()=>setOnly('')}>×</button></div>{listed.map(rowView)}{!listed.length&&<p className="p-2 text-xs text-muted-foreground">{selected.name} hasn't added an agent to this Team yet.</p>}{listed.length>0&&<button type="button" onClick={askPerson} className="px-2 py-1 text-left text-xs text-primary">✦ Ask my agent to ask {selected.name}’s agents…</button>}</>:<>
    {needs.length>0&&<section aria-label="Needs you"><h3 className={section}>Needs you</h3><ol>{needs.map(({team,need})=><NeedCard key={`${team.key}:${need.id}`} need={need} onAnswer={answer(team)} openFile={(need,file)=>teamRequest<TaskFileContent>(team.target,`/activity?task=${encodeURIComponent(need)}&file=${encodeURIComponent(file.id)}`)}/>)}</ol></section>}
    <section aria-label="Your Teams"><div className="flex items-center"><h3 className={`${section} flex-1`}>{byPerson?'People':'Your Teams'}</h3><span role="radiogroup" aria-label="Group" className="mr-1 inline-flex overflow-hidden rounded-md border border-border text-[10px]">{([[false,'By Team'],[true,'By person']] as const).map(([value,label])=><button key={label} type="button" role="radio" aria-checked={byPerson===value} onClick={()=>setByPerson(value)} className={`px-1.5 py-0.5 ${byPerson===value?'bg-foreground text-background':'text-muted-foreground hover:bg-muted'}`}>{label}</button>)}</span></div>
     {byPerson?<>{(allTeams?people:people.slice(0,5)).map(p=><div key={`${p.network||''}:${p.user_id}`} className="min-w-0 px-2 py-1">
      <div className="flex min-w-0 items-center gap-1.5"><span className="min-w-0 flex-1 truncate text-xs font-medium">{p.person}</span>{p.working?<span className="shrink-0 text-[10px] text-emerald-600 dark:text-emerald-400">{p.working} working</span>:null}{multi&&p.network&&<span title={p.network} className="max-w-20 truncate text-[9px] text-muted-foreground">{p.network}</span>}</div>
      {p.now.slice(0,2).map((n,i)=><p key={i} title={nowLine(n,false)} className="truncate text-[10px] text-muted-foreground"><span className="text-foreground/50">Now </span>{nowLine(n,false)}</p>)}
      {p.done&&<p title={p.done} className="truncate text-[10px] text-muted-foreground"><span className="text-foreground/50">Done </span>{p.done}</p>}
     </div>)}{!allTeams&&people.length>5&&<button type="button" className="px-2 py-1 text-xs text-muted-foreground" onClick={()=>setAllTeams(true)}>+ {people.length-5} more people</button>}</>
     :<>{(allTeams?teams:teams.slice(0,3)).map(team=><div key={team.key} className="min-w-0 px-2 py-1">
     <div className="flex min-w-0 items-center gap-1.5"><button type="button" onClick={()=>{setOpen(false);team.open()}} className="min-w-0 flex-1 truncate text-left text-xs font-medium">{team.name}</button>{team.work?.label?<span className="shrink-0 text-[10px] text-emerald-600 dark:text-emerald-400">{team.work.label}</span>:team.work?.last_worked_label&&<span className="shrink-0 text-[10px] text-muted-foreground">{team.work.last_worked_label}</span>}{multi&&team.network&&<span title={team.network} className="max-w-20 truncate text-[9px] text-muted-foreground">{team.network}</span>}<span className="flex shrink-0 -space-x-1">{team.faces.slice(0,3).map(avatar)}</span>{team.faces.length>3&&<span className="text-[9px] text-muted-foreground">+{team.faces.length-3}</span>}</div>
     {team.overview&&<div className="min-w-0 text-[10px] text-muted-foreground">
      {team.overview.now.map((n,i)=><p key={i} title={nowLine(n)} className="truncate"><span className="text-foreground/50">Now </span>{nowLine(n)}</p>)}
      {!!team.overview.now_more&&<button type="button" onClick={()=>{setOpen(false);team.openOverview()}} className="text-primary">+{team.overview.now_more} more working</button>}
      {team.overview.done&&<p title={team.overview.done} className="truncate"><span className="text-foreground/50">Done </span>{team.overview.label}: {team.overview.done}</p>}
      {team.overview.last&&<p title={team.overview.last.line} className="truncate"><span className="text-foreground/50">Last </span>{team.overview.last.person} {team.overview.last.line} · {timeAgo(team.overview.last.at)}</p>}
     </div>}
    </div>)}{!allTeams&&teams.length>3&&<button type="button" className="px-2 py-1 text-xs text-muted-foreground" onClick={()=>setAllTeams(true)}>+ {teams.length-3} more Teams</button>}
     {teams[0]&&<button type="button" onClick={()=>{setOpen(false);teams[0].openOverview()}} className="block px-2 py-1 text-xs text-primary">Open overview →</button>}</>}
     {tokens&&<p className="px-2 pt-1 text-[10px] text-muted-foreground">{tokens}</p>}
    </section>
   </>}
   {notice&&<p role="status" className="border-t border-border px-2 pt-2 text-[10px] text-muted-foreground">{notice}</p>}
  </div>,document.body)}
 </div>
}

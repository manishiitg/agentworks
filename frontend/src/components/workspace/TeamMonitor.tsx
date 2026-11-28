import { ForkTeammateAgent } from '../../products/work/ForkTeammateAgent'
import { openProjectInPlace } from '../../utils/openProjectInPlace'
import { TeamMonitorAgentRow } from './TeamMonitorAgentRow'
import { fillMainDraft } from '../../utils/agentPurposeDraft'
import { useChatStore } from '../../stores/useChatStore'
import { Avatar, type AvatarFields } from '../ui/Avatar'
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import api from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { useAppStore } from '../../stores/useAppStore'
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

type Crew = { id: string; name: string; team_live?: TeamLiveView }
type Row = { you?:boolean; id?:string; target?:TeamTarget; purpose?: string; examples?: string[]; cli?:string; engine?:string; typing?: boolean; last_message?: string; last_message_at?: string; private?: boolean; can_fork?: boolean; key: string; personKey: string; person: string; network: string; agent: string; crew: string; activity: string; doing?: string; waiting?: string; open: () => void }
type Face = AvatarFields & { connection?: string; key: string; name: string; network: string; online: boolean; line: string; working: number; waiting: number; idle: number }


type Member=AvatarFields&{name:string;connection?:string;faceKey?:string}
type MonitorTeam={members:Map<string,Member>;overview?:TeamOverview;work?:TeamMonitorWork;key:string;name:string;network:string;networkId:string;faces:Face[];rows:Row[];needs:TeamNeed[];target:TeamTarget;open:()=>void}
const workingRow=(row:Row)=>row.activity==='running'||row.activity==='waiting'
const newestTime=(team:MonitorTeam)=>Date.parse(team.work?.last_worked_at||'')||0
const ignoreOwnSummary=()=>{}

/** Every Crew in the person's menu, on this network and each connected one, with its Team as the server words it. */
// Admin only: someone working on that server outside your Teams, as the server words it (activity, never chat text).
export type AdminPerson={user_id:string;person:string;active?:boolean;last?:string;done?:string;projects?:string[];mac?:string;online?:boolean;quiet?:boolean}
export type MonitorSource={connectionId:string;network:string;networkId:string;crews:Crew[];others:AdminPerson[];error?:string}
// period=week is the Teams overview page's "This week"; the menu always reads today.
export function useTeamCrews(period:'day'|'week'='day'):{sources:MonitorSource[];notice:string;people:TeamOverviewPerson[];summary:string;loading:boolean} {
 const user=useAuthStore(state=>state.user?.id||'')
 const active=getRawActiveWorkspaceId()
 const allowed=(useAuthStore(state=>state.user?.allowed_products)||['work']).includes('work')
 const [loaded,setLoaded]=useState<{identity:number;period:string;sources:MonitorSource[];notice:string;people:TeamOverviewPerson[];summary:string}>()
 const [refresh,setRefresh]=useState(0)
 useLiveRefetch(()=>setRefresh(v=>v+1),{kinds:['sessions','crew_panel'],fallbackMs:60000,safetyMs:60000,minIntervalMs:5000,enabled:allowed&&!!user})
 useEffect(()=>{const reload=()=>setRefresh(v=>v+1);window.addEventListener('agentworks:team-updated',reload);return()=>window.removeEventListener('agentworks:team-updated',reload)},[])
 useEffect(()=>{
  if(!allowed||!user)return
  let disposed=false
  const identity=captureChatIdentity()
  void api.get<{connections:{connection:string;network?:string;network_id?:string;teams:TeamLiveView[];others?:AdminPerson[];error?:string}[];notice?:string;people?:TeamOverviewPerson[];summary?:string}>(`/api/crew-team-monitor${period==='week'?'?period=week':''}`).then(({data})=>{
   if(disposed||!isChatIdentityCurrent(identity))return
   setLoaded({identity,period,notice:data.notice||'',people:data.people||[],summary:data.summary||'',sources:data.connections.map(source=>({connectionId:source.connection==='local'?active:source.connection,network:source.network||'',networkId:source.network_id||window.location.origin,crews:source.teams.map(live=>({id:live.project_id!,name:live.name||'Team',team_live:live})),others:source.others||[],error:source.error}))})
  }).catch(()=>{if(!disposed&&isChatIdentityCurrent(identity))setLoaded({identity,period,sources:[],people:[],summary:'',notice:'Teams temporarily unavailable. Try again.'})})
  return()=>{disposed=true}
 },[active,user,allowed,refresh,period])
 return loaded&&isChatIdentityCurrent(loaded.identity)&&loaded.period===period?{...loaded,loading:false}:{sources:[],notice:'',people:[],summary:'',loading:true}
}

export function openCrewTeam(connectionId: string, crewId: string, section: 'agents' | 'activity' = 'agents') {
  // A Crew on another server opens in place too (#3383, #4331), with its Team panel.
  if (connectionId === getRawActiveWorkspaceId()) openSidebarProject('crew', crewId)
  else openProjectInPlace(connectionId, { surface: 'work', id: crewId })
  openCrewAccess(crewId, '', 'team', section)
}

// Open overview: one full page for every Team and person, not a tab in one Team (owner, 25 Nov).
export const openTeamsOverview=()=>useAppStore.getState().setAdminPage('teams')

/** The header's Team (owner, 12 Nov, mockup "Monitor with Team" v4): the faces of teammates across every Crew, a
 * ring on those whose agents work or wait, and a panel of who is working where. The server decides every state and
 * word; this only gathers each Crew's reply. The left menu keeps only the person's own agents (PLAT-746-926). */
export function TeamMonitor({ representedSessionIds }: { representedSessionIds?: readonly string[] } = {}) {
 const {sources,notice,people,summary}=useTeamCrews()
 const [byPerson,setByPerson]=useState(false)
 const [open,setOpen]=useState(false),[only,setOnly]=useState(''),[revision,setRevision]=useState(0)
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
   const members=new Map<string,Member>()
   for(const p of live.people||[])if(!members.has(p.user_id))members.set(p.user_id,{...p,connection:source.connectionId,faceKey:p.you?undefined:personKey(p.user_id,p.device)})
   found.set(key,{members,overview:live.overview,work:live.work,key,name:live.name||crew.name,network,networkId,faces:[...faces.values()],rows,needs:live.needs||[],target:{project:live.project_id||crew.id,connection:live.connection||source.connectionId},open:()=>openCrewTeam(source.connectionId,crew.id)})
  }
  return [...found.values()].sort((a,b)=>Number((b.work?.working||0)>0)-Number((a.work?.working||0)>0)||newestTime(b)-newestTime(a)||a.name.localeCompare(b.name))
 },[sources,revision])
 const rows=teams.flatMap(team=>team.rows),workingRows=rows.filter(workingRow)
 // People, not agents (owner, 27 Nov): someone running four agents is one person working.
 const workingPeople=new Set(workingRows.map(row=>row.personKey.slice(0,row.personKey.lastIndexOf(':')))).size
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
 },[open,only,byPerson,needs.length,rows.length])
 const avatar=(face:Face)=><button key={face.key} type="button" title={`${face.name} · ${face.line}`} aria-label={`Show ${face.name}'s agents`} onClick={()=>setOnly(face.key)} className="relative inline-flex h-5 w-5 shrink-0 rounded-full"><Avatar {...face} name={face.name} connection={face.connection} className="h-5 w-5 shrink-0 rounded-full text-[9px]"/>{face.online&&<span aria-label="Mac online" className="absolute -bottom-px -right-px h-1.5 w-1.5 rounded-full border border-background bg-emerald-500"/>}</button>
 const rowView=(row:Row)=>{const face=faces.find(face=>face.key===row.personKey);return <div key={row.key} className="flex min-w-0 items-center"><TeamMonitorAgentRow name={row.agent} purpose={row.purpose} examples={row.examples} cli={row.cli||row.engine} activity={row.activity} line={row.last_message||row.doing||row.waiting} team={row.crew} server={multi?row.network:undefined} onOpen={()=>{setOpen(false);row.open()}}/>{row.can_fork&&row.id&&row.target&&<ForkTeammateAgent target={row.target} agent={row.id} name={row.agent} compact/>}{!selected&&face&&avatar(face)}</div>}
 const askPerson=()=>{if(!selected)return;if(fillMainDraft('',`Ask ${selected.name}’s agents in their Teams: `))setOpen(false);else useChatStore.getState().addToast('Open one of your own agents first.','info')}
 const answer=(team:MonitorTeam)=>async(need:string,button:string,helper?:string,comment?:string)=>{
  const identity=captureChatIdentity()
  const out=await teamRequest<TeamNeedAnswer>(team.target,'/work','POST',{op:'work_answer',need,button,...(helper?{helper}:{}),...(comment?{comment}:{})})
  if(isChatIdentityCurrent(identity)){setRevision(v=>v+1);window.dispatchEvent(new Event('agentworks:team-updated'));if(hasDesktopConnections())void useConnectedWorkStore.getState().refresh(true)}
  return out
 }
 const section='px-2 pb-1 pt-2 text-[10px] font-medium uppercase tracking-wide text-muted-foreground'
 // One line of activity, led by the person's face (mockup "Team menu v2", owner 25 Nov).
 const who=(team:MonitorTeam|undefined,user:string|undefined,name:string,working:boolean)=>{const m=user?team?.members.get(user):undefined;const face=m?.faceKey?faces.find(f=>f.key===m.faceKey):undefined
  const pic=<Avatar {...(m||{})} name={m?.name||name} connection={m?.connection} className="h-[18px] w-[18px] shrink-0 rounded-full text-[9px]"/>
  return <span className="relative inline-flex shrink-0">{face?<button type="button" aria-label={`Show ${name}'s agents`} onClick={()=>setOnly(face.key)} className="inline-flex rounded-full">{pic}</button>:pic}{working&&<span aria-label="Working" className="absolute -bottom-px -right-px h-1.5 w-1.5 rounded-full border border-popover bg-emerald-500"/>}</span>}
 const act=(key:string|number,face:ReactNode,person:string,what:string,when:string)=><p key={key} title={`${person} · ${what}`} className="mt-1 flex min-w-0 items-center gap-1.5 text-[11px]">{face}<span className="shrink-0 font-medium">{person}</span><span className="min-w-0 flex-1 truncate text-muted-foreground">{what}</span><span className="shrink-0 text-[10px] text-muted-foreground">{when}</span></p>
 const short=(at?:string)=>at?timeAgo(at).replace(/ ago$/,''):''
 const capital=(text:string)=>text.charAt(0).toUpperCase()+text.slice(1)
 const status=(working:string|undefined,quietLabel:string|undefined)=>working?<span className="shrink-0 rounded-full bg-emerald-500/10 px-2 py-px text-[10px] font-medium text-emerald-700 dark:text-emerald-400">{working}</span>:quietLabel?<span className="shrink-0 text-[10px] text-muted-foreground">{quietLabel}</span>:null
 const teamView=(team:MonitorTeam)=>{const o=team.overview
  return <div key={team.key} className="min-w-0 rounded-md px-2 py-1.5 hover:bg-muted/50">
   <div className="flex min-w-0 items-baseline gap-2"><button type="button" onClick={()=>{setOpen(false);team.open()}} className="min-w-0 flex-1 truncate text-left text-[13px] font-semibold">{team.name}</button>{status(team.work?.label,team.work?.last_worked_label)}</div>
   {o?.done&&<p title={o.done} className="truncate text-[10px] text-muted-foreground">{capital(o.label)}: {o.done}</p>}
   {o?.now.map((n,i)=>act(i,who(team,n.user_id,n.person,true),n.person,`${n.agent}${n.title?` — ${n.title}`:''}`,'now'))}
   {!!o?.now_more&&<button type="button" onClick={()=>{setOpen(false);openTeamsOverview()}} className="ml-6 mt-0.5 text-[10px] text-emerald-700 dark:text-emerald-400">+{o.now_more} more working</button>}
   {o&&!o.now.length&&o.last&&act('last',who(team,o.last.user_id,o.last.person,false),o.last.person,o.last.line,short(o.last.at))}
  </div>}
 const quietLine=(names:{key:string;name:string;when?:string;open?:()=>void}[],label:string)=>names.length>0&&<details className="group px-2 py-1"><summary className="cursor-pointer list-none text-[11px] text-muted-foreground hover:text-foreground">{label} <span className="inline-block transition-transform group-open:rotate-90">›</span></summary><div className="mt-1 flex flex-wrap gap-1">{names.map(n=><button key={n.key} type="button" disabled={!n.open} onClick={()=>{setOpen(false);n.open?.()}} className="rounded-full border border-border bg-background px-2 py-px text-[11px] hover:border-muted-foreground">{n.name}{n.when?` · ${n.when}`:''}</button>)}</div></details>
 // Server name once per section, never on each row; one server needs no heading.
 const isQuiet=(team:MonitorTeam)=>!!team.overview?.quiet&&!team.work?.working
 const groups=[...new Set([...teams.filter(team=>!isQuiet(team)),...teams.filter(isQuiet)].map(team=>team.networkId))].map(id=>({id,teams:teams.filter(team=>team.networkId===id)}))
 const byTeam=groups.map(group=>{const quiet=group.teams.filter(isQuiet)
  return <div key={group.id} className="min-w-0">{multi&&<h4 className={`${section} border-t border-border`}>{group.teams[0].network||'This server'}</h4>}
   {group.teams.filter(team=>!quiet.includes(team)).map(teamView)}
   {quietLine(quiet.map(team=>({key:team.key,name:team.name,when:team.work?.last_worked_label?.replace(/ ago$/,''),open:team.open})),`${quiet.length} quiet ${quiet.length===1?'Team':'Teams'}`)}
  </div>})
 const personView=(p:TeamOverviewPerson)=>{const team=teams.find(team=>team.network===(p.network||'')&&team.members.has(p.user_id))
  return <div key={`${p.network||''}:${p.user_id}`} className="min-w-0 rounded-md px-2 py-1.5 hover:bg-muted/50">
   <div className="flex min-w-0 items-center gap-2">{who(team,p.user_id,p.person,p.working>0)}<span className="min-w-0 flex-1 truncate text-[13px] font-semibold">{p.person}</span>{status(p.working?'working':undefined,p.last?timeAgo(p.last.at):undefined)}</div>
   {(p.done||p.teams.length>0)&&<p className="ml-6 truncate text-[10px] text-muted-foreground">{[p.done&&`Today: ${p.done}`,p.teams.join(', ')].filter(Boolean).join(' · ')}</p>}
   {p.now.slice(0,1).map((n,i)=><p key={i} title={nowLine(n,false)} className="ml-6 truncate text-[11px] text-muted-foreground">{`${n.agent}${n.title?` — ${n.title}`:''}${n.team?` · ${n.team}`:''}`}</p>)}
  </div>}
// A live reload can keep a Team list fetched before others existed; skip it instead of crashing the app.
 const adminView=sources.filter(source=>source.others?.length).map(source=><div key={`others:${source.connectionId}`} className="min-w-0">
   <h4 className={`${section} border-t border-border`}>Not in your Teams{multi&&source.network?` · ${source.network}`:''} <span className="ml-1 rounded bg-muted px-1 normal-case tracking-normal">admin</span></h4>
   {source.others.filter(p=>!p.quiet).map(p=><div key={p.user_id} className="min-w-0 rounded-md px-2 py-1.5 hover:bg-muted/50">
    <div className="flex min-w-0 items-center gap-2"><span className="relative inline-flex shrink-0"><Avatar name={p.person} className="h-[18px] w-[18px] shrink-0 rounded-full text-[9px]"/>{p.active&&<span aria-label="Working" className="absolute -bottom-px -right-px h-1.5 w-1.5 rounded-full border border-popover bg-emerald-500"/>}</span><span className="min-w-0 flex-1 truncate text-[13px] font-semibold">{p.person}</span>{status(p.active?'active now':undefined,p.last?timeAgo(p.last):p.online?'Mac app open':undefined)}</div>
    {(p.done||p.projects?.length||(p.online&&p.last))&&<p className="ml-6 truncate text-[10px] text-muted-foreground">{[p.done&&`Today: ${p.done}`,(p.projects||[]).join(', '),p.online&&p.last&&'Mac app open'].filter(Boolean).join(' · ')}</p>}
    {p.mac&&<p className="ml-6 truncate text-[10px] text-muted-foreground">Mac app on the company account: {p.mac}</p>}
   </div>)}
   {quietLine(source.others.filter(p=>p.quiet).map(p=>({key:`quiet:${source.connectionId}:${p.user_id}`,name:p.person})),`${source.others.filter(p=>p.quiet).length} quiet this week`)}
  </div>)
 const busyPeople=people.filter(p=>p.working>0),earlier=people.filter(p=>!p.working&&(p.done||p.last)),quietPeople=people.filter(p=>!p.working&&!p.done&&!p.last)
 const listed=rows.filter(row=>row.personKey===only).sort((a,b)=>Number(workingRow(b))-Number(workingRow(a)))
 const [lead,...rest]=summary.split(' · ')
 if(!notice&&!teams.length&&!adminView.length)return own?<div ref={rootRef} className="flex items-center gap-1.5">{own}</div>:null
 return <div ref={rootRef} className="relative flex items-center gap-1.5" data-theme-team-working={workingRows.length>0?'true':'false'} data-theme-team-waiting={needsInput>0?'true':'false'}>
  {own}
  {/* Not on phones (owner, 27 Nov): the Team panel stays in each project. */}
  <button type="button" onClick={()=>setOpen(v=>!v)} aria-expanded={open} aria-label="Open Team monitor" className="hidden h-8 items-center gap-1.5 rounded-full border border-border px-2 text-xs text-muted-foreground hover:bg-muted md:flex">Team · <span>{workingPeople} working</span>{needsInput>0&&<span className="text-warning">· {needsInput} needs you</span>}</button>
  {open&&createPortal(<div ref={panelRef} role="dialog" aria-label="Team monitor" style={position} className="fixed z-50 flex max-h-[75vh] w-[25rem] max-w-[calc(100vw-1.5rem)] flex-col overflow-y-auto rounded-xl border border-border bg-popover p-2 shadow-xl">
   {selected?<><div className="flex items-center gap-2 border-b border-border px-1 pb-1">{avatar(selected)}<b className="text-xs">{selected.name}</b><span className="text-[10px] text-muted-foreground">{listed.length} agents</span><button type="button" aria-label="Back to all Teams" className="ml-auto text-xs" onClick={()=>setOnly('')}>×</button></div>{listed.map(rowView)}{!listed.length&&<p className="p-2 text-xs text-muted-foreground">{selected.name} hasn't added an agent to this Team yet.</p>}{listed.length>0&&<button type="button" onClick={askPerson} className="px-2 py-1 text-left text-xs text-primary">✦ Ask my agent to ask {selected.name}’s agents…</button>}</>:<>
    {needs.length>0&&<section aria-label="Needs you"><h3 className={section}>Needs you</h3><ol>{needs.map(({team,need})=><NeedCard key={`${team.key}:${need.id}`} need={need} onAnswer={answer(team)} openFile={(need,file)=>teamRequest<TaskFileContent>(team.target,`/activity?task=${encodeURIComponent(need)}&file=${encodeURIComponent(file.id)}`)}/>)}</ol></section>}
    <section aria-label="Your Teams">
     <div className="flex items-center px-2 pt-1"><h3 className="flex-1 text-[13px] font-semibold">Your Teams</h3><span role="radiogroup" aria-label="Group" className="inline-flex rounded-md bg-muted p-0.5 text-[11px]">{([[false,'By Team'],[true,'By person']] as const).map(([value,label])=><button key={label} type="button" role="radio" aria-checked={byPerson===value} onClick={()=>setByPerson(value)} className={`rounded px-2 py-0.5 ${byPerson===value?'bg-background text-foreground shadow-sm':'text-muted-foreground'}`}>{label}</button>)}</span></div>
     {summary&&<p className="px-2 pb-1 text-[11px] text-muted-foreground">{lead.includes('working')?<b className="font-semibold text-emerald-700 dark:text-emerald-400">{lead}</b>:lead}{rest.map(part=>` · ${part}`).join('')}</p>}
     {byPerson?<>
      {busyPeople.length>0&&<><h4 className={`${section} border-t border-border`}>Working now</h4>{busyPeople.map(personView)}</>}
      {earlier.length>0&&<><h4 className={`${section} border-t border-border`}>Earlier</h4>{earlier.map(personView)}</>}
      {adminView}
      {quietLine(quietPeople.map(p=>({key:`${p.network||''}:${p.user_id}`,name:p.person})),`${quietPeople.length} ${quietPeople.length===1?'person':'people'} quiet today`)}
     </>:byTeam}
    </section>
   </>}
   {!selected&&(teams[0]||notice)&&<div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-border px-2 pt-2 text-[11px]">{teams[0]&&<button type="button" onClick={()=>{setOpen(false);openTeamsOverview()}} className="flex-1 text-left font-medium text-emerald-700 dark:text-emerald-400">Open overview →</button>}{notice&&<span role="status" className="text-[10px] text-warning">{notice}</span>}</div>}
  </div>,document.body)}
 </div>
}

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
import type { TeamLiveView } from './TeamLiveAgents'
import { NeedCard } from '../../products/work/ActivitySection'
import type { TeamNeed, TeamNeedAnswer } from '../../products/work/TeamActivity'
import type { TeamPanelEvent } from '../../products/work/TeamPanel'
import { teamRequest, type TeamTarget } from '../../products/work/teamApi'
import { GlobalActivityMonitor } from '../GlobalActivityMonitor'

type Crew = { id: string; name: string; team_live?: TeamLiveView }
type Row = { cli?:string; engine?:string; typing?: boolean; last_message?: string; last_message_at?: string; private?: boolean; key: string; personKey: string; person: string; network: string; agent: string; crew: string; activity: string; doing?: string; waiting?: string; open: () => void }
type Face = AvatarFields & { connection?: string; key: string; name: string; network: string; online: boolean; line: string; working: number; waiting: number; idle: number }


type MonitorTeam={key:string;name:string;network:string;networkId:string;faces:Face[];rows:Row[];needs:TeamNeed[];recent?:TeamPanelEvent|null;target:TeamTarget;open:()=>void;openActivity:()=>void}
const workingRow=(row:Row)=>row.activity==='running'||row.activity==='waiting'
const newestTime=(team:MonitorTeam)=>Math.max(0,...team.rows.map(row=>Date.parse(row.last_message_at||'')||0))
// An old connection label can be a hostname; never put it into the compact Team chip.
const ignoreOwnSummary=()=>{}
const displayMonitorServer=(name:string)=>name.includes('.')||name.includes('://')?'Server':name||'Server'

/** Every Crew in the person's menu, on this network and each connected one, with its Team as the server words it. */
type MonitorSource={connectionId:string;network:string;networkId:string;crews:Crew[];error?:string}
function useTeamCrews():MonitorSource[] {
 const user=useAuthStore(state=>state.user?.id||'')
 const active=getRawActiveWorkspaceId()
 const allowed=(useAuthStore(state=>state.user?.allowed_products)||['work']).includes('work')
 const [loaded,setLoaded]=useState<{identity:number;sources:MonitorSource[]}>()
 const [refresh,setRefresh]=useState(0)
 useLiveRefetch(()=>setRefresh(v=>v+1),{kinds:['sessions','crew_panel'],fallbackMs:60000,safetyMs:60000,minIntervalMs:5000,enabled:allowed&&!!user})
 useEffect(()=>{const reload=()=>setRefresh(v=>v+1);window.addEventListener('agentworks:team-updated',reload);return()=>window.removeEventListener('agentworks:team-updated',reload)},[])
 useEffect(()=>{
  if(!allowed||!user)return
  let disposed=false
  const identity=captureChatIdentity()
  void api.get<{connections:{connection:string;network?:string;network_id?:string;teams:TeamLiveView[];error?:string}[]}>('/api/crew-team-monitor').then(({data})=>{
   if(disposed||!isChatIdentityCurrent(identity))return
   setLoaded({identity,sources:data.connections.map(source=>({connectionId:source.connection==='local'?active:source.connection,network:source.network||'',networkId:source.network_id||window.location.origin,crews:source.teams.map(live=>({id:live.project_id!,name:live.name||'Team',team_live:live})),error:source.error}))})
  }).catch(()=>{if(!disposed&&isChatIdentityCurrent(identity))setLoaded({identity,sources:[{connectionId:active,network:'',networkId:window.location.origin,crews:[],error:'Teams could not be loaded. Try again.'}]})})
  return()=>{disposed=true}
 },[active,user,allowed,refresh])
 return loaded&&isChatIdentityCurrent(loaded.identity)?loaded.sources:[]
}

function openCrewTeam(connectionId: string, crewId: string, section:'agents'|'activity'='agents') {
  if (connectionId === getRawActiveWorkspaceId()) {
    openSidebarProject('crew', crewId)
    openCrewAccess(crewId, '', 'team', section)
  } else void window.electronAPI?.openDesktopConnection?.(connectionId, `/crew/projects/${encodeURIComponent(crewId)}`)
}

/** The header's Team (owner, 12 Nov, mockup "Monitor with Team" v4): the faces of teammates across every Crew, a
 * ring on those whose agents work or wait, and a panel of who is working where. The server decides every state and
 * word; this only gathers each Crew's reply. The left menu keeps only the person's own agents (PLAT-746-926). */
export function TeamMonitor({ representedSessionIds }: { representedSessionIds?: readonly string[] } = {}) {
 const sources=useTeamCrews()
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
   const network=displayMonitorServer(live.network||source.network)
   const faces=new Map<string,Face>()
   const personKey=(id:string,device?:string)=>`${networkId}:${id}:${device||''}`
   for(const p of live.people||[]){const k=personKey(p.user_id,p.device);faces.set(k,{...p,connection:source.connectionId,key:k,name:p.name,network,online:p.online,line:p.line,working:0,waiting:0,idle:0})}
   const rows:Row[]=[]
   for(const agent of [...(live.agents||[]),...(live.idle||[])]){
    const pk=personKey(agent.owner_id,agent.device)
    const face=faces.get(pk)||{...agent,connection:source.connectionId,key:pk,name:agent.person,network,online:false,line:'',working:0,waiting:0,idle:0}
    if(agent.activity==='running')face.working++;else if(agent.activity==='waiting')face.waiting++;else face.idle++
    faces.set(pk,face)
    rows.push({...agent,key:`${key}:${agent.id}`,personKey:pk,person:agent.person,network,agent:agent.name,crew:live.name||crew.name,activity:agent.activity||'idle',open:()=>openCrewTeam(source.connectionId,crew.id)})
   }
   found.set(key,{key,name:live.name||crew.name,network,networkId,faces:[...faces.values()],rows,needs:live.needs||[],recent:live.recent,target:{project:live.project_id||crew.id,connection:live.connection||source.connectionId},open:()=>openCrewTeam(source.connectionId,crew.id),openActivity:()=>openCrewTeam(source.connectionId,crew.id,'activity')})
  }
  return [...found.values()].sort((a,b)=>b.rows.filter(workingRow).length-a.rows.filter(workingRow).length||newestTime(b)-newestTime(a)||a.name.localeCompare(b.name))
 },[sources,revision])
 const rows=teams.flatMap(team=>team.rows),workingRows=rows.filter(workingRow)
 const faces=[...new Map(teams.flatMap(team=>team.faces).map(face=>[face.key,face])).values()]
 const selected=faces.find(face=>face.key===only)
 const needs=teams.flatMap(team=>team.needs.map(need=>({team,need})))
 const recent=teams.filter(team=>team.recent).sort((a,b)=>Date.parse(b.recent!.at)-Date.parse(a.recent!.at))[0]
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
 const rowView=(row:Row)=>{const face=faces.find(face=>face.key===row.personKey);return <div key={row.key} className="flex min-w-0 items-center"><TeamMonitorAgentRow name={row.agent} cli={row.cli||row.engine} activity={row.activity} line={row.last_message||row.doing||row.waiting} team={row.crew} server={multi?row.network:undefined} onOpen={()=>{setOpen(false);row.open()}}/>{!selected&&face&&avatar(face)}</div>}
 const askPerson=()=>{if(!selected)return;if(fillMainDraft('',`Ask ${selected.name}’s agents in their Teams: `))setOpen(false);else useChatStore.getState().addToast('Open one of your own agents first.','info')}
 const answer=(team:MonitorTeam)=>async(need:string,button:string,helper?:string)=>{
  const identity=captureChatIdentity()
  const out=await teamRequest<TeamNeedAnswer>(team.target,'/work','POST',{op:'work_answer',need,button,...(helper?{helper}:{})})
  if(isChatIdentityCurrent(identity)){setRevision(v=>v+1);window.dispatchEvent(new Event('agentworks:team-updated'));if(hasDesktopConnections())void useConnectedWorkStore.getState().refresh(true)}
  return out
 }
 const section='px-2 pb-1 pt-2 text-[10px] font-medium uppercase tracking-wide text-muted-foreground'
 const listed=rows.filter(row=>row.personKey===only).sort((a,b)=>Number(workingRow(b))-Number(workingRow(a)))
 if(!sources.some(source=>source.error)&&!teams.length)return <div ref={rootRef}>{own}</div>
 return <div ref={rootRef} className="relative" data-theme-team-working={workingRows.length>0?'true':'false'} data-theme-team-waiting={needsInput>0?'true':'false'}>
  {own}
  <button type="button" onClick={()=>setOpen(v=>!v)} aria-expanded={open} aria-label="Open Team monitor" className="flex h-8 items-center gap-1.5 rounded-full border border-border px-2 text-xs text-muted-foreground hover:bg-muted">Team · <span>{workingRows.length} working</span>{needsInput>0&&<span className="text-warning">· {needsInput} needs you</span>}</button>
  {open&&createPortal(<div ref={panelRef} role="dialog" aria-label="Team monitor" style={position} className="fixed z-50 flex max-h-[75vh] w-96 max-w-[calc(100vw-1.5rem)] flex-col overflow-y-auto rounded-lg border border-border bg-popover p-2 shadow-xl">
   {sources.filter(source=>source.error).map(source=><p key={source.connectionId} role="alert" className="px-2 py-1 text-xs text-destructive">{source.error}</p>)}
   {selected?<><div className="flex items-center gap-2 border-b border-border px-1 pb-1">{avatar(selected)}<b className="text-xs">{selected.name}</b><span className="text-[10px] text-muted-foreground">{listed.length} agents</span><button type="button" aria-label="Back to all Teams" className="ml-auto text-xs" onClick={()=>setOnly('')}>×</button></div>{listed.map(rowView)}{!listed.length&&<p className="p-2 text-xs text-muted-foreground">{selected.name} hasn't added an agent to this Team yet.</p>}{listed.length>0&&<button type="button" onClick={askPerson} className="px-2 py-1 text-left text-xs text-primary">✦ Ask my agent to ask {selected.name}’s agents…</button>}</>:<>
    {needs.length>0&&<section aria-label="Needs you"><h3 className={section}>Needs you</h3><ol>{needs.map(({team,need})=><NeedCard key={`${team.key}:${need.id}`} need={need} onAnswer={answer(team)}/>)}</ol></section>}
    <section aria-label="Your Teams"><h3 className={section}>Your Teams</h3>{(allTeams?teams:teams.slice(0,3)).map(team=><div key={team.key} className="flex min-w-0 items-center gap-1.5 px-2 py-1"><button type="button" onClick={()=>{setOpen(false);team.open()}} className="min-w-0 flex-1 truncate text-left text-xs font-medium">{team.name}</button>{team.rows.filter(workingRow).length>0&&<span className="shrink-0 text-[10px] text-emerald-600 dark:text-emerald-400">{team.rows.filter(workingRow).length} working</span>}{multi&&<span className="max-w-16 truncate text-[9px] text-muted-foreground">{team.network}</span>}<span className="flex shrink-0 -space-x-1">{team.faces.slice(0,3).map(avatar)}</span>{team.faces.length>3&&<span className="text-[9px] text-muted-foreground">+{team.faces.length-3}</span>}</div>)}{!allTeams&&teams.length>3&&<button type="button" className="px-2 py-1 text-xs text-muted-foreground" onClick={()=>setAllTeams(true)}>+ {teams.length-3} more Teams</button>}</section>
    {recent?.recent&&<section aria-label="Activity"><h3 className={section}>Activity</h3><button type="button" onClick={()=>{setOpen(false);recent.openActivity()}} className="block w-full truncate px-2 py-1 text-left text-[11px] text-muted-foreground">{recent.recent.title} {recent.recent.verb} {recent.recent.line}</button></section>}
   </>}
  </div>,document.body)}
 </div>
}

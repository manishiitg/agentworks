import { Avatar } from '../ui/Avatar'
import { openOnServerWebsite } from '../../utils/openOnServerWebsite'
import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import { desktopRequest } from '../../services/desktopConnections'
import { answerTeamInvite, useTeamInvites, type TeamInvite } from '../../products/work/teamApi'
import { openSidebarProject } from './SidebarServerGroup'
import { productRoutePath } from '../../utils/productRoutes'
import { hasDesktopConnections } from '../../stores/useConnectedWorkStore'
import { getRawActiveWorkspaceId, useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { useTeamInviteNotice } from './teamInviteNotice'

const inviteKey = (invite: TeamInvite) => `${invite.connection || ''}:${invite.project_id}`
type Joined = {key:string;line:string;project:string;connection:string;invite:TeamInvite}
type InvitesState = {invites:TeamInvite[];waiting:TeamInvite[];busy:boolean;error:string;joined?:Joined;join:(invite:TeamInvite)=>Promise<void>;joinAll:()=>Promise<void>;notNow:(invite:TeamInvite)=>void}
const TeamInvitesContext = createContext<InvitesState|undefined>(undefined)
const log=(action:'join'|'not_now'|'join_all',project?:string)=>console.info('[TEAM_JOIN]',{event:'invite_line',action,...(project?{project}:{})})

export function TeamInviteProvider({ children }: { children: ReactNode }) {
 const person=useAuthStore(state=>state.user?.id),workspace=useWorkspaceConnectionStore(state=>state.activeWorkspaceId)
 return <ScopedTeamInvites key={JSON.stringify([person,workspace])}>{children}</ScopedTeamInvites>
}
function ScopedTeamInvites({children}:{children:ReactNode}) {
 const invites=useTeamInvites()
 const [hidden,setHidden]=useState<Record<string,true>>({})
 const [busy,setBusy]=useState(false),[error,setError]=useState(''),[joined,setJoined]=useState<Joined>()
 const active=useRef(true),working=useRef(false)
 useEffect(()=>{active.current=true;return()=>{active.current=false}},[])
 useEffect(()=>{useTeamInviteNotice.setState({count:invites.length});return()=>useTeamInviteNotice.setState({count:0})},[invites.length])
 useEffect(()=>{const keys=new Set(invites.map(inviteKey));setHidden(current=>Object.keys(current).some(key=>!keys.has(key))?Object.fromEntries(Object.entries(current).filter(([key])=>keys.has(key))):current)},[invites])
 useEffect(()=>{if(!joined)return;const timer=setTimeout(()=>setJoined(undefined),8000);return()=>clearTimeout(timer)},[joined])
 const hide=(invite:TeamInvite)=>setHidden(current=>({...current,[inviteKey(invite)]:true}))
 const accept=async(invite:TeamInvite)=>{
  const workspace=getRawActiveWorkspaceId(),connection=invite.connection||(workspace!=='local'?workspace:undefined)
  let project=invite.project_id,line=invite.notice?.joined||'',targetConnection=connection||workspace
  if(hasDesktopConnections()&&invite.can_add_to_mac===true){
   if(!connection)throw new Error(invite.notice?.connection_required)
   const route=`/${encodeURIComponent(connection)}/crew-teams/${encodeURIComponent(invite.project_id)}/join`
   const result=await desktopRequest<{project_id:string;notice:string}>(route,'POST',{})
   project=result.project_id;line=result.notice;targetConnection='local'
  }else await answerTeamInvite(invite,true)
  if(!active.current)return
  hide(invite);setJoined({key:inviteKey(invite),line,project,connection:targetConnection,invite})
  window.dispatchEvent(new CustomEvent('agentworks:team-updated'))
 }
 const run=async(items:TeamInvite[],all=false)=>{
  if(working.current)return
  working.current=true;setBusy(true);setError('')
  log(all?'join_all':'join',all?undefined:items[0]?.project_id)
  try{for(const invite of items){if(!active.current)break;await accept(invite)}}
  catch(cause){if(active.current)setError(cause instanceof Error?cause.message:'Could not join. Try again.')}
  finally{working.current=false;if(active.current)setBusy(false)}
 }
 const waiting=invites.filter(invite=>!hidden[inviteKey(invite)])
 return <TeamInvitesContext.Provider value={{invites,waiting,busy,error,joined,join:invite=>run([invite]),joinAll:()=>run(waiting,true),notNow:invite=>{log('not_now',invite.project_id);hide(invite)}}}>{children}</TeamInvitesContext.Provider>
}

/** One presentation on Mac and web; no overlay, focus capture or chat obstruction. */
export function TeamInviteLine(){
 const state=useContext(TeamInvitesContext),[review,setReview]=useState(false)
 if(!state)return null
 const {waiting,busy,joined}=state
 const copy=waiting[0]?.notice
 return <div className="shrink-0 text-xs" aria-label="Team invitation notices">
  {joined&&<div role="status" className="flex min-w-0 items-center gap-2 border-b border-border px-3 py-1.5"><span aria-hidden="true" className="text-emerald-500">✓</span><span className="min-w-0 flex-1 truncate" title={joined.line}>{joined.line}</span><button type="button" className="shrink-0 text-primary" onClick={()=>{if(joined.connection!==getRawActiveWorkspaceId())void openOnServerWebsite(joined.connection,productRoutePath({surface:'work',id:joined.project}));else openSidebarProject('crew',joined.project)}}>{joined.invite.notice?.open}</button></div>}
  {waiting.length===1?<TeamInviteRow invite={waiting[0]}/>:waiting.length>1?<>
   <div className="flex min-w-0 items-center gap-2 border-b border-border px-3 py-1.5"><span className="min-w-0 flex-1 truncate" title={waiting.map(i=>i.name).join(', ')}>{copy?.multiple.replace('{count}',String(waiting.length)).replace('{names}',waiting.map(i=>i.name).join(', '))}</span><button type="button" disabled={busy} onClick={()=>void state.joinAll()} className="shrink-0 rounded bg-primary px-2 py-0.5 text-primary-foreground disabled:opacity-50">{copy?.join_all}</button><button type="button" aria-expanded={review} onClick={()=>setReview(value=>!value)} className="shrink-0 text-muted-foreground">{copy?.review}</button></div>
   {review&&waiting.map(invite=><TeamInviteRow key={inviteKey(invite)} invite={invite}/>)}
  </>:null}
  {state.error&&<p role="alert" className="px-3 text-destructive">{state.error}</p>}
 </div>
}
/** Invites remain discoverable here even after Not now. */
export function TeamInvites(){
 const state=useContext(TeamInvitesContext)
 return state?.invites.length?<section aria-label="Team invites" className="shrink-0 px-2 pb-2"><h3 className="px-3 text-xs text-muted-foreground">Invites</h3>{state.invites.map(invite=><TeamInviteRow key={inviteKey(invite)} invite={invite}/>)}</section>:null
}
export function TeamInviteRow({invite}:{invite:TeamInvite}){
 const state=useContext(TeamInvitesContext)
 if(!state)return null
 return <div role="group" aria-label={`Team invite: ${invite.name}`} className="flex min-w-0 items-center gap-2 border-b border-border px-3 py-1.5 text-xs">
  <Avatar {...invite} name={invite.owner_name} connection={invite.connection} className="h-4 w-4 shrink-0 text-[8px]"/>
  <span className="min-w-0 flex-1 truncate" title={invite.notice?.line}>{invite.notice?.line}</span>
  <button type="button" disabled={state.busy} onClick={()=>void state.join(invite)} className="shrink-0 rounded bg-primary px-2 py-0.5 text-primary-foreground disabled:opacity-50">{invite.notice?.join}</button>
  <button type="button" disabled={state.busy} onClick={()=>state.notNow(invite)} className="shrink-0 text-muted-foreground">{invite.notice?.not_now}</button>
 </div>
}

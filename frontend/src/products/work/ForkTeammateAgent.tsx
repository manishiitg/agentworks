import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { GitFork } from 'lucide-react'
import { getAuthToken, getDesktopApiBaseUrl } from '../../services/api'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { hasDesktopConnections, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { useChatStore } from '../../stores/useChatStore'
import type { TeamTarget } from './teamApi'

export async function forkTeammateAgent(target:TeamTarget,agent:string) {
 const local=target.localProject||target.project
 const response=await fetch(`${getDesktopApiBaseUrl()}/api/crew-teams/${encodeURIComponent(local)}/agent-fork-local?self=1`,{
  method:'POST',headers:{Authorization:`Bearer ${getAuthToken()||''}`,'Content-Type':'application/json'},
  body:JSON.stringify({agent_id:agent,...(!target.localProject&&target.connection?{connection_id:target.connection}:{})}),
 })
 const data=await response.json()
 if(!response.ok)throw new Error(data?.error?.message||'The copy could not be created. Try again.')
 if(!data.accepted)throw new Error('The copy was not accepted. Open your Crew to check pending copies.')
 return data as {accepted:true;snapshot_at:string;project_id:string;conversation_key:string}
}

/** Both entry points use the same server operation. Hidden is never inferred
 * from stale UI metadata: the current source is admitted on the server. */
export function ForkTeammateAgent({target,agent,name,compact=false}:{target:TeamTarget;agent:string;name:string;compact?:boolean}) {
 const [open,setOpen]=useState(false),[busy,setBusy]=useState(false),[error,setError]=useState(''),[saved,setSaved]=useState('')
 const local=hasDesktopConnections()
 const scope=`${captureChatIdentity()}:${target.connection}:${target.project}:${target.localProject}:${agent}`
 const current=useRef(scope);current.current=scope
 useEffect(()=>{setOpen(false);setBusy(false);setError('');setSaved('')},[scope])
 useEffect(()=>{if(!open)return;const close=(e:KeyboardEvent)=>{if(e.key==='Escape'&&!busy)setOpen(false)};document.addEventListener('keydown',close);return()=>document.removeEventListener('keydown',close)},[open,busy])
 async function fork(){
  if(busy)return
  const identity=captureChatIdentity(),requestScope=scope
  setBusy(true);setError('')
  try{
   const result=await forkTeammateAgent(target,agent)
   if(!isChatIdentityCurrent(identity)||current.current!==requestScope)return
   setSaved(result.snapshot_at)
   window.dispatchEvent(new Event('agentworks:team-updated'))
   void useConnectedWorkStore.getState().refresh(true)
   useChatStore.getState().addToast(`${name} was copied to your Mac. Open it in your Crew's agent list.`,'success')
  }catch(cause){if(isChatIdentityCurrent(identity)&&current.current===requestScope)setError(cause instanceof Error?cause.message:'Could not fork this agent.')}
  finally{if(isChatIdentityCurrent(identity)&&current.current===requestScope)setBusy(false)}
 }
 return <>
  <button type="button" title={`Fork ${name} to my Mac`} aria-label={`Fork ${name} to my Mac`} onClick={()=>{setOpen(true);setError('');setSaved('')}} className="shrink-0 rounded px-2 py-1 text-xs text-muted-foreground hover:bg-muted hover:text-foreground"><GitFork className="inline h-3.5 w-3.5"/>{!compact&&' Fork to my Mac…'}</button>
  {open&&createPortal(<div className="fixed inset-0 z-[110] grid place-items-center bg-black/30 p-4" onClick={()=>{if(!busy)setOpen(false)}}>
   <section role="dialog" aria-modal="true" aria-label={`Fork ${name}`} className="w-full max-w-md space-y-3 rounded-xl border border-border bg-popover p-5 shadow-xl" onClick={event=>event.stopPropagation()}>
    <h2 className="font-semibold">Fork {name} to my Mac</h2>
    <p className="text-sm text-muted-foreground">Create your own private agent from its latest complete saved conversation, using your own AI account. The original stays unchanged. Files, logins and schedules aren't copied.</p>
    {!local&&<p role="status" className="text-sm">Open AgentWorks on your connected Mac to fork this agent.</p>}
    {error&&<p role="alert" className="text-sm text-destructive">{error}</p>}
    {saved&&<p role="status" className="text-sm">Copied from the version saved {new Date(saved).toLocaleString()}. Your new agent is in this Crew on your Mac.</p>}
    <div className="flex justify-end gap-2"><button type="button" disabled={busy} onClick={()=>setOpen(false)} className="rounded-md px-3 py-2 text-sm hover:bg-muted">{saved?'Done':'Cancel'}</button>{!saved&&<button type="button" disabled={!local||busy} onClick={()=>void fork()} className="rounded-md bg-primary px-3 py-2 text-sm text-primary-foreground disabled:opacity-50">{busy?'Copying…':'Fork to my Mac'}</button>}</div>
   </section>
  </div>,document.body)}
 </>
}

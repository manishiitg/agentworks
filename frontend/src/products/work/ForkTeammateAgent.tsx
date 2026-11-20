import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { fillMainDraft } from '../../utils/agentPurposeDraft'
import { GitFork } from 'lucide-react'
import { getAuthToken, getDesktopApiBaseUrl } from '../../services/api'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { hasDesktopConnections, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { useChatStore } from '../../stores/useChatStore'
import type { TeamTarget } from './teamApi'

export type ForkProgress={stage:string;line:string;percent:number}

/** The Mac makes the copy as a job (#3218); this follows its stage until it finishes. It keeps going if the dialog
 * closes, and the caller's toast says when the copy is ready. */
export async function forkTeammateAgent(target:TeamTarget,agent:string,onProgress?:(p:ForkProgress)=>void) {
 const local=target.localProject||target.project
 const base=`${getDesktopApiBaseUrl()}/api/crew-teams/${encodeURIComponent(local)}/agent-fork-local`
 const headers={Authorization:`Bearer ${getAuthToken()||''}`,'Content-Type':'application/json'}
 const response=await fetch(`${base}?self=1&async=1`,{method:'POST',headers,
  body:JSON.stringify({agent_id:agent,...(!target.localProject&&target.connection?{connection_id:target.connection}:{})}),
 })
 let job=await response.json()
 if(!response.ok)throw new Error(job?.error?.message||'The copy could not be created. Try again.')
 for(;;){
  onProgress?.({stage:job.stage,line:job.line,percent:job.percent})
  if(job.stage==='done')return job.result as {accepted:true;snapshot_at:string;project_id:string;conversation_key:string}
  if(job.stage==='failed')throw new Error(job.error||'The copy could not be created. Try again.')
  await new Promise(resolve=>setTimeout(resolve,1000))
  const next=await fetch(`${base}/jobs/${encodeURIComponent(job.id)}`,{headers})
  const data=await next.json()
  if(!next.ok)throw new Error(data?.error?.message||'Lost track of this copy. Open your Crew to check its agents.')
  job=data
 }
}

/** Both entry points use the same server operation. Hidden is never inferred
 * from stale UI metadata: the current source is admitted on the server. */
export function ForkTeammateAgent({target,agent,name,compact=false}:{target:TeamTarget;agent:string;name:string;compact?:boolean}) {
 const [open,setOpen]=useState(false),[busy,setBusy]=useState(false),[error,setError]=useState(''),[saved,setSaved]=useState(''),[progress,setProgress]=useState<ForkProgress|null>(null)
 const local=hasDesktopConnections()
 const scope=`${captureChatIdentity()}:${target.connection}:${target.project}:${target.localProject}:${agent}`
 const current=useRef(scope);current.current=scope
 useEffect(()=>{setOpen(false);setBusy(false);setError('');setSaved('');setProgress(null)},[scope])
 useEffect(()=>{if(!open)return;const close=(e:KeyboardEvent)=>{if(e.key==='Escape')setOpen(false)};document.addEventListener('keydown',close);return()=>document.removeEventListener('keydown',close)},[open,busy])
 function requestCopy(){
  const prompt=`Request a copy of ${name} (agent ${agent}, Team ${target.project}) from its maker. Ask the maker to use the existing Send a copy to a teammate flow from their own account; I will accept their offer. Do not change chat visibility or export a Hidden chat.`
  if(fillMainDraft('',prompt))setOpen(false)
  else useChatStore.getState().addToast('Open one of your own agents first, then ask it to request this copy.','info')
 }
 async function fork(){
  if(busy)return
  const identity=captureChatIdentity(),requestScope=scope
  setBusy(true);setError('');setProgress({stage:'server',line:'Starting…',percent:2})
  try{
   const result=await forkTeammateAgent(target,agent,p=>{if(current.current===requestScope)setProgress(p)})
   if(!isChatIdentityCurrent(identity)||current.current!==requestScope)return
   setSaved(result.snapshot_at)
   window.dispatchEvent(new Event('agentworks:team-updated'))
   void useConnectedWorkStore.getState().refresh(true)
   useChatStore.getState().addToast(`${name} was copied to your Mac. Open it in your Crew's agent list.`,'success')
  }catch(cause){
   const line=cause instanceof Error?cause.message:'Could not fork this agent.'
   if(isChatIdentityCurrent(identity)&&current.current===requestScope)setError(line)
   useChatStore.getState().addToast(`${name} wasn't copied: ${line}`,'error')
  }
  finally{if(current.current===requestScope){setBusy(false);setProgress(null)}}
 }
 return <>
  <button type="button" title={`Fork ${name} to my Mac`} aria-label={`Fork ${name} to my Mac`} onClick={()=>{setOpen(true);setError('');setSaved('')}} className="shrink-0 rounded px-2 py-1 text-xs text-muted-foreground hover:bg-muted hover:text-foreground"><GitFork className={`inline h-3.5 w-3.5 ${busy?'animate-pulse text-primary':''}`}/>{!compact&&(busy&&progress?` Copying… ${progress.percent}%`:' Fork to my Mac…')}</button>
  {open&&createPortal(<div className="fixed inset-0 z-[110] grid place-items-center bg-background/70 p-4" onClick={()=>setOpen(false)}>
   <section role="dialog" aria-modal="true" aria-label={`Fork ${name}`} className="w-full max-w-md space-y-3 rounded-xl border border-border bg-popover p-5 shadow-xl" onClick={event=>event.stopPropagation()}>
    <h2 className="font-semibold">Fork {name} to my Mac</h2>
    <p className="text-sm text-muted-foreground">Create your own private agent from its latest complete saved conversation, using your own AI account. The original stays unchanged. Files, logins and schedules aren't copied.</p>
    {!local&&<p role="status" className="text-sm">Open AgentWorks on your connected Mac to fork this agent.</p>}
    {error&&<><p role="alert" className="text-sm text-destructive">{error}</p><button type="button" onClick={requestCopy} className="text-sm text-primary hover:underline">Request a copy via my agent…</button><p className="text-xs text-muted-foreground">Opens a draft in your own chat. No request or copy is sent yet.</p></>}
    {busy&&progress&&<div role="status" className="space-y-1.5"><div className="h-1.5 overflow-hidden rounded-full bg-muted"><div className="h-full rounded-full bg-primary transition-all duration-500" style={{width:`${progress.percent}%`}}/></div><p className="text-sm">{progress.line}</p><p className="text-xs text-muted-foreground">You can close this; the copy keeps going and you'll get a notice when it's ready.</p></div>}
    {saved&&<p role="status" className="text-sm">Copied from the version saved {new Date(saved).toLocaleString()}. Your new agent is in this Crew on your Mac.</p>}
    <div className="flex justify-end gap-2"><button type="button" onClick={()=>setOpen(false)} className="rounded-md px-3 py-2 text-sm hover:bg-muted">{saved?'Done':busy?'Close':'Cancel'}</button>{!saved&&<button type="button" disabled={!local||busy} onClick={()=>void fork()} className="rounded-md bg-primary px-3 py-2 text-sm text-primary-foreground disabled:opacity-50">{busy?'Copying…':'Fork to my Mac'}</button>}</div>
   </section>
  </div>,document.body)}
 </>
}

import { useEffect, useRef, useState } from 'react'
import api, { getAuthToken, getDesktopApiBaseUrl } from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { teamAgentFor, useTeamTab, type TeamTarget } from './teamApi'

type Reply = { message_id:string; message:string; sender?:string }
type Mail = { message_id?:string; inbox_id?:string; delivery?:string; messages?:Reply[]; next_cursor?:number }
/** The same AgentBridge calls as an external person, never a turn in the watched native session. */
export async function teamAgentMail(target: TeamTarget, args: Record<string,unknown>): Promise<Mail> {
  const body={name:'messages',arguments:args}
  if (!target.connection) return (await api.post<Mail>('/api/external/v1/call',body,{skipSessionContext:true})).data
  const response=await fetch(`${getDesktopApiBaseUrl()}/api/connections/${encodeURIComponent(target.connection)}/api/external/v1/call`,{method:'POST',headers:{Authorization:`Bearer ${getAuthToken()||''}`,'Content-Type':'application/json'},body:JSON.stringify(body)})
  const data=await response.json()
  if(!response.ok) throw new Error(data?.error?.message||'Could not reach this agent.')
  return data as Mail
}

/** Docked under the existing teammate history (or its private notice). */
export function TeamAgentAsk({tabId}:{tabId:string}) {
 const tab=useChatStore(state=>state.chatTabs[tabId])
 const watching=tab?.metadata?.agentProfileTabCanWrite===false
 const team=useTeamTab(watching?tab?.metadata?.agentProfileProjectId:undefined)
 const agent=watching?teamAgentFor(team.view?.agents,tab?.metadata?.agentProfileConversationKey):undefined
 const recipient=team.view?.reachable?.find(row=>row.agent_id===agent?.id)
 const [draft,setDraft]=useState(''),[error,setError]=useState(''),[status,setStatus]=useState(''),[busy,setBusy]=useState(false)
 const [replies,setReplies]=useState<Reply[]>([]),[inbox,setInbox]=useState('')
 const cursor=useRef(0)
 const scope=`${team.target?.connection}:${team.target?.project}:${recipient?.address}`
 const currentScope=useRef(scope);currentScope.current=scope
 useEffect(()=>{setInbox('');setReplies([]);setError('');setStatus('');setBusy(false);cursor.current=0},[scope])
 useEffect(()=>{
  if(!watching||!inbox||!recipient||!team.view?.team.on||!team.target) return
  const identity=captureChatIdentity(),target=team.target
  let active=true,reading=false
  const read=async()=>{
   if(reading) return
   reading=true
   try {
    const data=await teamAgentMail(target,{action:'read',inbox_id:inbox,after:cursor.current,limit:30})
    if(!active||!isChatIdentityCurrent(identity))return
    cursor.current=data.next_cursor||cursor.current
    setReplies(old=>[...old,...(data.messages||[]).filter(row=>!old.some(item=>item.message_id===row.message_id))]);setError('')
   }catch(cause){if(active&&isChatIdentityCurrent(identity)){setReplies([]);setError(cause instanceof Error?cause.message:'Could not read replies.')}}
   finally{reading=false}
  }
  void read(); const timer=window.setInterval(()=>{if(!document.hidden)void read()},5000)
  return()=>{active=false;window.clearInterval(timer)}
 },[scope,inbox,watching,team.view?.team.on]) // eslint-disable-line react-hooks/exhaustive-deps
 if(!watching) return null
 const admitted=!!recipient&&!!team.target&&!!team.view?.team.on&&!team.error
 return <section aria-label="Ask this agent" className="shrink-0 border-t border-border px-4 py-3 text-sm">
  {replies.map(row=><p key={row.message_id} className="mb-2 whitespace-pre-wrap break-words"><b>{row.sender||agent?.name}: </b>{row.message}</p>)}
  {error&&<p role="alert" className="mb-2 text-xs text-destructive">{error}</p>}
  {status&&<p role="status" className="mb-2 text-xs text-muted-foreground">{status}</p>}
  <form onSubmit={async event=>{
   event.preventDefault();if(!admitted||busy||!draft.trim()||!team.target||!recipient)return
   const identity=captureChatIdentity(),sendScope=scope,text=draft.trim()
   setBusy(true);setError('')
   try{
    const data=await teamAgentMail(team.target,{action:'send',...(inbox?{inbox_id:inbox}:{target:recipient.address}),message:text,submission_id:crypto.randomUUID()})
    if(!isChatIdentityCurrent(identity)||currentScope.current!==sendScope)return
    if(!data.inbox_id)throw new Error('No conversation receipt was returned. Check delivery before sending again.')
    setInbox(data.inbox_id);setReplies(old=>[...old,{message_id:data.message_id||crypto.randomUUID(),sender:'You',message:text}]);setDraft('');setStatus(data.delivery==='delivered'?'Delivered.':'Sent; waiting for delivery. Replies appear here.')
   }catch(cause){if(isChatIdentityCurrent(identity)&&currentScope.current===sendScope)setError(cause instanceof Error?cause.message:'Could not send this message.')}
   finally{if(isChatIdentityCurrent(identity)&&currentScope.current===sendScope)setBusy(false)}
  }} className="flex items-end gap-2">
   <textarea aria-label={`Ask ${agent?.name||'this agent'}`} placeholder={`Ask ${agent?.name||'this agent'}…`} rows={2} value={draft} onChange={event=>setDraft(event.target.value)} disabled={!admitted||busy} className="min-w-0 flex-1 resize-none rounded-lg border border-border bg-background px-3 py-2 disabled:opacity-50"/>
   <button type="submit" disabled={!admitted||busy||!draft.trim()} className="rounded-lg bg-primary px-3 py-2 text-primary-foreground disabled:opacity-50">{busy?'Sending…':'Send'}</button>
  </form>
  <p className="mt-1 text-xs text-muted-foreground">Your message goes to this agent through AgentBridge, not into their private chat.</p>
 </section>
}

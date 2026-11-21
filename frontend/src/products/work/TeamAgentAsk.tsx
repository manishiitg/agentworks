import { randomUUID } from '../../utils/randomUUID'
import { useEffect, useRef, useState, type ReactNode } from 'react'
import api, { getAuthToken, getDesktopApiBaseUrl } from '../../services/api'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { type TeamTarget } from './teamApi'
import { useTeammateChatIdentity } from './TeammateChatIdentity'

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

/** One AgentBridge ask thread to an admitted address over the Team's saved connection; never a navigation or a turn in someone's own chat. */
export function AgentAskThread({target,address,label,scope,enabled,sender,children}:{target?:TeamTarget;address?:string;label:string;scope:string;enabled:boolean;sender?:string;children?:ReactNode}) {
 const [threadScope,setThreadScope]=useState(scope)
 const [draft,setDraft]=useState(''),[error,setError]=useState(''),[status,setStatus]=useState(''),[busy,setBusy]=useState(false)
 const [replies,setReplies]=useState<Reply[]>([]),[inbox,setInbox]=useState('')
 const cursor=useRef(0)
 const currentScope=useRef(scope);currentScope.current=scope
 useEffect(()=>{setThreadScope(scope);setDraft('');setInbox('');setReplies([]);setError('');setStatus('');setBusy(false);cursor.current=0},[scope])
 useEffect(()=>{
  if(!enabled||!inbox||!address||!target) return
  const identity=captureChatIdentity()
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
 },[scope,inbox,enabled]) // eslint-disable-line react-hooks/exhaustive-deps
 const admitted=enabled&&!!address&&!!target
 return <section aria-label="Ask this agent" className="shrink-0 border-t border-border px-4 py-3 text-sm">
  {admitted&&threadScope===scope&&replies.length>0&&<div className="mb-2 max-h-64 space-y-2 overflow-y-auto">{replies.map(row=><p key={row.message_id} className="whitespace-pre-wrap break-words"><b>{row.sender||sender}: </b>{row.message}</p>)}</div>}
  {error&&<p role="alert" className="mb-2 text-xs text-destructive">{error}</p>}
  {status&&<p role="status" className="mb-2 text-xs text-muted-foreground">{status}</p>}
  <form onSubmit={async event=>{
   event.preventDefault();if(!admitted||busy||!draft.trim()||!target||!address)return
   const identity=captureChatIdentity(),sendScope=scope,text=draft.trim()
   setBusy(true);setError('')
   try{
    const data=await teamAgentMail(target,{action:'ask',...(inbox?{inbox_id:inbox}:{target:address}),message:text,submission_id:randomUUID()})
    if(!isChatIdentityCurrent(identity)||currentScope.current!==sendScope)return
    if(!data.inbox_id)throw new Error('No conversation receipt was returned. Check delivery before sending again.')
    setInbox(data.inbox_id);setReplies(old=>[...old,{message_id:data.message_id||randomUUID(),sender:'You',message:text}]);setDraft('');setStatus(data.delivery==='delivered'?'Delivered.':'Sent; waiting for delivery. Replies appear here.')
   }catch(cause){if(isChatIdentityCurrent(identity)&&currentScope.current===sendScope)setError(cause instanceof Error?cause.message:'Could not send this message.')}
   finally{if(isChatIdentityCurrent(identity)&&currentScope.current===sendScope)setBusy(false)}
  }} className="flex items-end gap-2">
   <textarea aria-label={label} placeholder={`${label}…`} rows={2} value={threadScope===scope?draft:''} onChange={event=>setDraft(event.target.value)} disabled={!admitted||busy} className="min-w-0 flex-1 resize-none rounded-lg border border-border bg-background px-3 py-2 disabled:opacity-50"/>
   <button type="submit" disabled={!admitted||busy||!draft.trim()} className="rounded-lg bg-primary px-3 py-2 text-primary-foreground disabled:opacity-50">{busy?'Sending…':'Send'}</button>
  </form>
  {children}
 </section>
}

/** The bottom of a teammate's agent view (owner, 20 Nov): one "Ask <agent>…" box. It sends an AgentBridge ask from
 * you to the agent's admitted address, and its answer shows here. The server decides whether you may ask. */
export function TeamAgentAsk({tabId}:{tabId:string}) {
 const identity=useTeammateChatIdentity(tabId)
 if(!identity)return null
 const {name,place,online,ask}=identity
 return <TeammateAsk target={ask.target} address={ask.address} name={name} where={place} online={online} enabled={ask.enabled} scope={`${tabId}:${ask.address||''}`}/>
}

export function TeammateAsk({target,address,name,where,online,enabled,scope}:{target?:TeamTarget;address?:string;name:string;where:string;online?:boolean;enabled:boolean;scope:string}) {
 const hint=!enabled?`You can't ask ${name} right now.`:online===false?`${where.charAt(0).toUpperCase()}${where.slice(1)} is offline. Your ask goes to its online copy if it has one.`:`Goes to ${name} as your ask. Its answer shows here.`
 return <AgentAskThread target={target} address={address} label={`Ask ${name}`} scope={scope} enabled={enabled} sender={name}>
  <p className="mt-1 text-xs text-muted-foreground">{hint}</p>
 </AgentAskThread>
}

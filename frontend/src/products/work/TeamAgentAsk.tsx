import { closeTerminalConversation } from './terminalConversationSelection'
import { fillAskFromMyAgentDraft } from '../../utils/agentPurposeDraft'
import { ownerShortName } from '../../components/chat/chatTabName'
import { useEffect, useRef, useState, type ReactNode } from 'react'
import api, { getAuthToken, getDesktopApiBaseUrl } from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { useAuthStore } from '../../stores/useAuthStore'
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
  {admitted&&threadScope===scope&&replies.map(row=><p key={row.message_id} className="mb-2 whitespace-pre-wrap break-words"><b>{row.sender||sender}: </b>{row.message}</p>)}
  {error&&<p role="alert" className="mb-2 text-xs text-destructive">{error}</p>}
  {status&&<p role="status" className="mb-2 text-xs text-muted-foreground">{status}</p>}
  <form onSubmit={async event=>{
   event.preventDefault();if(!admitted||busy||!draft.trim()||!target||!address)return
   const identity=captureChatIdentity(),sendScope=scope,text=draft.trim()
   setBusy(true);setError('')
   try{
    const data=await teamAgentMail(target,{action:'ask',...(inbox?{inbox_id:inbox}:{target:address}),message:text,submission_id:crypto.randomUUID()})
    if(!isChatIdentityCurrent(identity)||currentScope.current!==sendScope)return
    if(!data.inbox_id)throw new Error('No conversation receipt was returned. Check delivery before sending again.')
    setInbox(data.inbox_id);setReplies(old=>[...old,{message_id:data.message_id||crypto.randomUUID(),sender:'You',message:text}]);setDraft('');setStatus(data.delivery==='delivered'?'Delivered.':'Sent; waiting for delivery. Replies appear here.')
   }catch(cause){if(isChatIdentityCurrent(identity)&&currentScope.current===sendScope)setError(cause instanceof Error?cause.message:'Could not send this message.')}
   finally{if(isChatIdentityCurrent(identity)&&currentScope.current===sendScope)setBusy(false)}
  }} className="flex items-end gap-2">
   <textarea aria-label={label} placeholder={`${label}…`} rows={2} value={threadScope===scope?draft:''} onChange={event=>setDraft(event.target.value)} disabled={!admitted||busy} className="min-w-0 flex-1 resize-none rounded-lg border border-border bg-background px-3 py-2 disabled:opacity-50"/>
   <button type="submit" disabled={!admitted||busy||!draft.trim()} className="rounded-lg bg-primary px-3 py-2 text-primary-foreground disabled:opacity-50">{busy?'Sending…':'Send'}</button>
  </form>
  {children}
 </section>
}

/** A teammate is contacted by the viewer's own agent, never a direct composer. */
export function AskMyAgentAction({project,person,name,address,enabled,replyInTerminal=false}:{project?:string;person:string;name:string;address?:string;enabled:boolean;replyInTerminal?:boolean}) {
 const [copied,setCopied]=useState(false),[copyError,setCopyError]=useState(false)
 const request='check the release status.'
 const example=`Ask ${name} to ${request}`
 return <section aria-label={`How to talk to ${name}`} className="shrink-0 border-b border-border px-4 py-2 text-xs">
  <p className="mb-1 text-muted-foreground">To talk to {name}, ask any of your agents:</p>
  <div className="flex flex-wrap items-center gap-2">
   <code className="select-all rounded bg-muted px-2 py-1">{example}</code>
   <button type="button" aria-label="Copy example prompt" className="text-muted-foreground hover:text-foreground" onClick={async()=>{
    try {await navigator.clipboard.writeText(example);setCopied(true);setCopyError(false)} catch {setCopyError(true)}
   }}>{copied?'Copied':'Copy'}</button>
   <button type="button" disabled={!enabled||!project||!address} className="rounded-lg border border-border px-2 py-1 hover:bg-muted disabled:opacity-50" onClick={()=>{
    if(!project)return
    if(fillAskFromMyAgentDraft(project,person,name,address,request))closeTerminalConversation()
    else useChatStore.getState().addToast('Open your own agent in this Crew to ask from it.','info')
   }}>✦ Open my agent with this</button>
   {replyInTerminal&&<span className="text-muted-foreground">Or reply in your terminal.</span>}
  </div>
  {copyError&&<p role="status" className="mt-1 text-muted-foreground">Select and copy the example.</p>}
 </section>
}

/** Docked under the existing teammate history (or its private notice). */
export function TeamAgentAsk({tabId}:{tabId:string}) {
 const tab=useChatStore(state=>state.chatTabs[tabId])
 const onlineCopy=tab?.metadata?.agentProfileChatLocation==='online_copy'
 const viewer=useAuthStore(state=>state.user?.id)
 const watching=!onlineCopy&&tab?.metadata?.agentProfileTabCanWrite===false&&tab.metadata.agentProfileTabOwnerID!==viewer
 const team=useTeamTab(watching?tab?.metadata?.agentProfileProjectId:undefined)
 const agent=watching?teamAgentFor(team.view?.agents,tab?.metadata?.agentProfileConversationKey):undefined
 const recipient=team.view?.reachable?.find(row=>row.agent_id===agent?.id)
 if(onlineCopy) {
  const project=tab?.metadata?.agentProfileProjectId
  const address=tab?.metadata?.agentProfileOnlineCopyAddress
  const own=!!viewer&&tab?.metadata?.agentProfileTabOwnerID===viewer
  return <AgentAskThread target={project?{project}:undefined} address={address} label={`Ask ${tab?.name||'this agent'}`} sender={tab?.name} scope={`${captureChatIdentity()}:${project}:${address}`} enabled={own&&!!project&&!!address}>
   <p className="mt-1 text-xs text-muted-foreground">Ask through AgentBridge. Replies stay here.</p>
  </AgentAskThread>
 }
 if(!watching||(!agent&&team.view)) return null
 const person=ownerShortName(agent?.owner_name||tab?.metadata?.agentProfileTabOwnerName||'your teammate')
 const admitted=!!recipient&&!!team.target&&!!team.view?.team.on&&!team.error
 return <AskMyAgentAction project={tab?.metadata?.agentProfileProjectId} person={person} name={agent?.name||tab?.name||'agent'} address={recipient?.address} enabled={admitted}/>
}

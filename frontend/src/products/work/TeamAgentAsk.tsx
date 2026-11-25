import { randomUUID } from '../../utils/randomUUID'
import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { ArrowUp, Loader2, Square } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { ChatComposerArea, ChatComposerAttachmentTiles, ChatComposerBand, ChatComposerControls, ChatComposerForm, ChatComposerTextarea, resizeChatComposerTextarea } from '../../components/chat/ChatComposer'
import { ComposerAttachButton } from '../../components/chat/ComposerActionsMenu'
import { getClipboardImageFiles } from '../../components/clipboardImages'
import { CodingCliIcon } from '../../components/chat/CodingCliIcon'
import { MarkdownRenderer } from '../../components/ui/MarkdownRenderer'
import { cn } from '../../lib/utils'
import axios from 'axios'
import api, { getAuthToken, getDesktopApiBaseUrl } from '../../services/api'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { type TeamTarget } from './teamApi'
import { useTeammateChatIdentity } from './TeammateChatIdentity'

type Reply = { message_id:string; message:string; sender?:string; files?:string[] }
/** What the teammate's agent runs, as its Team row says; shown, never changeable (it is their agent). */
export type AskAgentRuntime = { cli?:string; engine?:string; model_id?:string; reasoning_effort?:string }
type StopResult = { stopped?:boolean; line?:string }
type Mail = { message_id?:string; inbox_id?:string; delivery?:string; messages?:Reply[]; next_cursor?:number } & StopResult
/** The server's refusal as it sent it: code offline_choice carries line and readable for the Ask box (#3194). */
type MailRefusal = { code?:string; message?:string; line?:string; readable?:boolean }
export class TeamAgentMailError extends Error {
  readonly refusal:MailRefusal
  constructor(refusal:MailRefusal) { super(refusal.message||'Could not reach this agent.'); this.refusal=refusal }
}
/** The same AgentBridge calls as an external person, never a turn in the watched native session. */
export async function teamAgentMail(target: TeamTarget, args: Record<string,unknown>): Promise<Mail> {
  const body={name:'messages',arguments:args}
  if (!target.connection) {
    try { return (await api.post<Mail>('/api/external/v1/call',body,{skipSessionContext:true})).data }
    catch(cause){ const refusal=axios.isAxiosError(cause)?cause.response?.data?.error:undefined; if(refusal)throw new TeamAgentMailError(refusal); throw cause }
  }
  const response=await fetch(`${getDesktopApiBaseUrl()}/api/connections/${encodeURIComponent(target.connection)}/api/external/v1/call`,{method:'POST',headers:{Authorization:`Bearer ${getAuthToken()||''}`,'Content-Type':'application/json'},body:JSON.stringify(body)})
  const data=await response.json()
  if(!response.ok) throw new TeamAgentMailError(data?.error||{})
  return data as Mail
}

const fileBase64=(file:File)=>new Promise<string>((resolve,reject)=>{const reader=new FileReader();reader.onload=()=>resolve(String(reader.result).split(',')[1]||'');reader.onerror=()=>reject(new Error(`${file.name} could not be read.`));reader.readAsDataURL(file)})

/** One AgentBridge ask thread to an admitted address over the Team's saved connection; never a navigation or a turn in someone's own chat. */
// answersAbove: the agent's live chat is shown above this box and already has the answer, so the box lists nothing twice.
// thread: which conversation the box is in (who is asked); changing it starts a new one but keeps what you typed.
// wrap: the words actually sent (Quick answer sends your question inside an instruction to your own agent).
export function AgentAskThread({target,address,label,scope,thread='',enabled,sender,agent,children,answersAbove=false,top,chip,placeholder,wrap,onQuick}:{target?:TeamTarget;address?:string;label:string;scope:string;thread?:string;enabled:boolean;sender?:string;agent?:AskAgentRuntime;children?:ReactNode;answersAbove?:boolean;top?:ReactNode;chip?:ReactNode;placeholder?:string;wrap?:(text:string)=>string;onQuick?:()=>void}) {
 const [threadScope,setThreadScope]=useState(scope)
 const [draft,setDraft]=useState(''),[error,setError]=useState(''),[status,setStatus]=useState(''),[busy,setBusy]=useState(false)
 const [replies,setReplies]=useState<Reply[]>([]),[inbox,setInbox]=useState('')
 // The server's offline choice for this draft (#3194): nothing was sent; Wait sends it again for the real agent.
 const [offline,setOffline]=useState<MailRefusal|null>(null)
 // Files in the box, sent with the ask; the ask still running (its Stop), cleared when the agent answers or it stops.
 const [files,setFiles]=useState<File[]>([]),[dragging,setDragging]=useState(false),[running,setRunning]=useState<{inbox:string;message:string}|null>(null),[stopping,setStopping]=useState(false)
 const picker=useRef<HTMLInputElement>(null)
 const cursor=useRef(0)
 const currentScope=useRef(scope);currentScope.current=scope
 useEffect(()=>{setThreadScope(scope);setDraft('');setInbox('');setReplies([]);setError('');setStatus('');setBusy(false);setOffline(null);setFiles([]);setRunning(null);cursor.current=0},[scope])
 const firstThread=useRef(true)
 useEffect(()=>{if(firstThread.current){firstThread.current=false;return}setInbox('');setReplies([]);setError('');setStatus('');setOffline(null);setRunning(null);cursor.current=0},[thread])
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
    const fresh=(data.messages||[])
    if(fresh.length){setRunning(null);setStatus(answersAbove?'Answered in its chat above.':'')}
    setReplies(old=>[...old,...fresh.filter(row=>!old.some(item=>item.message_id===row.message_id))]);setError('')
   }catch(cause){if(active&&isChatIdentityCurrent(identity)){setReplies([]);setError(cause instanceof Error?cause.message:'Could not read replies.')}}
   finally{reading=false}
  }
  void read(); const timer=window.setInterval(()=>{if(!document.hidden)void read()},5000)
  return()=>{active=false;window.clearInterval(timer)}
 },[scope,inbox,enabled]) // eslint-disable-line react-hooks/exhaustive-deps
 const admitted=enabled&&!!address&&!!target
 const box=useRef<HTMLTextAreaElement>(null),list=useRef<HTMLDivElement>(null)
 const value=threadScope===scope?draft:''
 useLayoutEffect(()=>{if(box.current)resizeChatComposerTextarea(box.current,true)},[value])
 useEffect(()=>{list.current?.scrollTo({top:list.current.scrollHeight})},[replies.length])
 const send=async(wait=false)=>{
  if(!admitted||busy||running||(!draft.trim()&&!files.length)||!target||!address)return
  const identity=captureChatIdentity(),sendScope=scope,text=draft.trim(),sending=files
  setBusy(true);setError('');setOffline(null)
  try{
   const attachments=await Promise.all(sending.map(async file=>({file_name:file.name,file_base64:await fileBase64(file)})))
   const data=await teamAgentMail(target,{action:'ask',...(inbox?{inbox_id:inbox}:{target:address}),message:wrap?wrap(text):text,submission_id:randomUUID(),...(attachments.length?{attachments}:{}),...(wait?{offline:'wait'}:{})})
   if(!isChatIdentityCurrent(identity)||currentScope.current!==sendScope)return
   if(!data.inbox_id)throw new Error('No conversation receipt was returned. Check delivery before sending again.')
   const id=data.message_id||randomUUID()
   setInbox(data.inbox_id);setReplies(old=>[...old,{message_id:id,sender:'You',message:text,files:sending.map(file=>file.name)}]);setDraft('');setFiles([]);if(data.message_id)setRunning({inbox:data.inbox_id,message:data.message_id})
   setStatus(wait?'Sent. It answers here when it is back online.':data.delivery==='delivered'?'Delivered.':`Sent; waiting for delivery. ${answersAbove?'Its answer shows in its chat above.':'Replies appear here.'}`)
  }catch(cause){if(isChatIdentityCurrent(identity)&&currentScope.current===sendScope){
   if(cause instanceof TeamAgentMailError&&cause.refusal.code==='offline_choice')setOffline(cause.refusal)
   else setError(cause instanceof Error?cause.message:'Could not send this message.')
  }}
  finally{if(isChatIdentityCurrent(identity)&&currentScope.current===sendScope){setBusy(false);box.current?.focus()}}
 }
 const stop=async()=>{
  if(!running||!target||stopping)return
  const identity=captureChatIdentity(),sendScope=scope
  setStopping(true);setError('')
  try{
   const out=await teamAgentMail(target,{action:'stop',inbox_id:running.inbox,message_id:running.message})
   if(!isChatIdentityCurrent(identity)||currentScope.current!==sendScope)return
   setRunning(null);setStatus(out.line||'You stopped your ask.')
  }catch(cause){if(isChatIdentityCurrent(identity)&&currentScope.current===sendScope)setError(cause instanceof Error?cause.message:'Could not stop your ask. Try again.')}
  finally{if(isChatIdentityCurrent(identity)&&currentScope.current===sendScope)setStopping(false)}
 }
 const addFiles=(more:File[])=>{if(more.length){setFiles(old=>[...old,...more]);setError('')}}
 const runtime=[agent?.cli||agent?.engine,agent?.model_id,agent?.reasoning_effort].filter(Boolean).join(' · ')
 const canSend=admitted&&!busy&&(!!draft.trim()||files.length>0)
 return <section aria-label="Ask this agent" className="relative shrink-0"
  onDragOver={event=>{if(admitted&&!running&&event.dataTransfer?.types?.includes('Files')){event.preventDefault();setDragging(true)}}}
  onDragLeave={event=>{if(!event.currentTarget.contains(event.relatedTarget as Node))setDragging(false)}}
  onDrop={event=>{if(!event.dataTransfer?.files?.length)return;event.preventDefault();setDragging(false);if(admitted&&!running)addFiles(Array.from(event.dataTransfer.files))}}>
  {dragging&&<div aria-hidden="true" className="pointer-events-none absolute inset-0 z-10 grid place-items-center rounded-xl border-2 border-dashed border-primary bg-primary/10 text-sm font-medium text-primary">Drop files to add them to your ask</div>}
  {!answersAbove&&admitted&&threadScope===scope&&replies.length>0&&<div ref={list} className="max-h-72 space-y-2 overflow-y-auto border-t border-border px-4 py-3 text-sm">{replies.map(row=>{
   const mine=row.sender==='You'
   return <div key={row.message_id} className={cn('flex',mine?'justify-end':'justify-start')}>
    <div className={cn('max-w-[85%] min-w-0 break-words rounded-2xl px-3 py-2',mine?'bg-primary/10':'bg-muted')}>
     {!mine&&<p className="mb-0.5 text-xs font-medium text-muted-foreground">{row.sender||sender}</p>}
     {mine?<>{row.message&&<p className="whitespace-pre-wrap">{row.message}</p>}{!!row.files?.length&&<p className="mt-1 flex flex-wrap gap-1">{row.files.map((name,index)=><span key={index} className="rounded-md border border-border bg-background px-1.5 py-0.5 text-xs">📎 {name}</span>)}</p>}</>:<MarkdownRenderer content={row.message} untrustedContent disablePathLinking copyableCode />}
    </div>
   </div>})}</div>}
  <ChatComposerBand product className="shadow-none">
   <ChatComposerArea product className="px-3">
    {error&&<p role="alert" className="mb-1 px-1 text-xs text-destructive">{error}</p>}
    {status&&!error&&!offline&&<p role="status" className="mb-1 px-1 text-xs text-muted-foreground">{status}</p>}
    {offline&&<div role="alert" className="mb-1 flex flex-wrap items-center gap-x-2 gap-y-1 px-1 text-xs text-muted-foreground">
     <span className="min-w-0">{offline.line}{offline.readable?' Its earlier conversation may already have the answer.':''}</span>
     <Button type="button" variant="outline" size="xs" disabled={busy||(!draft.trim()&&!files.length)} onClick={()=>void send(true)}>Send when it's back</Button>
     {onQuick&&offline.readable&&<Button type="button" variant="outline" size="xs" onClick={()=>{setOffline(null);onQuick()}}>Quick answer now</Button>}
    </div>}
    {top}
    <ChatComposerForm product onSubmit={event=>{event.preventDefault();if(running)void stop();else void send()}}>
     {files.length>0&&<ChatComposerAttachmentTiles files={files} onRemove={index=>setFiles(old=>old.filter((_,at)=>at!==index))}/>}
     <ChatComposerTextarea product className="!min-h-[52px]" ref={box} aria-label={label} placeholder={placeholder||`${label}…`} value={value} disabled={!admitted||busy}
      onChange={event=>{setDraft(event.target.value);if(offline)setOffline(null)}}
      onPaste={event=>{const images=getClipboardImageFiles(event.clipboardData);if(images.length&&admitted){event.preventDefault();addFiles(images)}}}
      onKeyDown={event=>{if(event.key==='Enter'&&!event.shiftKey&&!event.nativeEvent.isComposing){event.preventDefault();if(!running)void send()}}}/>
     <ChatComposerControls>
      <span className="flex min-w-0 max-w-full flex-wrap items-center gap-1">
       <ComposerAttachButton uploading={busy&&files.length>0} onAttach={()=>{if(admitted&&!running)picker.current?.click()}}/>
       {chip??(runtime&&<span title="This is their agent, so its AI is their choice" className="inline-flex min-w-0 max-w-48 items-center gap-1 truncate px-1 text-[11px] text-muted-foreground"><CodingCliIcon provider={agent?.cli||agent?.engine||'terminal'} className="inline-flex h-3 w-3 shrink-0 items-center"/><span className="truncate">{runtime}</span></span>)}
      </span>
      <span className="flex shrink-0 items-center gap-1">
      {running
       ?<Button type="submit" variant="ghost" size="icon" aria-label="Stop your ask" title="Stop your ask" disabled={stopping} className="h-7 w-7 rounded-full p-0 bg-diff-del/15 text-diff-del hover:bg-diff-del/25 hover:text-diff-del">{stopping?<Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden="true"/>:<Square className="h-3.5 w-3.5" fill="currentColor" aria-hidden="true"/>}</Button>
       :<Button type="submit" variant="ghost" size="xs" aria-label={busy?'Sending':'Send'} title="Send (Enter)" disabled={!canSend} className="w-6 border border-border px-0 text-primary hover:bg-muted [&_svg]:size-3.5">{busy?<Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden="true"/>:<ArrowUp className="h-3.5 w-3.5" aria-hidden="true"/>}</Button>}
      </span>
     </ChatComposerControls>
    </ChatComposerForm>
    <input ref={picker} type="file" multiple className="hidden" onChange={event=>{addFiles(Array.from(event.target.files||[]));event.target.value=''}}/>
    {children&&<p className="mt-0.5 truncate px-1.5 text-[11px] text-muted-foreground">{children}</p>}
   </ChatComposerArea>
  </ChatComposerBand>
 </section>
}

/** The bottom of a teammate's agent view (owner, 20 Nov): one box with two ways to ask (owner, 25 Nov mockup).
 * Ask <agent>: an AgentBridge ask to its admitted address; the server decides whether you may ask.
 * Quick answer: one of your own agents answers from what this agent already said; it is not messaged. */
export function TeamAgentAsk({tabId}:{tabId:string}) {
 const identity=useTeammateChatIdentity(tabId)
 if(!identity)return null
 const {name,person,place,online,ask,runtime,readable,own}=identity
 // Its chat can be read (Live chat, on the server), online or offline, and you have an agent of your own in this Team (#3577).
 const quick=readable&&ask.address&&own.length?{agents:own,prompt:(question:string)=>`Answer my question from what ${person}'s agent ${name} has already said, without messaging that agent or ${person}: read its chat with read_agent_chat (target ${ask.address}; search a few key words, then read the matching lines), answer in plain words, and say the answer comes from what ${name} said earlier, so the last few minutes may be missing. My question: ${question}`}:undefined
 return <TeammateAsk target={ask.target} address={ask.address} name={name} where={place} online={online} enabled={ask.enabled} agent={runtime} scope={`${tabId}:${ask.address||''}`} quick={quick} answersAbove={online!==false&&readable}/>
}

export type OwnAskAgent={name:string;address:string;runtime:AskAgentRuntime}
const QUICK_AGENT='agentworks:quick-answer-agent'

export function TeammateAsk({target,address,name,where,online,enabled,scope,agent,quick,answersAbove}:{target?:TeamTarget;address?:string;name:string;where:string;online?:boolean;enabled:boolean;scope:string;agent?:AskAgentRuntime;quick?:{agents:OwnAskAgent[];prompt:(question:string)=>string};answersAbove?:boolean}) {
 const offline=online===false,canQuick=!!quick?.agents.length
 // Offline opens on Quick answer, online on asking the agent; you can switch either way.
 const [mode,setMode]=useState<'agent'|'quick'>(offline&&canQuick?'quick':'agent')
 useEffect(()=>{setMode(offline&&canQuick?'quick':'agent')},[scope,offline,canQuick])
 const [picked,setPicked]=useState(()=>{try{return localStorage.getItem(QUICK_AGENT)||''}catch{return ''}})
 const helper=quick?.agents.find(row=>row.address===picked)||quick?.agents[0]
 const quickAgent=mode==='quick'?helper:undefined,isQuick=!!quickAgent
 const choose=(address:string)=>{setPicked(address);try{localStorage.setItem(QUICK_AGENT,address)}catch{/* kept for this view only */}}
 const top=canQuick?<div role="radiogroup" aria-label="How to ask" className="mb-1 ml-0.5 inline-flex rounded-full border border-border bg-background p-0.5 text-xs">
  {([['agent',`Ask ${name}`],['quick','Quick answer']] as const).map(([value,text])=><button key={value} type="button" role="radio" aria-checked={mode===value} onClick={()=>setMode(value)}
   title={value==='quick'?`One of your own agents answers from what ${name} already said. ${name} isn't messaged.`:`Your message goes to ${name}.`}
   className={cn('rounded-full px-2.5 py-0.5 text-muted-foreground hover:text-foreground',mode===value&&'bg-muted font-semibold text-foreground')}>{text}</button>)}
 </div>:undefined
 const helperRuntime=quickAgent?[quickAgent.runtime.cli||quickAgent.runtime.engine,quickAgent.runtime.model_id].filter(Boolean).join(' · '):''
 const chip=quickAgent?<label title="Which of your agents answers" className="inline-flex min-w-0 max-w-64 items-center gap-1 rounded px-1 text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground">
  <CodingCliIcon provider={quickAgent.runtime.cli||quickAgent.runtime.engine||'terminal'} className="inline-flex h-3 w-3 shrink-0 items-center"/>
  <select aria-label="Which of your agents answers" value={quickAgent.address} onChange={event=>choose(event.target.value)} className="min-w-0 truncate bg-transparent outline-none">
   {quick!.agents.map(row=><option key={row.address} value={row.address}>{row.name}{row.address===quickAgent.address&&helperRuntime?` · ${helperRuntime}`:''}</option>)}
  </select>
 </label>:undefined
 const placeholder=quickAgent?`Your ${quickAgent.name} answers from what ${name} already said. ${name} isn't messaged.`
  :offline?`Message ${name}. It answers when it's back online.`:`Message ${name}. It answers ${answersAbove?'in its chat above':'here'}.`
 return <AgentAskThread target={target} address={quickAgent?quickAgent.address:address} label={`Ask ${name}`} scope={scope} thread={quickAgent?`quick:${quickAgent.address}`:'agent'} enabled={enabled} sender={quickAgent?quickAgent.name:name}
  agent={isQuick?undefined:agent} answersAbove={!isQuick&&answersAbove} top={top} chip={chip} placeholder={placeholder} wrap={quickAgent&&quick?quick.prompt:undefined} onQuick={canQuick?()=>setMode('quick'):undefined}>
  {!enabled?`You can't ask ${name} right now.`:offline&&!isQuick?`${where.charAt(0).toUpperCase()}${where.slice(1)} is offline. Your message reaches it when it's back.`:undefined}
 </AgentAskThread>
}

import { type AvatarFields } from '../../components/ui/Avatar'
import { Fragment, useEffect, useRef, useState } from 'react'
import { ChevronRight } from 'lucide-react'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { useChatStore } from '../../stores/useChatStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { captureChatIdentity,isChatIdentityCurrent } from '../../utils/chatIdentity'
import { fillMainDraft } from '../../utils/agentPurposeDraft'
import { teamFailure, teamRequest, type TeamTarget } from './teamApi'

export type ActivityItem = AvatarFields & { private?:boolean; id:string; kind:string; at:string; person:string; title:string; verb:string; line?:string; why?:string; unseen?:boolean;today?:boolean; bridge?:{thread_id:string;can_open:boolean} }
/** A Needs-you card (#2853): an agent asks one person, who answers with one of its buttons. The server decides who may answer (for_me). */
export type TeamNeed = { id:string; line:string; link?:string; task?:string; task_title?:string; for:string; for_me:boolean; buttons:string[]; agent:string; person:string; at:string }
export type TeamNeedAnswer = { note?:string; choose_agent?:{id:string;name:string;online:boolean}[] }
type ActivityView = {feed:ActivityItem[];needs?:TeamNeed[];next_cursor?:string;refresh_after_ms?:number;unseen?:number;today?:number}

const clock = (at: string) => new Date(at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
function day(at: string) {
  const date = new Date(at), today = new Date(), yesterday = new Date()
  yesterday.setDate(today.getDate() - 1)
  return date.toDateString() === today.toDateString() ? 'Today' : date.toDateString() === yesterday.toDateString() ? 'Yesterday' : date.toLocaleDateString([], { day: 'numeric', month: 'short', year: 'numeric' })
}

/** Same crew_panel notices as the Team page; older pages use a server message-ID cursor, not a timer. */
export function useTeamActivity(target: TeamTarget | undefined, projectId: string, enabled = true) {
  const user = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = JSON.stringify([user, server, target?.connection, target?.project, projectId])
  const [loadedScope, setLoadedScope] = useState('')
  const [view, setView] = useState<ActivityView>()
  const [error, setError] = useState('')
  const [olderLoading, setOlderLoading] = useState(false)
  const [olderExhausted, setOlderExhausted] = useState(false)
  const loadRef = useRef<(older?: boolean) => void>(() => {})
  useEffect(() => {
    if (!target || !enabled) return
    let active = true, request = 0, current: ActivityView | undefined
    let boundary: ReturnType<typeof setTimeout> | undefined
    setView(undefined); setError(''); setOlderLoading(false); setOlderExhausted(false); setLoadedScope(scope)
    const load = (older = false) => {
      clearTimeout(boundary)
      const cursor = older ? current?.next_cursor : undefined
      if (older && !cursor) return
      const sequence = ++request
      if (older) setOlderLoading(true)
      void teamRequest<ActivityView>(target, `/activity${cursor ? `?before=${encodeURIComponent(cursor)}` : ''}`)
        .then(data => {
          if (!active || sequence !== request) return
          setOlderExhausted(older && !data.next_cursor)
          current = { ...data, feed: older && current ? [...current.feed, ...data.feed] : data.feed }
          setView(current); setError('')
          if (data.refresh_after_ms && data.refresh_after_ms > 0) boundary = setTimeout(() => load(), data.refresh_after_ms)
        })
        .catch(reason => { if (active && sequence === request) {current=undefined;setView(undefined);setError(teamFailure(reason).message)} })
        .finally(() => { if (active && sequence === request) setOlderLoading(false) })
    }
    loadRef.current = load
    const refresh = () => load()
    refresh()
    window.addEventListener('agentworks:team-updated', refresh)
    return () => { active = false; clearTimeout(boundary); loadRef.current = () => {}; window.removeEventListener('agentworks:team-updated', refresh) }
  }, [target?.project, target?.connection, projectId, scope, enabled])
  useLiveRefetch(()=>loadRef.current(),{kinds:['crew_panel'],project:projectId,scopeKey:scope,enabled:enabled&&!!target,fallbackMs:15000,safetyMs:60000,minIntervalMs:3000})
  const markSeen=async(through:string)=>{
    if(!target||!enabled||!through)return
    const identity=captureChatIdentity()
    try {await teamRequest(target,'/activity','POST',{seen_through:through});if(isChatIdentityCurrent(identity))loadRef.current()}catch{/* A failed mark never clears the server badge. */}
  }
  // One click is the person's answer; the server sends it on and the card leaves on the next read.
  const answer=async(need:string,button:string,helper?:string)=>{
    if(!target)throw new Error('The Team is unavailable.')
    const out=await teamRequest<TeamNeedAnswer>(target,'/work','POST',{op:'work_answer',need,button,...(helper?{helper}:{})})
    if(!out.choose_agent)loadRef.current()
    return out
  }
  return { answer, markSeen, view: loadedScope === scope ? view : undefined, error: loadedScope === scope ? error : '', olderLoading, olderExhausted: loadedScope === scope && olderExhausted, reload: () => loadRef.current(), older: () => loadRef.current(true) }
}

export function TeamActivity({ target, projectId }: { target: TeamTarget; projectId: string }) {
  return <TeamActivityContent target={target} projectId={projectId} activity={useTeamActivity(target, projectId)}/>
}

export function TeamActivityContent({target,projectId,activity,initialOpen=''}:{target:TeamTarget;projectId:string;activity:ReturnType<typeof useTeamActivity>;initialOpen?:string}) {
 const {view,error,reload,older,olderLoading,olderExhausted}=activity
 const [open,setOpen]=useState(initialOpen)
 const [filter,setFilter]=useState('all')
 const seen=useRef('')
 useEffect(()=>{if(initialOpen)setOpen(initialOpen)},[initialOpen])
 useEffect(()=>{const id=view?.feed[0]?.id,key=JSON.stringify([captureChatIdentity(),target.connection,target.project,id]);if(id&&seen.current!==key){seen.current=key;void activity.markSeen?.(id)}},[view?.feed[0]?.id,activity.markSeen])
 if(!view)return error?<div role="alert"><p>Could not load Team activity. {error}</p><button onClick={reload}>Try again</button></div>:<p role="status">Loading Team activity…</p>
 const items=view.feed.filter(item=>filter==='all'||item.kind===filter)
 return <section aria-label="Activity" className="space-y-3 text-sm">
  <div role="group" aria-label="Filter activity" className="flex flex-wrap gap-1 text-xs">{[['all','All'],['message','Messages'],['decision','Decisions'],['finding','Findings'],['merged','Merged']].map(([kind,label])=><button key={kind} type="button" aria-pressed={filter===kind} onClick={()=>setFilter(kind)} className={`rounded-md px-2 py-1 ${filter===kind?'bg-muted font-medium':'text-muted-foreground hover:bg-muted/50'}`}>{label}</button>)}</div>
  {!items.length&&<p className="text-xs text-muted-foreground">Nothing yet.</p>}
  {items.map((item,index)=><Fragment key={item.id}>
   {(index===0||day(items[index-1].at)!==day(item.at))&&<h3 className="text-xs text-muted-foreground">{day(item.at)}</h3>}
   <div className="flex items-start gap-2 py-2"><span aria-hidden="true">{item.kind==='message'?'↗':item.kind==='decision'?'◆':item.kind==='finding'?'●':'✓'}</span><div className="min-w-0 flex-1">
    {item.bridge?.can_open?<button type="button" aria-expanded={open===item.id} onClick={()=>setOpen(open===item.id?'':item.id)} className="text-left hover:text-primary"><b>{item.title}</b> {item.verb}<ChevronRight aria-hidden="true" className="inline h-3 w-3"/></button>:<p><b>{item.title}</b> {item.verb}</p>}
    {item.line&&<p className="truncate text-xs text-muted-foreground">{item.line}</p>}{item.why&&<p className="text-xs text-muted-foreground">{item.why}</p>}
    {open===item.id&&item.bridge?.can_open&&<TeamActivityThread target={target} projectId={projectId} thread={item.bridge.thread_id}/>}
   </div><time dateTime={item.at} className="shrink-0 text-[10px] text-muted-foreground">{clock(item.at)}</time></div>
  </Fragment>)}
  {view.next_cursor&&<button type="button" disabled={olderLoading} onClick={older} className="text-xs text-primary">{olderLoading?'Loading…':'Show older'}</button>}
  {olderExhausted&&<p role="status">No older activity</p>}
  {error&&<p role="alert">{error}<button onClick={reload}>Try again</button></p>}
 </section>
}
function TeamActivityThread({target,projectId,thread}:{target:TeamTarget;projectId:string;thread:string}) {
 const account=useAuthStore(state=>state.user?.id)
 const key=JSON.stringify([captureChatIdentity(),account,target.connection,target.project,thread])
 const [state,setState]=useState<{key:string;messages?:{id:string;at:string;agent:string;text:string}[];error?:string}>()
 useEffect(()=>{
  let active=true;const identity=captureChatIdentity()
  void teamRequest<{messages:{id:string;at:string;agent:string;text:string}[]}>(target,`/activity?thread=${encodeURIComponent(thread)}`).then(data=>{if(active&&isChatIdentityCurrent(identity))setState({key,messages:data.messages})}).catch(()=>{if(active&&isChatIdentityCurrent(identity))setState({key,error:'This thread is no longer available to you.'})})
  return()=>{active=false}
 },[key])
 const current=state?.key===key?state:undefined
 return <section aria-label="AgentBridge thread" className="mt-2 space-y-2 rounded border border-border p-2 text-xs">
  {current?.error?<p role="alert">{current.error}</p>:current?.messages?current.messages.map(message=><p key={message.id} className="whitespace-pre-wrap break-words"><b>{message.agent}</b> · {clock(message.at)}<br/>{message.text}</p>):<p>Loading thread…</p>}
  {current?.messages&&<button type="button" onClick={()=>{if(!fillMainDraft(projectId,`Ask my agent about this Team AgentBridge thread: ${thread}. Read it only through my admitted inbox tools; ask me what to follow up on.`))useChatStore.getState().addToast('Open your own agent in this Crew first.','info')}} className="text-primary">✦ Ask my agent about this</button>}
 </section>
}

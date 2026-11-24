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
import { openCrewAccess } from './MacSharedAgents'
import { CodingCliIcon } from '../../components/chat/CodingCliIcon'
import { type TaskFile, type TaskFileContent } from './TaskAttachments'

export type ActivityItem = AvatarFields & { private?:boolean; id:string; kind:string; at:string; person:string; title:string; verb:string; line?:string; why?:string; unseen?:boolean;today?:boolean; cli?:string; bridge?:{thread_id:string;can_open:boolean} }
/** A Needs-you card (#2853): an agent asks one person, who answers with one of its buttons. The server decides who may answer (for_me). */
export type TeamNeed = { id:string; line:string; link?:string; links?:string[]; files?:TaskFile[]; task?:string; task_title?:string; for:string; for_me:boolean; buttons:string[]; agent:string; person:string; mine?:boolean; at:string }
export type TeamNeedAnswer = { note?:string; choose_agent?:{id:string;name:string;online:boolean}[] }
type ActivityView = {feed:ActivityItem[];collapsed_item?:ActivityItem|null;needs?:TeamNeed[];next_cursor?:string;refresh_after_ms?:number;unseen?:number;today?:number}

const clock = (at: string) => new Date(at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
function day(at: string) {
  const date = new Date(at), today = new Date(), yesterday = new Date()
  yesterday.setDate(today.getDate() - 1)
  return date.toDateString() === today.toDateString() ? 'Today' : date.toDateString() === yesterday.toDateString() ? 'Yesterday' : date.toLocaleDateString([], { day: 'numeric', month: 'short', year: 'numeric' })
}

// The last list per account, server and Team: the sidebar and the Team page show it at once and refresh behind it (#2965 speed).
const activityCache=new Map<string,ActivityView>()
const remember=<V,>(cache:Map<string,V>,key:string,value:V)=>{cache.delete(key);cache.set(key,value);if(cache.size>30)cache.delete(cache.keys().next().value as string)}

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
    let active = true, request = 0, current: ActivityView | undefined = activityCache.get(scope)
    let boundary: ReturnType<typeof setTimeout> | undefined
    setView(current); setError(''); setOlderLoading(false); setOlderExhausted(false); setLoadedScope(scope)
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
          if (!older) remember(activityCache, scope, current)
          setView(current); setError('')
          if (data.refresh_after_ms && data.refresh_after_ms > 0) boundary = setTimeout(() => load(), data.refresh_after_ms)
        })
        .catch(reason => { if (active && sequence === request) {current=undefined;activityCache.delete(scope);setView(undefined);setError(teamFailure(reason).message)} })
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
  const answer=async(need:string,button:string,helper?:string,comment?:string)=>{
    if(!target)throw new Error('The Team is unavailable.')
    const out=await teamRequest<TeamNeedAnswer>(target,'/work','POST',{op:'work_answer',need,button,...(helper?{helper}:{}),...(comment?{comment}:{})})
    if(!out.choose_agent)loadRef.current()
    return out
  }
  // A card's file opens through its task: the card is the request, so the same check applies.
  const openFile=(need:string,file:TaskFile)=>{if(!target)return Promise.reject(new Error('The Team is unavailable.'));return teamRequest<TaskFileContent>(target,`/activity?task=${encodeURIComponent(need)}&file=${encodeURIComponent(file.id)}`)}
  return { answer, openFile, markSeen, view: loadedScope === scope ? view : undefined, error: loadedScope === scope ? error : '', olderLoading, olderExhausted: loadedScope === scope && olderExhausted, reload: () => loadRef.current(), older: () => loadRef.current(true) }
}

const openEvent = 'agentworks:team-activity-open'
let pendingOpen: { projectId: string; id: string } | undefined
/** Opens the Team's complete activity in the main pane (owner, 16 Nov), at one AgentBridge thread when given. */
export function openTeamActivity(projectId: string, id = '') {
  pendingOpen = { projectId, id }
  openCrewAccess(projectId, '', 'team', 'activity')
  window.dispatchEvent(new CustomEvent(openEvent, { detail: { projectId, id } }))
}

export function TeamActivity({ target, projectId }: { target: TeamTarget; projectId: string }) {
  return <TeamActivityContent target={target} projectId={projectId} activity={useTeamActivity(target, projectId)}/>
}

export function TeamActivityContent({target,projectId,activity,initialOpen=''}:{target:TeamTarget;projectId:string;activity:ReturnType<typeof useTeamActivity>;initialOpen?:string}) {
 const {view,error,reload,older,olderLoading,olderExhausted}=activity
 const [open,setOpen]=useState(()=>{const pending=pendingOpen?.projectId===projectId?pendingOpen.id:'';if(pending)pendingOpen=undefined;return pending||initialOpen})
 const seen=useRef('')
 useEffect(()=>{if(initialOpen)setOpen(initialOpen)},[initialOpen])
 useEffect(()=>{
  const opened=(event:Event)=>{const detail=(event as CustomEvent<{projectId:string;id:string}>).detail;if(detail?.projectId===projectId){pendingOpen=undefined;if(detail.id)setOpen(detail.id)}}
  window.addEventListener(openEvent,opened);return()=>window.removeEventListener(openEvent,opened)
 },[projectId])
 useEffect(()=>{const id=view?.feed[0]?.id,key=JSON.stringify([captureChatIdentity(),target.connection,target.project,id]);if(id&&seen.current!==key){seen.current=key;void activity.markSeen?.(id)}},[view?.feed[0]?.id,activity.markSeen])
 if(!view)return error?<div role="alert"><p>Could not load Team activity. {error}</p><button onClick={reload}>Try again</button></div>:<p role="status">Loading Team activity…</p>
 const items=view.feed
 return <section aria-label="Activity" className="space-y-3 text-sm">
  {!items.length&&<p className="text-xs text-muted-foreground">Nothing yet.</p>}
  {items.map((item,index)=><Fragment key={item.id}>
   {(index===0||day(items[index-1].at)!==day(item.at))&&<h3 className="text-xs text-muted-foreground">{day(item.at)}</h3>}
   <div className="flex items-start gap-2 py-2"><span aria-hidden="true">{item.kind==='message'?'↗':item.kind==='decision'?'◆':item.kind==='finding'?'●':item.verb==='started'?'▸':item.verb==='added'?'+':'✓'}</span><div className="min-w-0 flex-1">
    {item.bridge?.can_open?<button type="button" aria-expanded={open===item.id} onClick={()=>setOpen(open===item.id?'':item.id)} className="text-left hover:text-primary"><CodingCliIcon provider={item.cli} className="mr-1 inline-flex h-3.5 w-3.5 shrink-0 items-center align-[-2px]"/><b>{item.title}</b> {item.verb}<ChevronRight aria-hidden="true" className="inline h-3 w-3"/></button>:<p><CodingCliIcon provider={item.cli} className="mr-1 inline-flex h-3.5 w-3.5 shrink-0 items-center align-[-2px]"/><b>{item.title}</b> {item.verb}</p>}
    {item.line&&<p className="truncate text-xs text-muted-foreground">{item.line}</p>}{item.why&&<p className="text-xs text-muted-foreground">{item.why}</p>}
    {open===item.id&&item.bridge?.can_open&&<TeamActivityThread target={target} projectId={projectId} thread={item.bridge.thread_id}/>}
   </div><time dateTime={item.at} className="shrink-0 text-[10px] text-muted-foreground">{clock(item.at)}</time></div>
  </Fragment>)}
  {view.next_cursor&&<button type="button" disabled={olderLoading} onClick={older} className="text-xs text-primary">{olderLoading?'Loading…':'Show older'}</button>}
  {olderExhausted&&<p role="status">No older activity</p>}
  {error&&<p role="alert">{error}<button onClick={reload}>Try again</button></p>}
 </section>
}
type ThreadMessage={id:string;at:string;agent:string;text:string}
const threadKey=(target:TeamTarget,thread:string)=>JSON.stringify([captureChatIdentity(),useAuthStore.getState().user?.id,target.connection,target.project,thread])
// One fetch per thread at a time: a click starts it before the Team page opens; the thread shows the last copy at once.
const threadLoads=new Map<string,{at:number;load:Promise<ThreadMessage[]>}>()
const threadShown=new Map<string,ThreadMessage[]>()
export function loadTeamThread(target:TeamTarget,thread:string):Promise<ThreadMessage[]>{
 const key=threadKey(target,thread),started=threadLoads.get(key)
 if(started&&Date.now()-started.at<5000)return started.load
 const load=teamRequest<{messages:ThreadMessage[]}>(target,`/activity?thread=${encodeURIComponent(thread)}`).then(data=>{remember(threadShown,key,data.messages);return data.messages})
 load.catch(()=>{threadLoads.delete(key);threadShown.delete(key)})
 remember(threadLoads,key,{at:Date.now(),load})
 return load
}
function TeamActivityThread({target,projectId,thread}:{target:TeamTarget;projectId:string;thread:string}) {
 const account=useAuthStore(state=>state.user?.id)
 const key=JSON.stringify([captureChatIdentity(),account,target.connection,target.project,thread])
 const [state,setState]=useState<{key:string;messages?:ThreadMessage[];error?:string}>()
 useEffect(()=>{
  let active=true;const identity=captureChatIdentity()
  void loadTeamThread(target,thread).then(messages=>{if(active&&isChatIdentityCurrent(identity))setState({key,messages})}).catch(()=>{if(active&&isChatIdentityCurrent(identity))setState({key,error:'This thread is no longer available to you.'})})
  return()=>{active=false}
 },[key])
 const shown=threadShown.get(key)
 const current=state?.key===key?state:shown?{key,messages:shown}:undefined
 return <section aria-label="AgentBridge thread" className="mt-2 space-y-2 rounded border border-border p-2 text-xs">
  {current?.error?<p role="alert">{current.error}</p>:current?.messages?current.messages.map(message=><p key={message.id} className="whitespace-pre-wrap break-words"><b>{message.agent}</b> · {clock(message.at)}<br/>{message.text}</p>):<p>Loading thread…</p>}
  {current?.messages&&<button type="button" onClick={()=>{if(!fillMainDraft(projectId,`Ask my agent about this Team AgentBridge thread: ${thread}. Read it only through my admitted inbox tools; ask me what to follow up on.`))useChatStore.getState().addToast('Open your own agent in this project first.','info')}} className="text-primary">✦ Ask my agent about this</button>}
 </section>
}

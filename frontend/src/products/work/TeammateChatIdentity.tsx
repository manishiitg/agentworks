import { useAuthStore } from '../../stores/useAuthStore'
import { useChatStore } from '../../stores/useChatStore'
import { ownerShortName } from '../../components/chat/chatTabName'
import { openAgentTab } from '../../utils/openAgentTab'
import { openAppProduct } from '../../utils/appNavigation'
import { teamAgentFor, useTeamTab } from './teamApi'

/** Display only the observed tab and its server row, never the last own tab. */
export function useTeammateChatIdentity(tabId?:string|null) {
 const tab=useChatStore(state=>tabId?state.chatTabs[tabId]:undefined)
 const viewer=useAuthStore(state=>state.user?.id)
 const watching=tab?.metadata?.agentProfileTabCanWrite===false&&tab.metadata.agentProfileTabOwnerID!==viewer
 const team=useTeamTab(watching?tab?.metadata?.agentProfileProjectId:undefined)
 const agent=watching?teamAgentFor(team.view?.agents,tab?.metadata?.agentProfileConversationKey):undefined
 const reach=team.view?.reachable?.find(row=>row.agent_id===agent?.id)
 if(!watching)return null
 return {name:agent?.name||tab?.name||'Agent',person:ownerShortName(agent?.owner_name||tab?.metadata?.agentProfileTabOwnerName||'another person'),place:agent?.place==='mac'?'their Mac':agent?.place==='server'?'server':'another computer',online:reach?.online,working:agent?.activity==='running'}
}

export function TeammateChatIdentity({tabId,banner=false}:{tabId?:string|null;banner?:boolean}) {
 const identity=useTeammateChatIdentity(tabId)
 const tabs=useChatStore(state=>state.chatTabs)
 const viewer=useAuthStore(state=>state.user?.id)
 const project=tabs[tabId||'']?.metadata?.agentProfileProjectId
 // Existing visit timestamps remember the previous own chat, even when it
 // belongs to another Crew. Never return to a watched or automated history.
 const own=Object.values(tabs).filter(tab=>{
  const m=tab.metadata
  return !!viewer && m?.agentProfileId==='work' && !m.isViewOnly && m.agentProfileTabCanWrite!==false
   && m.agentProfileBuilder!==true && !m.isScheduledRun && !m.isBotRun && !m.isExecutionRun
   && (!m.agentProfileTabOwnerID || m.agentProfileTabOwnerID===viewer)
 })
 const previous=own.filter(tab=>!!tab.lastAccessedAt).sort((a,b)=>(b.lastAccessedAt||0)-(a.lastAccessedAt||0))[0]
 const back=previous||own.find(tab=>tab.metadata?.agentProfileProjectId===project&&tab.metadata?.agentProfileIsMainChat)
 if(!identity)return null
 return <>
  <TeammateAgentIdentity identity={identity} banner={banner}/>
  {banner&&project&&<div className="shrink-0 border-b border-border px-4 py-2">
   <button type="button" className="text-sm font-medium text-primary hover:underline" onClick={()=>back?openAgentTab(back.tabId):openAppProduct({surface:'work',id:project,agent:'main'})}>← Back to {back?.name||'Main'}</button>
  </div>}
 </>
}

export function TeammateAgentIdentity({identity,banner=false}:{identity:{name:string;person:string;place:string;online?:boolean;working?:boolean};banner?:boolean}) {
 const {name,person,place,online,working}=identity
 if(banner)return <div role="note" className="shrink-0 border-b border-sky-200 bg-sky-50 px-4 py-3 text-sm text-sky-950 dark:border-sky-900 dark:bg-sky-950/40 dark:text-sky-100">You’re viewing {person}&apos;s agent on {place==='server'?'the server':place}. Ask your agent to talk to it.</div>
 return <div data-agent-header aria-label="Teammate agent" className="flex min-w-0 items-center gap-2 px-2 py-1 text-sm">
  <span aria-hidden="true" className="relative grid h-7 w-7 shrink-0 place-items-center rounded-full bg-sky-100 font-semibold text-sky-900 dark:bg-sky-900 dark:text-sky-100">{person.slice(0,1).toUpperCase()}<span className={`absolute -bottom-0.5 -right-0.5 h-2 w-2 rounded-full border border-background ${online?'bg-emerald-500':'bg-muted-foreground/50'}`}/></span>
  <span className="min-w-0 truncate font-semibold">{name}</span>
  <span title={`${person} · ${place}`} className="min-w-0 truncate rounded-full border border-sky-200 bg-sky-50 px-2 py-0.5 text-xs text-sky-900 dark:border-sky-900 dark:bg-sky-950/40 dark:text-sky-100">{person} · {place}</span>
  <span className="shrink-0 text-xs text-muted-foreground">{working?'Working':online===true?'Online':online===false?'Offline':''}</span>
 </div>
}

import { ArrowLeft, Lock } from 'lucide-react'
import { ForkTeammateAgent } from './ForkTeammateAgent'
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
 const person=ownerShortName(agent?.owner_name||tab?.metadata?.agentProfileTabOwnerName||'another person')
 return {fork:agent&&team.target?{target:team.target,agent:agent.id}:undefined,name:agent?.name||tab?.name||'Agent',person,place:agent?.place==='mac'?`${person}'s Mac`:agent?.place==='server'?'the server':'another computer',online:reach?.online,working:agent?.activity==='running',hidden:agent?.chat_share==='agent'||!!agent?.chat_private_line,
  ask:{target:team.target,address:reach?.address,enabled:!!reach&&!!team.target&&!!team.view?.team.on&&!team.error}}
}

/** One header line for a teammate's agent (owner, 20 Nov): back, the agent, whose computer and its state, Fork as an icon.
 * No banner or Back row; the Ask box sits at the bottom of the conversation. */
export function TeammateChatIdentity({tabId}:{tabId?:string|null}) {
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
 return <div className="flex min-w-0 flex-1 items-center gap-1">
  {project&&<button type="button" aria-label={`Back to ${back?.name||'Main'}`} title={`Back to ${back?.name||'Main'}`} onClick={()=>back?openAgentTab(back.tabId):openAppProduct({surface:'work',id:project,agent:'main'})} className="shrink-0 rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"><ArrowLeft className="h-4 w-4"/></button>}
  <TeammateAgentIdentity identity={identity}/>
  {identity.fork&&<ForkTeammateAgent compact target={identity.fork.target} agent={identity.fork.agent} name={identity.name}/>}
 </div>
}

export function TeammateAgentIdentity({identity}:{identity:{name:string;person:string;place:string;online?:boolean;working?:boolean;hidden?:boolean}}) {
 const {name,person,place,online,working,hidden}=identity
 const state=working?'Working':online===true?'Online':online===false?'Offline':''
 return <div data-agent-header aria-label="Teammate agent" className="flex min-w-0 items-center gap-2 px-1 py-1 text-sm">
  <span aria-hidden="true" className="relative grid h-7 w-7 shrink-0 place-items-center rounded-full bg-muted font-semibold">{person.slice(0,1).toUpperCase()}<span className={`absolute -bottom-0.5 -right-0.5 h-2 w-2 rounded-full border border-background ${online?'bg-emerald-500':'bg-muted-foreground/50'}`}/></span>
  <span className="min-w-0 shrink truncate font-semibold">{name}</span>
  <span className="min-w-0 truncate text-xs text-muted-foreground">{place}{state&&<> · <span className={working?'text-emerald-600 dark:text-emerald-400':undefined}>{state}</span></>}{hidden&&<> · <Lock aria-hidden="true" className="inline h-3 w-3"/> Chat hidden</>}</span>
 </div>
}

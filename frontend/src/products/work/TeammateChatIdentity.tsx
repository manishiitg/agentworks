import { useAuthStore } from '../../stores/useAuthStore'
import { useChatStore } from '../../stores/useChatStore'
import { ownerShortName } from '../../components/chat/chatTabName'
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
 if(!identity)return null
 const {name,person,place,online,working}=identity
 if(banner)return <div role="note" className="shrink-0 border-b border-sky-200 bg-sky-50 px-4 py-3 text-sm text-sky-950 dark:border-sky-900 dark:bg-sky-950/40 dark:text-sky-100">You’re viewing {person}&apos;s agent on {place==='server'?'the server':place}. You can ask it; you can’t type in its chat.</div>
 return <div data-agent-header aria-label="Teammate agent" className="flex min-w-0 items-center gap-2 px-2 py-1 text-sm">
  <span aria-hidden="true" className="relative grid h-7 w-7 shrink-0 place-items-center rounded-full bg-sky-100 font-semibold text-sky-900 dark:bg-sky-900 dark:text-sky-100">{person.slice(0,1).toUpperCase()}<span className={`absolute -bottom-0.5 -right-0.5 h-2 w-2 rounded-full border border-background ${online?'bg-emerald-500':'bg-muted-foreground/50'}`}/></span>
  <span className="min-w-0 truncate font-semibold">{name}</span>
  <span title={`${person} · ${place}`} className="min-w-0 truncate rounded-full border border-sky-200 bg-sky-50 px-2 py-0.5 text-xs text-sky-900 dark:border-sky-900 dark:bg-sky-950/40 dark:text-sky-100">{person} · {place}</span>
  <span className="shrink-0 text-xs text-muted-foreground">{working?'Working':online===true?'Online':online===false?'Offline':''}</span>
 </div>
}

import { useEffect, useRef, useState } from 'react'
import { TeamPanel, type TeamPanelAgent, type TeamPanelPost } from './TeamPanel'
import { teamRequest, type TeamTabState } from './teamApi'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { fillAskFromMyAgentDraft } from '../../utils/agentPurposeDraft'
import { teamAgentChatTab } from './AgentSharingRow'
import { openAgentTab } from '../../utils/openAgentTab'
import { openCrewAccess } from './MacSharedAgents'
import { useChatStore } from '../../stores/useChatStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'

type Posts = { doing: Record<string, TeamPanelPost>; decisions: TeamPanelPost[] }
export function CrewTeamPanel({ scope, projectId, team }: {scope:string; projectId:string; team:TeamTabState}) {
 const user = useAuthStore(state => state.user?.id)
 const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
 const target = team.target
 const enabled = !!target && !!team.view?.team.on && !team.error
 const [loaded,setLoaded] = useState<{scope:string;posts:TeamPanelPost[];error?:string}>()
 const refresh = useRef(()=>{})
 useEffect(()=>{
  let active=true, reading=false, again=false
  const identity=captureChatIdentity()
  const read=async()=>{
   if(!enabled || !target) return
   if(reading) { again=true; return }
   reading=true
   try {
    const data=await teamRequest<Posts>(target,'/activity-posts')
    if(active && isChatIdentityCurrent(identity)) setLoaded({scope,posts:[...Object.values(data.doing||{}),...(data.decisions||[])]})
   } catch { if(active && isChatIdentityCurrent(identity)) setLoaded({scope,posts:[],error:'Could not load Team activity.'}) }
   finally { reading=false; if(active && again) {again=false;void read()} }
  }
  refresh.current=()=>{void read()};void read()
  return ()=>{active=false;refresh.current=()=>{}}
 },[scope,enabled,target?.project,target?.connection])
 useLiveRefetch(()=>refresh.current(),{kinds:['crew_panel'],project:projectId,scopeKey:scope,enabled,fallbackMs:15000,safetyMs:60000,minIntervalMs:3000})
 if(team.error) return <p role="alert" className="px-1 text-[11px] text-muted-foreground">Could not load this Team. Try again.</p>
 if(!enabled || !team.view) return null
 const live=new Map([...(team.view.live?.agents||[]),...(team.view.live?.idle||[])].map(row=>[row.id,row]))
 const people=new Map(team.view.people.map(person=>[person.user_id,person]))
 const admittedRows=team.view.agents.filter(row=>row.mine||row.in_team)
 const rows:TeamPanelAgent[]=admittedRows.map(row=>{
  const current=live.get(row.id||'')
  return {id:row.id||row.key,name:row.name,person:row.owner_name||'You',mine:row.mine,online:row.place==='server'||people.get(row.owner_id)?.presence?.online===true,place:row.place,activity:row.activity,last_message:current?.last_message||current?.doing,last_message_at:current?.last_message_at}
 })
 const ask=(row:TeamPanelAgent)=>{
  if(useAuthStore.getState().user?.id!==user || useWorkspaceConnectionStore.getState().activeWorkspaceId!==server) return
  if(row.mine) {
   const own=team.view!.agents.find(agent=>agent.mine&&(agent.id||agent.key)===row.id)
   const tab=own&&teamAgentChatTab(team,projectId,own.key)
   if(tab) {openAgentTab(tab.tabId);return}
  } else {
   const admitted=team.view!.agents.find(agent=>!agent.mine&&agent.in_team&&(agent.id||agent.key)===row.id)
   if(admitted?.address && fillAskFromMyAgentDraft(projectId,row.person,row.name,admitted.address)) return
  }
  useChatStore.getState().addToast('Open your own agent in this Crew, then try again.','error')
 }
 return <TeamPanel scope={scope} agents={rows} posts={loaded?.scope===scope?loaded.posts:[]} error={loaded?.scope===scope?loaded.error:undefined} onAsk={ask} onActivity={()=>openCrewAccess(projectId,'','team','activity')}/>
}

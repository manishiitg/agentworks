import { selectReportDocument } from '../../components/workflow/reportDocuments'
import { useEffect, useRef, useState } from 'react'
import { TeamPanel, type TeamPanelAgent, type TeamPanelPost, type TeamPanelShared } from './TeamPanel'
import { crewDashboardsTab, teamRequest, type CrewDashboardsTab, type TeamOnlineView, type TeamTabState } from './teamApi'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { openTeammateAgent } from './openTeammateAgent'
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
 const [onlineError,setOnlineError]=useState('')
 const [online, setOnline] = useState<{scope:string;workspace:string;items:(CrewDashboardsTab['items'][number] & {addedBy?:string})[]}>()
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
 useEffect(()=>{
  let active=true
  const identity=captureChatIdentity()
  const read=async()=>{
   if(!enabled || !target) return
   try {
    const mapping=await teamRequest<TeamOnlineView>(target,'/online-items')
    if(!mapping.home_project_id) { if(active && isChatIdentityCurrent(identity)) {setOnline({scope,workspace:'',items:[]});setOnlineError('')} return }
    const home=await crewDashboardsTab({...target,project:mapping.home_project_id},'all')
    const accepted=new Map((mapping.items||[]).filter(item=>item.kind!=='automation').map(item=>[item.destination_item_id,item]))
    if(active && isChatIdentityCurrent(identity)) {setOnline({scope,workspace:home.workspace,items:home.items.filter(item=>accepted.has(item.id)).map(item=>({...item,addedBy:accepted.get(item.id)?.added_by_name}))});setOnlineError('');console.info('[TEAM_ARTIFACTS]', {project:target.project,items:accepted.size})}
   } catch { if(active && isChatIdentityCurrent(identity)) {setOnline(undefined);setOnlineError('Could not load Team artifacts. Retrying.')} }
  }
  void read();window.addEventListener('agentworks:team-updated',read)
  return ()=>{active=false;window.removeEventListener('agentworks:team-updated',read)}
 },[scope,enabled,target?.project,target?.connection,loaded])
 useLiveRefetch(()=>refresh.current(),{kinds:['crew_panel'],project:projectId,scopeKey:scope,enabled,fallbackMs:15000,safetyMs:60000,minIntervalMs:3000})
 if(team.error) return <p role="alert" className="px-1 text-[11px] text-muted-foreground">Could not load this Team. Try again.</p>
 if(!enabled || !team.view) return null
 const rows:TeamPanelAgent[]=(team.view.reachable||[]).map(row=>({...row,connection:target?.connection,id:row.agent_id,name:row.name,person:row.person||'Team',mine:false,team_agent:row.kind==='team_agent',online:row.online,place:row.place,activity:row.activity,last_message:row.last_message,last_message_at:row.last_message_at,typing:row.typing,cli:row.cli,engine:row.engine,model_id:row.model_id,reasoning_effort:row.reasoning_effort}))
 const ask=(row:TeamPanelAgent)=>{
  if(useAuthStore.getState().user?.id!==user || useWorkspaceConnectionStore.getState().activeWorkspaceId!==server) return
  if(row.mine) {
   const own=team.view!.agents.find(agent=>agent.mine&&(agent.id||agent.key)===row.id)
   const tab=own&&teamAgentChatTab(team,projectId,own.key)
   if(tab) {openAgentTab(tab.tabId);return}
  } else {
   const admitted=team.view!.reachable?.find(agent=>agent.agent_id===row.id)
   const agent=team.view!.agents.find(agent=>!agent.mine&&agent.id===admitted?.agent_id)
   if(agent) {openTeammateAgent(projectId,agent.watch_key||agent.key,agent.owner_id);return}
  }
  useChatStore.getState().addToast('Open your own agent in this Crew, then try again.','error')
 }
 // Shared by you: the person's own agents shared with the team, with their mode and audience from the server rows.
 const shared:TeamPanelShared[]=team.view.agents.filter(agent=>agent.mine&&agent.in_team).map(agent=>({key:agent.key,name:agent.name,canChange:Boolean(agent.can_make_changes),chosen:agent.audience==='chosen'?(agent.chosen_people||[]).length:undefined}))
 const openShared=(row:TeamPanelShared)=>{openCrewAccess(projectId,'','team','agents');window.setTimeout(()=>document.getElementById(`team-agent-card-${row.key}`)?.scrollIntoView({block:'center'}),300)}
 const artifacts=online?.scope===scope?online.items:[]
 const openArtifact=(id:string)=>{
  if(useAuthStore.getState().user?.id!==user || useWorkspaceConnectionStore.getState().activeWorkspaceId!==server || online?.scope!==scope) return
  const item=online.items.find(row=>row.id===id)
  if(item) selectReportDocument(online.workspace,item.path,true)
 }
 return <TeamPanel artifacts={artifacts.map(item=>({id:item.id,title:item.title,kind:item.kind,addedBy:item.addedBy}))} onArtifact={openArtifact} shared={shared} onShared={openShared} scope={scope} agents={rows} posts={loaded?.scope===scope?loaded.posts:[]} error={onlineError || (loaded?.scope===scope?loaded.error:undefined)} onAsk={ask} onActivity={()=>openCrewAccess(projectId,'','team','activity')}/>
}

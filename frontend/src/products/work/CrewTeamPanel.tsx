import { TeamSetups, type TeamSetupList } from './TeamSetups'
import { selectReportDocument } from '../../components/workflow/reportDocuments'
import { useEffect, useRef, useState } from 'react'
import { TeamPanel, type TeamPanelAgent, type TeamPanelView, type TeamPanelShared } from './TeamPanel'
import { crewDashboardsTab, teamHomeRoutines, teamRequest, type CrewDashboardsTab, type TeamOnlineView, type TeamHomeRoutine, type TeamTabState } from './teamApi'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { useTeamActivity, TeamActivityContent } from './TeamActivity'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { openTeammateAgent } from './openTeammateAgent'
import { AgentAskThread } from './TeamAgentAsk'
import { teamAgentChatTab } from './AgentSharingRow'
import { openAgentTab } from '../../utils/openAgentTab'
import { openCrewAccess } from './MacSharedAgents'
import { useChatStore } from '../../stores/useChatStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'

export function CrewTeamPanel({ scope, projectId, team, panelView='live', onPanelView }: {scope:string; projectId:string; team:TeamTabState; panelView?:TeamPanelView; onPanelView:(view:TeamPanelView)=>void}) {
 const user = useAuthStore(state => state.user?.id)
 const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
 const target = team.target
 const enabled = !!target && !!team.view?.team.on && !team.error
 const activity=useTeamActivity(target,projectId,enabled)
 const [onlineError,setOnlineError]=useState('')
 const [online, setOnline] = useState<{scope:string;workspace:string;items:(CrewDashboardsTab['items'][number] & {addedBy?:string})[];routines:TeamHomeRoutine[]}>()
 const [setups,setSetups]=useState<{scope:string;list:TeamSetupList;version:number}>()
 const [setupError,setSetupError]=useState('')
 const refresh = useRef(()=>{})
 useEffect(()=>{
  let active=true, reading=false
  const identity=captureChatIdentity()
  const read=async()=>{
   if(!enabled || !target || reading) return
   reading=true
   // One list read per existing visible Team refresh; no per-row requests or timer.
   const setupRead=teamRequest<TeamSetupList>(target,'/checklists').then(list=>{
    if(active && isChatIdentityCurrent(identity)) {setSetups(old=>({scope,list,version:(old?.version||0)+1}));setSetupError('')}
   }).catch(()=>{if(active && isChatIdentityCurrent(identity)) {setSetups(undefined);setSetupError('Could not load Team setups. Try again.')}})
   try {
    const mapping=await teamRequest<TeamOnlineView>(target,'/online-items')
    if(!mapping.home_project_id) { if(active && isChatIdentityCurrent(identity)) {setOnline({scope,workspace:'',items:[],routines:[]});setOnlineError('')} return }
    const home=await crewDashboardsTab({...target,project:mapping.home_project_id},'all')
    const routines=await teamHomeRoutines(target,mapping.home_project_id,home.workspace)
    const accepted=new Map((mapping.items||[]).filter(item=>item.kind!=='automation').map(item=>[item.destination_item_id,item]))
    if(active && isChatIdentityCurrent(identity)) {setOnline({scope,workspace:home.workspace,items:home.items.map(item=>({...item,addedBy:accepted.get(item.id)?.added_by_name})),routines});setOnlineError('');}
   } catch { if(active && isChatIdentityCurrent(identity)) {setOnline(undefined);setOnlineError('Could not load items on the Team. Retrying.')} }
   finally {await setupRead;reading=false}
  }
  refresh.current=()=>{void read()};void read();window.addEventListener('agentworks:team-updated',read)
  return ()=>{active=false;refresh.current=()=>{};window.removeEventListener('agentworks:team-updated',read)}
 },[scope,enabled,target?.project,target?.connection])
 useLiveRefetch(()=>refresh.current(),{kinds:['crew_panel'],project:projectId,scopeKey:scope,enabled,fallbackMs:15000,safetyMs:60000,minIntervalMs:3000})
 if(team.error) return <p role="alert" className="px-1 text-[11px] text-muted-foreground">Could not load this Team. Try again.</p>
 if(!enabled || !team.view) return null
 const rows:TeamPanelAgent[]=(team.view.reachable||[]).map(row=>({...row,connection:target?.connection,id:row.agent_id,name:row.name,person:row.person||'Team',mine:false,team_agent:row.kind==='team_agent',online:row.online,place:row.place,activity:row.activity,last_message:row.last_message,last_message_at:row.last_message_at,typing:row.typing,cli:row.cli,engine:row.engine,model_id:row.model_id,reasoning_effort:row.reasoning_effort}))
 const homeAgent=team.view.reachable?.find(agent=>agent.kind==='team_agent')
 const ask=(row:TeamPanelAgent)=>{
  if(useAuthStore.getState().user?.id!==user || useWorkspaceConnectionStore.getState().activeWorkspaceId!==server) return
  // The Team agent answers over AgentBridge inside this panel: never switch the app's connection or open the online home.
  if (row.team_agent && homeAgent?.agent_id===row.id) { onPanelView('ask'); return }
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
 return <TeamPanel setupsToDo={setups?.scope===scope?setups.list.to_do:undefined} setups={<>{setupError?<p role="alert">{setupError}</p>:<TeamSetups key={scope} target={target!} projectId={projectId} list={setups?.scope===scope?setups.list:undefined} refreshKey={setups?.version||0}/>}</>} view={panelView} onNavigate={onPanelView} fullActivity={<TeamActivityContent target={target!} projectId={projectId} activity={activity}/>} routines={online?.scope===scope?online.routines:[]} artifacts={artifacts.map(item=>({id:item.id,title:item.title,kind:item.kind,addedBy:item.addedBy}))} onArtifact={openArtifact} shared={shared} onShared={openShared} scope={scope} agents={rows} events={activity.view?.feed||[]} error={onlineError||activity.error} onAsk={ask} teamAgentAsk={<AgentAskThread target={target} address={homeAgent?.address} label={`Ask the ${homeAgent?.name||'Team agent'}`} sender={homeAgent?.name||'Team agent'} scope={`${scope}:${target?.connection}:${target?.project}:${homeAgent?.address}`} enabled={!!homeAgent}><p className="mt-1 text-xs text-muted-foreground">Ask through AgentBridge. Replies stay here.</p></AgentAskThread>}/>
}

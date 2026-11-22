import { openTerminalConversation } from './terminalConversationSelection'
import { selectReportDocument } from '../../components/workflow/reportDocuments'
import { useEffect, useRef, useState } from 'react'
import { TeamFilesRow } from './TeamFilesRow'
import { TeamPanel, type TeamPanelAgent, type TeamPanelView, type TeamPanelShared } from './TeamPanel'
import { crewDashboardsTab, teamHomeRoutines, teamRequest, type CrewDashboardsTab, type TeamOnlineView, type TeamHomeRoutine, type TeamTabState } from './teamApi'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { openTeammateAgent } from './openTeammateAgent'
import { TeamAgentModelChip } from './TeamAgentModelChip'
import { TeammateAsk } from './TeamAgentAsk'
import { TeammateAgentIdentity } from './TeammateChatIdentity'
import { teamAgentChatTab } from './AgentSharingRow'
import { openAgentTab } from '../../utils/openAgentTab'
import { openCrewAccess } from './MacSharedAgents'
import { useChatStore } from '../../stores/useChatStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'

export function CrewTeamPanel({ scope, projectId, team, panelView='live', onPanelView, selectedPerson, onPersonSelect, collapsed=false, askAgent }: {askAgent?:string;collapsed?:boolean;selectedPerson?:string;onPersonSelect?:(id?:string)=>void;scope:string; projectId:string; team:TeamTabState; panelView?:TeamPanelView; onPanelView:(view:TeamPanelView,agent?:string)=>void}) {
 const user = useAuthStore(state => state.user?.id)
 const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
 const target = team.target
 const enabled = !!target && !!team.view?.team.on && !team.error
 const [onlineError,setOnlineError]=useState('')
 const [online, setOnline] = useState<{scope:string;workspace:string;items:(CrewDashboardsTab['items'][number] & {addedBy?:string})[];routines:TeamHomeRoutine[]}>()
 const refresh = useRef(()=>{})
 useEffect(()=>{
  let active=true, reading=false
  const identity=captureChatIdentity()
  const read=async()=>{
   if(!enabled || !target || reading) return
   reading=true
   try {
    const mapping=await teamRequest<TeamOnlineView>(target,'/online-items')
    if(!mapping.home_project_id) { if(active && isChatIdentityCurrent(identity)) {setOnline({scope,workspace:'',items:[],routines:[]});setOnlineError('')} return }
    const home=await crewDashboardsTab({...target,project:mapping.home_project_id},'all')
    const routines=await teamHomeRoutines(target,mapping.home_project_id,home.workspace)
    const accepted=new Map((mapping.items||[]).filter(item=>item.kind!=='automation').map(item=>[item.destination_item_id,item]))
    if(active && isChatIdentityCurrent(identity)) {setOnline({scope,workspace:home.workspace,items:home.items.map(item=>({...item,addedBy:accepted.get(item.id)?.added_by_name})),routines});setOnlineError('');}
   } catch { if(active && isChatIdentityCurrent(identity)) {setOnline(undefined);setOnlineError('Could not load items on the Team. Retrying.')} }
   finally {reading=false}
  }
  refresh.current=()=>{void read()};void read();window.addEventListener('agentworks:team-updated',read)
  return ()=>{active=false;refresh.current=()=>{};window.removeEventListener('agentworks:team-updated',read)}
 },[scope,enabled,target?.project,target?.connection])
 useLiveRefetch(()=>refresh.current(),{kinds:['crew_panel'],project:projectId,scopeKey:scope,enabled,fallbackMs:15000,safetyMs:60000,minIntervalMs:3000})
 if(collapsed)return null
 if(team.error) return <p role="alert" className="px-1 text-[11px] text-muted-foreground">Could not load this Team. Try again.</p>
 if(!enabled || !team.view) return null
 const rows:TeamPanelAgent[]=(team.view.reachable||[]).map(row=>({...row,connection:target?.connection,id:row.agent_id,name:row.name,person:row.person||'Team',mine:false,team_agent:row.kind==='team_agent',online:row.online,place:row.place,activity:row.activity,last_message:row.last_message,last_message_at:row.last_message_at,typing:row.typing,cli:row.cli||team.view!.agents.find(agent=>agent.id===row.agent_id)?.cli,engine:row.engine||team.view!.agents.find(agent=>agent.id===row.agent_id)?.engine,model_id:row.model_id,reasoning_effort:row.reasoning_effort}))
 // Own rows come only from the own-agent Team projection, and remain out of the default teammate list.
 for(const agent of team.view.agents.filter(row=>row.mine)) rows.push({...agent,id:agent.id||agent.key,person_id:agent.owner_id,person:agent.owner_name,mine:true,online:agent.activity==='running'||agent.activity==='waiting',activity:agent.activity,place:agent.place})
 const homeAgent=team.view.reachable?.find(agent=>agent.kind==='team_agent')
 const direct = !!askAgent
 const asked = direct ? team.view.reachable?.find(agent => agent.agent_id === askAgent) : homeAgent
 const askName = direct ? asked ? `${asked.person || 'Team'} · ${asked.name}` : 'Agent unavailable' : homeAgent?.name || 'Team agent'
 const clickLog = (id:string,outcome:string,reason?:string) => console.info('[TEAM_AGENT_CLICK]', {project:projectId,agent:id,outcome,...(reason ? {reason} : {})})
 const ask=(row:TeamPanelAgent)=>{
  if(useAuthStore.getState().user?.id!==user || useWorkspaceConnectionStore.getState().activeWorkspaceId!==server) {clickLog(row.id,'refused','scope_changed');return}
  const native = team.view!.agents.find(agent => agent.id === row.id && agent.kind === 'terminal')
  if (native && target) { openTerminalConversation(projectId, target, {id:native.id,name:native.name}, native.mine); return }
  if(row.mine) {
   const own=team.view!.agents.find(agent=>agent.mine&&(agent.id||agent.key)===row.id)
   const tab=own&&teamAgentChatTab(team,projectId,own.key)
   if(tab) {openAgentTab(tab.tabId);clickLog(row.id,'chat_opened');return}
  } else {
   const admitted=team.view!.reachable?.find(agent=>agent.agent_id===row.id)
   const agent=team.view!.agents.find(agent=>!agent.mine&&agent.id===admitted?.agent_id)
   // Hidden chat is not a transcript destination (native or app agent).
   // Keep the read-only view selected by admitted ID even when its compact row goes idle.
   // A hidden chat opens in the main chat like any agent (owner, 20 Nov: "ux should be same always"), with its notice and Ask.
   if(admitted?.address && target && (admitted.chat_share==='agent' || agent?.chat_share==='agent' || agent?.chat_private_line)) {
    if(admitted.kind==='team_agent') {onPanelView('ask',admitted.agent_id);clickLog(row.id,'private_view_opened');return}
    openTerminalConversation(projectId,target,{id:admitted.agent_id,name:admitted.name,address:admitted.address},false,'app');return
   }
   if(agent) {clickLog(row.id,'chat_opened');openTeammateAgent(projectId,agent.watch_key||agent.key,agent.owner_id);return}
  }
  clickLog(row.id,'refused','agent_unavailable')
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
 return <TeamPanel files={<TeamFilesRow projectId={projectId} target={target||undefined} sync={team.view.sync} enabled={!!team.view.team.folder_access}/>} selectedPerson={selectedPerson} onPersonSelect={onPersonSelect} people={team.view.people} askLabel={askName} view={panelView} onNavigate={onPanelView} routines={online?.scope===scope?online.routines:[]} artifacts={artifacts.map(item=>({id:item.id,title:item.title,kind:item.kind,addedBy:item.addedBy}))} onArtifact={openArtifact} shared={shared} onShared={openShared} scope={scope} agents={rows} error={onlineError} onAsk={ask} teamAgent={team.view.team_agent} teamAgentAsk={direct ? <>
   {asked&&<><TeammateAgentIdentity identity={{name:asked.name,person:asked.person||'Team',place:asked.place==='mac'?`${asked.person||'their'}'s Mac`:'the server',online:asked.online,working:asked.activity==='running',hidden:true}}/>
    {asked.kind==='team_agent'&&target&&<div className="px-4 py-1"><TeamAgentModelChip target={target} program={team.view.team_agent?.program} canChange={team.view.team_agent?.can_change_program}/></div>}
    <p className="min-h-0 flex-1 px-4 py-8 text-sm text-muted-foreground">This agent's chat isn't shared. You can still ask it below.</p>
    <TeammateAsk target={target||undefined} address={asked.address} name={asked.name} where={asked.place==='mac'?`${asked.person||'their'}'s Mac`:'the server'} online={asked.online} enabled={!!asked.address} scope={`${scope}:${asked.address||''}`}/></>}
   {!asked&&<p className="px-4 py-3 text-sm text-muted-foreground">This agent is no longer available.</p>}
  </> : null}/>
}

import { fillMainDraft } from '../../utils/agentPurposeDraft'
import { openTerminalConversation } from './terminalConversationSelection'
import { selectReportDocument } from '../../components/workflow/reportDocuments'
import { useEffect, useRef, useState } from 'react'
import { TeamPanel, type TeamPanelAgent, type TeamPanelView, type TeamPanelShared } from './TeamPanel'
import { crewDashboardsTab, teamHomeRoutines, teamRequest, type CrewDashboardsTab, type TeamOnlineView, type TeamHomeRoutine, type TeamTabState } from './teamApi'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { useTeamActivity, TeamActivityContent } from './TeamActivity'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { openTeammateAgent } from './openTeammateAgent'
import { AgentAskThread, AskMyAgentAction } from './TeamAgentAsk'
import { TeammateAgentIdentity } from './TeammateChatIdentity'
import { teamAgentChatTab } from './AgentSharingRow'
import { openAgentTab } from '../../utils/openAgentTab'
import { openCrewAccess } from './MacSharedAgents'
import { useChatStore } from '../../stores/useChatStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'

export function CrewTeamPanel({ scope, projectId, team, panelView='live', onPanelView, selectedPerson, onPersonSelect, collapsed=false, onNeedsCount }: {collapsed?:boolean;onNeedsCount?:(count:number)=>void;selectedPerson?:string;onPersonSelect?:(id?:string)=>void;scope:string; projectId:string; team:TeamTabState; panelView?:TeamPanelView; onPanelView:(view:TeamPanelView)=>void}) {
 const user = useAuthStore(state => state.user?.id)
 const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
 const target = team.target
 const enabled = !!target && !!team.view?.team.on && !team.error
 const activity=useTeamActivity(target,projectId,enabled)
 const needsCount=(activity.view?.needs||[]).filter(need=>need.for_me).length
 useEffect(()=>{onNeedsCount?.(enabled?needsCount:0)},[onNeedsCount,enabled,needsCount])
 const [activityEvent,setActivityEvent]=useState<{scope:string;id:string}>()
 const [teammateView,setTeammateView]=useState<{scope:string;id:string}>()
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
 const direct = teammateView?.scope === scope
 const asked = direct ? team.view.reachable?.find(agent => agent.agent_id === teammateView.id) : homeAgent
 const askName = direct ? asked ? `${asked.person || 'Team'} · ${asked.name}` : 'Agent unavailable' : homeAgent?.name || 'Team agent'
 const clickLog = (id:string,outcome:string,reason?:string) => console.info('[TEAM_AGENT_CLICK]', {project:projectId,agent:id,outcome,...(reason ? {reason} : {})})
 const ask=(row:TeamPanelAgent)=>{
  if(useAuthStore.getState().user?.id!==user || useWorkspaceConnectionStore.getState().activeWorkspaceId!==server) {clickLog(row.id,'refused','scope_changed');return}
  // The Team agent answers over AgentBridge inside this panel: never switch the app's connection or open the online home.
  if (row.team_agent && homeAgent?.agent_id===row.id) { setTeammateView(undefined); onPanelView('ask'); clickLog(row.id,'ask_opened'); return }
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
   if(admitted?.address && (admitted.chat_share==='agent' || agent?.chat_share==='agent' || agent?.chat_private_line)) {
    setTeammateView({scope,id:admitted.agent_id});onPanelView('ask');clickLog(row.id,'private_view_opened');return
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
 const selected=team.view.people?.find(person=>person.user_id===selectedPerson)
 const peers=team.view.reachable?.filter(agent=>agent.person_id===selectedPerson&&agent.address)||[]
 const personAction=selected&&peers.length>0?<button type="button" className="block w-full min-w-0 truncate px-1 py-2 text-left text-xs text-primary" title={`Ask my agent to ask ${selected.name}'s agents`} onClick={()=>{
  if(useAuthStore.getState().user?.id!==user||useWorkspaceConnectionStore.getState().activeWorkspaceId!==server)return
  const prompt=`Ask ${selected.name}'s agents: \n\nAvailable agents: ${peers.map(agent=>`${agent.name} (${agent.address})`).join(', ')}. Use AgentBridge ask through the current admitted address; do not send directly from this view.`
  if(!fillMainDraft(projectId,prompt))useChatStore.getState().addToast('Open your own agent in this Crew first.','info')
 }}>✦ Ask my agent to ask {selected.name}'s agents…</button>:null
 return <TeamPanel unseen={activity.view?.unseen} today={activity.view?.today} onActivitySeen={activity.markSeen} onActivityEvent={event=>{setActivityEvent({scope,id:event.bridge?.can_open?event.id:''});onPanelView('activity')}} selectedPerson={selectedPerson} onPersonSelect={onPersonSelect} people={team.view.people} personAction={personAction} askLabel={askName} view={panelView} onNavigate={onPanelView} fullActivity={<TeamActivityContent target={target!} projectId={projectId} activity={activity} initialOpen={activityEvent?.scope===scope?activityEvent.id:undefined}/>} routines={online?.scope===scope?online.routines:[]} artifacts={artifacts.map(item=>({id:item.id,title:item.title,kind:item.kind,addedBy:item.addedBy}))} onArtifact={openArtifact} shared={shared} onShared={openShared} scope={scope} agents={rows} events={activity.view?.feed||[]} needs={activity.view?.needs||[]} onAnswer={activity.answer} error={onlineError||activity.error} onAsk={ask} teamAgentAsk={direct ? <>
   {asked&&<><TeammateAgentIdentity identity={{name:asked.name,person:asked.person||'Team',place:asked.place==='mac'?'their Mac':'server',online:asked.online,working:asked.activity==='running'}}/>
    <TeammateAgentIdentity banner identity={{name:asked.name,person:asked.person||'Team',place:asked.place==='mac'?'their Mac':'server'}}/>
    <AskMyAgentAction project={projectId} person={asked.person||'Team'} name={asked.name} address={asked.address} enabled={!!asked}/>
    <p className="px-4 py-8 text-sm text-muted-foreground">This agent's chat isn't shared</p></>}
   {!asked&&<p className="px-4 py-3 text-sm text-muted-foreground">This agent is no longer available.</p>}
   {!asked&&<AskMyAgentAction project={projectId} person="Team" name="agent" enabled={false}/>}
  </> : <AgentAskThread target={target} address={asked?.address} label={`Ask ${direct ? askName : `the ${askName}`}`} sender={askName} scope={`${scope}:${target?.connection}:${target?.project}:${teammateView?.scope === scope ? teammateView.id : 'team'}:${asked?.address}`} enabled={!!asked}><p className="mt-1 text-xs text-muted-foreground">{asked ? 'Ask through AgentBridge. Replies stay here.' : 'This agent is no longer available.'}</p></AgentAskThread>}/>
}

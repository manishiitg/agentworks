// @vitest-environment happy-dom
import { expect, it, vi } from 'vitest'
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { CrewTeamPanel } from './CrewTeamPanel'
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
const f=vi.hoisted(()=>({navigate:vi.fn(),desktop:vi.fn(),view:vi.fn(),teammate:vi.fn()}))
vi.mock('../../utils/appNavigation',()=>({openAppProduct:(...args:unknown[])=>f.navigate(...args)}))
vi.mock('../../stores/useAuthStore',()=>({useAuthStore:Object.assign((select:any)=>select({user:{id:'member'}}),{getState:()=>({user:{id:'member'}})})}))
vi.mock('../../stores/useWorkspaceConnectionStore',()=>({useWorkspaceConnectionStore:Object.assign((select:any)=>select({activeWorkspaceId:'mac'}),{getState:()=>({activeWorkspaceId:'mac'})})}))
vi.mock('../../components/workflow/reportDocuments',()=>({selectReportDocument:vi.fn()}))
vi.mock('./AgentSharingRow',()=>({teamAgentChatTab:vi.fn()}))
vi.mock('../../utils/openAgentTab',()=>({openAgentTab:vi.fn()}))
vi.mock('./MacSharedAgents',()=>({openCrewAccess:vi.fn()}))
vi.mock('./openTeammateAgent',()=>({openTeammateAgent:(...args:unknown[])=>f.teammate(...args)}))
vi.mock('../../stores/useChatStore',()=>({useChatStore:Object.assign((select:any)=>select({chatTabs:{}}),{getState:()=>({chatTabs:{},addToast:vi.fn()})})}))
vi.mock('../../utils/agentPurposeDraft',()=>({fillAskFromMyAgentDraft:vi.fn()}))
vi.mock('../../components/chat/chatTabName',()=>({ownerShortName:(v:string)=>v}))
vi.mock('../../services/api',()=>({default:{post:vi.fn()},getAuthToken:()=>'',getDesktopApiBaseUrl:()=>''}))
vi.mock('../../utils/chatIdentity',()=>({captureChatIdentity:()=>1,isChatIdentityCurrent:()=>true}))
vi.mock('../../hooks/useLiveRefetch',()=>({useLiveRefetch:()=>{}}))
vi.mock('./TeamActivity',()=>({useTeamActivity:()=>({}),TeamActivityContent:()=>null}))
vi.mock('./teamApi',()=>({teamRequest:()=>new Promise(()=>{}),crewDashboardsTab:vi.fn(),teamHomeRoutines:vi.fn(),useTeamTab:()=>({}),teamAgentFor:vi.fn()}))
vi.mock('./TeamPanel',()=>({TeamPanel:({agents,onAsk}:any)=><>{agents.map((row:any)=><button key={row.id} onClick={()=>onAsk(row)}>{row.name}</button>)}</>}))
vi.mock('./TeamSetups',()=>({TeamSetups:()=>null}))
// Regression (PLAT-746): clicking the Team agent switched the whole app to the server connection and its online home Crew.
it('opens the Team agent as a teammate tab in the current Crew without changing the connection or Crew',async()=>{
 Object.assign(window,{electronAPI:{openDesktopConnection:f.desktop}})
 const team:any={target:{project:'crew',connection:'server'},view:{team:{on:true,online_home:'home'},agents:[],reachable:[{agent_id:'team-agent',kind:'team_agent',name:'Team agent',address:'agent:team/home',watch_key:'home:chat:owner-1',place:'server',online:true}]}}
 const host=document.createElement('div');const root=createRoot(host)
 await act(async()=>root.render(<CrewTeamPanel scope="s" projectId="crew" team={team} onPanelView={f.view}/>))
 await act(async()=>host.querySelector('button')!.click())
 expect(f.teammate).toHaveBeenCalledWith('crew','home:chat:owner-1')
 expect(f.navigate).not.toHaveBeenCalled();expect(f.desktop).not.toHaveBeenCalled();expect(f.view).not.toHaveBeenCalled()
})

// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { CrewTeamPanel } from './CrewTeamPanel'
import { openTerminalConversation } from './terminalConversationSelection'
import { openTeammateAgent } from './openTeammateAgent'
import { openTeamAgentChat } from './openTeamAgentChat'
import { fillAskFromMyAgentDraft, fillMainDraft } from '../../utils/agentPurposeDraft'
afterEach(() => { vi.clearAllMocks(); vi.restoreAllMocks() })
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
const f=vi.hoisted(()=>({navigate:vi.fn(),desktop:vi.fn(),view:vi.fn()}))
vi.mock('../../utils/appNavigation',()=>({openAppProduct:(...args:unknown[])=>f.navigate(...args)}))
vi.mock('../../stores/useAuthStore',()=>({useAuthStore:Object.assign((select:any)=>select({user:{id:'member'}}),{getState:()=>({user:{id:'member'}})})}))
vi.mock('../../stores/useWorkspaceConnectionStore',()=>({useWorkspaceConnectionStore:Object.assign((select:any)=>select({activeWorkspaceId:'mac'}),{getState:()=>({activeWorkspaceId:'mac'})})}))
vi.mock('../../components/workflow/reportDocuments',()=>({selectReportDocument:vi.fn()}))
vi.mock('./AgentSharingRow',()=>({teamAgentChatTab:vi.fn()}))
vi.mock('../../utils/openAgentTab',()=>({openAgentTab:vi.fn()}))
vi.mock('./MacSharedAgents',()=>({openCrewAccess:vi.fn()}))
vi.mock('./terminalConversationSelection',()=>({openTerminalConversation:vi.fn(),closeTerminalConversation:vi.fn()}))
vi.mock('./openTeammateAgent',()=>({openTeammateAgent:vi.fn()}))
vi.mock('./openTeamAgentChat',()=>({openTeamAgentChat:vi.fn()}))
vi.mock('../../stores/useChatStore',()=>({useChatStore:Object.assign((select:any)=>select({chatTabs:{}}),{getState:()=>({chatTabs:{},addToast:vi.fn()})})}))
vi.mock('../../utils/agentPurposeDraft',()=>({fillAskFromMyAgentDraft:vi.fn(),fillMainDraft:vi.fn(()=>true)}))
vi.mock('../../components/chat/chatTabName',()=>({ownerShortName:(v:string)=>v}))
vi.mock('../../services/api',()=>({default:{post:vi.fn()},getAuthToken:()=>'',getDesktopApiBaseUrl:()=>''}))
vi.mock('../../utils/chatIdentity',()=>({captureChatIdentity:()=>1,isChatIdentityCurrent:()=>true}))
vi.mock('../../hooks/useLiveRefetch',()=>({useLiveRefetch:()=>{}}))
vi.mock('./teamApi',()=>({teamRequest:()=>new Promise(()=>{}),crewDashboardsTab:vi.fn(),teamHomeRoutines:vi.fn(),useTeamTab:()=>({}),teamAgentFor:vi.fn()}))
vi.mock('./TeamPanel',()=>({TeamPanel:({agents,onAsk,view,teamAgentAsk,selectedPerson,personAction}:any)=><>{agents.map((row:any)=><button key={row.id} onClick={()=>onAsk(row)}>{row.name}</button>)}{selectedPerson&&personAction}{view==='ask'&&teamAgentAsk}</>}))
// Regression (PLAT-746): clicking the Team agent switched the whole app to the server connection and its online home
// Crew. #3084: it opens the person's own chat with the Team agent in the chat screen, through the Team's connection.
it('opens the Team agent chat inside the current Crew without changing the connection or Crew',async()=>{
 Object.assign(window,{electronAPI:{openDesktopConnection:f.desktop}})
 const team:any={target:{project:'crew',connection:'server'},view:{team:{on:true,online_home:'home'},agents:[],team_agent:{state:'ready',line:'Ready',how:[],program:'Claude · Opus',can_change_program:true},reachable:[{agent_id:'team-agent',kind:'team_agent',name:'Team agent',address:'agent:team/home',place:'server',online:true}]}}
 const host=document.createElement('div');const root=createRoot(host)
 await act(async()=>root.render(<CrewTeamPanel scope="s" projectId="crew" team={team} panelView="live" onPanelView={f.view}/>))
 await act(async()=>host.querySelector('button')!.click())
 expect(openTeamAgentChat).toHaveBeenCalledWith('crew',{project:'crew',connection:'server'},undefined,{program:'Claude · Opus',canChangeProgram:true})
 expect(f.view).not.toHaveBeenCalled();expect(f.navigate).not.toHaveBeenCalled();expect(f.desktop).not.toHaveBeenCalled()
 await act(async()=>root.unmount())
})


// PLAT-746: a hidden native Codex row must offer an own-agent draft, never try
// opening its refused journal. Going idle changes compact visibility, not Ask.
it('opens a private teammate view with only an own-agent action, retained across idle but disabled on revocation',async()=>{
 const log=vi.spyOn(console,'info').mockImplementation(()=>{})
 const native={agent_id:'native',kind:'agent',name:'Computer use',person:'Example teammate',person_id:'teammate',address:'agent:example/native',chat_share:'agent',engine:'codex',place:'mac',online:true,activity:'running'}
 const team:any={target:{project:'crew',connection:'server'},view:{team:{on:true},agents:[{id:'native',key:'native',mine:false,owner_id:'teammate',chat_share:'agent'}],reachable:[native]}}
 const host=document.createElement('div');const root=createRoot(host)
 const render=(view:any)=>act(async()=>root.render(<CrewTeamPanel scope="native-scope" projectId="crew" team={team} panelView={view} onPanelView={f.view}/>))
 try {
  await render('live')
  await act(async()=>host.querySelector('button')!.click())
  expect(f.view).toHaveBeenCalledWith('ask')
  expect(openTeammateAgent).not.toHaveBeenCalled()
  expect(log).toHaveBeenCalledWith('[TEAM_AGENT_CLICK]',{project:'crew',agent:'native',outcome:'private_view_opened'})
  await render('ask')
  expect(host.querySelector('textarea')).toBeNull()
  expect(host.textContent).toContain("This agent's chat isn't shared")
  const ask=()=>[...host.querySelectorAll('button')].find(button=>button.textContent==='✦ Open my agent with this')!
  await act(async()=>ask().click())
  expect(fillAskFromMyAgentDraft).toHaveBeenCalledWith('crew','Example teammate','Computer use','agent:example/native','check the release status.')
  native.activity=''
  await render('ask')
  expect(host.querySelector('textarea')).toBeNull();expect(ask().disabled).toBe(false)
  team.view.reachable=[]
  await render('ask')
  expect(host.textContent).toContain('This agent is no longer available.')
  expect(host.querySelector('textarea')).toBeNull();expect([...host.querySelectorAll('button')].find(button=>button.textContent==='✦ Open my agent with this')?.disabled).toBe(true)
 } finally {await act(async()=>root.unmount())}
})

it('drafts a person-view question only to the currently admitted address and drops it on removal',async()=>{
 const peer={agent_id:'peer',kind:'terminal',person_id:'person',person:'Colleague',name:'Reviewer',address:'agent:example/peer',online:false,place:'mac'}
 const team:any={target:{project:'crew'},view:{team:{on:true},people:[{user_id:'person',name:'Colleague'}],agents:[],reachable:[peer]}}
 const host=document.createElement('div'),root=createRoot(host)
 const render=()=>act(async()=>root.render(<CrewTeamPanel scope="current" projectId="crew" team={team} selectedPerson="person" onPanelView={f.view}/>))
 try {
  await render()
  expect(host.textContent).not.toContain('To talk to')
  await act(async()=>[...host.querySelectorAll('button')].find(button=>button.textContent==="✦ Ask my agent to ask Colleague's agents…")!.click())
  expect(fillMainDraft).toHaveBeenCalledWith('crew',expect.stringContaining('Reviewer (agent:example/peer)'))
  expect(host.querySelector('textarea')).toBeNull()
  team.view.reachable=[];await render()
  expect(host.textContent).not.toContain("Ask my agent to ask Colleague's agents")
 } finally {await act(async()=>root.unmount())}
})

// Backend registry kind, never the name/model, selects the native main-pane view.
it('opens a server-marked native peer in the main transcript even when its chat is private',async()=>{
 const native={id:'native',key:'native',kind:'terminal',name:'Review',mine:false,owner_id:'peer',chat_share:'agent'}
 const team:any={target:{project:'crew',connection:'server'},view:{team:{on:true},agents:[native],reachable:[{agent_id:'native',name:'Review',kind:'agent',address:'agent:peer/review',place:'mac',online:true}]}}
 const host=document.createElement('div'),root=createRoot(host)
 await act(async()=>root.render(<CrewTeamPanel scope="s" projectId="crew" team={team} onPanelView={f.view}/>))
 await act(async()=>host.querySelector('button')!.click())
 expect(openTerminalConversation).toHaveBeenCalledWith('crew',team.target,{id:'native',name:'Review'},false)
 expect(openTeammateAgent).not.toHaveBeenCalled();expect(f.view).not.toHaveBeenCalled()
 await act(async()=>root.unmount())

})


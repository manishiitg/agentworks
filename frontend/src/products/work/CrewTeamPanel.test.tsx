// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { CrewTeamPanel } from './CrewTeamPanel'
import { openTerminalConversation } from './terminalConversationSelection'
import { openTeammateAgent } from './openTeammateAgent'
afterEach(() => { vi.clearAllMocks(); vi.restoreAllMocks() })
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
const f=vi.hoisted(()=>({navigate:vi.fn(),desktop:vi.fn(),view:vi.fn()}))
vi.mock('../../utils/appNavigation',()=>({openAppProduct:(...args:unknown[])=>f.navigate(...args)}))
vi.mock('../../stores/useAuthStore',()=>({useAuthStore:Object.assign((select:any)=>select({user:{id:'member'}}),{getState:()=>({user:{id:'member'}})})}))
vi.mock('../../stores/useWorkspaceConnectionStore',()=>({getWorkspaceScopedStorageKey:(key:string)=>key,useWorkspaceConnectionStore:Object.assign((select:any)=>select({activeWorkspaceId:'mac'}),{getState:()=>({activeWorkspaceId:'mac'})})}))
vi.mock('./AgentSharingRow',()=>({teamAgentChatTab:vi.fn()}))
vi.mock('../../utils/openAgentTab',()=>({openAgentTab:vi.fn()}))
vi.mock('./MacSharedAgents',()=>({openCrewAccess:vi.fn()}))
vi.mock('./terminalConversationSelection',()=>({openTerminalConversation:vi.fn(),closeTerminalConversation:vi.fn()}))
vi.mock('./openTeammateAgent',()=>({openTeammateAgent:vi.fn()}))
vi.mock('../../stores/useChatStore',()=>({useChatStore:Object.assign((select:any)=>select({chatTabs:{}}),{getState:()=>({chatTabs:{},addToast:vi.fn()})})}))
vi.mock('../../utils/agentPurposeDraft',()=>({fillAskFromMyAgentDraft:vi.fn(),fillMainDraft:vi.fn(()=>true)}))
vi.mock('../../components/chat/chatTabName',()=>({ownerShortName:(v:string)=>v}))
vi.mock('../../services/api',()=>({default:{post:vi.fn()},getAuthToken:()=>'',getApiBaseUrl:()=>'',getDesktopApiBaseUrl:()=>''}))
vi.mock('./teamApi',()=>({useTeamTab:()=>({}),teamAgentFor:vi.fn()}))
vi.mock('./TeamPanel',()=>({TeamPanel:({agents,onAsk,view,teamAgentAsk,selectedPerson}:any)=><>{agents.map((row:any)=><button key={row.id} onClick={()=>onAsk(row)}>{row.name}</button>)}{view==='ask'&&teamAgentAsk}</>}))
// #3426: a teammate agent with a hidden chat opens in the main chat like any agent (its notice and Ask there), never
// its refused transcript and never a side view.
it('opens a hidden-chat teammate agent in the main chat',async()=>{
 const native={agent_id:'native',kind:'agent',name:'Computer use',person:'Example teammate',person_id:'teammate',address:'agent:example/native',chat_share:'agent',engine:'codex',place:'mac',online:true,activity:'running'}
 const team:any={target:{project:'crew',connection:'server'},view:{team:{on:true},agents:[{id:'native',key:'native',mine:false,owner_id:'teammate',chat_share:'agent'}],reachable:[native]}}
 const host=document.createElement('div');const root=createRoot(host)
 try {
  await act(async()=>root.render(<CrewTeamPanel scope="native-scope" projectId="crew" team={team} panelView="live" onPanelView={f.view}/>))
  await act(async()=>host.querySelector('button')!.click())
  expect(openTerminalConversation).toHaveBeenCalledWith('crew',team.target,{id:'native',name:'Computer use',address:'agent:example/native'},false,'app')
  expect(f.view).not.toHaveBeenCalledWith('ask','native')
  expect(openTeammateAgent).not.toHaveBeenCalled()
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


import { afterEach, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { openTeammateAgent } from './openTeammateAgent'
import { TeamAgentAsk } from './TeamAgentAsk'
const f=vi.hoisted(()=>({tabs:{} as Record<string,any>,open:vi.fn(),post:vi.fn(),team:{} as any,identity:1}))
vi.mock('../../stores/useChatStore',()=>({useChatStore:Object.assign((select:any)=>select({chatTabs:f.tabs}),{getState:()=>({chatTabs:f.tabs})})}))
vi.mock('../../components/chat/AgentCard',()=>({useCrewAgentsStore:{getState:()=>({projectId:'crew',closed:[]})}}))
vi.mock('../../utils/openAgentTab',()=>({openAgentTab:(id:string)=>f.open(id)}))
vi.mock('../../utils/chatIdentity',()=>({captureChatIdentity:()=>f.identity,isChatIdentityCurrent:(value:number)=>value===f.identity}))
vi.mock('../../services/api',()=>({default:{post:(...args:unknown[])=>f.post(...args)},getAuthToken:()=>'',getDesktopApiBaseUrl:()=>''}))
vi.mock('./teamApi',()=>({useTeamTab:()=>f.team,teamAgentFor:(rows:any[])=>rows?.find(row=>row.id==='peer')}))
afterEach(()=>{vi.clearAllMocks();f.tabs={};f.team={};f.identity=1})
it('opens the exact observed teammate tab rather than Main or a different Crew with the same key',()=>{
 f.tabs={wrong:{tabId:'wrong',metadata:{agentProfileProjectId:'other',agentProfileConversationKey:'native',agentProfileTabCanWrite:false}},main:{tabId:'main',metadata:{agentProfileProjectId:'crew',agentProfileConversationKey:'native',agentProfileTabCanWrite:true}},peer:{tabId:'peer-tab',metadata:{agentProfileProjectId:'crew',agentProfileConversationKey:'crew:team:native',agentProfileTabCanWrite:false,agentProfileTabOwnerID:'colleague'}}}
 openTeammateAgent('crew','native','colleague');expect(f.open).toHaveBeenCalledWith('peer-tab')
})
it('sends from the watched view through AgentBridge without querying the native session or opening Main',async()=>{
 f.tabs.watch={metadata:{agentProfileProjectId:'crew',agentProfileTabCanWrite:false,agentProfileConversationKey:'peer-native'}}
 f.team={target:{project:'crew'},view:{team:{on:true},agents:[{id:'peer',name:'Review',in_team:true}],reachable:[{agent_id:'peer',address:'workspace:work:crew/agent/peer'}]}}
 f.post.mockImplementation((_url:string,body:any)=>Promise.resolve({data:body.arguments.action==='send'?{inbox_id:'inbox',delivery:'pending'}:{messages:[],next_cursor:0}}))
 const mounted=render(<TeamAgentAsk tabId="watch"/>);fireEvent.change(screen.getByRole('textbox'),{target:{value:'Please review this'}});fireEvent.click(screen.getByRole('button',{name:'Send'}))
 await waitFor(()=>expect(f.post).toHaveBeenCalledWith('/api/external/v1/call',{name:'messages',arguments:expect.objectContaining({action:'send',target:'workspace:work:crew/agent/peer',message:'Please review this'})},{skipSessionContext:true}))
 expect(f.open).not.toHaveBeenCalled();expect(f.post.mock.calls[0][1]).not.toHaveProperty('session_id');mounted.unmount()
})

it('does not carry a draft into a different signed-in chat identity',()=>{
 f.tabs.watch={metadata:{agentProfileProjectId:'crew',agentProfileTabCanWrite:false,agentProfileConversationKey:'peer-native'}}
 f.team={target:{project:'crew'},view:{team:{on:true},agents:[{id:'peer',name:'Review',in_team:true}],reachable:[{agent_id:'peer',address:'workspace:work:crew/agent/peer'}]}}
 const mounted=render(<TeamAgentAsk tabId="watch"/>);fireEvent.change(screen.getByRole('textbox'),{target:{value:'Private unsent question'}})
 f.identity=2;mounted.rerender(<TeamAgentAsk tabId="watch"/>);expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe('');expect(f.post).not.toHaveBeenCalled();mounted.unmount()
})

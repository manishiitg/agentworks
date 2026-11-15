// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
const host=document.createElement('div');document.body.append(host)
const root=createRoot(host)
const render=async()=>act(async()=>root.render(<TeamAgentAsk tabId="watch"/>))
const textbox=()=>host.querySelector('textarea')!
const type=async(value:string)=>act(async()=>{Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value')!.set!.call(textbox(),value);textbox().dispatchEvent(new Event('input',{bubbles:true}))})
import { openTeammateAgent } from './openTeammateAgent'
import { TeammateChatIdentity } from './TeammateChatIdentity'
import { TeamAgentAsk } from './TeamAgentAsk'
import { useChatShareLines } from './ChatEmptyState'
const f=vi.hoisted(()=>({tabs:{} as Record<string,any>,open:vi.fn(),navigate:vi.fn(),ask:vi.fn(),post:vi.fn(),team:{} as any,identity:1}))
vi.mock('../../stores/useChatStore',()=>({useChatStore:Object.assign((select:any)=>select({chatTabs:f.tabs}),{getState:()=>({chatTabs:f.tabs})})}))
vi.mock('../../components/chat/AgentCard',()=>({useCrewAgentsStore:{getState:()=>({projectId:'crew',closed:[]})}}))
vi.mock('../../stores/useAuthStore',()=>({useAuthStore:(select:any)=>select({user:{id:'viewer'}})}))
vi.mock('../../utils/agentPurposeDraft',()=>({fillAskFromMyAgentDraft:(...args:unknown[])=>{f.ask(...args);return true}}))
vi.mock('../../utils/appNavigation',()=>({openAppProduct:(...args:unknown[])=>f.navigate(...args)}))
vi.mock('../../utils/openAgentTab',()=>({openAgentTab:(id:string)=>f.open(id)}))
vi.mock('../../utils/chatIdentity',()=>({captureChatIdentity:()=>f.identity,isChatIdentityCurrent:(value:number)=>value===f.identity}))
vi.mock('../../services/api',()=>({default:{post:(...args:unknown[])=>f.post(...args)},getAuthToken:()=>'',getDesktopApiBaseUrl:()=>''}))
vi.mock('./teamApi',()=>({useTeamTab:()=>f.team,teamAgentFor:(rows:any[])=>rows?.find(row=>row.id==='peer')}))
afterEach(async()=>{await act(async()=>root.render(null));vi.clearAllMocks();f.tabs={};f.team={};f.identity=1})
it('opens the exact observed teammate tab rather than Main or a different Crew with the same key',()=>{
 f.tabs={wrong:{tabId:'wrong',metadata:{agentProfileProjectId:'other',agentProfileConversationKey:'native',agentProfileTabCanWrite:false}},main:{tabId:'main',metadata:{agentProfileProjectId:'crew',agentProfileConversationKey:'native',agentProfileTabCanWrite:true}},peer:{tabId:'peer-tab',metadata:{agentProfileProjectId:'crew',agentProfileConversationKey:'crew:team:native',agentProfileTabCanWrite:false,agentProfileTabOwnerID:'colleague'}}}
 openTeammateAgent('crew','native','colleague');expect(f.open).toHaveBeenCalledWith('peer-tab')
})
it('sends from the watched view through AgentBridge without querying the native session or opening Main',async()=>{
 f.tabs.watch={metadata:{agentProfileProjectId:'crew',agentProfileTabCanWrite:false,agentProfileConversationKey:'peer-native'}}
 f.team={target:{project:'crew'},view:{team:{on:true},agents:[{id:'peer',name:'Review',in_team:true}],reachable:[{agent_id:'peer',address:'agent:colleague/review'}]}}
 f.post.mockImplementation((_url:string,body:any)=>Promise.resolve({data:body.arguments.action==='ask'?{inbox_id:'inbox',delivery:'pending'}:{messages:[],next_cursor:0}}))
 await render();await type('Please review this');await act(async()=>host.querySelector('form')!.dispatchEvent(new Event('submit',{bubbles:true,cancelable:true})))
 expect(f.post).toHaveBeenCalledWith('/api/external/v1/call',{name:'messages',arguments:expect.objectContaining({action:'ask',target:'agent:colleague/review',message:'Please review this'})},{skipSessionContext:true})
 expect(f.open).not.toHaveBeenCalled();expect(f.post.mock.calls[0][1]).not.toHaveProperty('session_id')
})

it('does not carry a draft into a different signed-in chat identity',async()=>{
 f.tabs.watch={metadata:{agentProfileProjectId:'crew',agentProfileTabCanWrite:false,agentProfileConversationKey:'peer-native'}}
 f.team={target:{project:'crew'},view:{team:{on:true},agents:[{id:'peer',name:'Review',in_team:true}],reachable:[{agent_id:'peer',address:'agent:colleague/review'}]}}
 await render();await type('Private unsent question');expect(textbox().value).toBe('Private unsent question')
 f.identity=2;await render();expect(textbox().value).toBe('');expect(f.post).not.toHaveBeenCalled()
})

// The side-chat API marks a person's own scheduled history can_write=false too.
it('does not apply a teammate-private gate or ask box to own schedule history',async()=>{
 f.tabs.watch={metadata:{agentProfileProjectId:'crew',agentProfileTabCanWrite:false,agentProfileTabOwnerID:'viewer',agentProfileConversationKey:'crew:schedule:job'}}
 f.team={view:{team:{on:false},agents:[]}}
 function Gate(){const line=useChatShareLines('watch');return <><span>{line.private||'own-history-visible'}</span><TeamAgentAsk tabId="watch"/></>}
 await act(async()=>root.render(<Gate/>));expect(host.textContent).toContain('own-history-visible');expect(host.querySelector('textarea')).toBeNull()
})

// Owner T17 screenshot: the header kept the last own agent while viewing a peer.
it('identifies the observed person and Mac above the transcript with one direct composer',async()=>{
 f.tabs.watch={name:'Peer agent',metadata:{agentProfileId:'work',agentProfileProjectId:'crew',agentProfileTabCanWrite:false,agentProfileTabOwnerID:'colleague',agentProfileTabOwnerName:'Colleague',agentProfileConversationKey:'peer-native'}}
 f.tabs.own={name:'My previous agent',metadata:{agentProfileProjectId:'crew',agentProfileTabCanWrite:true,agentProfileTabOwnerID:'viewer'}}
 f.team={target:{project:'crew'},view:{team:{on:true},agents:[{id:'peer',name:'Peer agent',owner_name:'Colleague',place:'mac',activity:'running',in_team:true}],reachable:[{agent_id:'peer',online:true,address:'agent:colleague/review'}]}}
 await act(async()=>root.render(<><TeammateChatIdentity tabId="watch"/><TeammateChatIdentity tabId="watch" banner/><TeamAgentAsk tabId="watch"/></>))
 const header=host.querySelector('[data-agent-header]')!
 expect(header.textContent).toContain('Peer agent');expect(header.textContent).toContain('Colleague');expect(header.textContent).toContain('their Mac');expect(header.textContent).toContain('Working');expect(header.textContent).not.toContain('My previous agent')
 expect(host.querySelector('[role="note"]')?.textContent).toContain('Colleague')
 expect(host.querySelectorAll('textarea')).toHaveLength(1);expect(textbox().placeholder).toContain("Colleague's Peer agent")
 expect(host.querySelector('details')?.open).toBe(false)
 await act(async()=>{host.querySelector('details')!.open=true;[...host.querySelectorAll('button')].find(button=>button.textContent==='Ask from my agent')!.click()})
 expect(f.ask).toHaveBeenCalledWith('crew','Colleague','Peer agent','agent:colleague/review')
 expect(host.querySelectorAll('textarea')).toHaveLength(1)
})

// Offline readiness is server-admitted metadata, never a second client routing decision.
it('explains copy-first offline asks but sends only the admitted original address',async()=>{
 f.tabs.watch={metadata:{agentProfileProjectId:'crew',agentProfileTabCanWrite:false,agentProfileConversationKey:'peer-native'}}
 const peer={agent_id:'peer',online:false,offline_copy_ready:true,address:'agent:colleague/review'}
 const home={agent_id:'home',kind:'team_agent',online:true,address:'agent:team/main'}
 f.team={target:{project:'crew'},view:{team:{on:true},agents:[{id:'peer',name:'Review',in_team:true}],reachable:[peer,home]}}
 f.post.mockImplementation((_url:string,body:any)=>Promise.resolve({data:body.arguments.action==='ask'?{inbox_id:'copy-ask',delivery:'pending'}:{messages:[],next_cursor:0}}))
 await render();expect(host.textContent).toContain('Its copy answers')
 expect(host.textContent).not.toContain('new ask goes to the Team agent')
 await type('Please review');await act(async()=>host.querySelector('form')!.dispatchEvent(new Event('submit',{bubbles:true,cancelable:true})))
 expect(f.post.mock.calls[0][1].arguments).toMatchObject({action:'ask',target:peer.address,message:'Please review'})
 expect(f.post.mock.calls[0][1].arguments).not.toHaveProperty('offline_copy_id')
 peer.offline_copy_ready=false
 await render();expect(host.textContent).toContain('new ask goes to the Team agent')
 f.team.view.reachable=[peer]
 await render();expect(host.textContent).toContain('no Team agent is available')
 peer.online=true
 await render();expect(host.textContent).not.toContain('This agent is offline')
})

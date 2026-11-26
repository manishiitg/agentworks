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
import { useCrewWorksElsewhere } from './CrewWorksElsewhere'
const f=vi.hoisted(()=>({tabs:{} as Record<string,any>,open:vi.fn(),navigate:vi.fn(),ask:vi.fn(),post:vi.fn(),toast:vi.fn(),refresh:vi.fn().mockResolvedValue(undefined),team:{} as any,identity:1}))
vi.mock('../../stores/useChatStore',()=>({useChatStore:Object.assign((select:any)=>select({chatTabs:f.tabs}),{getState:()=>({chatTabs:f.tabs,addToast:f.toast})})}))
vi.mock('../../components/chat/AgentCard',()=>({useCrewAgentsStore:{getState:()=>({projectId:'crew',closed:[]})}}))
vi.mock('../../stores/useAuthStore',()=>({useAuthStore:(select:any)=>select({user:{id:'viewer'}})}))
vi.mock('../../utils/agentPurposeDraft',()=>({fillMainDraft:()=>true,sendToOwnMain:()=>true,fillAskFromMyAgentDraft:(...args:unknown[])=>{f.ask(...args);return true}}))
vi.mock('../../utils/appNavigation',()=>({openAppProduct:(...args:unknown[])=>f.navigate(...args)}))
vi.mock('../../utils/openAgentTab',()=>({openAgentTab:(id:string)=>f.open(id)}))
vi.mock('../../utils/chatIdentity',()=>({captureChatIdentity:()=>f.identity,isChatIdentityCurrent:(value:number)=>value===f.identity}))
vi.mock('../../stores/useConnectedWorkStore',()=>({hasDesktopConnections:()=>true,useConnectedWorkStore:{getState:()=>({refresh:f.refresh})}}))
vi.mock('../../services/api',()=>({default:{post:(...args:unknown[])=>f.post(...args)},getAuthToken:()=>'',getApiBaseUrl:()=>'',getDesktopApiBaseUrl:()=>''}))
vi.mock('./teamApi',()=>({useTeamTab:()=>f.team,teamAgentFor:(rows:any[])=>rows?.find(row=>row.id==='peer')}))
afterEach(async()=>{await act(async()=>root.render(null));vi.clearAllMocks();f.tabs={};f.team={};f.identity=1;vi.unstubAllGlobals()})
it('opens the exact observed teammate tab rather than Main or a different Crew with the same key',()=>{
 f.tabs={wrong:{tabId:'wrong',metadata:{agentProfileProjectId:'other',agentProfileConversationKey:'native',agentProfileTabCanWrite:false}},main:{tabId:'main',metadata:{agentProfileProjectId:'crew',agentProfileConversationKey:'native',agentProfileTabCanWrite:true}},peer:{tabId:'peer-tab',metadata:{agentProfileProjectId:'crew',agentProfileConversationKey:'crew:team:native',agentProfileTabCanWrite:false,agentProfileTabOwnerID:'colleague'}}}
 openTeammateAgent('crew','native','colleague');expect(f.open).toHaveBeenCalledWith('peer-tab')
})
// An older side-chat answer marked a person's own schedule chat can_write=false; it still gets no teammate gate.
it('does not apply a teammate-private gate or ask box to own schedule history',async()=>{
 f.tabs.watch={metadata:{agentProfileProjectId:'crew',agentProfileTabCanWrite:false,agentProfileTabOwnerID:'viewer',agentProfileConversationKey:'crew:schedule:job'}}
 f.team={view:{team:{on:false},agents:[]}}
 function Gate(){const line=useChatShareLines('watch');return <><span>{line.private||'own-history-visible'}</span><TeamAgentAsk tabId="watch"/></>}
 await act(async()=>root.render(<Gate/>));expect(host.textContent).toContain('own-history-visible');expect(host.querySelector('textarea')).toBeNull()
})

// Owner T17 screenshot: the header kept the last own agent while viewing a peer.
// Owner, 20 Nov: one header line, no banner or Back row, and an Ask box that sends an AgentBridge ask from you.
it('identifies the observed person and Mac in one line and asks the agent directly from the bottom box',async()=>{
 f.tabs.watch={name:'Peer agent',metadata:{agentProfileId:'work',agentProfileProjectId:'crew',agentProfileTabCanWrite:false,agentProfileTabOwnerID:'colleague',agentProfileTabOwnerName:'Colleague',agentProfileConversationKey:'peer-native'}}
 f.tabs.own={tabId:'own',name:'My previous agent',lastAccessedAt:20,metadata:{agentProfileId:'work',agentProfileProjectId:'crew',agentProfileTabCanWrite:true,agentProfileTabOwnerID:'viewer'}}
 f.team={target:{project:'crew'},view:{team:{on:true},agents:[{id:'peer',name:'Peer agent',owner_name:'Colleague',place:'mac',activity:'running',in_team:true}],reachable:[{agent_id:'peer',online:true,address:'agent:colleague/review'}]}}
 await act(async()=>root.render(<><TeammateChatIdentity tabId="watch"/><TeamAgentAsk tabId="watch"/></>))
 const header=host.querySelector('[data-agent-header]')!
 expect(header.textContent).toContain('Peer agent');expect(header.textContent).toContain("Colleague's Mac");expect(header.textContent).toContain('Working');expect(header.textContent).not.toContain('My previous agent')
 expect(host.querySelector('[role="note"]')).toBeNull()
 // #2971: return to the exact previous own chat, not the peer or Main.
 f.tabs.main={tabId:'main',name:'Main',lastAccessedAt:10,metadata:{agentProfileId:'work',agentProfileProjectId:'crew',agentProfileIsMainChat:true,agentProfileTabOwnerID:'viewer'}}
 await act(async()=>host.querySelector<HTMLButtonElement>('[aria-label="Back to My previous agent"]')!.click())
 expect(f.open).toHaveBeenLastCalledWith('own')
 delete f.tabs.own;delete f.tabs.main.lastAccessedAt
 await act(async()=>root.render(<TeammateChatIdentity tabId="watch"/>))
 await act(async()=>host.querySelector<HTMLButtonElement>('[aria-label="Back to Main"]')!.click())
 expect(f.open).toHaveBeenLastCalledWith('main')
 // No own agent open here: Back opens the project and the server picks its default agent (#4046: Main is not special).
 delete f.tabs.main
 await act(async()=>root.render(<TeammateChatIdentity tabId="watch"/>))
 await act(async()=>host.querySelector<HTMLButtonElement>('[aria-label="Back"]')!.click())
 expect(f.navigate).toHaveBeenCalledWith({surface:'work',id:'crew'})
 // The ask goes to the admitted address as the viewer; the server decides access.
 f.post.mockResolvedValue({data:{inbox_id:'inbox-1',message_id:'m1',delivery:'delivered'}})
 await render();await type('Is the release ready?')
 await act(async()=>host.querySelector('form')!.dispatchEvent(new Event('submit',{bubbles:true,cancelable:true})))
 expect(f.post).toHaveBeenCalledWith('/api/external/v1/call',expect.objectContaining({name:'messages',arguments:expect.objectContaining({action:'ask',target:'agent:colleague/review',message:'Is the release ready?'})}),expect.anything())
 expect(host.textContent).toContain('Is the release ready?')
 // #3661: while your ask runs, Send is Stop, and Stop names only your own ask.
 f.post.mockResolvedValue({data:{stopped:true,line:'You stopped your ask.'}})
 await act(async()=>host.querySelector<HTMLButtonElement>('[aria-label="Stop your ask"]')!.click())
 expect(f.post).toHaveBeenLastCalledWith('/api/external/v1/call',expect.objectContaining({arguments:{action:'stop',inbox_id:'inbox-1',message_id:'m1'}}),expect.anything())
 expect(host.querySelector('[aria-label="Stop your ask"]')).toBeNull()
 // Quick answer (owner, 25 Nov mockup): one of your own agents answers from what this agent already said; it is never messaged.
 // It is the default once you have an agent for it (owner, 26 Nov: ask the offline copy, not the agent in real time).
 f.team.view.agents.push({id:'helper',name:'Helper',mine:true,in_team:true,address:'agent:viewer/helper'});f.team.view.reachable[0].chat_readable=true
 f.post.mockReset();f.post.mockResolvedValue({data:{inbox_id:'inbox-2',message_id:'m2',delivery:'delivered'}})
 await act(async()=>root.render(null));await render()
 expect(host.querySelector('[role="radio"][aria-checked="true"]')!.textContent).toBe('Quick answer')
 await type('What did it work on last?')
 await act(async()=>host.querySelector('form')!.dispatchEvent(new Event('submit',{bubbles:true,cancelable:true})))
 expect(f.post).toHaveBeenCalledWith('/api/external/v1/call',expect.objectContaining({arguments:expect.objectContaining({action:'ask',target:'agent:viewer/helper',message:expect.stringContaining('What did it work on last?')})}),expect.anything())
 expect(JSON.stringify(f.post.mock.calls)).not.toContain('"target":"agent:colleague/review"')
 f.team.view.reachable=[];await act(async()=>root.render(null));await render();expect(textbox().disabled).toBe(true)
})

// #2758: a teammate's observed transcript must not be replaced by the
// viewer's own Mac/web working-copy gate or its move button.
it('does not apply my working-copy move gate to a teammate I am watching',async()=>{
 f.tabs.watch={metadata:{agentProfileTabCanWrite:false,agentProfileTabOwnerID:'colleague'}}
 f.tabs.own={metadata:{agentProfileTabCanWrite:true,agentProfileTabOwnerID:'viewer'}}
 f.team={view:{team:{where:'mac'}},target:{project:'crew'}}
 function Gate(){const peer=useCrewWorksElsewhere('crew','watch');const own=useCrewWorksElsewhere('crew','own');return <span>{peer.elsewhere||'peer-history'} / {own.elsewhere||'own-history'}</span>}
 await act(async()=>root.render(<Gate/>))
 expect(host.textContent).toBe('peer-history / mac')
 f.team.view.team.where='web';f.team.target.connection='saved'
 await act(async()=>root.render(<Gate/>))
 expect(host.textContent).toBe('peer-history / web')
})

// #2975: a Hidden chat still offers Fork, but the server refusal cannot mutate
// its observed tab. Successful copies use the same local endpoint afterwards.
it('keeps Fork visible and shows the server Hidden refusal without touching the watched chat',async()=>{
 f.tabs.watch={name:'Peer agent',metadata:{agentProfileProjectId:'crew',agentProfileTabCanWrite:false,agentProfileTabOwnerID:'peer',agentProfileConversationKey:'peer-chat'}}
 f.team={target:{project:'remote-crew',connection:'saved',localProject:'crew'},view:{agents:[{id:'peer',name:'Peer agent',owner_name:'Colleague',place:'mac',chat_share:'agent'}]}}
 const request=vi.fn().mockResolvedValue({ok:false,json:async()=>({error:{message:"This chat is hidden. You can't fork it."}})})
 vi.stubGlobal('fetch',request)
 await act(async()=>root.render(<TeammateChatIdentity tabId="watch"/>))
 await act(async()=>host.querySelector<HTMLButtonElement>('[aria-label="Fork Peer agent to my Mac"]')!.click())
 expect(request).not.toHaveBeenCalled()
 const submit=()=>[...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find(button=>button.textContent==='Fork to my Mac')!
 await act(async()=>submit().click())
 expect(document.querySelector('[role="alert"]')?.textContent).toContain('chat is hidden')
 expect(request).toHaveBeenCalledWith('/api/crew-teams/crew/agent-fork-local?self=1&async=1',expect.objectContaining({method:'POST',body:JSON.stringify({agent_id:'peer',name:'Peer agent'})}))
 expect(f.tabs.watch.metadata.agentProfileConversationKey).toBe('peer-chat');expect(f.open).not.toHaveBeenCalled()
 request.mockResolvedValue({ok:true,json:async()=>({id:'job-1',stage:'done',percent:100,result:{accepted:true,snapshot_at:'2026-11-17T05:00:00Z',project_id:'crew',conversation_key:'crew:chat:copy'}})})
 await act(async()=>submit().click())
 expect(document.querySelector('[role="dialog"]')?.textContent).toContain('Copied from the version saved')
 expect(f.refresh).toHaveBeenCalledWith(true)
 await act(async()=>[...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find(button=>button.textContent==='Done')!.click())
})

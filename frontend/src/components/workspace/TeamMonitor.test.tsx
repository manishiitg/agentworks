// @vitest-environment happy-dom
import React,{act} from 'react'
import {createRoot} from 'react-dom/client'
import {expect,it,vi} from 'vitest'
import {TeamMonitor} from './TeamMonitor'
const f=vi.hoisted(()=>({list:vi.fn(),request:vi.fn(),draft:vi.fn(()=>true)}))
vi.mock('../../services/api',()=>({default:{get:vi.fn()},getAuthToken:()=>'',getDesktopApiBaseUrl:()=>'',getLocalApiBaseUrl:()=>'',agentApi:{listWorkspaceNavigation:f.list}}))
vi.mock('../../stores/useAuthStore',()=>({useAuthStore:(select:(s:unknown)=>unknown)=>select({user:{id:'viewer',allowed_products:['work']}})}))
vi.mock('../../stores/useConnectedWorkStore',()=>({hasDesktopConnections:()=>false,useConnectedWorkPolling:()=>{},connectedLocations:()=>[],useConnectedWorkStore:()=>({connections:[]})}))
vi.mock('../../hooks/useLiveRefetch',()=>({useLiveRefetch:()=>{}}))
vi.mock('../../utils/chatIdentity',()=>({captureChatIdentity:()=>1,isChatIdentityCurrent:()=>true}))
vi.mock('../../stores/useChatStore',()=>({useChatStore:{getState:()=>({addToast:vi.fn()})}}))
vi.mock('../../stores/useWorkspaceConnectionStore',()=>({getRawActiveWorkspaceId:()=> 'local',useWorkspaceConnectionStore:(select:(s:unknown)=>unknown)=>select({activeWorkspaceId:'local'})}))
vi.mock('../../utils/desktopConnection',()=>({getRawActiveWorkspaceId:()=> 'local'}))
vi.mock('../../utils/agentPurposeDraft',()=>({fillMainDraft:f.draft}))
vi.mock('../GlobalActivityMonitor',()=>({GlobalActivityMonitor:()=>null}))
vi.mock('./SidebarServerGroup',()=>({openSidebarProject:vi.fn()}))
vi.mock('../../products/work/MacSharedAgents',()=>({openCrewAccess:vi.fn()}))
vi.mock('../../products/work/teamApi',()=>({teamRequest:f.request,teamError:(e:unknown)=>String(e)}))
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
it('groups server-admitted working agents by Team, pins personal needs, and expands one person without repeating identity',async()=>{
 const person={user_id:'peer',name:'Colleague',online:true,line:'Last seen just now'}
 const busy={id:'a',owner_id:'peer',tab:'a',chat_key:'crew:chat:a',name:'Builder (fork)',person:'Colleague',activity:'running',cli:'codex',last_message:'Peer live text'}
 f.list.mockResolvedValue({workspaces:[{id:'crew',name:'Example',profile_id:'work',team_live:{project_id:'crew',people:[person],agents:[busy],idle:[{...busy,id:'b',name:'Main',activity:''}],needs:[{id:'need',line:'Review request',for:'You',for_me:true,buttons:['Approve'],agent:'Helper',person:'Colleague',at:'2026-11-16T10:00:00Z'}],recent:{id:'event',kind:'decision',title:'Peer',verb:'decided',line:'External event',at:'2026-11-16T10:00:00Z'}}}]})
 const host=document.createElement('div'),root=createRoot(host);document.body.append(host)
 const button=(text:string)=>[...document.querySelectorAll<HTMLButtonElement>('button')].find(b=>b.textContent?.includes(text))!
 try{
  await act(async()=>{root.render(<TeamMonitor/>);await Promise.resolve()})
  expect(host.textContent).toContain('1 working');expect(host.textContent).toContain('1 needs you')
  await act(async()=>host.querySelector('button')!.click())
  const panel=()=>document.querySelector('[aria-label="Team monitor"]')!
  expect(panel().querySelector('[aria-label="Needs you"]')?.textContent).toContain('Review request')
  expect(panel().querySelector('[aria-label="Working now"]')?.textContent).toContain('Builder (fork)')
  expect(panel().querySelector('[aria-label="Working now"]')?.textContent).not.toContain('Main')
  expect(panel().querySelector('[aria-label="Activity"]')?.textContent).toContain('External event')
  expect(panel().textContent).not.toContain('localhost')
  await act(async()=>panel().querySelector<HTMLButtonElement>('[aria-label="Show Colleague\'s agents"]')!.click())
  expect(panel().querySelectorAll('[role="menuitem"]')).toHaveLength(2)
  expect(panel().querySelectorAll('[role="menuitem"] .font-medium')[0].textContent).toBe('Builder (fork)')
  expect(panel().querySelectorAll('[aria-label="Show Colleague\'s agents"]')).toHaveLength(1) // person header only
  await act(async()=>button('Ask my agent to ask').click());expect(f.draft).toHaveBeenCalledOnce()
 }finally{await act(async()=>root.unmount());host.remove()}
})

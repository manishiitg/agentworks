// @vitest-environment happy-dom
import React,{act} from 'react'
import {createRoot} from 'react-dom/client'
import {expect,it,vi} from 'vitest'
import {TeamMonitor} from './TeamMonitor'
const f=vi.hoisted(()=>({list:vi.fn(),request:vi.fn(),draft:vi.fn(()=>true),openTeam:vi.fn(),openProject:vi.fn()}))
vi.mock('../../services/api',()=>({default:{get:f.list},getAuthToken:()=>'',getDesktopApiBaseUrl:()=>'',getLocalApiBaseUrl:()=>'',agentApi:{listWorkspaceNavigation:vi.fn()}}))
vi.mock('../../stores/useAuthStore',()=>({useAuthStore:(select:(s:unknown)=>unknown)=>select({user:{id:'viewer',allowed_products:['work']}})}))
vi.mock('../../stores/useConnectedWorkStore',()=>({hasDesktopConnections:()=>false,useConnectedWorkPolling:()=>{},connectedLocations:()=>[],useConnectedWorkStore:()=>({connections:[]})}))
vi.mock('../../hooks/useLiveRefetch',()=>({useLiveRefetch:()=>{}}))
vi.mock('../../utils/chatIdentity',()=>({captureChatIdentity:()=>1,isChatIdentityCurrent:()=>true}))
vi.mock('../../stores/useChatStore',()=>({useChatStore:{getState:()=>({addToast:vi.fn()})}}))
vi.mock('../../stores/useWorkspaceConnectionStore',()=>({getRawActiveWorkspaceId:()=> 'local',useWorkspaceConnectionStore:(select:(s:unknown)=>unknown)=>select({activeWorkspaceId:'local'})}))
vi.mock('../../utils/desktopConnection',()=>({getRawActiveWorkspaceId:()=> 'local'}))
vi.mock('../../utils/agentPurposeDraft',()=>({fillMainDraft:f.draft}))
vi.mock('../GlobalActivityMonitor',()=>({GlobalActivityMonitor:()=>null}))
vi.mock('./SidebarServerGroup',()=>({openSidebarProject:f.openProject}))
vi.mock('../../products/work/MacSharedAgents',()=>({openCrewAccess:f.openTeam}))
vi.mock('../../products/work/teamApi',()=>({teamRequest:f.request,teamError:(e:unknown)=>String(e)}))
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
it('groups server-admitted working agents by Team, pins personal needs, and expands one person without repeating identity',async()=>{
 const person={user_id:'peer',name:'Colleague',online:true,line:'Last seen just now'}
 const busy={id:'a',owner_id:'peer',tab:'a',chat_key:'crew:chat:a',name:'Builder (fork)',purpose:'Review shared changes',examples:['Check a patch','Explain a failure'],person:'Colleague',activity:'running',cli:'codex',last_message:'Peer live text'}
 f.list.mockResolvedValue({data:{connections:[{connection:'local',teams:[{project_id:'crew',name:'Example',work:{working:1,label:'1 working',lines:['Builder (fork) · Peer live text · Colleague']},people:[{...person,user_id:'remote-viewer',name:'My own face',you:true},person],agents:[busy],idle:[{...busy,id:'own',owner_id:'remote-viewer',person:'My own face',you:true,activity:'idle'},{...busy,id:'b',name:'Main',activity:''}],needs:[{id:'need',line:'Review request',for:'You',for_me:true,buttons:['Approve'],agent:'Helper',person:'Colleague',at:'2026-11-16T10:00:00Z'}],recent:{id:'event',kind:'decision',title:'Peer',verb:'decided',line:'External event',at:'2026-11-16T10:00:00Z'}}]}]}})
 const host=document.createElement('div'),root=createRoot(host);document.body.append(host)
 const button=(text:string)=>[...document.querySelectorAll<HTMLButtonElement>('button')].find(b=>b.textContent?.includes(text))!
 try{
  await act(async()=>{root.render(<TeamMonitor/>);await Promise.resolve()})
  expect(host.textContent).toContain('1 working');expect(host.textContent).toContain('1 needs you')
  await act(async()=>host.querySelector('button')!.click())
  const panel=()=>document.querySelector('[aria-label="Team monitor"]')!
  expect(panel().querySelector('[aria-label="Needs you"]')?.textContent).toContain('Review request')
  expect(panel().querySelector('[aria-label="Working now"]')).toBeNull()
  expect(panel().querySelector('[aria-label="Your Teams"]')?.textContent).toContain('Builder (fork) · Peer live text · Colleague')
  expect(panel().querySelector('[aria-label="Show My own face\'s agents"]')).toBeNull()
  expect(panel().querySelector('[role="menuitem"]')).toBeNull()
  expect(panel().querySelector('[aria-label="Activity"]')?.textContent).toContain('External event')
  expect(panel().textContent).not.toContain('localhost')
  await act(async()=>[...panel().querySelectorAll<HTMLButtonElement>('[aria-label="Your Teams"] button')].find(row=>row.textContent==='Example')!.click())
  expect(f.openProject).toHaveBeenCalledWith('crew','crew')
  expect(f.openTeam).toHaveBeenLastCalledWith('crew','','team','agents')
  await act(async()=>host.querySelector('button')!.click())
  await act(async()=>panel().querySelector<HTMLButtonElement>('[aria-label="Activity"] button')!.click())
  expect(f.openTeam).toHaveBeenLastCalledWith('crew','','team','activity')
  await act(async()=>host.querySelector('button')!.click())
  await act(async()=>panel().querySelector<HTMLButtonElement>('[aria-label="Show Colleague\'s agents"]')!.click())
  expect(panel().querySelectorAll('[role="menuitem"]')).toHaveLength(2)
  const hover=panel().querySelector('[role="menuitem"]')?.getAttribute('title')
  expect(hover).toContain('Review shared changes');expect(hover).toContain('Check a patch');expect(hover).toContain('Explain a failure')
  expect(panel().querySelectorAll('[role="menuitem"] .font-medium')[0].textContent).toBe('Builder (fork)')
  expect(panel().querySelectorAll('[aria-label="Show Colleague\'s agents"]')).toHaveLength(1) // person header only
  await act(async()=>button('Ask my agent to ask').click());expect(f.draft).toHaveBeenCalledOnce()
 }finally{await act(async()=>root.unmount());host.remove()}
})

// #2938: all admitted Teams come from the monitor inventory, not own-project
// navigation. Quiet Teams stay discoverable but do not repeat zero-working text.
it('shows all Teams beyond three even without sidebar metadata and keeps quiet rows quiet',async()=>{
 const teams=Array.from({length:6},(_,i)=>({work:{working:0,lines:[],last_worked_at:`2026-11-17T0${i}:00:00Z`,last_worked_label:`last worked ${6-i}h ago`},project_id:`team-${i}`,name:`Team ${i}`,people:[],agents:[],idle:[],needs:[]}))
 f.list.mockResolvedValue({data:{notice:'2 servers unreachable',connections:[{connection:'saved-a',network:'Example North',network_id:'https://north.example.com',teams:teams.slice(0,3)},{connection:'saved-b',network:'Example South',network_id:'https://south.example.com',teams:teams.slice(3)},{connection:'offline-a',teams:[],error:'Teams could not be loaded. Try again.'},{connection:'offline-b',teams:[],error:'Teams could not be loaded. Try again.'}]}})
 const host=document.createElement('div'),root=createRoot(host);document.body.append(host)
 try{
  await act(async()=>{root.render(<TeamMonitor/>);await Promise.resolve()})
  expect(f.list).toHaveBeenLastCalledWith('/api/crew-team-monitor')
  await act(async()=>host.querySelector('button')!.click())
  const section=()=>document.querySelector('[aria-label="Your Teams"]')!
  expect(section().querySelectorAll('button')).toHaveLength(4) // three Teams and More
  expect(section().textContent).not.toContain('working')
  expect(document.querySelector('[aria-label="Team monitor"] [role="alert"]')).toBeNull()
  expect(document.querySelector('[aria-label="Team monitor"] [role="status"]')?.textContent).toBe('2 servers unreachable')
  expect(section().textContent).toContain('Example South')
  expect([...section().querySelectorAll('button')][0].textContent).toBe('Team 5')
  expect(section().textContent).toContain('last worked 1h ago')
  expect(section().textContent).toContain('+ 3 more Teams')
  await act(async()=>[...section().querySelectorAll<HTMLButtonElement>('button')].find(b=>b.textContent?.includes('more Teams'))!.click())
  expect(section().querySelectorAll('button')).toHaveLength(6)
 }finally{await act(async()=>root.unmount());host.remove()}
})

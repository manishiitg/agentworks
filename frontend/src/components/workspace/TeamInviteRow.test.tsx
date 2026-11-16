// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { TeamInviteLine, TeamInvites, TeamInviteProvider } from './TeamInviteRow'
import { useTeamInviteNotice } from './teamInviteNotice'
import { answerTeamInvite, type TeamInvite } from '../../products/work/teamApi'
import { desktopRequest } from '../../services/desktopConnections'
import { openSidebarProject } from './SidebarServerGroup'
vi.mock('../../services/desktopConnections',()=>({desktopRequest:vi.fn()}))
let listed:TeamInvite[]=[],mac=true,person='first'
vi.mock('../../products/work/teamApi',()=>({answerTeamInvite:vi.fn(),useTeamInvites:()=>listed}))
vi.mock('./SidebarServerGroup',()=>({openSidebarProject:vi.fn()}))
const ws=vi.hoisted(()=>({id:'local'}))
vi.mock('../../stores/useConnectedWorkStore',()=>({hasDesktopConnections:()=>mac}))
vi.mock('../../stores/useAuthStore',()=>({useAuthStore:(select:any)=>select({user:{id:person}})}))
vi.mock('../../stores/useWorkspaceConnectionStore',()=>({getRawActiveWorkspaceId:()=>ws.id,getWorkspaceScopedStorageKey:(key:string)=>key,useWorkspaceConnectionStore:(select:any)=>select({activeWorkspaceId:ws.id})}))
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
const invite=(id:string):TeamInvite=>({project_id:id,name:`Team ${id}`,owner_name:'Teammate',connection:'saved',can_add_to_mac:true,notice:{line:`Teammate invited you to Team ${id} · 3 people · 4 agents`,multiple:'{count} Team invites · {names}',joined:`Joined Team ${id} · its own project · available online`,join:'Join',not_now:'Not now',join_all:'Join all',review:'Review',open:'Open',connection_required:'Connect the server first.'}})
const page=(menu=false)=><TeamInviteProvider><main data-app-shell><TeamInviteLine/><input aria-label="Chat draft"/>{menu&&<TeamInvites/>}</main></TeamInviteProvider>
const click=async(label:string,scope:ParentNode=host)=>{const button=[...scope.querySelectorAll<HTMLButtonElement>('button')].find(b=>b.textContent===label);expect(button).toBeTruthy();await act(async()=>button!.click())}
afterEach(()=>{act(()=>root.render(null));listed=[];mac=true;person='first';ws.id='local';vi.clearAllMocks();vi.useRealTimers();useTeamInviteNotice.setState({count:0})})

// #2849: same line/actions on Mac and web, no modal/inert shell or folder/project picker.
it('joins into the automatic Mac project, confirms the actual folder and opens only on request',async()=>{
 listed=[invite('one')]
 vi.mocked(desktopRequest).mockResolvedValue({project_id:'local-copy',notice:'Joined Team one · its own project · files syncing to /example/Team one'} as never)
 await act(async()=>root.render(page()))
 expect(host.textContent).toContain(listed[0].notice!.line)
 expect(document.querySelector('[role="dialog"], select, input[type="radio"]')).toBeNull()
 expect(host.querySelector('main')!.inert).not.toBe(true)
 expect(host.querySelector<HTMLInputElement>('[aria-label="Chat draft"]')!.disabled).toBe(false)
 await click('Join')
 expect(desktopRequest).toHaveBeenCalledExactlyOnceWith('/saved/crew-teams/one/join','POST',{})
 expect(answerTeamInvite).not.toHaveBeenCalled()
 expect(host.querySelector('[role="status"]')?.textContent).toContain('files syncing to /example/Team one')
 expect(openSidebarProject).not.toHaveBeenCalled()
 await click('Open')
 expect(openSidebarProject).toHaveBeenCalledWith('crew','local-copy')
})
it('keeps Not now invites in the menu and badge, and resets notices across identity scopes',async()=>{
 listed=[invite('one')]
 await act(async()=>root.render(page()))
 await click('Not now')
 expect(host.querySelector('[aria-label^="Team invite:"]')).toBeNull()
 expect(useTeamInviteNotice.getState().count).toBe(1)
 await act(async()=>root.render(page(true)))
 expect(host.querySelector('[aria-label="Team invites"]')?.textContent).toContain('Team one')
 await act(async()=>root.render(page()))
 expect(host.textContent).not.toContain('invited you')
 person='second';await act(async()=>root.render(page()))
 expect(host.textContent).toContain('invited you')
 await click('Not now');ws.id='other';await act(async()=>root.render(page()))
 expect(host.textContent).toContain('invited you')
})
it('reviews several invites in place and joins each once on web without declining',async()=>{
 mac=false;listed=[invite('a'),invite('b')]
 await act(async()=>root.render(page()))
 expect(host.textContent).toContain('2 Team invites · Team a, Team b')
 expect(host.querySelector('[aria-label^="Team invite:"]')).toBeNull()
 await click('Review')
 expect(host.querySelectorAll('[aria-label^="Team invite:"]')).toHaveLength(2)
 await click('Join all')
 expect(answerTeamInvite).toHaveBeenCalledTimes(2)
 expect(answerTeamInvite).toHaveBeenNthCalledWith(1,listed[0],true)
 expect(answerTeamInvite).toHaveBeenNthCalledWith(2,listed[1],true)
 expect(host.querySelector('[role="status"]')?.textContent).toContain('available online')
 expect(desktopRequest).not.toHaveBeenCalled()
})
it('keeps a failed invite retryable and fades the successful receipt without hiding other invites',async()=>{
 vi.useFakeTimers();mac=false;listed=[invite('a')]
 vi.mocked(answerTeamInvite).mockRejectedValueOnce(new Error('Unavailable')).mockResolvedValue(undefined)
 await act(async()=>root.render(page()))
 await click('Join');expect(host.querySelector('[role="alert"]')?.textContent).toContain('Unavailable')
 await click('Join');expect(host.querySelector('[role="alert"]')).toBeNull()
 expect(host.querySelector('[role="status"]')).not.toBeNull()
 await act(async()=>vi.advanceTimersByTime(8000))
 expect(host.querySelector('[role="status"]')).toBeNull()
 listed=[invite('a'),invite('new')];await act(async()=>root.render(page()))
 expect(host.querySelector('[aria-label="Team invite: Team new"]')).not.toBeNull()
})

// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { it, expect, vi } from 'vitest'
const mocks=vi.hoisted(()=>({list:vi.fn(),project:vi.fn(),save:vi.fn(),open:vi.fn(),select:vi.fn(),schedules:vi.fn()}))
vi.mock('../work/workSessions',()=>({loadWorkSessions:mocks.list}))
vi.mock('../../stores/useAuthStore',()=>({useAuthStore:Object.assign((s:any)=>s({user:{id:'owner',allowed_products:['work']}}),{getState:()=>({user:{id:'owner',allowed_products:['work']}})})}))
vi.mock('../productSurfaceConfig',()=>({visibleProductSurfaceIDs:()=>['work']}))
vi.mock('../../stores/useProductSurfaceStore',()=>({useProductSurfaceStore:{getState:()=>({setSelectedWorkProjectId:mocks.select,setPendingWorkView:mocks.schedules})}}))
vi.mock('../../utils/productWorkspaceNavigation',()=>({openProductWorkspace:mocks.open}))
vi.mock('../../utils/quickNavigation',()=>({quickNavigationItems:()=>[],openQuickNavigation:vi.fn()}))
vi.mock('../../services/knowledgebaseApi',()=>({knowledgebaseApi:{project:mocks.project,setProjectAccess:mocks.save},knowledgebaseError:(e:Error)=>e.message}))
vi.mock('./KnowledgebaseSchedules',()=>({KnowledgebaseSchedules:()=>{mocks.schedules();return <p>Existing schedule</p>}}))
import { KnowledgebaseUseCrew } from './KnowledgebaseUseCrew'
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
it('does not grant access on selection and saves only explicit, revision-checked choices',async()=>{
 mocks.list.mockResolvedValue([{id:'crew',title:'My Crew',workspacePath:'Crew/mine'}]);mocks.project.mockResolvedValue({can_manage:true,manifest_version:'v1',brain_access:'off'});mocks.save.mockResolvedValue({manifest_version:'v2',brain_access:'read'})
 const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
 try{
  await act(async()=>root.render(<KnowledgebaseUseCrew onPreviousChat={()=>{}}/>))
  expect(mocks.save).not.toHaveBeenCalled();expect(mocks.schedules).not.toHaveBeenCalled()
  await act(async()=>{const s=host.querySelector('select')!;s.value='crew';s.dispatchEvent(new Event('change',{bubbles:true}))})
  expect(mocks.save).not.toHaveBeenCalled();expect(host.textContent).toContain('not limited to the folder currently open')
  await act(async()=>[...host.querySelectorAll('button')].find(b=>b.textContent==='Save Crew access')!.click())
  expect(mocks.save).toHaveBeenCalledWith(expect.objectContaining({workspace_path:'Crew/mine',mode:'read',expected_manifest_version:'v1'}))
  expect(mocks.open).not.toHaveBeenCalled()
 }finally{await act(async()=>root.unmount());host.remove()}
})

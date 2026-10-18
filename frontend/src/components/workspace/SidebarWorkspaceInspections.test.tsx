// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, expect, it, vi } from 'vitest'
import SidebarWorkspaceInspections from './SidebarWorkspaceInspections'
import { agentApi } from '../../services/api'
import { useChatStore, type ChatTab } from '../../stores/useChatStore'
import type { WorkspaceStepInspectionsResponse } from '../../services/api-types'
import { openWorkflowInspectionParent } from '../../utils/workflowSessionRestore'

const auth = vi.hoisted(() => ({user:{id:'alice',allowed_products:['agentworks'],allowed_workflow_ids:['goal']}}))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: Object.assign((select: (state: typeof auth) => unknown) => select(auth), {getState: () => auth}) }))
vi.mock('../../utils/workflowSessionRestore', () => ({openWorkflowInspectionParent:vi.fn()}))
const original = useChatStore.getState()
const receipt = {
 inspection:{session_id:'mcp-parent',execution_id:'prove',workspace_path:'Workflow/demo',step_id:'prove-contract',name:'Prove contract',started_at:'2026-10-18T00:00:00Z'},
 status:'completed',parent:{session_id:'mcp-parent',workspace_path:'Workflow/demo',status:'completed',triggered_by:'mcp'},
}
const data: WorkspaceStepInspectionsResponse={items:[receipt],next_cursor:null}
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
afterEach(()=>{useChatStore.setState(original,true);auth.user={id:'alice',allowed_products:['agentworks'],allowed_workflow_ids:['goal']};vi.restoreAllMocks()})
async function render(check:(host:HTMLDivElement,update:(scope?:string,selected?:boolean)=>Promise<void>)=>Promise<void>,selected=true) {
 useChatStore.setState({activeTabId:'main',chatTabs:{main:{tabId:'main',sessionId:'main-chat',name:'Chat',metadata:{mode:'workflow',phaseId:'workflow-builder',presetQueryId:'goal'}} as ChatTab}})
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
 const update=async(scope='local:alice',selected=true)=>{await act(async()=>root.render(<QueryClientProvider client={client}><SidebarWorkspaceInspections workspace="Workflow/demo" workflowId="goal" scope={scope} visible selected={selected} listedSessions={['main-chat']}/></QueryClientProvider>))}
 try {await update('local:alice',selected);await check(host,update)}finally{await act(async()=>root.unmount());client.clear();host.remove()}
}
it('discovers a step with no listed parent and opens its exact parent only on click',async()=>{
 const get=vi.spyOn(agentApi,'getWorkspaceStepInspections').mockResolvedValue(data)
 vi.mocked(openWorkflowInspectionParent).mockImplementation(async()=>{useChatStore.setState(state=>({activeTabId:'run',chatTabs:{...state.chatTabs,run:{tabId:'run',sessionId:'mcp-parent',name:'MCP run',metadata:{mode:'workflow',isViewOnly:true,presetQueryId:'goal'}} as ChatTab}}))})
 await render(async host=>{
  await vi.waitFor(()=>expect(host.querySelector('[aria-label="Inspect Prove contract"]')).not.toBeNull())
  expect(get).toHaveBeenCalledWith('Workflow/demo',{excludeSessionIds:['main-chat'],cursor:''})
  expect(host.textContent).toContain('MCP')
  expect(openWorkflowInspectionParent).not.toHaveBeenCalled()
  expect(Object.keys(useChatStore.getState().chatTabs)).toEqual(['main'])
  await act(async()=>host.querySelector<HTMLButtonElement>('[aria-label="Inspect Prove contract"]')!.click())
  expect(openWorkflowInspectionParent).toHaveBeenCalledWith(expect.objectContaining({session_id:'mcp-parent',workspace_path:'Workflow/demo',preset_query_id:'goal'}),expect.stringContaining('MCP'))
  expect(useChatStore.getState().chatTabs.main.sessionId).toBe('main-chat')
  expect(useChatStore.getState().chatTabs.run.metadata?.stepInspections?.items).toEqual([receipt.inspection])
 })
})
it('does not prefetch history for unrelated workspaces or after revocation',async()=>{
 const get=vi.spyOn(agentApi,'getWorkspaceStepInspections').mockResolvedValue(data)
 await render(async(host,update)=>{
  expect(get).not.toHaveBeenCalled()
  auth.user.allowed_workflow_ids=[]
  await update()
  expect(get).not.toHaveBeenCalled();expect(host.textContent).toBe('')
 },false)
})
it('discards old-account results and rejects mismatched workspace receipts',async()=>{
 let finish!:(value:WorkspaceStepInspectionsResponse)=>void
 vi.spyOn(agentApi,'getWorkspaceStepInspections').mockImplementationOnce(()=>new Promise(resolve=>{finish=resolve})).mockResolvedValue({items:[{...receipt,inspection:{...receipt.inspection,workspace_path:'Workflow/other'}}],next_cursor:null})
 await render(async(host,update)=>{
  auth.user.id='bob'
  await update('local:bob')
  await act(async()=>finish(data))
  expect(host.textContent).not.toContain('Prove contract')
 })
})
it('pages older receipts instead of silently dropping earlier runs',async()=>{
 const get=vi.spyOn(agentApi,'getWorkspaceStepInspections').mockResolvedValueOnce({items:[],next_cursor:'older'}).mockResolvedValue(data)
 await render(async host=>{
  await vi.waitFor(()=>expect(host.textContent).toContain('Earlier steps'))
  await act(async()=>host.querySelector<HTMLButtonElement>('button')!.click())
  await vi.waitFor(()=>expect(host.querySelector('[aria-label="Inspect Prove contract"]')).not.toBeNull())
  expect(get).toHaveBeenLastCalledWith('Workflow/demo',{excludeSessionIds:['main-chat'],cursor:'older'})
 })
})

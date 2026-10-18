// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, expect, it, vi } from 'vitest'
import SidebarStepInspections from './SidebarStepInspections'
import { agentApi } from '../../services/api'
import { useChatStore, type ChatTab } from '../../stores/useChatStore'
import type { ExecutionInspectionTarget, SessionExecutionTreeResponse } from '../../services/api-types'
import type { SidebarChat } from '../../utils/sidebarChats'
import { inspectionKey } from '../../utils/stepInspection'

const auth = vi.hoisted(() => ({user:{id:'alice',allowed_products:['agentworks'],allowed_workflow_ids:['goal']}}))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: Object.assign((select: (state: typeof auth) => unknown) => select(auth), {getState: () => auth}) }))
const original = useChatStore.getState()
const target: ExecutionInspectionTarget = {session_id:'parent', execution_id:'step', workspace_path:'Workflow/demo', name:'Research', step_id:'research', started_at:'2026-10-18T00:00:00Z'}
const tree = {session_id:'parent', root:{session_id:'parent',execution_id:'root',children:[{session_id:'parent',execution_id:'step',status:'running',inspection:target}]}} as SessionExecutionTreeResponse
const row: SidebarChat = {id:'parent',tabId:'parent-tab',title:'Daily briefing',surface:'agentworks',workflowId:'goal',workspace:'Workflow/demo',group:'Open',time:1,waiting:false}
Object.assign(globalThis, {IS_REACT_ACT_ENVIRONMENT:true})
afterEach(() => {useChatStore.setState(original,true);auth.user={id:'alice',allowed_products:['agentworks'],allowed_workflow_ids:['goal']};vi.restoreAllMocks()})
async function render(check: (host: HTMLDivElement, update: (scope:string,visible?:boolean) => Promise<void>) => Promise<void>, visible=true) {
  useChatStore.setState({activeTabId:'other',chatTabs:{'parent-tab':{tabId:'parent-tab',sessionId:'parent',name:'Daily briefing',metadata:{mode:'workflow'}} as ChatTab}})
  const client = new QueryClient({defaultOptions:{queries:{retry:false}}})
  const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
  const onOpen=async()=>{useChatStore.setState({activeTabId:'parent-tab'})}
  const update=async(scope:string,visible=true)=>{await act(async()=>root.render(<QueryClientProvider client={client}><SidebarStepInspections row={row} workspace="Workflow/demo" scope={scope} selected visible={visible} onOpen={onOpen}/></QueryClientProvider>))}
  try {await update('local:alice',visible);await check(host,update)} finally {await act(async()=>root.unmount());client.clear();host.remove()}
}
it('opens the exact step after selecting its parent and highlights the inspector',async()=>{
  vi.spyOn(agentApi,'getSessionExecutionTree').mockResolvedValue(tree)
  await render(async host=>{
    await vi.waitFor(()=>expect(host.querySelector('[aria-label="Inspect Research"]')).not.toBeNull())
    expect(host.querySelector('[aria-label="Step running"]')).not.toBeNull()
    await act(async()=>host.querySelector<HTMLButtonElement>('[aria-label="Inspect Research"]')!.click())
    expect(useChatStore.getState().activeTabId).toBe('parent-tab')
    expect(useChatStore.getState().chatTabs['parent-tab'].metadata?.stepInspections).toEqual({items:[target],activeId:inspectionKey(target)})
    expect(host.querySelector('[aria-current="page"]')?.getAttribute('aria-label')).toBe('Inspect Research')
  })
})
it('does not load work in a collapsed parent',async()=>{
  const api=vi.spyOn(agentApi,'getSessionExecutionTree').mockResolvedValue(tree)
  await render(async host=>{expect(api).not.toHaveBeenCalled();expect(host.textContent).toBe('')},false)
})
it('discards late data from the previous account even if session IDs coincide',async()=>{
  let finish!:(value:SessionExecutionTreeResponse)=>void
  vi.spyOn(agentApi,'getSessionExecutionTree').mockImplementationOnce(()=>new Promise(resolve=>{finish=resolve})).mockResolvedValue({...tree,root:{...tree.root,children:[]}})
  await render(async(host,update)=>{
    await update('local:bob')
    await act(async()=>{finish(tree)})
    expect(host.textContent).not.toContain('Research')
  })
})
it('rechecks current workflow permission before opening the inspector',async()=>{
  vi.spyOn(agentApi,'getSessionExecutionTree').mockResolvedValue(tree)
  await render(async host=>{
    await vi.waitFor(()=>expect(host.querySelector('[aria-label="Inspect Research"]')).not.toBeNull())
    auth.user={id:'alice',allowed_products:['agentworks'],allowed_workflow_ids:[]}
    await act(async()=>host.querySelector<HTMLButtonElement>('[aria-label="Inspect Research"]')!.click())
    expect(useChatStore.getState().chatTabs['parent-tab'].metadata?.stepInspections).toBeUndefined()
  })
})
it('does not display foreign executions or turn missing records into an inspector',async()=>{
  vi.spyOn(agentApi,'getSessionExecutionTree').mockResolvedValue({...tree,root:{...tree.root,children:[{...tree.root.children![0],inspection:{...target,session_id:'other'}}]}})
  await render(async host=>{await act(async()=>{await Promise.resolve()});expect(host.textContent).toBe('')})
})

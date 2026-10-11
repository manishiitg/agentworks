// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { RunInspectionButton } from './RunInspectionButton'
import { StepInspectionTabs } from './StepInspectionTabs'
import { useChatStore, type ChatTab } from '../../stores/useChatStore'
import { openStepInspection } from '../../utils/stepInspection'
import { inspectionConversation } from '../../utils/inspectionConversation'
import { agentApi } from '../../services/api'
import type { ExecutionInspectionTarget, ExecutionInspectionResponse, PollingEvent } from '../../services/api-types'

vi.mock('../workflow/ConversationViewer', () => ({ConversationViewer: ({content}: {content:string}) => <pre>{content}</pre>}))
vi.mock('../ui/MarkdownRenderer', () => ({MarkdownRenderer: ({content}: {content:string}) => <p>{content}</p>}))
const original = useChatStore.getState()
afterEach(() => {useChatStore.setState(original, true); vi.restoreAllMocks(); vi.unstubAllGlobals()})
const target: ExecutionInspectionTarget = {session_id:'parent', execution_id:'first', workspace_path:'Workflow/example', name:'Research', step_id:'research', run_folder:'iteration-1/default', started_at:'2026-10-11T00:00:00Z'}
const response = (id: string, text: string): ExecutionInspectionResponse => ({transcript:{session_id:'parent', agent_id:id, status:'completed', started_at:target.started_at, events:[{timestamp:target.started_at, type:'user_message', role:'user', text}]}, children:[]})

it('keeps the parent draft mounted, reuses exact tabs, restores targets and ignores an old request after closing', async () => {
 vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
 useChatStore.setState({activeTabId:'chat', chatTabs:{chat:{tabId:'chat', sessionId:'parent', metadata:{mode:'workflow'}} as ChatTab}})
 let finishFirst!: (data:ExecutionInspectionResponse) => void
 const api = vi.spyOn(agentApi,'inspectSessionExecution').mockImplementation(item => item.execution_id === 'first' ? new Promise(resolve => {finishFirst = resolve}) : Promise.resolve(response('second','Second dispatch')))
 const host = document.createElement('div'); document.body.append(host)
 let root = createRoot(host)
 try {
  const render = () => root.render(<StepInspectionTabs tabId="chat"><input defaultValue="Keep my draft" /></StepInspectionTabs>)
  await act(async () => {render()})
  const draft = host.querySelector('input')!
  await act(async () => {openStepInspection(target)})
  await vi.waitFor(async () => {await act(async () => {await new Promise(resolve => setTimeout(resolve, 0))}); expect(api).toHaveBeenCalledWith(target)})
  await act(async () => {openStepInspection(target)})
  expect(useChatStore.getState().getTab('chat')?.metadata?.stepInspections?.items).toHaveLength(1)
  await act(async () => {openStepInspection({...target,execution_id:'second'})})
  await vi.waitFor(async () => {await act(async () => {await new Promise(resolve => setTimeout(resolve, 0))}); expect(host.textContent).toContain('Second dispatch')})
  await act(async () => {finishFirst(response('first','Old response'))})
  expect(host.textContent).not.toContain('Old response')
  expect(host.querySelector('input')).toBe(draft)
  await act(async () => {host.querySelector<HTMLButtonElement>('[aria-label="Close inspection Research"]')!.click()})
  expect(api.mock.calls.map(call => call[0].execution_id)).toContain('second')
  // Serialize navigation as account-scoped chat persistence does, then remount the host.
  const persisted = JSON.parse(JSON.stringify(useChatStore.getState().chatTabs))
  await act(async () => {root.unmount()})
  useChatStore.setState({chatTabs:persisted})
  root = createRoot(host)
  await act(async () => {render()})
  expect(useChatStore.getState().getTab('chat')?.metadata?.stepInspections?.items).toHaveLength(1)
  await act(async () => {host.querySelector<HTMLButtonElement>('[role="tab"]')!.click()})
  expect(host.querySelector('input')?.value).toBe('Keep my draft')
  expect(openStepInspection({...target,session_id:'foreign-chat'})).toBe(false)
 } finally {await act(async () => root.unmount()); host.remove()}
})

it('renders the recorded system/user prompt and merges tool start/result without losing arguments', () => {
 const data = response('first','User instruction')
 data.transcript.events.unshift({timestamp:target.started_at,type:'system_message',role:'system',text:'System instruction'})
 data.transcript.events.push({timestamp:target.started_at,type:'tool_call',tool_call:{tool_call_id:'t',tool_name:'read_file',arguments:'{"path":"sources.txt"}',status:'running'}},{timestamp:target.started_at,type:'tool_call',tool_call:{tool_call_id:'t',tool_name:'read_file',result:'Facts',status:'completed'}})
 const content = inspectionConversation(data)
 expect(content).toContain('System instruction'); expect(content).toContain('User instruction'); expect(content).toContain('sources.txt'); expect(content).toContain('Facts')
 const tools = inspectionConversation(data,true)
 expect(tools).not.toContain('System instruction'); expect(tools).not.toContain('User instruction'); expect(tools).toContain('Facts')
})

it('opens the exact dispatch from the compact run row launch receipt', async () => {
 vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
 const event = (id: string, type: string, data: Record<string, unknown>): PollingEvent => ({id,type,timestamp:target.started_at,session_id:'parent',data:{type,data}} as PollingEvent)
 const start = event('launch-start','tool_call_start',{tool_call_id:'launch'})
 const events = [
  event('launch-end','tool_call_end',{tool_call_id:'launch',result:'Started step. execution_id: "second"'}),
  event('first-start','background_agent_started',{metadata:{inspection:target}}),
  event('second-start','background_agent_started',{metadata:{inspection:{...target,execution_id:'second'}}}),
 ]
 useChatStore.setState({activeTabId:'chat', chatTabs:{chat:{tabId:'chat',sessionId:'parent'} as ChatTab},tabEvents:{parent:events}})
 const host = document.createElement('div'); const root = createRoot(host)
 try {
  await act(async () => root.render(<RunInspectionButton event={start} />))
  await act(async () => host.querySelector<HTMLButtonElement>('button')!.click())
  expect(useChatStore.getState().getTab('chat')?.metadata?.stepInspections?.items.map(item => item.execution_id)).toEqual(['second'])
  await act(async () => useChatStore.setState({tabEvents:{parent:events.filter(event => event.type !== 'tool_call_end')}}))
  expect(host.querySelector('button')).toBeNull()
 } finally {await act(async () => root.unmount())}
})

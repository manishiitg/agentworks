// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { RunInspectionButton } from './RunInspectionButton'
import { StepInspectionTabs } from './StepInspectionTabs'
import StepInspectionPanel from './StepInspectionPanel'
import { useChatStore, type ChatTab } from '../../stores/useChatStore'
import { openStepInspection } from '../../utils/stepInspection'
import { inspectionChat, inspectionRecords } from '../../utils/inspectionConversation'
import { agentApi } from '../../services/api'
import type { ExecutionInspectionTarget, ExecutionInspectionResponse, PollingEvent } from '../../services/api-types'

vi.mock('../TerminalEventTranscript', () => ({TerminalEventTranscript: ({events}: {events:unknown}) => <pre>{JSON.stringify(events)}</pre>}))
vi.mock('../ui/MarkdownRenderer', () => ({MarkdownRenderer: ({content}: {content:string}) => <p>{content}</p>}))
const original = useChatStore.getState()
afterEach(() => {useChatStore.setState(original, true); vi.restoreAllMocks(); vi.unstubAllGlobals()})
const target: ExecutionInspectionTarget = {session_id:'parent', execution_id:'first', workspace_path:'Workflow/example', name:'Research', step_id:'research', run_folder:'iteration-1/default', started_at:'2026-10-11T00:00:00Z'}
const response = (id: string, text: string): ExecutionInspectionResponse => ({transcript:{session_id:'parent', agent_id:id, status:'completed', started_at:target.started_at, events:[{timestamp:target.started_at, type:'user_message', role:'user', text}]}, children:[]})

it('shows only the selected saved execution record without loading workflow history or retaining the previous task', async () => {
 vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
 const parent = response('first',''); parent.transcript.events=[]
 parent.execution_mode='agent'
 parent.children=[{agent_id:'task',name:'Task work',status:'completed'},{agent_id:'validation',name:'Final validation (prevalidation)',status:'completed'}]
 const task=response('task','Actual task instruction')
 task.transcript.events.push({...task.transcript.events[0]}) // Raw carriers stay available for debugging.
 let finishValidation!: (data:ExecutionInspectionResponse) => void
 vi.spyOn(agentApi,'inspectSessionExecution').mockImplementation(item => item.execution_id==='first' ? Promise.resolve(parent) : item.execution_id==='task' ? Promise.resolve(task) : new Promise(resolve=>{finishValidation=resolve}))
 const history=vi.spyOn(agentApi,'getExecutionLogs')
 const file=vi.spyOn(agentApi,'getLogFile')
 const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
 try {
  await act(async()=>{root.render(<StepInspectionPanel target={target} />)})
  await vi.waitFor(async()=>{await act(async()=>{await new Promise(resolve=>setTimeout(resolve,0))});expect(host.querySelector('[data-testid="inspection-saved-log"]')).not.toBeNull()})
  const saved=()=>host.querySelector('[data-testid="inspection-saved-log"] pre')
  const openSaved=async()=>{await act(async()=>{const disclosure=host.querySelector<HTMLDetailsElement>('[data-testid="inspection-saved-log"]')!;disclosure.open=true;disclosure.dispatchEvent(new Event('toggle'))})}
  expect(saved()).toBeNull()
  await openSaved()
  expect(JSON.parse(saved()!.textContent!).transcript).toEqual(task.transcript)
  expect(JSON.parse(saved()!.textContent!)).not.toHaveProperty('children')
  expect(host.textContent).not.toContain('Open saved logs')
  const select=host.querySelector<HTMLSelectElement>('select')!
  await act(async()=>{select.value='validation';select.dispatchEvent(new Event('change',{bubbles:true}))})
  expect(saved()).toBeNull()
  await act(async()=>{finishValidation(response('validation','Validation evidence'))})
  expect(saved()).toBeNull()
  expect(host.querySelector<HTMLDetailsElement>('[data-testid="inspection-saved-log"]')!.open).toBe(false)
  await openSaved()
  expect(JSON.parse(saved()!.textContent!).transcript.agent_id).toBe('validation')
  expect(saved()!.textContent).not.toContain('Actual task instruction')
  expect(history).not.toHaveBeenCalled();expect(file).not.toHaveBeenCalled()
 } finally {await act(async()=>root.unmount());host.remove()}
})

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
 const records = inspectionRecords(data)
 expect(records.system).toEqual(['System instruction']); expect(records.user).toEqual(['User instruction'])
 expect(records.tools[0].arguments).toContain('sources.txt'); expect(records.tools[0].result).toBe('Facts')
 const chat = inspectionChat(data)
 expect(chat.terminal.terminal_id).toBe('parent:first')
 expect(JSON.stringify(chat.events)).not.toContain('System instruction')
 expect(JSON.stringify(chat.events)).toContain('User instruction'); expect(JSON.stringify(chat.events)).toContain('sources.txt'); expect(JSON.stringify(chat.events)).toContain('Facts')
})

it('joins lifecycle/provider message carriers while retaining repeated instructions and raw evidence', () => {
 const data=response('first','Repeat this instruction')
 data.transcript.events[0].source_type='orchestrator_agent_start'
 data.transcript.events.push(
  {...data.transcript.events[0],source_type:'user_message',timestamp:'2026-10-11T00:00:00.012Z'},
  {timestamp:'2026-10-11T00:00:01Z',type:'tool_call',tool_call:{tool_call_id:'tool',tool_name:'read_file',status:'running'}},
  {timestamp:'2026-10-11T00:00:02Z',type:'assistant_message',role:'assistant',source_type:'llm_generation_end',text:'Finished'},
  {timestamp:'2026-10-11T00:00:02.006Z',type:'assistant_message',role:'assistant',source_type:'orchestrator_agent_end',text:'Finished'},
  {timestamp:'2026-10-11T00:00:03Z',type:'user_message',role:'user',source_type:'user_message',text:'Repeat this instruction'},
 )
 const original=JSON.stringify(data)
 const chat=inspectionChat(data)
 expect(chat.events.filter(e=>e.type==='user_message')).toHaveLength(2)
 expect(chat.events.filter(e=>e.type==='llm_generation_end')).toHaveLength(1)
 expect(JSON.stringify(data)).toBe(original)
 // Two provider messages are two instructions, even when identical/adjacent.
 data.transcript.events[0].source_type='user_message'
 expect(inspectionChat(data).events.filter(e=>e.type==='user_message')).toHaveLength(3)
 // Compatibility for the recorded 12 ms kickoff echo has no source field.
 delete data.transcript.events[0].source_type;delete data.transcript.events[1].source_type
 expect(inspectionChat(data).events.filter(e=>e.type==='user_message')).toHaveLength(2)
 data.transcript.events[1].timestamp='2026-10-11T00:00:00.500Z'
 expect(inspectionChat(data).events.filter(e=>e.type==='user_message')).toHaveLength(3)
})

it('opens the exact dispatch from the compact run row launch receipt', async () => {
 vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
 const event = (id: string, type: string, data: Record<string, unknown>): PollingEvent => ({id,type,timestamp:target.started_at,session_id:'parent',data:{type,data}} as PollingEvent)
 const start = event('launch-start','tool_call_start',{tool_call_id:'launch'})
 const events = [
  event('launch-end','tool_call_end',{tool_call_id:'launch',result:JSON.stringify({content:[{type:'text',text:'Started step. execution_id: "second"'}]})}),
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

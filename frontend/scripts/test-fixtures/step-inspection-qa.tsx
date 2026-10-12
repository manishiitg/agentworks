import { createRoot } from 'react-dom/client'
import { StepInspectionTabs } from '../../src/components/chat/StepInspectionTabs'
import { TerminalEventTranscript } from '../../src/components/TerminalEventTranscript'
import { useChatStore, type ChatTab } from '../../src/stores/useChatStore'
import { agentApi } from '../../src/services/api'
import { openStepInspection } from '../../src/utils/stepInspection'
import type { ExecutionInspectionTarget, ExecutionInspectionResponse, PollingEvent } from '../../src/services/api-types'
import '../../src/index.css'

document.documentElement.classList.toggle('dark', new URLSearchParams(location.search).get('theme') !== 'light')
const started = '2026-10-12T11:26:33Z'
const target: ExecutionInspectionTarget = {session_id:'qa-chat',execution_id:'step-run',workspace_path:'Workflow/example',name:'Write a status file',step_id:'write-status',run_folder:'iteration-0/default',started_at:started}
const root: ExecutionInspectionResponse = {execution_mode:'agent',transcript:{session_id:target.session_id,agent_id:target.execution_id,name:target.name,status:'completed',started_at:started,inspection:target,events:[]},result:'Step completed and validation passed.',children:[{agent_id:'work-task',name:'Agent item -> Write a status file / write-status (user_message)',status:'completed',started_at:started},{agent_id:'validation-task',name:'Agent item -> Write a status file / __automatic_final_validation__ (prevalidation)',status:'completed',started_at:started}]}
const task: ExecutionInspectionResponse = {execution_mode:'agent',transcript:{session_id:target.session_id,agent_id:'work-task',status:'completed',started_at:started,events:[{timestamp:started,type:'system_message',role:'system',text:'Write only inside the assigned output folder. Verify the requested JSON file before finishing.'},{timestamp:started,type:'user_message',role:'user',text:'Create status.json with status INITIALIZED and report the result.'},{timestamp:started,type:'tool_call',tool_call:{tool_call_id:'tool-1',tool_name:'write_file',arguments:'{"path":"status.json","status":"INITIALIZED"}',status:'running'}},{timestamp:started,type:'tool_call',tool_call:{tool_call_id:'tool-1',tool_name:'write_file',result:'status.json saved.',status:'completed'}},{timestamp:started,type:'assistant_message',role:'assistant',text:'Created status.json with status INITIALIZED.'}]},children:[],result:'Created status.json with status INITIALIZED.'}
// The real runtime emits kickoff/provider and completion/provider carriers.
task.transcript.events[1].source_type='orchestrator_agent_start'
task.transcript.events.splice(2,0,{...task.transcript.events[1],source_type:'user_message'})
task.transcript.events.at(-1)!.source_type='llm_generation_end'
task.transcript.events.push({...task.transcript.events.at(-1)!,source_type:'orchestrator_agent_end'})
if (new URLSearchParams(location.search).get('legacy')==='1') task.transcript.events.forEach(event=>{delete event.source_type})
const script: ExecutionInspectionResponse = {execution_mode:'script',transcript:{session_id:target.session_id,agent_id:'script-run',name:'Write status with Python',status:'completed',started_at:started,events:[]},children:[],scripts:[{started_at:started,path:'code/status/main.py',source:'import json\nprint(json.dumps({"status":"INITIALIZED"}))\n',stdout:'{"status":"INITIALIZED"}',stderr:'',exit_code:0,status:'completed'}],result:'Created status.json and validation passed.'}
agentApi.inspectSessionExecution = async selected => selected.execution_id === 'script-run' ? script : selected.execution_id === target.execution_id ? root : selected.execution_id === 'work-task' ? task : {execution_mode:'validation',transcript:{session_id:target.session_id,agent_id:'validation-task',status:'completed',started_at:started,events:[]},children:[],result:'Validation passed.'}
const params = new URLSearchParams(location.search)
const nativeLaunch = params.get('mode')==='launch'
const parentID = params.get('parent')==='steer' ? 'synthetic-turn:steer-message-1791824440777049000' : 'query_1791824440777049000'
const launchEvents: PollingEvent[] = nativeLaunch ? [{id:'native-launch',type:'background_agent_started',session_id:target.session_id,timestamp:started,data:{type:'background_agent_started',data:{agent_id:target.execution_id,parent_execution_id:parentID,execution_kind:'workflow_step',metadata:{inspection:target}}}} as PollingEvent] : []
useChatStore.setState({activeTabId:'qa',chatTabs:{qa:{tabId:'qa',sessionId:target.session_id,metadata:{mode:'workflow'}} as ChatTab},tabEvents:{[target.session_id]:launchEvents}})
if (!nativeLaunch) openStepInspection(params.get('mode')==='script' ? {...target,execution_id:'script-run',name:'Write status with Python'} : target)
createRoot(document.getElementById('root')!).render(<main className="h-dvh bg-background text-foreground"><StepInspectionTabs tabId="qa"><div className="flex h-full min-h-0 flex-col"><textarea aria-label="Chat draft" defaultValue="Keep this draft" />{nativeLaunch && <TerminalEventTranscript terminal={null} events={launchEvents} />}</div></StepInspectionTabs></main>)

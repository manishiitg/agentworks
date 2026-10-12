import type { ExecutionInspectionResponse, PollingEvent, TerminalSnapshot } from '../services/api-types'

/** Exact recorded values for the inspector's four explicit sections. */
export function inspectionRecords(response: ExecutionInspectionResponse) {
 const events = [...response.transcript.events].sort((a, b) => Date.parse(a.timestamp) - Date.parse(b.timestamp))
 const system = [...new Set(events.filter(event => event.role === 'system').map(event => event.text).filter((text): text is string => !!text))]
 const user = events.filter(event => event.role === 'user' || event.type === 'user_message').map(event => event.text).filter((text): text is string => !!text)
 const assistant = events.filter(event => event.role === 'assistant' || event.type === 'assistant_message').map(event => event.text).filter((text): text is string => !!text)
 const tools = new Map<string, NonNullable<ExecutionInspectionResponse['transcript']['events'][number]['tool_call']>>()
 events.forEach((event, index) => {
  const call = event.tool_call
  if (!call) return
  const key = call.tool_call_id || `${call.tool_name}:${index}`
  const previous = tools.get(key)
  tools.set(key, {...previous, ...call, arguments: call.arguments ?? previous?.arguments, result: call.result ?? previous?.result, error: call.error ?? previous?.error, status: previous && call.status === 'running' && previous.status !== 'running' ? previous.status : call.status})
 })
 return {system, user, tools: [...tools.values()], result: response.result || assistant.at(-1) || '', assistant}
}

export function inspectionTaskLabel(name: string): string {
 const item = name.match(/^Agent item -> .* \/ ([^/]+) \(([^)]+)\)$/)
 if (!item) return name
 if (item[1].startsWith('__automatic_final_validation')) return 'Final validation'
 return item[2] === 'prevalidation' ? `Validation ${item[1]}` : `Task ${item[1]}`
}

/** Feed saved execution records through the same renderer as ChatArea. */
export function inspectionChat(response: ExecutionInspectionResponse) {
 const transcript = response.transcript
 const events: PollingEvent[] = []
 let lastAssistant = ''
 for (const [index, event] of [...transcript.events].sort((a,b)=>Date.parse(a.timestamp)-Date.parse(b.timestamp)).entries()) {
  const payload: Record<string,unknown> = {metadata:{execution_owner_id:transcript.agent_id,execution_kind:'background_agent'}}
  let type = ''
  if (event.tool_call) {
   const call = event.tool_call
   type = call.status === 'running' ? 'tool_call_start' : call.error || call.status === 'failed' ? 'tool_call_error' : 'tool_call_end'
   Object.assign(payload,{tool_name:call.tool_name,tool_call_id:call.tool_call_id || `unpaired-${index}`,arguments:call.arguments,tool_params:{arguments:call.arguments},result:call.result,error:call.error})
  } else if (event.role === 'user' || event.type === 'user_message') {type='user_message';Object.assign(payload,{content:event.text,role:'user'})}
  else if (event.role === 'assistant' || event.type === 'assistant_message') {type='llm_generation_end';Object.assign(payload,{content:event.text});lastAssistant=event.text || ''}
  if (type) events.push({id:`${transcript.agent_id}:${index}`,type,timestamp:event.timestamp,session_id:transcript.session_id,sequence:index,data:{type,data:payload}} as PollingEvent)
 }
 if (response.result && response.result.trim() !== lastAssistant.trim()) events.push({id:`${transcript.agent_id}:result`,type:'llm_generation_end',timestamp:transcript.completed_at || transcript.started_at,session_id:transcript.session_id,sequence:transcript.events.length,data:{type:'llm_generation_end',data:{content:response.result,metadata:{execution_owner_id:transcript.agent_id,execution_kind:'background_agent'}}}} as PollingEvent)
 const terminal: TerminalSnapshot = {terminal_id:`${transcript.session_id}:${transcript.agent_id}`,session_id:transcript.session_id,execution_id:transcript.agent_id,execution_kind:'background_agent',content:'',rows:[],chunk_index:0,active:transcript.status==='running',state:transcript.status,status:{},created_at:transcript.started_at,updated_at:transcript.completed_at || transcript.started_at}
 return {events,terminal}
}

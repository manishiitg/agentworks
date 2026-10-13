import type { ExecutionInspectionResponse, PollingEvent, TerminalSnapshot } from '../services/api-types'

export function inspectionEvents(response: ExecutionInspectionResponse) {
 return response.conversation?.events ?? response.transcript.events
}

export function inspectionHasConversation(response: ExecutionInspectionResponse) {
 return inspectionEvents(response).some(event => event.tool_call || ['system','user','assistant'].includes(event.role || ''))
}

/** Exact recorded values for the inspector's four explicit sections. */
export function inspectionRecords(response: ExecutionInspectionResponse) {
 const events = [...inspectionEvents(response)].sort((a, b) => Date.parse(a.timestamp) - Date.parse(b.timestamp))
 const system = [...new Set(events.filter(event => event.role === 'system').map(event => event.text).filter((text): text is string => !!text))]
 const user = events.filter(event => event.role === 'user' || event.type === 'user_message').map(event => event.text).filter((text): text is string => !!text)
 const assistant = events.filter(event => event.role === 'assistant' || event.type === 'assistant_message').map(event => event.text).filter((text): text is string => !!text)
 const tools = new Map<string, NonNullable<ExecutionInspectionResponse['transcript']['events'][number]['tool_call']>>()
 events.forEach((event, index) => {
  const call = event.tool_call
  if (!call) return
  const key = `${event.execution_id || response.transcript.agent_id}:${call.tool_call_id || `${call.tool_name}:${index}`}`
  const previous = tools.get(key)
  tools.set(key, {...previous, ...call, arguments: call.arguments ?? previous?.arguments, result: call.result ?? previous?.result, error: call.error ?? previous?.error, status: previous && call.status === 'running' && previous.status !== 'running' ? previous.status : call.status})
 })
 return {system, user, tools: [...tools.values()], result: (response.conversation ? assistant.at(-1) : response.result) || response.result || assistant.at(-1) || '', assistant}
}

export function inspectionTaskLabel(name: string): string {
 const item = name.match(/^Agent item -> .* \/ ([^/]+) \(([^)]+)\)$/)
 if (!item) return name
 if (item[1] === '__step_description__') return 'Step task'
 if (item[1].startsWith('__automatic_final_validation')) return 'Final validation'
 return item[2] === 'prevalidation' ? `Validation ${item[1]}` : `Task ${item[1]}`
}

/** Feed saved execution records through the same renderer as ChatArea. */
export function inspectionChat(response: ExecutionInspectionResponse) {
 const transcript = response.transcript
 const events: PollingEvent[] = []
 let lastAssistant = ''
 let previousMessage: typeof transcript.events[number] | undefined
 let kickoff = true
 const recordedEvents = inspectionEvents(response)
 for (const [index, event] of [...recordedEvents].sort((a,b)=>Date.parse(a.timestamp)-Date.parse(b.timestamp)).entries()) {
  const role = event.role || (event.type==='user_message' ? 'user' : event.type==='assistant_message' ? 'assistant' : '')
  if (role==='user' || role==='assistant') {
   const previous = previousMessage
   if (previous && previous.execution_id===event.execution_id && previous.role===role && event.text?.trim() && event.text.trim()===previous.text?.trim()) {
    const pair = new Set([previous.source_type,event.source_type])
    const carrierPair = role==='user' ? pair.has('orchestrator_agent_start') && pair.has('user_message') : pair.has('orchestrator_agent_end') && pair.has('llm_generation_end')
    // Older transcripts lost carrier identity. Limit compatibility to the
    // first adjacent kickoff echo, before any tools/answers, within 100 ms.
    const delta = Date.parse(event.timestamp)-Date.parse(previous.timestamp)
    const sinceStart = Date.parse(previous.timestamp)-Date.parse(transcript.started_at)
    const legacyKickoff = kickoff && role==='user' && !previous.source_type && !event.source_type && delta>=0 && delta<=100 && sinceStart>=0 && sinceStart<=1000
    if (carrierPair || legacyKickoff) continue
   }
   if (previousMessage || role==='assistant') kickoff=false
   previousMessage={...event,role}
  } else if (event.tool_call) {previousMessage=undefined;kickoff=false}
  const payload: Record<string,unknown> = {metadata:{execution_owner_id:transcript.agent_id,execution_kind:'background_agent'}}
  let type = ''
  if (event.tool_call) {
   const call = event.tool_call
   type = call.status === 'running' ? 'tool_call_start' : call.error || call.status === 'failed' ? 'tool_call_error' : 'tool_call_end'
   const callID=call.tool_call_id || `unpaired-${index}`
   Object.assign(payload,{tool_name:call.tool_name,tool_call_id:event.execution_id ? `${event.execution_id}:${callID}` : callID,arguments:call.arguments,tool_params:{arguments:call.arguments},result:call.result,error:call.error})
  } else if (event.role === 'user' || event.type === 'user_message') {type='user_message';Object.assign(payload,{content:event.text,role:'user'})}
  else if (event.role === 'assistant' || event.type === 'assistant_message') {type='llm_generation_end';Object.assign(payload,{content:event.text});lastAssistant=event.text || ''}
  if (type) events.push({id:`${transcript.agent_id}:${index}`,type,timestamp:event.timestamp,session_id:transcript.session_id,sequence:index,data:{type,data:payload}} as PollingEvent)
 }
 if (response.result && !(response.conversation && lastAssistant.trim()) && response.result.trim() !== lastAssistant.trim()) events.push({id:`${transcript.agent_id}:result`,type:'llm_generation_end',timestamp:transcript.completed_at || transcript.started_at,session_id:transcript.session_id,sequence:recordedEvents.length,data:{type:'llm_generation_end',data:{content:response.result,metadata:{execution_owner_id:transcript.agent_id,execution_kind:'background_agent'}}}} as PollingEvent)
 const terminal: TerminalSnapshot = {terminal_id:`${transcript.session_id}:${transcript.agent_id}`,session_id:transcript.session_id,execution_id:transcript.agent_id,execution_kind:'background_agent',content:'',rows:[],chunk_index:0,active:transcript.status==='running',state:transcript.status,status:{},created_at:transcript.started_at,updated_at:transcript.completed_at || transcript.started_at}
 return {events,terminal}
}

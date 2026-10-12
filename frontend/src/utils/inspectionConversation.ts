import type { ExecutionInspectionResponse } from '../services/api-types'

/** Reuse ConversationViewer's existing message/tool rendering contract. */
export function inspectionConversation(response: ExecutionInspectionResponse, toolsOnly = false): string {
 const messages: Array<{Role: string; Parts: Array<Record<string, unknown>>}> = []
 const tools = new Map<string, { index: number; resultIndex: number; name: string; arguments: string; result?: string; error?: string; status: string }>()
 response.transcript.events.forEach((event, index) => {
  if (event.tool_call) {
   const call = event.tool_call
   const key = call.tool_call_id || `${call.tool_name}:${index}`
   const previous = tools.get(key)
   tools.set(key, { index: previous?.index ?? index, resultIndex: index, name: call.tool_name || previous?.name || 'Tool', arguments: call.arguments ?? previous?.arguments ?? '', result: call.result ?? previous?.result, error: call.error ?? previous?.error, status: call.status })
  } else if (!toolsOnly && event.text) {
   const role = event.role === 'system' ? 'system' : event.role === 'assistant' || event.type === 'assistant_message' ? 'ai' : 'human'
   // A provider may emit both conversation_start and system_prompt with the same prompt.
   if (role === 'system' && messages.some(message => message.Role === role && message.Parts[0]?.Text === event.text)) return
   messages.push({Role: role, Parts: [{Text: event.text, inspectionIndex: index}]})
  }
 })
 tools.forEach((tool, key) => {
  messages.push({Role: 'ai', Parts: [{ID: key, FunctionCall: {Name: tool.name, Arguments: tool.arguments}, inspectionIndex: tool.index}]})
  messages.push({Role: 'tool', Parts: [{ToolCallID: key, Name: tool.name, Content: tool.error || tool.result || (tool.status === 'running' ? 'Running…' : 'No result recorded'), inspectionIndex: tool.resultIndex + 0.1}]})
 })
 messages.sort((a, b) => Number(a.Parts[0].inspectionIndex ?? -1) - Number(b.Parts[0].inspectionIndex ?? -1))
 return JSON.stringify({conversation_history: messages})
}

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
